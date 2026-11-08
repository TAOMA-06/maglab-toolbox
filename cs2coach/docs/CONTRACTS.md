# 包间契约（CONTRACTS）— 冻结接口，实现方不得改动签名

模块：`cs2-demo-review`（Go 1.27，唯一外部依赖 `github.com/markus-wa/demoinfocs-golang/v5 v5.2.0`，GOPROXY 已设为 goproxy.cn）

数据类型一律用 `internal/model`（已写完，只许 import 不许改）。
设计锁（design.md）：**解析层只提事实；数字和证据永远由 tactics 层产生；LLM 只解读不发明。**

---

## internal/parse —— 解析层

```go
package parse

// Options 控制解析行为。
type Options struct {
    FocusSteamID string // 焦点玩家 SteamID64 十进制字符串；空则自动：POV 玩家 > 报错列出名单
    FocusName    string // 按名字匹配（忽略大小写，精确匹配优先）；与 FocusSteamID 二选一
}

// Parse 解析 .dem 文件，返回完整事实 Match。
func Parse(demoPath string, opts Options) (*model.Match, error)

// ParseToFile 解析并写 JSON（缩进 2 空格）。
func ParseToFile(demoPath, outPath string, opts Options) error
```

### demoinfocs-golang v5.2.0 API 要点（已核实，照抄可用）

```go
import (
    demoinfocs "github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs"
    events "github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/events"
    common "github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/common"
)

err := demoinfocs.ParseFile(path, func(p *demoinfocs.Parser) error {
    p.RegisterEventHandler(func(e events.Kill) { ... })
    return nil
}) // ParseFile 自动 ParseHeader + ParseToEnd
```

- `p.GameState() GameState`：`IngameTick() int`（ingame tick，demo_gototick 用）、`IsWarmupPeriod() bool`、`TeamTerrorists()/TeamCounterTerrorists() *common.TeamState`、`Participants()`（`.All()/.Playing()/.TeamMembers(team)`）、`TotalRoundsPlayed()`
- `p.CurrentTime() time.Duration`（demo 相对时间）、`p.TickRate() float64`、`p.CurrentFrame() int`（demo frame，与 ingame tick 不同，可另存）
- `common.Player` 字段：`SteamID64 uint64`、`Name string`、`Team common.Team`、`IsBot`、`IsConnected`
  方法：`GetTeam()`、`Money() int`、`Health() int`、`Armor() int`、`Position() r3.Vector`、`ActiveWeapon() *Equipment`、`Weapons() []*Equipment`、`FlashbangCount() uint64`、`HasHelmet() bool`、`HasDefuseKit() bool`、`EquipmentValueCurrent()/EquipmentValueRoundStart()/EquipmentValueFreezeTimeEnd() int`
- `common.TeamState`：`Score() int`、`MoneySpentThisRound() int`、`RoundStartEquipmentValue()/FreezeTimeEndEquipmentValue() int`、`Members() []*Player`
- `common.Team`：`TeamTerrorists = 2`、`TeamCounterTerrorists = 3`；`common.Equipment`：`.Type`（`common.EqAK47` 等 EquipmentType）、`.String()` 返回 "AK-47" 这类名、`.Class()` 返回 `EqClassRifle` 等
- `r3.Vector` 是 `github.com/go-gl/mathgl/v32/r3` 的 `Vector{X,Y,Z float32}`（由 demoinfocs 传递依赖提供，go.mod 自动引入）

事件（events 包，字段已核实）：

