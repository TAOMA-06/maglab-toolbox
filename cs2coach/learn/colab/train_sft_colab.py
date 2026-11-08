#!/usr/bin/env python3
"""train_sft_colab.py —— A 层教练小模型 Colab(CUDA) 训练入口。

地位：`learn/train_sft.py` 的 Colab 孪生版。
- Mac 本地走 MLX（train_sft.py，底座 mlx-community/*-4bit）；
- Colab T4 16GB 走这个脚本（HuggingFace + bitsandbytes 4bit + PEFT LoRA）。

同构保证：
1. 读同一个 `learn/configs/model.yaml`（超参：iters/batch/lr/rank/seq 复用）；
2. 读同一份 SFT 数据（`learn/data/sft/train.jsonl|valid.jsonl`，
   `build_sft.py` 默认 completion 格式与 --format chat 格式都吃）；
3. 产出同一个语义的适配器目录（含 adapter + tokenizer + 训练日志），
   拉回 Mac 后转回 MLX 底座推理或直接用 HF 推理。

用法（Colab 里按 notebook 顺序跑，这里只列等价命令）：
  python3 train_sft_colab.py --check                      # 不训练，只审计数据+环境
  python3 train_sft_colab.py --dry-run                    # 打印训练计划
  python3 train_sft_colab.py                              # 全量训练（默认 600 steps）
  python3 train_sft_colab.py --max-steps 50               # 先小跑验证通路
  python3 train_sft_colab.py --test-prompt "回合 facts..." # 适配器推理抽查

依赖：torch / transformers / peft / accelerate / bitsandbytes（见 requirements-colab.txt）。
--check / --dry-run 不需要这些，只用标准库（+可选 pyyaml）就能跑。
"""

from __future__ import annotations

import argparse
import json
import os
import platform
import shutil
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

HERE = Path(__file__).resolve().parent
LEARN = HERE.parent
REPO_ROOT = LEARN.parent
DEFAULT_CONFIG = LEARN / "configs" / "model.yaml"
DEFAULT_DATA_DIR = LEARN / "data" / "sft"
DEFAULT_ADAPTER_DIR = Path("coach-adapter-colab")

# mlx-community 4bit 底座 -> HuggingFace CUDA 底座（权重同源，只是量化容器不同）
MLX_TO_HF = {
    "mlx-community/Qwen2.5-1.5B-Instruct-4bit": "Qwen/Qwen2.5-1.5B-Instruct",
    "mlx-community/DeepSeek-R1-Distill-Qwen-1.5B-4bit": "deepseek-ai/DeepSeek-R1-Distill-Qwen-1.5B",
    "mlx-community/Qwen3-1.7B-4bit": "Qwen/Qwen3-1.7B",
    "mlx-community/Qwen3-0.6B-4bit": "Qwen/Qwen3-0.6B",
}

INSTALL_HINT = """\
x 缺训练依赖（torch / transformers / peft / accelerate / bitsandbytes 至少缺一个）。

Colab 里先跑：
  !pip install -q -r learn/colab/requirements-colab.txt

或：
  !pip install -q bitsandbytes accelerate peft transformers datasets pyyaml

装好后先审计：
  !python3 learn/colab/train_sft_colab.py --check --data-dir <sft目录>
"""


# --------------------------------------------------------------------------
# 配置（标准库 only，和 train_sft.py 同一套 mini-yaml 思路）
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
    rows: List[Tuple[int, str]] = []
    for raw in text.splitlines():
        line = _strip_comment(raw).rstrip()
        if not line.strip():
            continue
        indent = len(line) - len(line.lstrip(" "))
        rows.append((indent, line.strip()))
    root: Dict[str, Any] = {}
    stack: List[Tuple[int, Any]] = [(-1, root)]
    for indent, content in rows:
        while stack and indent <= stack[-1][0]:
            stack.pop()
        parent = stack[-1][1]
        if content.startswith("- "):
            assert isinstance(parent, list), "列表项的上级不是列表：{}".format(content)
            parent.append(_parse_scalar(content[2:].strip()))
            continue
        if ":" not in content:
            raise ValueError("看不懂的行：{}".format(content))
        key, _, rest = content.partition(":")
        key, rest = key.strip(), rest.strip()
        if rest == "":
            nxt_is_list = False  # 保守按 map 处理，列表场景 model.yaml 里暂无
            node: Any = [] if nxt_is_list else {}
            parent[key] = node
            stack.append((indent, node))
        else:
            parent[key] = _parse_scalar(rest)
    return root


