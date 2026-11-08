#!/usr/bin/env python3
"""train_sft.py —— A 层 MLX LoRA（QLoRA）微调入口。

只做三件事：
1. 读 learn/configs/model.yaml（base_model / lora 超参），生成 mlx-lm 能吃的训练配置；
2. 调 `mlx_lm lora --config ...` 训练，适配器存 learn/models/coach-adapter；
3. `--fuse` 调 `mlx_lm fuse` 把适配器合回底座，输出 learn/models/coach-fused。

换底座模型：改 model.yaml 的 base_model 一行，或临时 `--set base_model=...`。
没装 mlx-lm 时不会抛 traceback：打印安装指引，退出码 2。

用法：
  python3 learn/train_sft.py --check
  python3 learn/train_sft.py --dry-run
  python3 learn/train_sft.py
  python3 learn/train_sft.py --set base_model=mlx-community/Qwen3-1.7B-4bit
  python3 learn/train_sft.py --fuse
"""

from __future__ import annotations

import argparse
import importlib.util
import json
import platform
import shlex
import shutil
import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parent
DEFAULT_CONFIG = HERE / "configs" / "model.yaml"

INSTALL_HINT = """\
✗ 没找到 mlx-lm，A 层微调暂时跑不了。

为什么只能用 MLX：Unsloth / bitsandbytes / Axolotl 都是 CUDA-only，Mac 上没有可用路径；
PyTorch MPS 也没有 QLoRA 实现。macOS + Apple Silicon 上能跑的就是 mlx-lm。

安装（独立 venv，别装进系统 Python）：
  uv venv --python 3.12 {venv}
  uv pip install --python {venv}/bin/python "mlx-lm[train]"

或者用 pip：
  {venv}/bin/python -m pip install -U "mlx-lm[train]"

装好后先自检：
  {venv}/bin/python learn/train_sft.py --check

国内下载底座慢可以先设镜像：
  export HF_ENDPOINT=https://hf-mirror.com
"""


# --------------------------------------------------------------------------
# YAML：优先 PyYAML；没有就用最小解析器（model.yaml 的结构很简单）
# --------------------------------------------------------------------------


def _strip_comment(line: str) -> str:
    quote = ""
    for i, ch in enumerate(line):
        if quote:
            if ch == quote:
                quote = ""
        elif ch in "\"'":
            quote = ch
        elif ch == "#":
            return line[:i]
    return line


def _parse_scalar(text: str) -> Any:
    s = text.strip()
    if not s:
        return ""
    if len(s) >= 2 and s[0] == s[-1] and s[0] in "\"'":
        return s[1:-1]
    low = s.lower()
    if low in ("true", "yes", "on"):
        return True
    if low in ("false", "no", "off"):
        return False
    if low in ("null", "~", "none"):
        return None
    if s.startswith("[") and s.endswith("]"):
        inner = s[1:-1].strip()
        return [_parse_scalar(x) for x in inner.split(",")] if inner else []
    try:
        return int(s, 10)
    except ValueError:
        pass
    try:
        return float(s)
    except ValueError:
        pass
    return s


def mini_yaml(text: str) -> Dict[str, Any]:
    """最小 YAML 解析器：嵌套 map + 纯量列表 + 行内列表，够读 model.yaml。"""
    rows: List[Tuple[int, str]] = []
    for raw in text.splitlines():
        line = _strip_comment(raw).rstrip()
        if not line.strip():
            continue
        indent = len(line) - len(line.lstrip(" "))
        rows.append((indent, line.strip()))

    root: Dict[str, Any] = {}
    stack: List[Tuple[int, Any]] = [(-1, root)]
    for idx, (indent, content) in enumerate(rows):
        while stack and indent <= stack[-1][0]:
            stack.pop()
        if not stack:
            raise ValueError("缩进错误：{}".format(content))
        parent = stack[-1][1]
        if content.startswith("- "):
            if not isinstance(parent, list):
                raise ValueError("列表项出现在非列表位置：{}".format(content))
            value = content[2:].strip()
            if ":" in value and not value.startswith(("\"", "'")):
                raise ValueError("暂不支持列表内嵌 map：{}".format(content))
            parent.append(_parse_scalar(value))
            continue
        key, sep, rest = content.partition(":")
        if not sep:
            raise ValueError("缺少冒号：{}".format(content))
        key, rest = key.strip(), rest.strip()
        if rest:
            if not isinstance(parent, dict):
                raise ValueError("键值对出现在非 map 位置：{}".format(content))
            parent[key] = _parse_scalar(rest)
            continue
        nxt = rows[idx + 1] if idx + 1 < len(rows) else None
        child: Any = [] if (nxt and nxt[0] > indent and nxt[1].startswith("- ")) else {}
        if not isinstance(parent, dict):
            raise ValueError("嵌套块出现在非 map 位置：{}".format(content))
        parent[key] = child
        stack.append((indent, child))
    return root