| 事件 | 关键字段 |
|---|---|
| `RoundStart` | —（用 `gs.IsWarmupPeriod()` 过滤热身） |
| `RoundFreezetimeEnd` | — |
| `RoundEnd` | `Winner common.Team`、`Reason RoundEndReason`、`WinnerState/LoserState *TeamState`（注意：`TeamState.Score()` 此刻**未**+1，需自己累计比分） |
| `RoundEndOfficial` | —（回合真正结束，EndTick 用它，拿不到再退回 RoundEnd） |
| `Kill` | `Killer, Victim, Assister *common.Player`（可能 nil）、`Weapon *common.Equipment`、`IsHeadshot, ThroughSmoke, NoScope, AttackerBlind, AssistedFlash bool`、`PenetratedObjects int`、`Distance float32` |
| `PlayerHurt` | `Player, Attacker *common.Player`、`HealthDamage int`、`HitGroup` |
| `WeaponFire` | `Shooter *common.Player`、`Weapon *common.Equipment` |
| `GrenadeProjectileThrow` | `Projectile *common.GrenadeProjectile`（`.Thrower`、`.WeaponInstance`、`.Trajectory`、`.EntityID`、`.UniqueID()`） |
| `GrenadeProjectileDestroy` | `Projectile`（销毁 tick） |
| `HeExplode / FlashExplode / SmokeStart / SmokeExpired / DecoyStart / DecoyExpired` | 都嵌 `GrenadeEvent{GrenadeType, Grenade, Position, Thrower, GrenadeEntityID}` |
| `InfernoStart / InfernoExpired` | `Inferno *common.Inferno`（火；CS2 优先用它而非 FireGrenadeStart） |
| `PlayerFlashed` | `Player, Attacker *common.Player`、`Projectile *common.GrenadeProjectile`、`FlashDuration() time.Duration` |
| `BombPlantBegin / BombPlanted / BombDefuseStart / BombDefused / BombExploded / BombDropped / BombPickup` | `BombEvent{Player, Site Bombsite, Position}`（`events.BombsiteA/BombsiteB`） |
| `POVRecordingPlayerDetected` | `Player *common.Player` —— POV demo 的录制者 |
| `TeamSideSwitch` | 半场换边 |
| `MatchStart` | — |

`RoundEndReason` 映射：`RoundEndReasonTargetBombed→bomb_exploded`、`RoundEndReasonBombDefused→bomb_defused`、`RoundEndReasonCTWin→elimination`、`RoundEndReasonTerroristsWin→elimination`、`RoundEndReasonTargetSaved→time`、`RoundEndReasonDraw→draw`、`RoundEndReasonTerroristsSurrender/CTSurrender→surrender`，其余→other。

### parse 层职责细则（只提事实！）

1. 过滤热身（`IsWarmupPeriod()` 为 true 时的 RoundStart/RoundEnd 不计）。回合号从 1 递增。
2. **比分自己累计**：RoundEnd 时 winner +1，写入 `ScoreAfter`（注意 RoundEnd 注释里 Score() 未更新）。
3. 每回合在 **RoundFreezetimeEnd** 时点采样经济：`TeamState.FreezeTimeEndEquipmentValue()`、`MoneySpentThisRound()`、每人的 `Money()`（RoundStart 时读）、`EquipmentValueFreezeTimeEnd()`、`HasHelmet/HasDefuseKit`、`Weapons()` 名字列表、队伍 `RoundStartEquipmentValue()`。`SideEconomy.Money` = 回合开始全员现金合计。
4. 击杀：记录双方 id/名/边、武器名/类、爆头/穿烟/盲狙/穿墙(`PenetratedObjects>0`)、距离、双方坐标（`Position()`）。世界击杀（Killer nil）killer_* 留空。
5. 道具：以 `GrenadeProjectileThrow` 为「一次投掷」（tick/投掷者/投掷点）；用 `HeExplode/FlashExplode/SmokeStart/InfernoStart` 等补 `EffectStart`，`SmokeExpired/InfernoExpired` 等补 `EffectEnd`，落点 `land_pos` 用效果事件的 `Position`（没有就用 projectile 最后位置）。闪光只记 **敌方** 被闪玩家（`PlayerFlashed` 按 `Projectile` 关联到投掷）：`Flashes[]` + `Duration`（秒）。HE 伤害：同投掷者 2 秒内的 `PlayerHurt`（`Attacker==Thrower` 且武器为 he/世界炸）累加 `HEDamage`。
6. 炸弹事件按上表写 `BombEvent`（含 Site、坐标）。
7. `Alive` 序列：回合开始记录一次 {10?5+5} → 实际按双方存活数记；**每次击杀后**追加一条 {tick, t_alive, ct_alive}。
8. `RoundPlayer`：从击杀/伤害事件聚合每人 kills/deaths/assists/damage；`FirstKill/FirstDeath` = 本回合第一个击杀的凶手/死者；死亡 tick/时间/坐标；死亡时 `UtilityAtDeath` = 死亡时刻身上 flash+smoke+he+molotov 数（死亡事件里读 Victim 的 Weapons()）。
9. POV 检测：`POVRecordingPlayerDetected` → `Meta.POVPlayer`。焦点解析优先级：`Options.FocusSteamID` > `Options.FocusName` > POV 玩家 > **报错**（错误信息里列出全部玩家的 name+steamid64 帮用户选）。`Meta.FocusPlayer/FocusPlayerName` 写入最终选择。
10. `Meta.Map` 从 header 或 `common.Map`（若 v5.2.0 无 Map 字段则用 demo 文件名/空字符串——不许编造地图名）。tick_rate 用 `p.TickRate()`，总时长 `p.CurrentTime()`。
11. 所有 tick 一律 **ingame tick**（`gs.IngameTick()`）。`round_time` = (tick - round.StartTick) / tick_rate。

