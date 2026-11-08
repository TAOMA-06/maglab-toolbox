# cs2coach — CS2 AI 分析教练（赛后复盘）

> 2026-11-08：这份引擎现在在 maglab-toolbox 仓库的 `cs2coach/`。在本目录里执行 `go test` 和 `go run`。工具箱的「赛后」仍是上一级的 `coach/index.html`，不要用生成结果覆盖它。

一句话定位：**赛后复盘教练，不是统计网站。** 丢进一个 `.dem`，得到「这场哪几个回合不该丢、下把具体怎么改」的一页教练报告（每条结论强制带回合号+tick 证据），并且可以对这个 demo 追问。

本地 CLI/本地页，不做网站、不做实时教练、不做视频理解（取舍依据见 work-kb `projects/cs2-demo-review/design.md`）。

## 项目现状（2026-11-06）

| 线 | 状态 | 位置 |
| --- | --- | --- |
| Go 产品：解析/战术/教练三层 | 可用，`cs2coach` 二进制可跑 | `cmd/cs2coach`、`internal/` |
| M2 站位 + 对枪分析 | 代码已落地（`m2.go`），阈值已校准，待 3 场用户验收（M2c 截止 12-12） | `internal/tactics/m2.go`、`docs/M2-*.md` |
| Wails 图形版（Windows exe） | 可交叉编译，详见下 | `cmd/cs2coach-gui` |
| 胜率模型 T0 数据探针 | 待跑：67 个本地 demo，还没批量转 `match.json`（T0 微探针截止 11-28，门槛 1000 回合） | `learn/` |
| 胜率模型 T1（LightGBM） | 待 T0 出数后训，CPU 几分钟，**本地跑** | `learn/train_winrate.py` |
| A 层讲解 SFT（可选支线） | 脚本保留，排在胜率模型之后 | `learn/build_sft.py`、`learn/train_sft.py` |
| Colab GPU 训练 | **已暂停**：改走本地训练；`learn/colab/` 保留备用 | `learn/colab/` |

路线总纲：`PLAN.md`（第一期 M2 双轨）、`TRAIN-PLAN.md`（v2 预测式理解：胜率模型主线，DPO 仅可选支线）。

## 图形版（Windows exe）

`cmd/cs2coach-gui` 是 Wails 图形版：选 demo → 选玩家 → 归因 → 报告预览 → 追问，全流程覆盖 CLI 功能，设置页本地保存 LLM Key。

```bash
./scripts/build-windows.sh            # Mac 上交叉编译出 dist/cs2coach-gui.exe（需 brew install mingw-w64）
```

双击即用（目标机需 WebView2，Win10/11 自带）。流程：选 `.dem` → 探针列出玩家（POV 自动选中录制者）→ 解析 → 归因 → 报告（纯事实 / LLM / prompt 落盘+回填）→ 追问。

> 未签名说明：exe 没有商业代码签名证书，Windows 首次运行会弹 SmartScreen，
> 点“更多信息 → 仍要运行”即可；介意的话可自购证书签名后分发。

## 三层管线

| 层 | 包 | 职责 |
| --- | --- | --- |
| 解析层 | `internal/parse` | demoinfocs-golang v5 把 `.dem` 解析成结构化事实 JSON（回合时间线、击杀/武器/位置/tick、道具投掷与效果、经济、开局对枪、存活序列）。**只提事实。** |
| 战术层 | `internal/tactics` | 纯规则计算教练素材：丢分回合归因（首杀白给/trade 失败/道具缺口/经济局强起/残局决策）、可疑回合候选（带 tick 区间）、补枪/买型/残局判定。**数字和证据永远由这层产生。** |
| 教练层 | `internal/coach` | LLM 只解读不发明：输入结构化切片+游戏知识，输出教练报告+追问回答；`ValidateCitations` 强制校验每条结论的回合+tick 引用，无证据论断直接删。 |

## 站位 / 对枪分析（M2）

报告在原有「丢分回合 Top3」之外新增两节，全部由规则层产出、带 tick 证据：

- **站位分析**：焦点玩家每次死亡的暴露枪线（视锥近似，非视线遮挡）、最近队友距离、
  死亡前 10s 内己方烟/火掩护、死亡热区 Top3。高危规则（2026-11-08）：
  `有掩护数据且无掩护 + 队友还活着但距离远`；暴露枪线单独不算高危；
  队友全死（残局）与无道具数据都不单独判高危。
- **对枪分析**：接敌瞬间预瞄角度差、开枪瞬间水平速度（急停）、先手命中、命中数、
  对枪结果；大于 90° 记被侧身或背身，不算没预瞄；并输出阈值校准用的指标分布。

