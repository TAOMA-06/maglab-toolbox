# learn/ —— 胜率/价值模型（TRAIN-PLAN v2 主线）

这个目录现在的主线是 **TRAIN-PLAN v2：训练回合胜率/价值模型**。
「理解游戏」= 可验证的预测能力：输入 tick 级局面，输出焦点方赢下本回合的概率；
LLM 只把模型的数字讲成人话，不再是被训练对象。

```
.dem ──parse──> match.json（M2a 事实）──tactics──> analysis.json（M2b 指标）
                              │
                              ▼
                  winrate_data.py（T0：tick 快照 + 标签）
                              │  feature = 人数/经济/装备/比分/炸弹/击杀/道具/接敌/站位
                              │  label   = 本回合 focus_won
                              ▼
                  train_winrate.py（T1：LightGBM + 按 match 切分）
                              │  基线 = 人数差+经济 逻辑回归
                              │  验收 = AUC ≥ 基线 +0.10
                              ▼
                  learn/models/winrate/（模型 + meta + report）
                              │
                              ▼
                  --drop-points（T2：胜率掉点 + tick 事件证据）
```

## 文件

| 文件 | 作用 |
| --- | --- |
| `winrate_data.py` | T0 数据层：回合级 `samples.jsonl` / `match.json` tick 快照 / 特征 / 按 match 切分 |
| `train_winrate.py` | T1 训练入口：数据审计、GroupKFold、LightGBM、基线对比、T2 掉点/认同率 |
| `configs/winrate.yaml` | 超参与验收门槛（1000 回合、AUC +0.10、按场次切分） |
| `tests/test_winrate_pipeline.py` | 主线自测（16 项；没装 LightGBM 也能跑数据/切分/CLI） |
| `demo_fetch/` | demo 采集器：HLTV 职业比赛 / 5E / Renown / PWA / Steam / 手动 URL（见 `demo_fetch/README.md`） |
| `build_sft.py` / `train_sft.py` | **可选** A 层讲解风格 SFT（T3 方向，不是主线） |
| `baseline.py` / `train_baseline.py` / `features.py` | 旧 B 层回合级基线（保留兼容，不再当主线） |

## 快速开始

依赖（独立 venv，别污染系统 Python）：

```bash
cd ~/code/cs2-demo-review
uv venv --python 3.12 learn/.venv
uv pip install --python learn/.venv/bin/python -r learn/requirements.txt
```

### 0) 拉训练 demo

`learn/demo_fetch` 支持职业比赛和天梯，国内外来源都覆盖；默认输出到
`learn/data/<source>_demos/`，带 `manifest.jsonl` 去重清单。

```bash
# 国际天梯：Renown 排行榜前 10，每人最近 5 场
learn/.venv/bin/python -m learn.demo_fetch renown --leaderboard 10 --limit 5

# 国内天梯：5E 用户
learn/.venv/bin/python -m learn.demo_fetch 5e --user 11814738gjdwn7 --days 14

# 职业比赛：HLTV（下载一般需要代理/Cookie，详见 demo_fetch/README.md）
learn/.venv/bin/python -m learn.demo_fetch hltv --stars 5 --limit 10
```

完整来源、凭据、故障排查见 `learn/demo_fetch/README.md`。

### 1) 产出 match.json + analysis.json

`cs2coach run` 一次跑完 parse → tactics → report：

```bash
go build -o cs2coach ./cmd/cs2coach

# 单场
./cs2coach run testdata/demos/s2/pov.dem --out-dir out/pov

# 多场：每场一个子目录，方便 --match-json 递归找 match.json
for d in testdata/demos/s2/*.dem; do
  name=$(basename "$d" .dem)
  ./cs2coach run "$d" --out-dir "out/$name"
done
```

GOTV demo 没有 POV 录制者，需要显式给焦点玩家：
`--player <名字|SteamID64>`（先 `cs2coach players match.json` 看名单）。
POV demo 可省略，自动选录制者。

已有回合级 `samples.jsonl` 也可以先跑 v0（兼容路径）：

```bash
./cs2coach export <demo.dem|目录> -o out/samples.jsonl --recursive
```

### 2) 数据审计（不训练）

```bash
learn/.venv/bin/python learn/train_winrate.py --check --match-json out/
# T0 微探针：把审计结果落成 JSON，方便 2026-11-28 出数
learn/.venv/bin/python learn/train_winrate.py --check --match-json out/ \
  --audit-out out/winrate_audit.json
```

输出会写清：数据模式（tick / round）、样本行、回合数、比赛数、标签分布、
数据量是否达到 1000 回合门槛。**<1000 回合会明确标记「降级玩具版」，不冒充 T1。**

### 3) 训练

```bash
learn/.venv/bin/python learn/train_winrate.py \
  --match-json out/ \
  --out-dir learn/models/winrate
```

默认验收（`configs/winrate.yaml`）：