def load_config(path: Path) -> Dict[str, Any]:
    if not path.exists():
        raise SystemExit("配置文件不存在：{}".format(path))
    text = path.read_text(encoding="utf-8")
    cfg: Any
    try:
        import yaml  # type: ignore

        cfg = yaml.safe_load(text)
    except ImportError:
        try:
            cfg = mini_yaml(text)
        except ValueError as exc:
            raise SystemExit("解析 {} 失败（没有 PyYAML，用内置解析器）：{}".format(path, exc))
    except Exception as exc:  # PyYAML 语法错误
        raise SystemExit("解析 {} 失败：{}".format(path, exc))
    if not isinstance(cfg, dict):
        raise SystemExit("{} 顶层必须是 map".format(path))
    return cfg


def dig(cfg: Dict[str, Any], dotted: str, default: Any = None) -> Any:
    cur: Any = cfg
    for part in dotted.split("."):
        if not isinstance(cur, dict) or part not in cur:
            return default
        cur = cur[part]
    return cur


def put(cfg: Dict[str, Any], dotted: str, value: Any) -> None:
    parts = dotted.split(".")
    cur = cfg
    for part in parts[:-1]:
        nxt = cur.get(part)
        if not isinstance(nxt, dict):
            nxt = {}
            cur[part] = nxt
        cur = nxt
    cur[parts[-1]] = value


def coerce(text: str) -> Any:
    low = text.strip().lower()
    if low in ("true", "false"):
        return low == "true"
    for cast in (int, float):
        try:
            return cast(text)
        except ValueError:
            pass
    return text


# --------------------------------------------------------------------------
# 配置 → 训练参数
# --------------------------------------------------------------------------


def _need_int(cfg: Dict[str, Any], key: str, default: int) -> int:
    v = dig(cfg, key, default)
    try:
        return int(v)
    except (TypeError, ValueError):
        raise SystemExit("{} 必须是整数，当前是 {!r}".format(key, v))


def _need_float(cfg: Dict[str, Any], key: str, default: float) -> float:
    v = dig(cfg, key, default)
    try:
        return float(v)
    except (TypeError, ValueError):
        raise SystemExit("{} 必须是数字，当前是 {!r}".format(key, v))


def _need_bool(cfg: Dict[str, Any], key: str, default: bool) -> bool:
    v = dig(cfg, key, default)
    if isinstance(v, bool):
        return v
    if isinstance(v, str):
        return v.strip().lower() in ("1", "true", "yes", "on")
    return bool(v)


def _resolve(path_text: str) -> Path:
    p = Path(path_text).expanduser()
    return p if p.is_absolute() else (REPO_ROOT / p)


@dataclass
class Settings:
    base_model: str
    data_dir: Path
    adapter_path: Path
    fused_path: Path
    fine_tune_type: str = "lora"
    num_layers: int = 16
    batch_size: int = 1
    grad_accumulation_steps: int = 1
    learning_rate: float = 1e-4
    iters: int = 600
    max_seq_length: int = 2048
    mask_prompt: bool = True
    grad_checkpoint: bool = True
    seed: int = 42
    val_batches: int = 5
    steps_per_report: int = 10
    steps_per_eval: int = 100
    save_every: int = 200
    optimizer: str = "adamw"
    rank: int = 8
    scale: float = 16.0
    dropout: float = 0.05
    resume_adapter_file: str = ""
    trust_remote_code: bool = False
    alternatives: List[str] = field(default_factory=list)