---

## internal/tactics —— 战术层（纯规则，禁止 import 任何 LLM/网络包）

```go
package tactics

type Options struct {
    TradeWindowSec float64 // 默认 5.0
}

// Analyze 从 Match 计算全部教练素材。焦点玩家取 m.Meta.FocusPlayer。
func Analyze(m *model.Match, opts Options) (*model.Analysis, error)

// SaveJSON / LoadJSON 便捷序列化。
func SaveJSON(a *model.Analysis, path string) error
func LoadJSON(path string) (*model.Analysis, error)
```

### 规则细则（数值阈值必须以常量集中定义并写注释）

- **补枪窗口** `TradeWindowSec = 5.0`：A 死后 5s 内 A 的队友杀掉凶手 = 成功补枪（TradeEvent / OpeningInfo.TradedWithinWindow / FocusDeathInfo.Traded / PlayerMatchStats.TradedDeaths/UntradedDeaths/TradeKills）。
- **买型**（按 `SideEconomy.EquipValueFreezeEnd / 人数` 均值）：`avg ≥ 4750 → full`；`2500 ≤ avg < 4750 且 MoneySpent > 0 → force`（若 avg ≥ 3800 记 `half`）；`avg < 2500 → eco`。FocusBuy = 焦点玩家所在边的买型。
- **归因**（焦点方**丢分**回合打标签，可多标；每标附 `CauseDetail[tag]` 一句话证据，含数字）：
  1. `first_blood_gift`：焦点方首杀被对方拿到 **且** 未在窗口内补枪（若焦点玩家本人首死且未被补 → 标签保留，detail 注明「焦点玩家首死」）。
  2. `trade_fail`：本回合焦点方 ≥1 次「队友死亡后 5s 内没补掉凶手」，且死亡发生在人数还均等/占优时（排除 eco 大溃败）。
  3. `utility_gap`：`UtilBeforeContact` 焦点方 < 敌方 × 0.5，或焦点玩家死亡时 `UtilityHeld ≥ 2`（带着道具死）。
  4. `eco_force`：焦点方买型为 force/half 且对面 full（强起撞墙），或 eco 强行接团被横扫。
  5. `clutch_throw`：进入对焦点方有利的残局（人数占优或 `Advantageous`）但输掉。
  6. `man_advantage_lost`（附加标）：回合中焦点方曾有 ≥1 人数优势持续 ≥5s（见 ManAdvSpan）且最终丢分。
- **WinCost ∈ [0,1]**（Top3 排序用）：有利残局丢=1.0；有人数优势被翻=0.5+0.1*最大优势差（封顶 0.9）；首杀白给未补=0.6；trade_fail=0.4+0.05*次数（封顶 0.8）；道具缺口=0.35；eco/force 撞墙=0.15。多标签取 max。写清公式注释。
- **Clutch**：从 Alive 序列找双方都 ≤2 的最早 tick → 局面 `situation`（焦点侧 vs 敌侧，如 `1v2`）；结果=回合 winner。`Advantageous` = 残局开始时焦点侧人数 > 敌侧（注：初版另有「人数相等+HP」条件，但 Match 事实无残局开始时 HP，该条件暂无事实来源、未实现）。
- **残局人数/优势区间** ManAdvSpan：扫描 Alive 序列，凡焦点方存活数 − 敌方 ≥1 且持续 ≥5s 记一段；`Threw` = 该优势段后最终丢分。
- **点位标签** `FocusDeathInfo.PosLabel` / Evidence 里用：先用 `Analysis.SiteAnchors`（下包/拆包坐标锚，半径 800 units 内 → `A 点附近`/`B 点附近`）；否则格式 `坐标(x,y)`。**禁止编造点位名。**
- **Evidence**：每回合至少挂 3 条（首杀、焦点玩家死亡或关键击杀、经济/道具一句），Text 用中文事实句（含人名/武器/数字/tick 可回溯）。
- **Suspect**：丢分回合按 WinCost 降序取前 5（WinCost 为 0 也照取，不截断）+ 焦点玩家**全场** `FirstDeaths ≥ 3` 时补入未入选的首死丢分回合（最多补 3 个，防 prompt 膨胀）；每条给 `TickRange`（回合 start~end）与 tags。**Suspects 的顺序即 Top3 报告顺序（取前 3）。**
- **GoodRound**：取焦点方胜回合中焦点玩家贡献最高（kills*1 + clutch_win*3 + first_kill*1）者；Highlight 写事实。
- **LostRoundCauses**：五类标签（+man_advantage_lost 可选统计）在全部丢分回合的频次与占比，按 count 降序。
- **Story**：FinalScore、上下半场比分（用 Half 字段分割）、胜负回合号列表、`WonByBuy/LostByBuy`、连丢/连胜 ≥3 的节点写入 KeySwings（如「第 11-14 回合连丢 4 分」）。