def load_config(path: Path) -> Dict[str, Any]:
    try:
        import yaml  # type: ignore

        with path.open("r", encoding="utf-8") as fh:
            return yaml.safe_load(fh) or {}
    except ImportError:
        return mini_yaml(path.read_text(encoding="utf-8"))


def dig(cfg: Dict[str, Any], dotted: str, default: Any = None) -> Any:
    cur: Any = cfg
    for part in dotted.split("."):
        if not isinstance(cur, dict) or part not in cur:
            return default
        cur = cur[part]
    return cur


def _resolve(path_text: str) -> Path:
    p = Path(path_text).expanduser()
    if p.is_absolute():
        return p
    # Colab 里脚本可能被单独上传，相对路径优先按 CWD，再回退到仓库根
    cand = Path.cwd() / path_text
    repo_cand = REPO_ROOT / path_text
    if cand.exists():
        return cand
    return repo_cand if REPO_ROOT.exists() else cand


def map_base_model(mlx_id: str) -> str:
    mid = (mlx_id or "").strip()
    if mid in MLX_TO_HF:
        return MLX_TO_HF[mid]
    if mid.startswith("mlx-community/"):
        core = mid[len("mlx-community/"):]
        for suffix in ("-4bit",):
            if core.endswith(suffix):
                core = core[: -len(suffix)]
        return core  # 尽力而为：Qwen2.5-1.5B-Instruct 这类可直接对应 Qwen/ 官方仓
    return mid


@dataclass
class Settings:
    base_model_mlx: str = ""
    base_model_hf: str = "Qwen/Qwen2.5-1.5B-Instruct"
    data_dir: Path = Path("learn/data/sft")
    adapter_dir: Path = Path("coach-adapter-colab")
    num_layers: int = 16
    batch_size: int = 1
    grad_accumulation_steps: int = 4
    learning_rate: float = 1.0e-4
    iters: int = 600  # Colab 语义 = 优化器 steps
    max_seq_length: int = 2048
    mask_prompt: bool = True
    grad_checkpoint: bool = True
    seed: int = 42
    val_batches: int = 5
    steps_per_report: int = 10
    steps_per_eval: int = 100
    save_every: int = 200
    rank: int = 8
    scale: float = 16.0
    dropout: float = 0.05


def settings_from_config(cfg: Dict[str, Any]) -> Settings:
    base = str(dig(cfg, "base_model", "") or "").strip()
    data_raw = str(dig(cfg, "data_dir", "learn/data/sft"))
    return Settings(
        base_model_mlx=base,
        base_model_hf=map_base_model(base) if base else "Qwen/Qwen2.5-1.5B-Instruct",
        data_dir=_resolve(data_raw),
        num_layers=int(dig(cfg, "lora.num_layers", 16)),
        batch_size=int(dig(cfg, "lora.batch_size", 1)),
        grad_accumulation_steps=int(dig(cfg, "lora.grad_accumulation_steps", 4)),
        learning_rate=float(dig(cfg, "lora.learning_rate", 1.0e-4)),
        iters=int(dig(cfg, "lora.iters", 600)),
        max_seq_length=int(dig(cfg, "lora.max_seq_length", 2048)),
        mask_prompt=bool(dig(cfg, "lora.mask_prompt", True)),
        grad_checkpoint=bool(dig(cfg, "lora.grad_checkpoint", True)),
        seed=int(dig(cfg, "lora.seed", 42)),
        val_batches=int(dig(cfg, "lora.val_batches", 5)),
        steps_per_report=int(dig(cfg, "lora.steps_per_report", 10)),
        steps_per_eval=int(dig(cfg, "lora.steps_per_eval", 100)),
        save_every=int(dig(cfg, "lora.save_every", 200)),
        rank=int(dig(cfg, "lora.lora_parameters.rank", 8)),
        scale=float(dig(cfg, "lora.lora_parameters.scale", 16.0)),
        dropout=float(dig(cfg, "lora.lora_parameters.dropout", 0.05)),
    )


# --------------------------------------------------------------------------
# 数据审计（标准库 only）
# --------------------------------------------------------------------------