| 项 | 标准 |
| --- | --- |
| 主指标 | LightGBM AUC ≥ 「人数差+经济」逻辑回归基线 + **0.10** |
| 数据量 | 可用回合 ≥ **1000**；不足则降级，不算 T1 |
| 切分 | **按 match 分组**，同一场 demo 不会同时进 train/valid |
| 验证集 | 必须有按场次留出的 valid；只有训练集自评不算 T1 |
| 数据模式 | **tick 快照才算 v2 的 T1**；round v0 只做兼容，不冒充 T1 |

产物：

```
learn/models/winrate/
  winrate.txt          LightGBM booster
  winrate_meta.json    特征列 / 类别编码 / 指标 / 基线 / 验收 / 数据门槛
  report.md            人读报告
```

默认即使未达标也会产出模型/报告，方便诊断「差在哪」；`--strict` 会在 T1 验收
不通过时退出 2，适合放进 CI / 每周脚本，按 TRAIN-PLAN 的 kill criteria 硬停。

T2 的模型升级（小 Transformer / 序列局面模型）不在本脚本里；按 TRAIN-PLAN §3
先看 v0 的 GBDT 结果再定，继续用 GBDT 也是合法选项。

### 4) 单条预测

```bash
learn/.venv/bin/python learn/train_winrate.py \
  --out-dir learn/models/winrate \
  --predict '{"tick":88213,"round_number":7,"map":"mirage","focus_side":"CT",
              "focus_alive":3,"enemy_alive":5,"focus_money":9000,
              "enemy_money":18000,"focus_equip":12000,"enemy_equip":22000,
              "score_diff":-2,"bomb_planted":0,"round_time":62.0,
              "round_progress":0.54}'
```

### 5) T2：胜率掉点（带 tick 证据）

```bash
learn/.venv/bin/python learn/train_winrate.py \
  --drop-points --match-json out/ --top 20 \
  --out-dir learn/models/winrate \
  --drop-out out/winrate_drops.jsonl
```

每条输出形如：

```json
{"match_id":"...","round":7,
 "from_tick":87900,"from_round_time":36.9,
 "tick":88213,"round_time":41.2,
 "from_prob":0.71,"to_prob":0.42,"drop":0.29,
 "event":"kill","event_desc":"E（CT）用 M4A1 击杀 F（T）"}
```

`from_tick` 是掉点前的局面快照，`tick` 是导致变化的事件；复盘时看的是
`from_tick` 这一刻的决策，而不是事后事件本身。

Go/report 集成方式（v0）：训练侧先跑 `--drop-points` 产出 JSONL，report 层按
`match_id + round` 读取并渲染；不在 Go 里嵌 LightGBM，避免把训练依赖带进产品二进制。

这是给 tactics / 报告层的「这步决策让胜率从 X% 掉到 Y%」数据源；
LLM 只能解读这个数字，不能自己发明掉点。

人工盲评后算认同率（T2 验收 ≥60%）：

```bash
# judgments.jsonl 每行：{"index":1,"agree":true} 或
# {"match_id":"...","round":7,"tick":88213,"agree":false}
learn/.venv/bin/python learn/train_winrate.py \
  --score-drops out/winrate_drops.jsonl \
  --judgments out/judgments.jsonl \
  --min-agreement 0.60 --min-samples 20 \
  --drops-report out/drops_report.md
```

判定口径：这个 tick 前后模型胜率下降，人工判断「这是不是该改的决策点」；
认同率 ≥60% 且有效判定 ≥20 条才算通过。不通过时按 TRAIN-PLAN 的 kill criteria，
掉点输出**不得进报告**，先回退到「规则 + LLM」形态。

## 数据契约

### tick 快照（v2 主线）

`winrate_data.snapshots_from_match()` 从 `match.json`（+ 同目录 `analysis.json`）生成，
每个事件点一条，只取**截至该 tick 的事实**。
采样是**事件对齐**：回合开始 + 存活变化 + 炸弹事件 + 接敌事件，不是每 tick 全量；
逐 tick 全量需要 parse 层扩展，v0 先用 M2a 已有的事件/存活序列，避免为了采样去猜中间状态。
外部快照 JSONL 至少要有 `tick` / `round_number` / `focus_side` / `focus_alive` /
`enemy_alive` / `focus_won`；缺的字段按 0 处理，不会补事实。

| 类别 | 字段 |
| --- | --- |
| 局面 | `tick` / `round_time` / `round_progress` / `map` / `half` / `round_number` |
| 人数 | `t_alive` / `ct_alive` / `focus_alive` / `enemy_alive` / `alive_diff` |
| 经济 | 回合开始现金、冻结结束装备值、`money_diff` / `equip_diff`（M2a 没有逐 tick 资金变化，按回合开始值用，不伪造实时值） |
| 比分 | `score_before` / `score_diff` |
| 炸弹 | `bomb_planted` / `bomb_site` / `bomb_time_left` |
| 击杀 | `kills_focus` / `kills_enemy` / `first_blood_focus` / `focus_dead` |
| 道具 | 双方 flash / smoke / he / molotov 投掷数（截至 tick） |
| 接敌 | 最近一次 `DuelAnalysis` 的预瞄差 / 移动中开枪 / 先手差 / 结果 |
| 站位 | 焦点死亡后的暴露枪线 / 孤立 / 道具掩护 / 高危（来自 M2b） |