---

## M2 扩展契约（站位 / 对枪，2026-11-01 冻结）

> 实现细则见 `docs/M2-DESIGN.md`。以下类型已写入 `internal/model`，签名/字段名不得再改。

### parse 层新增事实

- `Round.Shots []ShotFact`：每次**枪械** `WeaponFire` 一条（tick / round_time / shooter / weapon /
  pos / yaw / pitch / speed / speed_valid）。刀挥/投掷物出手不算开枪，必须排除。
  - `Yaw` 取 `Player.ViewDirectionX()`（0..360），`Pitch` 取 `ViewDirectionY()`。
  - `Speed` = 相邻 `FrameDone` 采样帧的水平位置差 / 时间差（u/s）；
    无上一帧、瞬移（>2000 u/s）、dt<=0 → `SpeedValid=false`，speed=0。
- `Round.Duels []DuelFact`：两名敌对玩家的一次连续接敌（只由枪械/刀伤害或击杀触发，
  手雷/世界伤害不算）。
  - 接敌快照 = 首次开火前 1s 内最近一次 `WeaponFire` 的 pos/yaw/pitch/speed；
    没有开火则取首次 `PlayerHurt` 时刻的当前值（`FirstShotTick=0`）。
  - `DuelFact.Contact` = `hurt`（首次伤害触发，接敌瞬间）| `kill`（缺枪械伤害事件的降级）。
  - `DuelFact.FirstHurtBy` = 本次接敌第一个造成武器伤害的玩家 ID（先手命中事实）。
  - `DuelPlayerFact.TargetPos` = 该侧首次开火瞬间对手的位置（预瞄角度差的几何依据）。
  - 一侧若在接敌开始后才首次对准对手开火，需补记该侧首枪快照（不能永远 `FirstShotTick=0`）。
  - A/B 按先开火者在前；都未开火按 SteamID 字符串升序。
  - 同一对玩家最后一次活动后 3s 无新活动 → 结束（`EndTick`），再次交火算新接敌。
  - `KillTick!=0` 表示接敌窗口内出现击杀，`KillerID/VictimID` 为事实。
  - **parse 不得计算预瞄差、急停判定、胜负归因。**
- `KillEvent.VictimYaw/VictimPitch`：受害者死亡瞬间视角。
- `KillEvent.OtherAlive []PlayerSnapshot`：除受害者外全部存活玩家的
  {steamid64, name, side, pos, yaw, pitch, hp} 快照。

### tactics 层新增输出

- `Analysis.Positioning *PositioningReport`：Deaths / HighRiskCount / HighRiskRate /
  ExposedDeaths / NoCoverDeaths / IsolatedDeaths / HotZones（top3）/ Thresholds。
- `Analysis.Dueling *DuelingReport`：Total / Measured（预瞄可评估）/ Fired（焦点开火、急停可测）/
  KillOnly（仅击杀快照的 GOTV 降级数）/ PreAimFailures / MovingShots / FirstShotWins /
  FirstHitterFocus / FocusKills / FocusDeaths / Distribution / Thresholds。
- `RoundAnalysis.Positioning *PositionDeathAnalysis`：焦点死亡回合的逐次站位指标
  （ExposedLines / ExposedBy / NearestTeammateDist / Isolated / LastAlive /
  CoverEvaluated / CoverUtility / HighRisk / RiskReasons，全部带 tick 或数字）。
