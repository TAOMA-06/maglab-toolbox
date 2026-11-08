# M2 设计补充（站位 + 对枪）— 实现依据

> 本文件是 `PLAN.md` 第一期（M2a/M2b/M2c）的实现级细则。数据契约以 `internal/model` 与
> `docs/CONTRACTS.md` 为准；本文件只解释语义与算法，不改变「parse 只提事实 / tactics 出数字 /
> LLM 只解读」架构锁。

## 1. parse 层新增事实（M2a）

### 1.1 视角采样

- `common.Player.ViewDirectionX()` = yaw（0..360），`ViewDirectionY()` = pitch（-90..90）。
- **只在事件对齐时采样**（WeaponFire / PlayerHurt / Kill），不做每 tick 全量时间线。
- 所有 yaw 统一归一化到 `[0, 360)`，pitch 原样。

### 1.2 开枪瞬间水平速度

demoinfocs v5.2.0 的 `common.Player` **没有** velocity 访问器，CS2 pawn 也没有
`m_vecVelocity` 属性（已用 demo 实测）。因此按 PLAN 2.1 用位置差分：

- 注册 `events.FrameDone`，每帧对 `Participants().Playing()` 中**存活**玩家记录
  `{pos, tick, sec}` 到 `lastSample[player]`。
- `WeaponFire` 时：
  `speed = horizontalDist(pos, lastSample.pos) / max(dt, 1/tickRate)`，
  `dt = nowSec - lastSample.sec`；水平距离只取 X/Y。
- `speed < 0`、`dt <= 0`、无上一采样、瞬移（`speed > 2000 u/s`）→ `SpeedValid=false`，
  speed 记 0。**不要**用钳制后的值伪造可信数据。
- 速度只用于 tactics 的急停判定，不在这里判断「移动中开枪」。

### 1.3 ShotFact

每次**枪械** `WeaponFire`（非热身、回合内、shooter 非 nil；排除刀/投掷物）追加一条 `model.ShotFact`：
tick / round_time / shooter / weapon / pos / yaw / pitch / speed / speed_valid。
刀挥与投掷物出手也会触发 `WeaponFire`，但不是「开枪」，不得计入速度/预瞄分布。

### 1.4 DuelFact（接敌）

「接敌」= 两名敌对玩家在交火窗口内的一次连续交火。窗口常量：

- `duelSnapshotWindowSec = 1.0`：接敌瞬间取「首次开火前 1s 内最近一次开火」的快照。
- `duelReengageWindowSec = 3.0`：同一对玩家最后一次交火活动后 3s 无新活动，结束本次接敌；
  之后再次交火算新接敌。

配对键：两名玩家 SteamID64 字符串升序拼接（与谁是 A/B 无关）。

流程：

1. `WeaponFire`：把 shot 写入 `recentShots[shooter]`（只保留 1s）；若该 shooter 在当前
   进行中的接敌里，`Shots++`。
2. `PlayerHurt`（attacker/victim 均非 nil、双方阵营不同、回合内）：
   - 取/开该 pair 的接敌。
   - 开启时快照两侧：优先用各自 1s 内最近一次发射（pos/yaw/pitch/speed/tick）；
     没有发射则用当前 `Position()/ViewDirection`，`FirstShotTick=0`、speed=0/valid=false。
   - `StartTick` = 两侧首发射 tick 的较小非零值；都没有则当前 tick。
   - A/B 排序：先开火者在前；都未开火按 SteamID 升序。
   - `Hits++`、`Damage += HealthDamage`（攻击方一侧）。
   - 更新 `lastActivityTick`。
3. `Kill`：若 killer/victim 的 pair 有进行中的接敌，写 `KillTick/KillerID/VictimID/
   Headshot/Weapon` 并结束；没有则用当前快照新建一条再写结果（刀/雷等无 WeaponFire 的击杀）。
4. 回合收尾（`finalizeRound`）：把所有仍开着的接敌按 `StartTick` 升序写进 `Round.Duels`，
   `EndTick` = 最后活动 tick（无活动则 start tick）。