标签：`focus_won`（焦点方赢下本回合）。draw 默认丢弃；tick 模式在快照生成阶段就跳过
draw（胜率模型不该把平局当输），回合级 v0 的 `--keep-draws` 只影响旧 samples.jsonl。

**不进入特征的字段**：`winner_side` / `focus_won` / `end_reason` / `win_cost` /
`cause_tags` 等 outcome 字段；回合级的 `planted` / `defused` / `exploded` 是事后结果，
也不进特征。tick 快照里的 `bomb_planted` 是「截至该 tick 已下包」的状态，不是事后结果。
`winrate_data.LEAKAGE_FIELDS` 和测试会检查这条红线。

### 回合级 v0（兼容）

`cs2coach export` 的 `samples.jsonl` 一行一个回合，特征 = 回合开始经济/买型 +
开局对枪 + 交火前道具/存活。**不用整回合道具总数、人数优势区间、残局等事后字段**；
它只用于先把管线跑通，v2 的 T1 验收应以 tick 快照为准。

## 为什么不是 DPO / SFT 主线

TRAIN-PLAN v2 已把 DPO 降为 **T3 可选支线**（只调讲解风格，见文档附录的 schema）。
主线不再训练 LLM，原因：

- 偏好数据靠人工攒，慢且主观；回合胜负是 demo 现成的客观标签；
- 「说话像教练」不能证明「理解游戏」，胜率预测可以；
- LLM 仍受架构锁约束：只解读 tactics/模型给出的数字，不发明事实。

旧的 SFT 管线（`build_sft.py` / `train_sft.py`）保留可用，但排在胜率模型之后；
DPO 支线等 T2 结果再决定是否重建。

## 可选：A 层讲解风格 SFT（T3）

只有在胜率模型跑通、且「说话方式」成为瓶颈时才做。它不改事实层，只学怎么把
tactics/模型给出的数字讲清楚。需要额外装 MLX（不在主线 requirements 里）：

```bash
uv pip install --python learn/.venv/bin/python "mlx-lm[train]"

# 每回合 JSONL → SFT 数据（按 match 切分）
learn/.venv/bin/python learn/build_sft.py \
  --input out/rounds.jsonl --out-dir learn/data/sft

learn/.venv/bin/python learn/train_sft.py --check
learn/.venv/bin/python learn/train_sft.py
learn/.venv/bin/python learn/train_sft.py --fuse
```

底座与 LoRA 超参在 `configs/model.yaml`；数据格式、tick 引用校验、规则 teacher 的细节
以 `build_sft.py` 的 docstring 和 `tests/test_a_pipeline.py` 为准。**不要用训练 loss
判断 A 层好坏**——引用合法率和人工抽检才是验收口径。

## 自测

```bash
cd ~/code/cs2-demo-review
# 全部测试（含 demo_fetch / 旧 A/B 层）
learn/.venv/bin/python -m unittest discover -s learn/tests -v
# 只跑胜率主线
learn/.venv/bin/python -m unittest learn.tests.test_winrate_pipeline -v
```

覆盖：tick 快照生成、回合级特征、按 match 切分不泄漏、draw/缺标签过滤、
未知类别编码、AUC 门槛、无 valid 不算 T1、strict 退出码、掉点分组排序、
CLI check/dry-run。没有 LightGBM 时训练测试会 skip，数据/切分/CLI 测试仍全绿。

## 坑

- **<1000 回合**：TRAIN-PLAN §5 明确是降级玩具版；先按 2026-11-28 微探针补数据，
  不要拿 200 回合的 AUC 当 T1 结论。
- **只有一场 demo**：没有按场次留出的 valid，`t1_pass` 一定是 false；
  至少 3-5 场再谈验收。
- **GOTV 缺 PlayerHurt**：M2b 对枪指标会降级（`contact=kill`），预瞄/急停特征缺失时
  写 `-1`，不补数。
- **别按回合随机切**：同一场的回合会泄漏；`train_winrate.py` 只按 match 分组。
  旧 `train_baseline.py` 的 StratifiedKFold 就是反例，它的历史 AUC 不能当验收。
- **旧 B 层特征有事后字段**：`features.py` 里的整回合道具总数、人数优势区间、残局
  属于回合结束后才知道的信息；主线 `winrate_data.py` 已排除，旧脚本只作历史参考。
- **掉点不是因果**：它是「该 tick 前后模型概率变化」，事件描述只写事实；
  报告措辞必须保留「模型预测」而不是「必然因果」。
- **LightGBM 版本**：`eval_set` 在新版有弃用警告，不影响结果；升级时留意参数名。