def read_jsonl(path: Path) -> List[Dict[str, Any]]:
    rows = []
    with path.open("r", encoding="utf-8") as fh:
        for i, line in enumerate(fh, 1):
            line = line.strip()
            if not line:
                continue
            try:
                obj = json.loads(line)
            except json.JSONDecodeError as exc:
                raise ValueError("{} 第 {} 行 JSON 坏了：{}".format(path, i, exc))
            if not isinstance(obj, dict):
                raise ValueError("{} 第 {} 行不是 object".format(path, i))
            rows.append(obj)
    return rows


def audit_data(data_dir: Path) -> Dict[str, Any]:
    report: Dict[str, Any] = {"data_dir": str(data_dir), "splits": {}, "format": None, "problems": []}
    for split in ("train", "valid", "test"):
        p = data_dir / "{}.jsonl".format(split)
        if not p.exists():
            continue
        try:
            rows = read_jsonl(p)
        except ValueError as exc:
            report["problems"].append(str(exc))
            continue
        kinds: Dict[str, int] = {}
        too_long = 0
        for r in rows:
            if "messages" in r:
                kinds["chat"] = kinds.get("chat", 0) + 1
                text_len = sum(len(str(m.get("content", ""))) for m in r["messages"] if isinstance(m, dict))
            elif "prompt" in r and "completion" in r:
                kinds["completion"] = kinds.get("completion", 0) + 1
                text_len = len(str(r["prompt"])) + len(str(r["completion"]))
            else:
                kinds["bad"] = kinds.get("bad", 0) + 1
                continue
            if text_len > 6000:
                too_long += 1
        report["splits"][split] = {"rows": len(rows), "kinds": kinds, "over_6000_chars": too_long}
        if kinds.get("bad"):
            report["problems"].append("{} 有 {} 行缺 prompt/completion 或 messages".format(p, kinds["bad"]))
    if not report["splits"]:
        report["problems"].append("数据目录是空的（缺 train.jsonl）：先跑 build_sft.py 生成 SFT 数据")
    fmts = set()
    for info in report["splits"].values():
        for k in info["kinds"]:
            if k in ("chat", "completion"):
                fmts.add(k)
    if len(fmts) > 1:
        report["problems"].append("train/valid 混用了 chat 与 completion 两种格式（建议统一用 chat）")
    report["format"] = sorted(fmts)[0] if len(fmts) == 1 else (sorted(fmts) if fmts else None)
    return report


def check_torch_stack() -> Tuple[bool, Dict[str, str]]:
    mods = ("torch", "transformers", "peft", "accelerate", "bitsandbytes")
    info: Dict[str, str] = {}
    ok = True
    for m in mods:
        try:
            mod = __import__(m)
            info[m] = getattr(mod, "__version__", "installed")
        except Exception as exc:  # noqa: BLE001
            info[m] = "missing ({})".format(exc)
            if m != "bitsandbytes":
                ok = False
    cuda = False
    gpu_name = ""
    try:
        import torch  # type: ignore

        cuda = bool(torch.cuda.is_available())
        if cuda:
            gpu_name = torch.cuda.get_device_name(0)
    except Exception:  # noqa: BLE001
        pass
    info["cuda"] = str(cuda)
    if gpu_name:
        info["gpu"] = gpu_name
    return ok, info


# --------------------------------------------------------------------------
# GPU 训练（重依赖，调用时才 import）
# --------------------------------------------------------------------------

def rows_to_texts(rows: List[Dict[str, Any]], tokenizer: Any, max_len: int) -> List[Tuple[str, str]]:
    """每行 -> (全文text, prompt部分text)。mask 时对 prompt 部分算 loss 掩码。"""
    out = []
    for r in rows:
        if "messages" in r and isinstance(r["messages"], list):
            msgs = [m for m in r["messages"] if isinstance(m, dict) and "content" in m]
            full = tokenizer.apply_chat_template(msgs, tokenize=False, add_generation_prompt=False)
            pre = tokenizer.apply_chat_template(
                [m for m in msgs if m.get("role") != "assistant"],
                tokenize=False,
                add_generation_prompt=True,
            )
        else:
            pre, full = str(r.get("prompt", "")), str(r.get("prompt", "")) + str(r.get("completion", ""))
        out.append((full[: max_len * 4], pre[: max_len * 4]))
    return out