def settings_from_config(cfg: Dict[str, Any]) -> Settings:
    base = str(dig(cfg, "base_model", "") or "").strip()
    if not base:
        raise SystemExit("model.yaml 缺 base_model（例如 mlx-community/Qwen2.5-1.5B-Instruct-4bit）")
    lora = "lora"
    return Settings(
        base_model=base,
        data_dir=_resolve(str(dig(cfg, "data_dir", "learn/data/sft"))),
        adapter_path=_resolve(str(dig(cfg, "adapter_path", "learn/models/coach-adapter"))),
        fused_path=_resolve(str(dig(cfg, "fused_path", "learn/models/coach-fused"))),
        fine_tune_type=str(dig(cfg, lora + ".fine_tune_type", "lora")),
        num_layers=_need_int(cfg, lora + ".num_layers", 16),
        batch_size=_need_int(cfg, lora + ".batch_size", 1),
        grad_accumulation_steps=_need_int(cfg, lora + ".grad_accumulation_steps", 1),
        learning_rate=_need_float(cfg, lora + ".learning_rate", 1e-4),
        iters=_need_int(cfg, lora + ".iters", 600),
        max_seq_length=_need_int(cfg, lora + ".max_seq_length", 2048),
        mask_prompt=_need_bool(cfg, lora + ".mask_prompt", True),
        grad_checkpoint=_need_bool(cfg, lora + ".grad_checkpoint", True),
        seed=_need_int(cfg, lora + ".seed", 42),
        val_batches=_need_int(cfg, lora + ".val_batches", 5),
        steps_per_report=_need_int(cfg, lora + ".steps_per_report", 10),
        steps_per_eval=_need_int(cfg, lora + ".steps_per_eval", 100),
        save_every=_need_int(cfg, lora + ".save_every", 200),
        optimizer=str(dig(cfg, lora + ".optimizer", "adamw")),
        rank=_need_int(cfg, lora + ".lora_parameters.rank", 8),
        scale=_need_float(cfg, lora + ".lora_parameters.scale", 16.0),
        dropout=_need_float(cfg, lora + ".lora_parameters.dropout", 0.05),
        resume_adapter_file=str(dig(cfg, "resume_adapter_file", "") or ""),
        trust_remote_code=_need_bool(cfg, "trust_remote_code", False),
        alternatives=[str(x) for x in (dig(cfg, "alternatives", []) or [])],
    )


# --------------------------------------------------------------------------
# mlx 环境探测 / 命令构造
# --------------------------------------------------------------------------


def mlx_missing() -> List[str]:
    missing = []
    for mod in ("mlx", "mlx_lm"):
        try:
            if importlib.util.find_spec(mod) is None:
                missing.append(mod)
        except (ImportError, ValueError):
            missing.append(mod)
    return missing


def mlx_launcher() -> List[str]:
    """优先用当前解释器同目录的 mlx_lm 可执行文件，其次 PATH，最后 python -m mlx_lm。"""
    beside = Path(sys.executable).parent / "mlx_lm"
    if beside.exists():
        return [str(beside)]
    found = shutil.which("mlx_lm")
    if found:
        return [found]
    return [sys.executable, "-m", "mlx_lm"]