不允许在 parse 层算预瞄差/急停判定/胜负归因。

补充实现约束（2026-11-01 数据实测后）：

- 只有**枪械/刀**造成的 `PlayerHurt` 才触发接敌；手雷/世界伤害不算（否则扔雷会伪造「先手命中」）。
- `DuelFact.Contact` 记录来源：`hurt`（有接敌伤害快照）或 `kill`（缺枪械伤害事件、只有击杀时的降级，
  常见于 GOTV 缺 PlayerHurt 或非枪械击杀）。
- `DuelFact.FirstHurtBy` 记录本次接敌第一个造成武器伤害的玩家（先手命中事实）。
- 接敌可能由对手先手触发：若某侧之后才首次「对准对手」开火，`applyShotToDuel` 会补记该侧首枪快照与
  `TargetPos`（对手当时位置），不会永远停留在「未开火」。
- 同一 tick 内 WeaponFire 可能晚于 PlayerHurt/Kill 派发；因此报告对「有伤害但没采到开火事件」的情况
  写「开火事件未采到（以伤害判定接敌）」，不编造开火 tick。

### 1.5 死亡瞬间快照（站位用）

`Kill` 时除现有字段外记录：

- `VictimYaw/VictimPitch`：受害者死亡瞬间视角。
- `OtherAlive []PlayerSnapshot`：**除受害者外**所有存活玩家的 {id, name, side, pos, yaw, pitch, hp}。
- 只记录事实；「能不能看到你」由 tactics 用视锥近似计算（见 2.2）。

## 2. tactics 层指标（M2b）

### 2.1 具名阈值（集中定义、可调）

`internal/tactics/m2.go`（新文件）中定义：

```go
// 站位
const (
    ExposureMaxDistance   = 2500.0 // units
    ExposureConeHalfAngle = 45.0   // 度
    IsolatedTeammateDist  = 800.0  // units
    CoverRadius           = 200.0  // units
    CoverWindowSec        = 10.0   // 秒
    HotZoneRadius         = 400.0  // units
)

// 对枪（2026-11-01 用 3 场公开 demo 分布校准，详见 docs/M2-BASELINE.md；只改常量）
const (
    PreAimBadDeg       = 15.0  // 合计 P90 8.5°；15° 只抓明显没瞄
    MovingFireSpeedUPS = 100.0 // 仅枪械射击 P75≈36 / P90≈82；100 为保守的「明显跑动开枪」
)
```

所有阈值写入 `Analysis.Positioning.Thresholds` / `Analysis.Dueling.Thresholds`，
报告里必须能解释「按什么阈值算的」。

### 2.2 站位（每次焦点死亡）

来源：`Round.Kills` 中 victim == 焦点玩家的击杀 + `Round.Utility`。

1. **暴露枪线（视锥近似，用户 2026-11-01 拍板）**：
   - 对 `OtherAlive` 中每个敌人（side 不同、存活）：
     `dist(victimPos, enemyPos) <= ExposureMaxDistance` 且
     `yawDelta(enemy.Yaw, bearing(enemyPos -> victimPos)) <= ExposureConeHalfAngle`
     → 记一条暴露。
   - `Sector`：相对**受害者朝向**的 8 扇区（以「敌人→你」的反方向？不：用
     「你→敌人」方位减受害者 yaw 后分桶）：正前 / 右前 / 正右 / 右后 / 正后 / 左后 / 正左 / 左前。
   - 报告文案必须写「暴露枪线（视锥近似）」，不得写「敌人能看到你」。
2. **队友间距**：同阵营 `OtherAlive` 中最近距离；`Isolated = dist > IsolatedTeammateDist`。
   若同阵营无存活队友 → `LastAlive=true`，不是 `Isolated`（1vX 残局不等于站位孤立）。