- `RoundAnalysis.Duels []DuelAnalysis`：焦点参与的对枪
  （Contact / PreAimMeasured / PreAimDeltaDeg / FocusSpeed / FirstShotDiffSec / FirstHitter /
  命中数 / Outcome / Verdict）。焦点没开火也评预瞄（接敌瞬间准星），但急停只在焦点开火时评。
- 阈值必须是 `internal/tactics` 里的具名常量，并原样写进 `Thresholds`：
  - 站位：`ExposureMaxDistance=2500`、`ExposureConeHalfAngle=45`、
    `IsolatedTeammateDist=800`、`CoverRadius=200`、`CoverWindowSec=10`、`HotZoneRadius=400`。
  - 对枪：`PreAimBadDeg`、`MovingFireSpeedUPS`（M2b 用实测分布校准，只改常量）。
- 高危站位规则（2026-11-08）：
  `HighRisk = CoverEvaluated && !CoverUtility && Isolated`。
  暴露枪线单独不算高危（`exposed_deaths` 仍计数，但不据此叫高危）。
  队友全死记 `LastAlive`，不算 `Isolated`；无道具事实时 `CoverEvaluated=false`，
  不得用 `CoverUtility` 的默认 false 判「无掩护」。
- 对枪预瞄：`PreAimFailed = measured && raw > PreAimBadDeg && raw <= PreAimFlankDeg`；
  `Flanked = measured && raw > PreAimFlankDeg`（大于 90° 是被侧身或背身，不算没预瞄；正好 90° 仍算预瞄失败）。
- 「暴露枪线」是**视锥近似**（存活敌人 + 距离 + 敌人朝向对准你），不是视线遮挡；
  输出文案必须带「近似」。
- 分位统计用最近秩；样本为 0 时输出 0。

### 报告 / CLI 新增

- `report.Markdown/HTML` 在判决 / 好回合后输出 `## 站位分析`、`## 对枪分析` 两节；
  纯事实版与 LLM 版都必须有。HTML 与 Markdown 共用同一 `page` 事实。
  报告不再输出「丢分原因频次」；`LostRoundCauses` 仍留在分析数据里。
  split 按再死间隔（`SplitCloseSec`=10 秒）分「紧接着 / 隔开了」两种说法；
  第一屏在整场句后有一条「移动中开枪」改法；对枪明细仍留在后面的「对枪分析」。
- `coach.ReportPrompt` 的 system 固定结构追加 `## 站位解读`、`## 对枪解读`（在「好回合」后、
  「追问建议」前）；user 数据加入两模块事实与分布；引用仍受 `ValidateCitations` 校验。
- `qa.Retrieve` 关键词追加 positioning（站位/死位/枪线/暴露）与 dueling（预瞄/急停/对枪/准星）。
- 新 CLI：`cs2coach metrics <analysis.json> [more...]` 打印 M2b 指标分布（Markdown 表）。

## internal/coach —— 教练层（LLM）

```go
package coach

// Client 是最小 LLM 接口。
type Client interface {
    Complete(ctx context.Context, system, user string) (string, error)
    Model() string
}

// NewClientFromEnv 按环境变量构造（见下）；未配置密钥返回 ErrNotConfigured。
func NewClientFromEnv() (Client, error)

// ReportPrompt 生成教练报告的 system+user 提示词（纯函数，可 --prompt-only 落盘）。
func ReportPrompt(a *model.Analysis) (system, user string)

// QAPrompt 生成追问回答的提示词。
func QAPrompt(a *model.Analysis, slice *model.QASlice) (system, user string)

// ValidateCitations 校验报告里的回合/tick 引用是否存在于 Analysis；
// 移除或标注无证据论断（架构锁：LLM 只解读不发明）。返回净化后的文本与错误列表。
func ValidateCitations(report string, a *model.Analysis) (string, []CitationError)

type CitationError struct {
    Line int    `json:"line"`
    Text string `json:"text"`
    Why  string `json:"why"`
}
```