阈值集中在 `internal/tactics/m2.go`，2026-11-01 用公开 demo 基线校准（预瞄 15° /
开火速度 100 u/s），依据见 `docs/M2-BASELINE.md`。

> 数据口径：**对枪分析优先用 POV 自存 demo**。部分 GOTV demo 不含 `PlayerHurt` 游戏事件，
> 对枪会降级为「仅击杀」快照（报告/JSON 里 `contact=kill`），命中数无法统计；
> 这是数据源限制，不伪造。站位分析不受影响。

## 安装与构建

要求：Go ≥ 1.27（`brew install go`）。国内网络先把模块代理设为 goproxy.cn：

```bash
go env -w GOPROXY=https://goproxy.cn,direct
go build -o cs2coach ./cmd/cs2coach
```

## 快速开始（复盘）

```bash
# 一条龙：解析 → 归因 → 报告（无 LLM 时产出纯事实版报告，仍然可用）
# 注意：只有 POV 自存录像能自动认出「你」；官匹 GOTV 下载的 demo 必须加 --player
./cs2coach run /path/to/your.dem --out-dir out --player 你的名字

# 用 LLM 生成教练解读（默认 claude-opus-5-5，经官方 Anthropic SDK）
export ANTHROPIC_API_KEY=sk-ant-...        # 或已 ant auth login
./cs2coach run /path/to/your.dem --out-dir out --player 你的名字 --llm
# --llm 要求了 LLM 但不可用（未配置/调用失败）时：仍产出纯事实报告，但退出码为 4，
# 以便脚本区分「真解读」与「降级版」。

# 不想配置 API：把提示词落盘，人工粘贴给任意 LLM，再回填解读文件
./cs2coach run /path/to/your.dem --out-dir out --player 你的名字 --prompt-only
# 把 LLM 返回的解读存成 interpret.txt，然后：
./cs2coach report out/analysis.json --out-dir out --interpret interpret.txt

# 追问（纯事实或带 LLM）
./cs2coach ask out/analysis.json "第2回合我为什么先死？"
./cs2coach ask out/analysis.json -i --llm    # 交互模式（exit/quit/q/退出 或 Ctrl-D 结束）

# M2b 指标分布（预瞄角度差 / 开火速度），用于阈值校准
./cs2coach metrics out/analysis.json other/analysis.json

# 分步执行
./cs2coach parse  demo.dem -o match.json --player 你的名字
./cs2coach analyze match.json -o analysis.json
./cs2coach report analysis.json --out-dir out --llm
./cs2coach players match.json   # 列出玩家，帮 --player 选焦点（名字含空格/特殊字符时加引号，SteamID 更可靠）
```

产物（`out/`）：`report.md`（一页教练报告）、`report.html`（自包含本地页）、`match.json`（事实层）、`analysis.json`（战术层）、`prompt.txt`（提示词，可复用/审计）。

## 快速开始（本地训练：胜率模型主线）

先看 `learn/README.md`（完整版），这里是 3 步最小路径。依赖隔离在 `learn/.venv`，不污染系统 Python：

```bash
# 0) 建 venv 装依赖（一次性）
uv venv --python 3.12 learn/.venv
uv pip install --python learn/.venv/bin/python -r learn/requirements.txt

# 1) 批量产出 match.json（本地 67 个 demo 在 learn/data/{5e,hltv,renown}_demos/）
for d in learn/data/5e_demos/*.dem; do
  name=$(basename "$d" .dem)
  ./cs2coach run "$d" --out-dir "out/$name" --player 你的名字
done

# 2) 数据审计：先看有没有 1000 回合，不过不硬训
learn/.venv/bin/python learn/train_winrate.py --check --match-json out/ \
  --audit-out out/winrate_audit.json

# 3) 训练（LightGBM，CPU 分钟级；--strict 把 AUC/数据量/切分验收变硬门槛）
learn/.venv/bin/python learn/train_winrate.py \
  --match-json out/ --out-dir learn/models/winrate
```

验收口径（`learn/configs/winrate.yaml`）：AUC ≥「人数差+经济」基线 +0.10、回合 ≥1000、
按场次切分（同场不进 train/valid 两边）、必须 tick 快照模式。详见 `TRAIN-PLAN.md` §2–§3
与 `learn/README.md` 数据契约（泄露字段红线）。

拉更多 demo 用 `learn/demo_fetch`（Renown/5E/HLTV，见 `learn/demo_fetch/README.md`）。

## 如何回看 tick