def training_yaml(s: Settings) -> str:
    """生成 mlx-lm 的扁平训练配置（键名必须和 mlx_lm.lora 的参数一致）。"""
    lines = [
        "# 由 learn/train_sft.py 从 learn/configs/model.yaml 生成，不要手改",
        "model: {}".format(json.dumps(s.base_model)),
        "train: true",
        "data: {}".format(json.dumps(str(s.data_dir))),
        "fine_tune_type: {}".format(json.dumps(s.fine_tune_type)),
        "optimizer: {}".format(json.dumps(s.optimizer)),
        "num_layers: {}".format(s.num_layers),
        "batch_size: {}".format(s.batch_size),
        "grad_accumulation_steps: {}".format(s.grad_accumulation_steps),
        "learning_rate: {}".format(repr(float(s.learning_rate))),
        "iters: {}".format(s.iters),
        "val_batches: {}".format(s.val_batches),
        "steps_per_report: {}".format(s.steps_per_report),
        "steps_per_eval: {}".format(s.steps_per_eval),
        "save_every: {}".format(s.save_every),
        "max_seq_length: {}".format(s.max_seq_length),
        "adapter_path: {}".format(json.dumps(str(s.adapter_path))),
        "mask_prompt: {}".format("true" if s.mask_prompt else "false"),
        "grad_checkpoint: {}".format("true" if s.grad_checkpoint else "false"),
        "seed: {}".format(s.seed),
        "trust_remote_code: {}".format("true" if s.trust_remote_code else "false"),
    ]
    if s.resume_adapter_file:
        lines.append("resume_adapter_file: {}".format(json.dumps(s.resume_adapter_file)))
    if s.fine_tune_type in ("lora", "dora"):
        lines += [
            "lora_parameters:",
            "  rank: {}".format(s.rank),
            "  scale: {}".format(repr(float(s.scale))),
            "  dropout: {}".format(repr(float(s.dropout))),
        ]
    return "\n".join(lines) + "\n"


def train_cmd(s: Settings, config_path: Path) -> List[str]:
    return mlx_launcher() + ["lora", "--config", str(config_path)]


def fuse_cmd(s: Settings, dequantize: bool) -> List[str]:
    cmd = mlx_launcher() + [
        "fuse",
        "--model", s.base_model,
        "--adapter-path", str(s.adapter_path),
        "--save-path", str(s.fused_path),
    ]
    if dequantize:
        cmd.append("--de-quantize")
    return cmd


def adapter_weights(adapter_path: Path) -> Optional[Path]:
    for name in ("adapters.safetensors", "adapters.npz", "adapter_model.safetensors"):
        p = adapter_path / name
        if p.exists():
            return p
    return None


def data_counts(data_dir: Path) -> Dict[str, int]:
    out = {}
    for split in ("train", "valid", "test"):
        p = data_dir / "{}.jsonl".format(split)
        if not p.exists():
            continue
        n = 0
        with p.open("r", encoding="utf-8") as fh:
            for line in fh:
                if line.strip():
                    n += 1
        out[split] = n
    return out


def fmt_cmd(argv: List[str]) -> str:
    try:
        return shlex.join(argv)
    except AttributeError:
        return " ".join(shlex.quote(a) for a in argv)


def print_env(s: Settings, missing: List[str], counts: Dict[str, int]) -> None:
    print("平台      : {} {}".format(platform.system(), platform.machine()))
    print("Python    : {} ({})".format(platform.python_version(), sys.executable))
    print("mlx-lm    : {}".format("缺失（{}）".format("、".join(missing)) if missing else "已安装"))
    print("底座模型  : {}".format(s.base_model))
    print("数据目录  : {}（{}）".format(s.data_dir, ", ".join("{} {} 条".format(k, v) for k, v in counts.items()) or "空"))
    print("适配器    : {}".format(s.adapter_path))
    print("超参      : {} iters / batch {} × grad_accum {} / lr {} / {} 层 / seq {}".format(
        s.iters, s.batch_size, s.grad_accumulation_steps, s.learning_rate,
        s.num_layers, s.max_seq_length,
    ))