def train_gpu(s: Settings, args: argparse.Namespace) -> int:
    try:
        import torch  # type: ignore
        from transformers import AutoModelForCausalLM, AutoTokenizer, BitsAndBytesConfig  # type: ignore
        from peft import LoraConfig, get_peft_model, prepare_model_for_kbit_training  # type: ignore
    except ImportError:
        print(INSTALL_HINT, file=sys.stderr)
        return 2

    max_steps = args.max_steps or s.iters
    out_dir = Path(args.out_dir or str(s.adapter_dir)).expanduser()
    out_dir.mkdir(parents=True, exist_ok=True)

    if not torch.cuda.is_available():
        print("! 没检测到 CUDA（Colab 菜单：代码执行程序 -> 更改运行时类型 -> T4 GPU）。", file=sys.stderr)
        print("  加 --dry-run 只看计划；无 GPU 强行跑会极慢，已拒绝。", file=sys.stderr)
        return 2

    gpu = torch.cuda.get_device_name(0)
    print("GPU: {}".format(gpu))
    bf16_ok = torch.cuda.is_bf16_supported() if hasattr(torch.cuda, "is_bf16_supported") else False
    dtype = torch.bfloat16 if bf16_ok else torch.float16
    print("精度: {}".format("bf16" if bf16_ok else "fp16（T4 无 bf16，用 fp16）"))

    train_p = s.data_dir / "train.jsonl"
    valid_p = s.data_dir / "valid.jsonl"
    if not train_p.exists():
        print("! {} 里没有 train.jsonl：先跑 build_sft.py 生成 SFT 数据。".format(s.data_dir), file=sys.stderr)
        print("  python3 learn/build_sft.py --input out/rounds.jsonl --out-dir {} --format chat".format(s.data_dir),
              file=sys.stderr)
        return 2
    train_rows = read_jsonl(train_p)
    valid_rows = read_jsonl(valid_p) if valid_p.exists() else []
    print("数据: train {} 条{} / 底座 {}".format(
        len(train_rows),
        " / valid {} 条".format(len(valid_rows)) if valid_rows else "（无 valid，只看 train loss）",
        s.base_model_hf,
    ))

    print("载入 tokenizer + 4bit 底座 ...")
    tok = AutoTokenizer.from_pretrained(s.base_model_hf, trust_remote_code=True)
    if tok.pad_token is None:
        tok.pad_token = tok.eos_token
    tok.padding_side = "right"
    bnb = BitsAndBytesConfig(
        load_in_4bit=True,
        bnb_4bit_quant_type="nf4",
        bnb_4bit_compute_dtype=dtype,
        bnb_4bit_use_double_quant=True,
    )
    model = AutoModelForCausalLM.from_pretrained(
        s.base_model_hf,
        quantization_config=bnb,
        device_map="auto",
        trust_remote_code=True,
    )
    if s.grad_checkpoint:
        try:
            model.gradient_checkpointing_enable()
        except Exception:  # noqa: BLE001
            pass
    model = prepare_model_for_kbit_training(model)
    lora = LoraConfig(
        r=s.rank,
        lora_alpha=int(s.scale) if float(s.scale).is_integer() else s.scale,
        lora_dropout=s.dropout,
        bias="none",
        task_type="CAUSAL_LM",
        target_modules=["q_proj", "k_proj", "v_proj", "o_proj", "gate_proj", "up_proj", "down_proj"],
    )
    model = get_peft_model(model, lora)
    model.print_trainable_parameters()

    texts = rows_to_texts(train_rows, tok, s.max_seq_length)
    vtexts = rows_to_texts(valid_rows, tok, s.max_seq_length) if valid_rows else []

    def encode(full: str, pre: str) -> Dict[str, List[int]]:
        ids_full = tok(full, truncation=True, max_length=s.max_seq_length)["input_ids"]
        n_pre = len(tok(pre, truncation=True, max_length=s.max_seq_length)["input_ids"])
        labels = [-100] * min(n_pre, len(ids_full)) + ids_full[min(n_pre, len(ids_full)):]
        if not s.mask_prompt:
            labels = list(ids_full)
        return {"input_ids": ids_full, "labels": labels}

    enc = [encode(f, p) for f, p in texts]
    venc = [encode(f, p) for f, p in vtexts]

    torch.manual_seed(s.seed)
    opt = torch.optim.AdamW(model.parameters(), lr=s.learning_rate)
    model.train()
    step = 0
    accum = max(1, s.grad_accumulation_steps)
    per_device = max(1, s.batch_size)
    idx = 0
    import random as _random

    _random.seed(s.seed)
    order = list(range(len(enc)))
    _random.shuffle(order)
    running = 0.0
    log: List[Dict[str, Any]] = []
    print("开训: {} steps / batch {} x accum {} (=有效 {})".format(max_steps, per_device, accum, per_device * accum))
    opt.zero_grad()
    while step < max_steps:
        if idx >= len(order):
            _random.shuffle(order)
            idx = 0
        batch_idx = order[idx: idx + per_device]
        idx += len(batch_idx)
        maxlen = max(len(enc[i]["input_ids"]) for i in batch_idx)
        input_ids, labels, mask = [], [], []
        for i in batch_idx:
            ids, lab = enc[i]["input_ids"], enc[i]["labels"]
            pad = maxlen - len(ids)
            input_ids.append(ids + [tok.pad_token_id] * pad)
            labels.append(lab + [-100] * pad)
            mask.append([1] * len(ids) + [0] * pad)
        batch = {
            "input_ids": torch.tensor(input_ids, device=model.device),
            "attention_mask": torch.tensor(mask, device=model.device),
            "labels": torch.tensor(labels, device=model.device),
        }
        with torch.autocast(device_type="cuda", dtype=dtype):
            out = model(**batch)
            loss = out.loss / accum
        loss.backward()
        running += float(loss.detach()) * accum
        if (step + 1) % accum == 0:
            opt.step()
            opt.zero_grad()
        step += 1
        if step % s.steps_per_report == 0:
            avg = running / s.steps_per_report
            running = 0.0
            print("step {}/{} loss {:.4f}".format(step, max_steps, avg), flush=True)
        if step % s.steps_per_eval == 0 and venc:
            model.eval()
            with torch.no_grad():
                tot, n = 0.0, 0
                for f, p in vtexts[: max(1, s.val_batches * 4)]:
                    e = encode(f, p)
                    L = len(e["input_ids"])
                    b = {
                        "input_ids": torch.tensor([e["input_ids"]], device=model.device),
                        "attention_mask": torch.tensor([[1] * L], device=model.device),
                        "labels": torch.tensor([e["labels"]], device=model.device),
                    }
                    with torch.autocast(device_type="cuda", dtype=dtype):
                        tot += float(model(**b).loss.detach())
                    n += 1
                print("  [eval] loss {:.4f} ({} 条)".format(tot / max(1, n), n), flush=True)
            model.train()
        if step % s.save_every == 0:
            ckpt = out_dir / "checkpoint-{}".format(step)
            ckpt.mkdir(parents=True, exist_ok=True)
            model.save_pretrained(str(ckpt))
            print("  已存 {}".format(ckpt), flush=True)
    model.save_pretrained(str(out_dir))
    tok.save_pretrained(str(out_dir))
    meta = {
        "base_model_hf": s.base_model_hf,
        "base_model_mlx": s.base_model_mlx,
        "train_rows": len(train_rows),
        "valid_rows": len(valid_rows),
        "steps": max_steps,
        "learning_rate": s.learning_rate,
        "lora": {"r": s.rank, "alpha": s.scale, "dropout": s.dropout},
        "max_seq_length": s.max_seq_length,
        "gpu": gpu,
        "source": "train_sft_colab.py",
    }
    (out_dir / "colab_meta.json").write_text(json.dumps(meta, ensure_ascii=False, indent=2), encoding="utf-8")
    print("完成: {}".format(out_dir))
    print("把整个目录下载回 Mac（notebook 最后一步会自动打包 zip）。")
    return 0


