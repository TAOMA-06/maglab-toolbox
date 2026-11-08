# learn/colab/ —— Colab 训练（T4 16GB）

Mac 本地 `train_sft.py` 只能走 MLX（Apple Silicon 专用），Colab 的 CUDA 卡跑不了。
这个目录是它的 CUDA 孪生版：**同一套超参（`configs/model.yaml`）、同一份数据
（`build_sft.py` 产出的 `train.jsonl/valid.jsonl`），换 HuggingFace + QLoRA 实现。**

| 文件 | 作用 |
| --- | --- |
| `train_sft_colab.py` | 训练入口：`--check/--dry-run`（免依赖）+ GPU 全量 + `--test-prompt` 抽查 |
| `cs2coach_colab.ipynb` | 开箱 notebook：按顺序点 ▶，8 步拿适配器 |
| `requirements-colab.txt` | `pip install` 清单 |

## 本地 3 步（5 分钟）

```bash
# 1) 生成 SFT 数据（只要标准库，Mac 就能跑）
python3 learn/build_sft.py --input out/rounds.jsonl --out-dir learn/data/sft --format chat

# 2) 自检 Colab 脚本（免 torch，直接验数据+映射）
learn/.venv/bin/python learn/colab/train_sft_colab.py --check --data-dir learn/data/sft

# 3) 把 notebook + train.jsonl/valid.jsonl 传上 Colab（见 .ipynb 第 2–3 步）
open learn/colab/cs2coach_colab.ipynb
```

## 对应关系（防漂移）

- 底座映射：`mlx-community/Qwen2.5-1.5B-Instruct-4bit` → `Qwen/Qwen2.5-1.5B-Instruct`
  （完整表见 `train_sft_colab.py` 的 `MLX_TO_HF`；换底座改 `model.yaml` 一行，两边一起生效）
- 超参复用：`iters/batch/lr/rank/seq` 全从 `model.yaml` 读；Colab 的 `iters` 语义 = 优化器 steps。
- 数据格式：`completion`（prompt/completion）与 `chat`（messages）都吃；推荐 `--format chat`。
- 产出：`coach-adapter-colab/`（adapter + tokenizer + `colab_meta.json`），zip 下载回 Mac。

## 显存（T4 16GB）

4bit QLoRA + LoRA r8 + seq 2048 + batch 1×accum 4，约占 6–8GB。
爆显存就先降 `model.yaml` 的 `max_seq_length`（2048→1024），再降 batch。

## 胜率模型要不要上 Colab？

`train_winrate.py`（LightGBM）CPU 几分钟跑完，不用 GPU。
Colab 免费 CPU 就能跑，命令见 notebook 末尾附录。T2 的小 Transformer（序列局面）
还没实现，实现后再给它配 GPU 版——先看 v0 的 GBDT 结果（TRAIN-PLAN §3）。