# --------------------------------------------------------------------------
# main
# --------------------------------------------------------------------------


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        description="A 层 MLX LoRA 微调（读 learn/configs/model.yaml）",
        formatter_class=argparse.ArgumentDefaultsHelpFormatter,
    )
    p.add_argument("--config", default=str(DEFAULT_CONFIG), help="模型/训练配置文件")
    p.add_argument("--set", action="append", default=[], metavar="KEY=VALUE",
                   help="覆盖配置项，如 base_model=... 或 lora.iters=300（可多次）")
    # 常用覆盖项：省得每次都写 --set
    p.add_argument("--base-model", default="", help="覆盖 base_model")
    p.add_argument("--data-dir", default="", help="覆盖 data_dir")
    p.add_argument("--adapter-path", default="", help="覆盖 adapter_path")
    p.add_argument("--fused-path", default="", help="覆盖 fused_path（--fuse 的输出目录）")
    p.add_argument("--iters", type=int, default=0, help="覆盖 lora.iters")
    p.add_argument("--batch-size", type=int, default=0, help="覆盖 lora.batch_size")
    p.add_argument("--num-layers", type=int, default=0, help="覆盖 lora.num_layers")
    p.add_argument("--learning-rate", type=float, default=0.0, help="覆盖 lora.learning_rate")
    p.add_argument("--max-seq-length", type=int, default=0, help="覆盖 lora.max_seq_length")
    p.add_argument("--seed", type=int, default=None, help="覆盖 lora.seed")
    p.add_argument("--optimizer", default="", help="覆盖 lora.optimizer（adamw / adam / adafactor）")
    p.add_argument("--rank", type=int, default=0, help="覆盖 lora.lora_parameters.rank")
    p.add_argument("--resume-adapter-file", default="", help="从已有适配器权重继续训练")
    p.add_argument("--check", action="store_true", help="只检查环境/配置/数据，不训练")
    p.add_argument("--dry-run", action="store_true", help="打印训练命令和 mlx 配置，不执行")
    p.add_argument("--list-models", action="store_true", help="列出 base_model 与候选底座")
    p.add_argument("--fuse", action="store_true", help="把适配器合回底座，输出完整模型")
    p.add_argument("--dequantize", action="store_true", help="--fuse 时反量化成 fp16（转 GGUF 用）")
    return p