环境变量（优先级从高到低）：
- `CS2COACH_LLM_PROVIDER` = `anthropic` | `openai` | `ollama`（缺省 `anthropic`）
- `CS2COACH_LLM_API_KEY` > `ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN` / `OPENAI_API_KEY`（`ollama` 不需要密钥）
- `CS2COACH_LLM_BASE_URL`（anthropic 缺省 `https://api.anthropic.com`；openai 缺省 `https://api.openai.com/v1`——openai 兼容可指 DeepSeek/Kimi 等，只用于**非 Claude** 模型；ollama 缺省 `http://localhost:11434`）
- `CS2COACH_LLM_MODEL`（anthropic 缺省 `claude-opus-5-5`；openai 缺省 `gpt-4o-mini`；ollama 缺省 `llama3.2`，本机没装则自动改用第一个已装模型）
- `CS2COACH_OLLAMA_THINK`（仅 ollama：`1` 保留思考模型推理；缺省关闭——思考会让本地模型慢十几倍，300s 内跑不完一局报告）

**Anthropic 路径必须用官方 SDK** `github.com/anthropics/anthropic-sdk-go`（已入 go.mod）：
```go
client := anthropic.NewClient() // 自动解析 ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN / ant profile
resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
    Model: "claude-opus-5-5", MaxTokens: 16000, // 非流式安全上限
    System: []anthropic.TextBlockParam{{Text: system}},
    Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(user))},
})
// Content 遍历：block.AsAny().(anthropic.TextBlock).Text
// StopReason == anthropic.StopReasonRefusal 时优雅降级（报告被拒原因），不要当普通文本处理
```
- 模型缺省 `claude-opus-5-5`（可用 `CS2COACH_LLM_MODEL` 覆盖）；Thinking 参数留空（Opus 5.5 默认 adaptive）。
- OpenAI 兼容路径（非 Claude 模型，如 DeepSeek/Kimi）用标准库 `net/http` 调 `POST {base}/chat/completions`，单独文件实现，**禁止**用它调 Claude。
- Ollama 路径（`ollama.go`，本地模型，无密钥）用标准库 `net/http` 调 `POST {base}/api/chat`：`stream:false`、`think` 缺省 false、`options.num_ctx=16384`（Ollama 缺省 4k 会静默截断长提示词）；老版本没有 `/api/chat` 时回退 `POST {base}/api/generate`（system 单列）。显式 `CS2COACH_LLM_MODEL` 优先；未设置时按 `GET {base}/api/tags` 自动挑（缺省名存在就用它，否则用第一个已装模型）。模型不存在时列出已装清单，自动模式下换一个已装模型重试一次；连不上时提示 `ollama serve`。
- 三路径共用 `Client` 接口；超时 300s（`option.WithRequestTimeout` / `http.Client.Timeout`），429/5xx 重试 2 次指数退避。

### 报告提示词要求（中英混合指令，输出中文）

- system：你是 CS2 赛后复盘教练。**只允许解读给定数据，禁止发明任何事件/数字**；每条结论必须带 `回合R [tick T]` 引用，无证据的判断直接删。语气：直接、可执行、不客套。输出 Markdown，结构固定：
  1. `**判决**`：规则层整场句 + 分组（要你改的 / 分开的 / 其余）；LLM 不得盖掉开场判决
  2. `## 值得保留的好回合`
  3. `## 追问建议`：2-3 个可追问的问题示例
- user：焦点画像 + Story + LostRoundCauses（分析数据仍带；报告页不再渲染频次表）+ 按 Suspects 顺序的回合切片（RoundAnalysis + Evidence + 经济/击杀原文数据）+ 游戏知识（CS2 经济规则：胜 3250/下包胜 3500/拆包胜 3500、败方补偿 1400+500×连败（封顶 3400）、下包全队 +800、金钱上限 16000；残局与 trade 常识）。
- **ValidateCitations**：正则抽取 `回合\s*(\d+)` 与 `tick\s*[:=]?\s*(\d+)` / `demo_gototick\s+(\d+)`；校验回合号在 1..N、tick 落在对应回合 TickRange（放宽 ±2×tick_rate）。非法引用：整段替换成 `【无证据，已删】` 并记 CitationError。无引用的判断句（含「应该/需要/必须/建议」且无 `回合`）也删。

---

## internal/report —— 渲染层

```go
package report

// Markdown 渲染一页教练报告（facts 来自 Analysis，interpret 来自教练文本，可为空）。
func Markdown(a *model.Analysis, interpret string) (string, error)

// HTML 渲染自包含本地 HTML（内联 CSS，深色主题，无外部依赖）。
func HTML(a *model.Analysis, interpret string) (string, error)
```