def test_prompt(s: Settings, args: argparse.Namespace) -> int:
    try:
        import torch  # type: ignore
        from transformers import AutoModelForCausalLM, AutoTokenizer  # type: ignore
        from peft import PeftModel  # type: ignore
    except ImportError:
        print(INSTALL_HINT, file=sys.stderr)
        return 2
    out_dir = Path(args.out_dir or str(s.adapter_dir)).expanduser()
    if not (out_dir / "adapter_config.json").exists():
        print("! 适配器不存在：{}（先训练）".format(out_dir), file=sys.stderr)
        return 2
    tok = AutoTokenizer.from_pretrained(s.base_model_hf, trust_remote_code=True)
    base = AutoModelForCausalLM.from_pretrained(s.base_model_hf, device_map="auto", trust_remote_code=True)
    model = PeftModel.from_pretrained(base, str(out_dir))
    model.eval()
    prompt = args.test_prompt
    msgs = [{"role": "user", "content": prompt}]
    try:
        text = tok.apply_chat_template(msgs, tokenize=False, add_generation_prompt=True)
    except Exception:  # noqa: BLE001
        text = prompt
    ids = tok(text, return_tensors="pt").to(model.device)
    with torch.no_grad():
        gen = model.generate(**ids, max_new_tokens=args.max_new_tokens, do_sample=False)
    print(tok.decode(gen[0], skip_special_tokens=True))
    return 0