3. **道具掩护**：同阵营 smoke / molotov / incendiary，落点在 `CoverRadius` 内，且满足
   「死亡时仍生效（EffectEnd=0 或 >= 死亡 tick）或 EffectStart 在死亡前 `CoverWindowSec` 内」。
   `CoverTick` 记命中的 EffectStart。只有本回合存在道具事实时才置 `CoverEvaluated=true`；
   否则 `CoverUtility=false` 只表示「未知」，不能当作「无掩护」。
4. **高危规则（用户 2026-11-01 拍板，2026-11-01 评审收紧）**：
   `HighRisk = ExposedLines >= 2 || (CoverEvaluated && !CoverUtility && Isolated)`
   `RiskReasons` 生成带数字的中文理由，例如：
   - `暴露枪线 3 条（视锥近似），最近 A 距离 620 units（右前）`
   - `死亡前 10s 无己方烟/火覆盖该位置`
   - `最近队友 B 距离 1200 units（> 800）`
   - `死亡瞬间无存活队友（残局，不计孤立站位）`（仅说明，不据此判高危）
   - `本回合无道具事件，掩护不可评估（不据此判高危）`
   不命中高危时也要有指标，但不写主观形容词。
5. **死亡热区**：对焦点玩家全部死亡坐标做确定性贪心聚类（半径 `HotZoneRadius`，
   按 tick 顺序，落入首个 `dist <= radius` 的簇并更新均值为中心）；按死亡次数降序取前 3，
   并列按首次 tick。`PosLabel` 复用 `posLabel`（SiteAnchors 锚地 / 坐标兜底）。
6. 没有 `OtherAlive`/`Utility` 的旧 match.json：指标降级为 0，但**不报错、不编造**。

### 2.3 对枪（每次焦点参与的对枪）

来源：`Round.Duels` 中含焦点玩家 ID 的条目。

- `PreAimDeltaDeg = yawDelta(focus.Yaw, bearing(focus.Pos -> focus.TargetPos))`
  （`TargetPos` 是焦点首次开火瞬间对手的位置；焦点没开火时用接敌快照里对手的位置）。
  **焦点没开火也评预瞄**（接敌瞬间准星在哪），但急停只在焦点真正开火时评。
- `PreAimMeasured = DuelFact.Contact=="hurt" || focus.FirstShotTick>0`：
  有接敌伤害快照，或焦点确实开过火。GOTV 仅有击杀事件且焦点没开火时不可评估。
- `FocusSpeed = focus.Speed`，`MovingShot = focus.FirstShotTick>0 && focus.SpeedValid && FocusSpeed > MovingFireSpeedUPS`。
- `PreAimFailed = PreAimMeasured && PreAimDeltaDeg > PreAimBadDeg`。
- `HasBothShots = focus.FirstShotTick>0 && opp.FirstShotTick>0`；
  `FirstShotDiffSec = (focusTick - oppTick)/tickRate`（**正数 = 焦点更晚**）。
- 命中率：`FocusHits/FocusShots`、`OppHits/OppShots`（分母 0 时记 0，不发明）。
- `Outcome`：KillerID==焦点 → `focus_kill`；VictimID==焦点 → `focus_death`；否则 `no_kill`。
- `Verdict` 必须是中文事实句，至少含：预瞄差、速度、先手差（若有）、命中数、结果、tick。
  例：`第7回合 [tick 88213] 对 Q 对枪：预瞄偏差 23.4°（阈值 15°），开火速度 182 u/s
  （阈值 100），焦点晚开火 0.08s，命中 1/3，结果被击杀。`
- 汇总 `Analysis.Dueling`：Total / PreAimFailures / MovingShots / FirstShotWins / FocusKills /
  FocusDeaths / Thresholds。
- `FirstShotWins` 定义：Outcome=focus_kill 且（焦点先开火 或 对手未开火）。
- **分布**：`PreAimDeltaDeg` 取全部焦点对枪；`ShotSpeedUPS` 取焦点玩家全部
  `ShotFact` 且 `SpeedValid`（不足 10 条时退回对枪快照速度）。分位用最近秩（nearest-rank），
  输出 Min/P50/P75/P90/P95/Max。