- interpret 为空时输出 **纯事实版**（规则层数据 + 提示「配置 LLM 后可得解读」）——保证无 LLM 也可用。
- Markdown 结构：判决（整场句 + 移动中开枪一条 + 分开间隔提示 + 要你改的 / 分开的 / 其余）/ 好回合 / 站位 / 对枪 / 追问入口。报告不再输出「丢分原因频次」；`LostRoundCauses` 仍在分析数据。事实部分一律从 Analysis 生成，不依赖 LLM；开场判决不被 interpret 覆盖。
- HTML 与 Markdown 内容一致，样式参考 `docs/html-style-note.md`（深色、JetBrains Mono、卡片式）。

---

## internal/qa —— 追问层

```go
package qa

// Retrieve 从问题中抽取回合号/关键词，返回数据切片（纯规则检索）。
func Retrieve(a *model.Analysis, question string) *model.QASlice

// AnswerFacts 无 LLM 时的降级回答：把切片整理成事实清单。
func AnswerFacts(a *model.Analysis, slice *model.QASlice) string
```

- 回合号正则：`第?\s*(\d+)\s*回合`、`round\s*(\d+)`、`R\s*(\d+)\b`。只认真实存在的回合号；超范围丢弃。
- 关键词→标签映射：残局/clutch/1v → clutch；首杀/开局/first → opening；补枪/trade → trade；道具/闪光/烟/火 → utility；经济/买/起枪/eco/force → economy；下包/拆包/C4 → bomb。ASCII 词按词边界匹配（`1v2`/`trade失败` 紧贴中文也命中，`he` 不误命中 `hero`）。
- 无显式回合号时：按关键词命中最多的**丢分回合**（并列取 WinCost 高的）取前 3；无关键词时只看丢分回合按 WinCost 取前 3（全胜则返回空）；空问题直接返回空切片。
- 显式回合号最多取 6 个（防整场塞进 prompt）；ExtraFacts 同关键词过滤后 round-robin 取，最多 12 条。
- 检索输出 RoundAnalysis 全量 + 相关 Evidence。

---

## internal/knowledge —— 游戏知识（纯数据）

```go
package knowledge

//go:embed data/*.json
// EconomyRules、MapDefaults、Callouts 等以 JSON 内嵌。
func EconomyRules() (string, error)   // 给教练提示词的经济规则文本（中文）
func MapBrief(mapName string) string  // 该地图默认站位简述（没有则空串，禁止编造）
func CalloutTable(mapName string) map[string]model.Vec3 // 点位锚（可为空表）
```

`data/economy.md`：CS2 经济规则（胜 3250 / 下包胜 3500 / 拆包胜 3500 / 败方 1400+500×连败≤3400 / 下包 +800 / 上限 16000 / 半场换边）。
`data/maps.json`：`{"de_mirage": {"brief": "...", "callouts": {"A点": {"x":..,"y":..,"z":..}}}}` —— **只写确有把握的；没有就空对象，不许编坐标。**

---

## cmd/cs2coach —— CLI（stdlib flag 子命令即可，不引 cobra）

```
cs2coach parse   <demo.dem> [-o match.json] [--player name|steamid64]
cs2coach analyze <match.json> [-o analysis.json]
cs2coach report  <analysis.json> [--out-dir dir] [--llm] [--prompt-only] [--interpret 解读.txt]
cs2coach ask     <analysis.json> "问题" | -i   # -i 进入交互追问
cs2coach run     <demo.dem> [--out-dir dir] [--player ...] [--llm] [--prompt-only] [--interpret 解读.txt]
```

- `run` = parse → analyze → report（`--prompt-only` 只落盘提示词；`--llm` 调用 Client；`--interpret 文件` 回填外部解读；都没有则纯事实报告）。
- 报告产物：`out-dir/report.md`、`out-dir/report.html`、`out-dir/match.json`、`out-dir/analysis.json`、`out-dir/prompt.txt`（仅 prompt-only/llm 失败时）。
- exit code：0 成功；2 参数错（含 `--llm`/`--prompt-only`/`--interpret` 三选一互斥）；3 解析失败；4 LLM 解读不可用（未配置或调用失败，`--llm`/`--interpret` 被要求时；纯事实降级报告仍会产出）。
- 所有输出中文。

---

## 测试要求（每个包都要）

- 表驱动单测；tactics 的归因/买型/补枪窗口必须有用 `internal/model` 手工构造 Match 的用例（不依赖真实 demo）。
- parse 无法单测真 demo 就测纯函数（reason 映射、经济聚合、focus 解析）。
- `go vet ./...` 干净；`go test ./...` 全绿。