# --------------------------------------------------------------------------
# CLI
# --------------------------------------------------------------------------

def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description="A 层教练小模型 Colab(CUDA) 训练入口")
    p.add_argument("--config", default=str(DEFAULT_CONFIG), help="model.yaml（复用 Mac 同一套超参）")
    p.add_argument("--data-dir", default="", help="覆盖 SFT 数据目录")
    p.add_argument("--out-dir", default="", help="适配器输出目录（默认 coach-adapter-colab）")
    p.add_argument("--base-model", default="", help="覆盖 HF 底座（默认按 model.yaml 自动映射）")
    p.add_argument("--max-steps", type=int, default=0, help="覆盖 iters（小跑验证用，如 50）")
    p.add_argument("--check", action="store_true", help="只审计数据+环境，不训练")
    p.add_argument("--dry-run", action="store_true", help="打印训练计划，不训练")
    p.add_argument("--test-prompt", default="", help="用训好的适配器生成一段（抽查）")
    p.add_argument("--max-new-tokens", type=int, default=256)
    return p


def main(argv: Optional[List[str]] = None) -> int:
    args = build_parser().parse_args(argv)
    cfg_path = Path(args.config).expanduser()
    if not cfg_path.exists():
        alt = REPO_ROOT / "learn" / "configs" / "model.yaml"
        cfg_path = alt if alt.exists() else cfg_path
    cfg = load_config(cfg_path) if cfg_path.exists() else {}
    s = settings_from_config(cfg)
    if args.data_dir:
        s.data_dir = Path(args.data_dir).expanduser()
    if args.out_dir:
        s.adapter_dir = Path(args.out_dir).expanduser()
    if args.base_model:
        s.base_model_hf = args.base_model.strip()
    if args.max_steps:
        s.iters = args.max_steps

    if args.test_prompt:
        return test_prompt(s, args)

    if args.check or args.dry_run:
        print("底座(MLX): {}".format(s.base_model_mlx or "（未读到，按默认）"))
        print("底座(HF) : {}".format(s.base_model_hf))
        print("数据目录  : {}".format(s.data_dir))
        rep = audit_data(s.data_dir)
        for split, info in rep["splits"].items():
            print("  {}: {} 行 {} 超长>6000字符 {}".format(split, info["rows"], info["kinds"], info["over_6000_chars"]))
        if not rep["splits"]:
            print("! 数据是空的：先在本地跑 build_sft.py 生成 train.jsonl，再上传到 Colab。", file=sys.stderr)
            print("  python3 learn/build_sft.py --input out/rounds.jsonl --out-dir learn/data/sft --format chat",
                  file=sys.stderr)
        for prob in rep["problems"]:
            print("! {}".format(prob), file=sys.stderr)
        ok, info = check_torch_stack()
        print("环境: {} / {}".format(platform.python_version(), " ".join(sys.argv[:1])))
        for k, v in info.items():
            print("  {}: {}".format(k, v))
        if args.dry_run:
            print("计划: {} steps / batch {} x accum {} / lr {} / r{} / seq {} -> {}".format(
                s.iters, s.batch_size, s.grad_accumulation_steps, s.learning_rate,
                s.rank, s.max_seq_length, s.adapter_dir,
            ))
            print("命令: python3 learn/colab/train_sft_colab.py --data-dir {} --out-dir {}".format(
                s.data_dir, s.adapter_dir))
        if rep["problems"]:
            return 2
        if args.check and not ok:
            print("! torch 依赖栈不完整（--check 照样过，训练前 pip 一下即可）。")
        return 0

    return train_gpu(s, args)


if __name__ == "__main__":
    raise SystemExit(main())