def main(argv: Optional[List[str]] = None) -> int:
    args = build_parser().parse_args(argv)
    config_path = Path(args.config).expanduser()
    cfg = load_config(config_path)

    for item in args.set:
        if "=" not in item:
            print("--set 需要 KEY=VALUE，收到 {!r}".format(item), file=sys.stderr)
            return 2
        key, value = item.split("=", 1)
        if dig(cfg, key.strip(), None) is None and not key.strip().startswith("lora.lora_parameters"):
            print("! 覆盖的键 {} 原本不存在，确认没有拼错".format(key.strip()), file=sys.stderr)
        put(cfg, key.strip(), coerce(value))

    for key, value in (
        ("base_model", args.base_model),
        ("data_dir", args.data_dir),
        ("adapter_path", args.adapter_path),
        ("fused_path", args.fused_path),
        ("lora.optimizer", args.optimizer),
        ("resume_adapter_file", args.resume_adapter_file),
    ):
        if value:
            put(cfg, key, value)
    for key, value in (
        ("lora.iters", args.iters),
        ("lora.batch_size", args.batch_size),
        ("lora.num_layers", args.num_layers),
        ("lora.learning_rate", args.learning_rate),
        ("lora.max_seq_length", args.max_seq_length),
        ("lora.lora_parameters.rank", args.rank),
    ):
        if value:
            put(cfg, key, value)
    if args.seed is not None:
        put(cfg, "lora.seed", args.seed)

    s = settings_from_config(cfg)

    if args.list_models:
        print("当前底座：{}".format(s.base_model))
        print("候选底座：")
        for name in s.alternatives:
            mark = "（当前）" if name == s.base_model else ""
            print("  - {}{}".format(name, mark))
        if not s.alternatives:
            print("  （model.yaml 里没有 alternatives）")
        return 0

    missing = mlx_missing()
    counts = data_counts(s.data_dir)
    print_env(s, missing, counts)

    if args.check:
        ok = True
        if missing:
            print("\n" + INSTALL_HINT.format(venv=HERE / ".venv"), file=sys.stderr)
            ok = False
        if platform.system() != "Darwin" or platform.machine() != "arm64":
            print("! MLX 只支持 Apple Silicon；当前平台是 {} {}".format(
                platform.system(), platform.machine()), file=sys.stderr)
        if not counts.get("train"):
            print("! {} 里没有 train.jsonl：先跑 python3 learn/build_sft.py --input <rounds.jsonl>".format(s.data_dir),
                  file=sys.stderr)
            ok = False
        if not counts.get("valid"):
            print("! 没有 valid.jsonl：训练不会报验证 loss（建议多攒几场 demo 后重跑 build_sft）",
                  file=sys.stderr)
        print("\n{}".format("✓ 可以开始训练" if ok else "✗ 还有前置条件没满足"))
        return 0 if ok else 2

    if args.fuse:
        weights = adapter_weights(s.adapter_path)
        if weights is None:
            print("适配器不存在：{}（先训练，或检查 adapter_path）".format(s.adapter_path), file=sys.stderr)
            return 2
        cmd = fuse_cmd(s, args.dequantize)
        print("\n合并命令：\n  {}".format(fmt_cmd(cmd)))
        if args.dry_run:
            return 0
        if missing:
            print(INSTALL_HINT.format(venv=HERE / ".venv"), file=sys.stderr)
            return 2
        s.fused_path.parent.mkdir(parents=True, exist_ok=True)
        proc = subprocess.run(cmd)
        if proc.returncode != 0:
            print("mlx_lm fuse 失败（退出码 {}）".format(proc.returncode), file=sys.stderr)
            return proc.returncode
        print("\n✓ 完整模型：{}".format(s.fused_path))
        print("  直接用：python3 -m mlx_lm generate --model {} --prompt \"...\"".format(s.fused_path))
        print("  转 GGUF 给 Ollama：mlx_lm 的 --export-gguf 只支持 Llama/Mistral 结构；")
        print("  Qwen 先 `--fuse --dequantize` 得到 fp16，再用 llama.cpp 的 convert_hf_to_gguf.py 转。")
        return 0

    config_out = s.adapter_path.parent / "mlx_lora_config.yaml"
    cmd = train_cmd(s, config_out)
    yaml_text = training_yaml(s)

    print("\nmlx 训练配置（会写到 {}）：\n{}".format(config_out, yaml_text))
    print("训练命令：\n  {}".format(fmt_cmd(cmd)))
    print("预计耗时：{:.0f}-{:.0f} 分钟（1.5B QLoRA 实测 0.29-0.49 it/s；更大的底座会更慢）".format(
        s.iters / 0.49 / 60, s.iters / 0.29 / 60))

    if args.dry_run:
        return 0
    if missing:
        print("\n" + INSTALL_HINT.format(venv=HERE / ".venv"), file=sys.stderr)
        return 2
    if not counts.get("train"):
        print("\n没有训练数据：{}。先跑：\n  python3 learn/build_sft.py --input <exporter 的回合 JSONL> "
              "--out-dir {}".format(s.data_dir / "train.jsonl", s.data_dir), file=sys.stderr)
        return 2
    if platform.system() != "Darwin" or platform.machine() != "arm64":
        print("! MLX 只支持 Apple Silicon，当前平台大概率会直接失败", file=sys.stderr)

    config_out.parent.mkdir(parents=True, exist_ok=True)
    config_out.write_text(yaml_text, encoding="utf-8")
    print("\n开始训练…（Ctrl-C 中断；适配器会随时保存到 {}）".format(s.adapter_path))
    proc = subprocess.run(cmd)
    if proc.returncode != 0:
        print("训练失败（退出码 {}）".format(proc.returncode), file=sys.stderr)
        return proc.returncode

    weights = adapter_weights(s.adapter_path)
    print("\n✓ 训练完成：{}".format(weights or s.adapter_path))
    print("  试跑：{} generate --model {} --adapter-path {} --prompt \"回合7 ...\"".format(
        fmt_cmd(mlx_launcher()), s.base_model, s.adapter_path))
    print("  合并：python3 learn/train_sft.py --fuse")
    return 0


if __name__ == "__main__":
    sys.exit(main())