报告里的 `[tick N]` / `demo_gototick N` 是证据锚点：复制 tick，在 CS2 里打开该 demo 回放（`playdemo`），
先在设置里开启开发者控制台，然后在控制台输入 `demo_gototick N` 即可跳到对应时刻。`report.html` 里的 tick 徽章可一键选中复制。

## 焦点玩家

报告默认写「你」：`--player 名字|SteamID64` 手动指定；省略时自动取 POV demo 的录制者（CS2 官匹/优先单的自存 demo 都是 POV）；两者都没有时报错并列出玩家名单。

## LLM 配置

| 环境变量 | 说明 |
| --- | --- |
| `CS2COACH_LLM_PROVIDER` | `anthropic`（默认）、`openai`（OpenAI 兼容，可指 DeepSeek/Kimi 等，仅用于非 Claude 模型）或 `ollama`（本机 Ollama） |
| `CS2COACH_LLM_API_KEY` | 显式密钥（优先）；否则 anthropic 读 `ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN`/ant profile，openai 读 `OPENAI_API_KEY`；**ollama 不需要** |
| `CS2COACH_LLM_BASE_URL` | 自定义网关；ollama 缺省 `http://localhost:11434` |
| `CS2COACH_LLM_MODEL` | 默认 anthropic: `claude-opus-5-5`；openai: `gpt-4o-mini`；ollama: `llama3.2`（本机没装时自动改用第一个已装模型） |
| `CS2COACH_OLLAMA_THINK` | 仅 ollama：`1` 保留思考模型推理（更慢，实测一句话提示 127s vs 9s），默认关闭 |

本地 Ollama（数据不出本机，无需 API key）：

```bash
ollama list                                   # 看已装模型，如 jia:latest / wen:latest / luo:32b
export CS2COACH_LLM_PROVIDER=ollama
export CS2COACH_LLM_MODEL=jia:latest          # 可省略：缺省按已装模型自动挑
./cs2coach report analysis.json --out-dir out --llm
```

Ollama 路径优先 `POST /api/chat`（`stream:false`，老版本无该端点时回退 `/api/generate`），
并把 `num_ctx` 显式放大到 16384——Ollama 缺省的 4k 上下文会把长提示词从开头静默截断，
system 里的「只解读不发明」锁被吃掉后输出过不了引用校验。模型不存在时会列出本机已装模型。

证据架构锁：LLM 输出经 `ValidateCitations` 校验——引用的回合号必须存在、tick 必须落在对应回合区间，含「建议/应该/必须」但无回合引用的句子会被删除。报告页脚会声明这一机制。

## 测试

```bash
go vet ./... && go test ./...                     # Go 全量
learn/.venv/bin/python -m unittest discover -s learn/tests -v   # Python 全量
learn/.venv/bin/python -m unittest learn.tests.test_winrate_pipeline -v  # 只跑胜率主线
```

`testdata/demos/` 内含公开测试 demo（demoinfocs 回归集，CS2 时期），用于集成验证；**请用你自己的 demo 做真实复盘**（设计文档的 M0/M1 验收都基于「自己的 demo」）。

## 目录

```
cmd/cs2coach/       CLI 入口
cmd/cs2coach-gui/   Wails 图形版入口
internal/parse/     解析层（demoinfocs 封装）
internal/tactics/   战术层（规则引擎，含 M2 站位/对枪 m2.go）
internal/coach/     教练层（LLM + 引用校验）
internal/report/    Markdown/HTML 渲染
internal/qa/        追问检索
internal/knowledge/ 游戏知识（经济规则/地图默认，内嵌）
internal/model/     数据契约（Match / Analysis，特征 schema 唯一来源）
internal/export/    samples.jsonl 导出（回合级 v0 兼容）
internal/labels/    标注相关
gui/                Wails 前端+后端胶水
learn/              训练线（胜率主线，见 learn/README.md）
  winrate_data.py / train_winrate.py / configs/winrate.yaml
  demo_fetch/       demo 采集（HLTV / 5E / Renown / PWA…）
  build_sft.py / train_sft.py   A 层讲解 SFT（可选支线）
  colab/            Colab GPU 版（已暂停，保留备用）
  data/{5e,hltv,renown}_demos/  本地 67 个 demo
  models/           训练产物（winrate/ 待产出）
docs/               CONTRACTS.md（包间契约）/ M2-BASELINE.md / M2-DESIGN.md / M2-SPOTCHECK.md
testdata/           公开测试 demo + 回归数据
scripts/            构建/拉 demo 脚本（含 build-windows.sh、fetch-demos.sh）
PLAN.md             第一期执行计划（M2 双轨）
TRAIN-PLAN.md       训练路线 v2（预测式理解）
```