### 2.4 数据落点

- `RoundAnalysis.Positioning`（焦点死亡才非 nil）
- `RoundAnalysis.Duels`（焦点参与的对枪）
- `Analysis.Positioning` / `Analysis.Dueling` 汇总
- 旧字段与既有归因逻辑一律不改。

## 3. 报告 / 教练层（M2c）

### 3.1 报告两节（纯事实版必须有）

Markdown / HTML 都在「丢分原因频次」之后新增：

```
## 站位分析
<一行汇总：死亡 N 次，高危 M 次（比例）>
### 死亡热区 Top3
- 坐标/点位 · 3 次死亡 · 回合 4、9、15（tick 证据）
### 高危站位
- 回合 7 [tick 88213] 点位：暴露枪线 3 条（视锥近似）…；高危理由……
```

```
## 对枪分析
<一行汇总：对枪 N 次，预瞄可评估 M 次，焦点开火 F 次，先手命中 H 次；
 预瞄超阈值 A 次，移动中开枪 B 次；阈值：15°/100 u/s；GOTV 降级数 K>
### 对枪明细（至少 6 条焦点开火 + 最多 4 条最差未开火；CP-B 需要 ≥5 条带急停）
- 回合 7 [tick 88213] 对 Q：预瞄偏差 23.4°，速度 182 u/s（有效），晚开火 0.08s，命中 1/3，被击杀
- 回合 12 [tick 87234] 对 Marts：预瞄偏差 142.3°，焦点未开火，急停不可测，被击杀
```

- 焦点没开火的对枪只评预瞄，不评急停；报告里必须写明「焦点未开火，急停不可测」。
- `contact=kill` 的 GOTV 降级条目在汇总里单独注明，不得当成完整接敌数据。
- 如果焦点开火的对枪不足 5 条，汇总里明示「不足 CP-B 的 5 次展示量」，不要用未开火条目凑数。

- 数据不足时写「本场未采到 X 数据」而不是编造。
- 有 LLM 解读时，若 interpret 里存在 `## 站位` / `## 对枪` 小节，抽取其正文追加在对应事实节之后，
  标明「教练解读」；抽取失败就只显示事实。
- HTML 复用同一 `page` 数据，不能另起一套事实。

### 3.2 教练 prompt

- system 固定结构追加两项：`## 站位解读`、`## 对枪解读`（在「值得保留的好回合」之后、
  「追问建议」之前）。
- user 数据里加入：站位汇总/高危明细/热区；对枪汇总/明细/分布。
- 回合切片（`writeRoundSlice`）里加入该回合的 `Positioning` 与 `Duels` 事实行，每条带 tick。
- 所有新增句子仍受 `ValidateCitations` 约束（回合号 + tick 必须在对应回合区间）。
- 无 LLM 降级路径必须同样含两节（由 report 层保证）。

### 3.3 追问

`internal/qa` 关键词映射追加：站位/死位/枪线/暴露 → positioning；预瞄/急停/对枪/准星 → dueling。
检索到对应回合时，把 `RoundAnalysis.Positioning/Duels` 一并放进 `QASlice`（可复用 Analyses）。

## 4. 基线分布（M2b 交付物）

新增 CLI（`cmd/cs2coach`）：

```
cs2coach metrics <analysis.json> [more.json ...]
```

输出 Markdown 表：每场 + 合计的预瞄差/开火速度分位、样本数、当前阈值命中数。
用途：先用 `testdata/demos/s2` 公开 demo 出基线，用户再用自己的 3 场 demo 覆盖重跑定阈值。

## 5. 验收口径（不自欺）

- M2a 只能证明「字段有值、抽样与回放一致」；本仓库自动化只能做结构/范围校验，
  人工抽查 5 次对枪仍由用户完成。
- CP-A / CP-B 的「下把能用」只能由用户判定；代码只保证**每条结论都有 tick 证据**。
- 视锥近似不是视线遮挡；报告措辞必须保留「近似」二字。
