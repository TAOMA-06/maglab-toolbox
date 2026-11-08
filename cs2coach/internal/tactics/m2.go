package tactics

// M2 教练模块（站位 + 对枪）的规则实现。设计依据 docs/M2-DESIGN.md 第 2 节。
//
// 架构锁不变：本文件只做规则计算，输出全部带数字与 tick 证据；
// 「暴露枪线」是视锥近似（存活敌人 + 距离 + 敌人朝向对准你），不是视线遮挡，
// 因此所有对外文案必须保留「视锥近似 / 近似」字样，不得写成「敌人能看到你」。

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"cs2-demo-review/internal/labels"
	"cs2-demo-review/internal/model"
)

// M2 站位阈值（用户 2026-11-01 拍板；集中定义，改规则只改这里）。
const (
	// ExposureMaxDistance 是暴露枪线的最大水平距离（units）：超过这个距离的敌人不算暴露来源。
	ExposureMaxDistance = 2500.0
	// ExposureConeHalfAngle 是暴露枪线的视锥半角（度）：敌人朝向与「敌人→你」方位夹角不超过它才算对准。
	ExposureConeHalfAngle = 45.0
	// IsolatedTeammateDist 是孤立判定线（units）：死亡瞬间与最近队友距离超过它即为孤立。
	IsolatedTeammateDist = 800.0
	// CoverRadius 是道具掩护半径（units）：己方烟/火落点距离死亡位置不超过它才算覆盖。
	CoverRadius = 200.0
	// CoverWindowSec 是「死亡前 N 秒内」的道具/掩护观察窗口（秒）。
	CoverWindowSec = 10.0
	// HotZoneRadius 是死亡热区聚类半径（units）。
	HotZoneRadius = 400.0
)

// M2 对枪阈值（2026-11-01 用 3 场公开 demo 的分布校准；只改常量，不动算法）。
const (
	// PreAimBadDeg 是预瞄偏差「失败」阈值（度）：接敌瞬间准星与对手方位夹角 > 它记预瞄失败。
	// 基线（pov + arsham + s2，85 个可评估对枪）：P50 1.2° / P75 3.2° / P90 8.5°；
	// 15° 只命中约 5%，属于「保守、只抓明显没瞄」的档位，避免把正常拉枪误判成预瞄失败。
	PreAimBadDeg = 15.0
	// MovingFireSpeedUPS 是「移动中开枪」阈值（u/s）：开枪瞬间水平速度 > 它记未急停。
	// 基线（仅枪械射击，已排除刀/投掷物）：P50 0–15 / P75 0–36 / P90 25–82；
	// 100 u/s 是偏保守档位（各 demo 命中 <10%），只抓明显还在跑动的开枪。
	MovingFireSpeedUPS = 100.0
)

// 其他具名规则参数。
const (
	// MaxHotZones 是死亡热区输出上限（按死亡次数降序取前 N）。
	MaxHotZones = 3
	// MinShotSpeedSamples 是优先使用逐枪速度样本的最小条数；不足则退回对枪快照速度。
	MinShotSpeedSamples = 10
)

// ---------- 角度 / 距离 / 分位 ----------

// normalizeYaw 把角度归一化到 [0, 360)。
func normalizeYaw(deg float64) float64 {
	d := math.Mod(deg, 360)
	if d < 0 {
		d += 360
	}
	if d >= 360 {
		d = 0
	}
	return d
}

// yawDelta 返回两个朝向之间的最短夹角（0..180 度）。
func yawDelta(a, b float64) float64 {
	d := math.Abs(normalizeYaw(a) - normalizeYaw(b))
	if d > 180 {
		d = 360 - d
	}
	return d
}

// bearingDeg 返回 from -> to 的方位角（度，0 = +X 东，逆时针为正，归一化到 [0,360)）。
// 与 Source 引擎 yaw 约定一致：yaw 0 = +X，yaw 90 = +Y。
func bearingDeg(from, to model.Vec3) float64 {
	return normalizeYaw(math.Atan2(to.Y-from.Y, to.X-from.X) * 180 / math.Pi)
}

// horizDist 是水平面距离（只看 X/Y，忽略 Z）。
func horizDist(a, b model.Vec3) float64 {
	return math.Hypot(a.X-b.X, a.Y-b.Y)
}

// sectorNames 是 8 个方位扇区（相对玩家朝向、顺时针）：0° 正前，45° 右前，……，315° 左前。
var sectorNames = [8]string{"正前", "右前", "正右", "右后", "正后", "左后", "正左", "左前"}

// sectorOf 把「你→目标」的方位角映射为相对你朝向的 8 扇区。
// Source 引擎 yaw 逆时针为正（yaw 0=+X、yaw 90=+Y），因此「顺时针偏移」= yaw - bearing，
// 这样 45° 偏移才对应玩家视角里的右侧。
func sectorOf(facingYaw, bearing float64) string {
	cw := normalizeYaw(facingYaw - bearing)
	idx := int(math.Floor((cw+45.0/2)/45.0)) % len(sectorNames)
	return sectorNames[idx]
}

// nearestRank 返回最近秩分位（p ∈ [0,100]，sorted 必须已升序）。
// 最近秩定义：rank = ceil(p/100 * n)，取第 rank 个（1-based）。
func nearestRank(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	rank := int(math.Ceil(p / 100 * float64(n)))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return sorted[rank-1]
}

// percentiles 计算 Min/P50/P75/P90/P95/Max（最近秩）；空样本全部为 0（不编造）。
func percentiles(vals []float64) model.Percentiles {
	if len(vals) == 0 {
		return model.Percentiles{}
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	return model.Percentiles{
		Min: round3(s[0]),
		P50: round3(nearestRank(s, 50)),
		P75: round3(nearestRank(s, 75)),
		P90: round3(nearestRank(s, 90)),
		P95: round3(nearestRank(s, 95)),
		Max: round3(s[len(s)-1]),
	}
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// fmtUnits 渲染距离/速度：整数值不带小数，非整数保留 1 位。
// 用于理由句，避免「800.4 units（> 800）」被四舍五入写成看似矛盾的「800」。
func fmtUnits(v float64) string {
	if math.Abs(v-math.Round(v)) < 1e-9 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

// ---------- 站位（每次焦点死亡） ----------

// buildPositioning 计算焦点玩家本回合死亡瞬间的站位指标；本回合未死亡返回 nil。
// CS2 没有复活，一回合最多一次焦点死亡，因此 RoundAnalysis.Positioning 单槽足够。
//
// 旧 match.json（无 OtherAlive / Utility）的处理：缺事实的指标保持 0 且不生成理由，
// 不推断「没有队友」「没有掩护」，也不编造距离。
func buildPositioning(c *roundCtx, kills []model.KillEvent) *model.PositionDeathAnalysis {
	if c.focusID == "" || c.round == nil {
		return nil
	}
	for _, k := range kills {
		if k.VictimID != c.focusID {
			continue
		}
		hasSnapshots := len(k.OtherAlive) > 0
		hasUtility := len(c.round.Utility) > 0

		victimSide := k.VictimSide
		if victimSide != model.SideT && victimSide != model.SideCT {
			victimSide = focusSideInRound(c.round, c.focusID)
		}

		pa := &model.PositionDeathAnalysis{
			Round:     c.round.Number,
			Tick:      k.Tick,
			RoundTime: roundTime(k.Tick, c.round.StartTick, c.rate),
			Pos:       k.VictimPos,
			PosLabel:  posLabel(k.VictimPos, c.anchors),
		}

		// 1. 暴露枪线（视锥近似）：存活敌人 + 距离阈值内 + 敌人朝向对准你。
		nearestIdx, nearestRaw := -1, math.MaxFloat64
		if hasSnapshots {
			for _, snap := range k.OtherAlive {
				if snap.Side == model.SideNone || snap.Side == "" || snap.Side == victimSide {
					continue // 同阵营/无阵营不算敌人枪线
				}
				d := horizDist(snap.Pos, k.VictimPos)
				if d > ExposureMaxDistance {
					continue
				}
				delta := yawDelta(snap.Yaw, bearingDeg(snap.Pos, k.VictimPos))
				if delta > ExposureConeHalfAngle {
					continue
				}
				pa.ExposedBy = append(pa.ExposedBy, model.ExposedEnemy{
					SteamID64:   snap.SteamID64,
					Name:        snap.Name,
					Side:        snap.Side,
					Distance:    round1(d),
					YawDeltaDeg: round1(delta),
					Sector:      sectorOf(k.VictimYaw, bearingDeg(k.VictimPos, snap.Pos)),
				})
				if d < nearestRaw {
					nearestRaw, nearestIdx = d, len(pa.ExposedBy)-1
				}
			}
			pa.ExposedLines = len(pa.ExposedBy)
		}

		// 2. 队友间距：同阵营 OtherAlive 中最近距离；无存档数据时不推断。
		var mateName string
		mateDist, hasMate := 0.0, false
		if hasSnapshots {
			for _, snap := range k.OtherAlive {
				if snap.Side == "" || snap.Side != victimSide || snap.SteamID64 == c.focusID {
					continue
				}
				d := horizDist(snap.Pos, k.VictimPos)
				if !hasMate || d < mateDist {
					hasMate, mateDist, mateName = true, d, snap.Name
				}
			}
		}
		if hasMate {
			pa.NearestTeammateDist = round1(mateDist)
			pa.NearestTeammateName = mateName
			pa.Isolated = mateDist > IsolatedTeammateDist
		} else if hasSnapshots {
			// 快照存在但同阵营无存活者 = 最后存活（1vX），不是「站位孤立」。
			pa.LastAlive = true
		}

		// 3. 道具掩护：己方烟/火落点覆盖死亡位置，且死亡瞬间仍在生效。
		// CoverEvaluated 区分「有道具数据但没有掩护」与「没有道具数据、无法判断」。
		if hasUtility {
			pa.CoverEvaluated = true
			if typ, tick, ok := findCover(c.round, k.VictimPos, k.Tick, victimSide, c.rate); ok {
				pa.CoverUtility = true
				pa.CoverType = typ
				pa.CoverTick = tick
			}
		}

		// 4. 高危规则：暴露枪线 >= 2 条，或「有掩护数据 + 无掩护 + 队友远」。
		// 道具数据缺失时不得用 CoverUtility 的默认 false 判「无掩护」。
		pa.HighRisk = pa.ExposedLines >= 2 || (pa.CoverEvaluated && !pa.CoverUtility && pa.Isolated)

		// 5. 量化理由（中文事实句，含数字；不写主观形容词）。数据缺失的分支不生成句子。
		if nearestIdx >= 0 {
			ee := pa.ExposedBy[nearestIdx]
			pa.RiskReasons = append(pa.RiskReasons, fmt.Sprintf(
				"暴露枪线 %d 条（视锥近似），最近 %s 距离 %s units（%s）",
				pa.ExposedLines, labels.Or(ee.Name, "敌人"), fmtUnits(ee.Distance), ee.Sector))
		}
		if pa.CoverEvaluated && !pa.CoverUtility {
			pa.RiskReasons = append(pa.RiskReasons, fmt.Sprintf(
				"死亡前 %.0f 秒无己方烟/火覆盖该位置（半径 %.0f units 内无有效落点）",
				CoverWindowSec, CoverRadius))
		}
		if !pa.CoverEvaluated && pa.Isolated {
			pa.RiskReasons = append(pa.RiskReasons, "本回合无道具事件，掩护不可评估（不据此判高危）")
		}
		if pa.Isolated {
			pa.RiskReasons = append(pa.RiskReasons, fmt.Sprintf(
				"最近队友 %s 距离 %s units（> %.0f）",
				labels.Or(mateName, "队友"), fmtUnits(mateDist), IsolatedTeammateDist))
		}
		if pa.LastAlive {
			pa.RiskReasons = append(pa.RiskReasons, "死亡瞬间无存活队友（残局，不计孤立站位）")
		}
		return pa
	}
	return nil
}

// coverTypes 是能挡枪线的己方道具类型：烟与火（flash/he/decoy 不算掩护）。
var coverTypes = map[string]bool{"smoke": true, "molotov": true, "incendiary": true}

// findCover 找死亡位置的有效己方掩护道具，返回类型与生效 tick。
//
// 条件（docs/M2-DESIGN.md 2.2.3 + 用户 2026-11-01 拍板的 AND 版）：
//   - 同阵营 smoke / molotov / incendiary；敌方道具永远不算掩护；
//   - LandPos 与死亡位置水平距离 <= CoverRadius；
//   - 死亡时仍生效（EffectEnd==0 表示没有结束记录，视为仍生效；否则必须 >= 死亡 tick）；
//   - 不得晚于死亡时刻生效（EffectStart <= 死亡 tick），且必须有 EffectStart（缺失不猜）。
//
// CoverWindowSec 决定证据优先级：死亡前 10s 内刚落下的道具优先作为 CoverTick 证据，
// 同优先级取最晚（最贴近死亡）的一条；顺序遍历 + 严格大于保证确定性。
func findCover(r *model.Round, deathPos model.Vec3, deathTick int, side model.Side, rate float64) (string, int, bool) {
	if r == nil || (side != model.SideT && side != model.SideCT) {
		return "", 0, false
	}
	windowTicks := int(math.Round(CoverWindowSec * rate))
	bestType, bestTick, bestRank := "", 0, -1
	for i := range r.Utility {
		u := r.Utility[i]
		if !coverTypes[u.Type] || u.ThrowerSide != side {
			continue
		}
		if horizDist(u.LandPos, deathPos) > CoverRadius {
			continue
		}
		es := u.EffectStart
		if es <= 0 || es > deathTick {
			continue // 生效 tick 缺失或晚于死亡：不可用
		}
		if u.EffectEnd != 0 && u.EffectEnd < deathTick {
			continue // 死亡时已消散/熄灭
		}
		rank := 0
		if deathTick-es <= windowTicks {
			rank = 1 // 死亡前窗口内刚落下，作为更贴切的证据
		}
		if rank > bestRank || (rank == bestRank && es > bestTick) {
			bestType, bestTick, bestRank = u.Type, es, rank
		}
	}
	return bestType, bestTick, bestTick > 0
}

// buildPositioningReport 汇总全场焦点死亡站位指标；无焦点死亡返回 nil。
//
// m 用于判断该回合是否有 M2 事实（Utility/OtherAlive/Shots/Duels）：
// 旧 match.json 完全缺事实时不把「无掩护」计入 NoCoverDeaths（不编造）。
func buildPositioningReport(rounds []model.RoundAnalysis, anchors map[string][]model.Vec3) *model.PositioningReport {
	var deaths []model.PositionDeathAnalysis
	for _, ra := range rounds {
		if ra.Positioning != nil {
			deaths = append(deaths, *ra.Positioning)
		}
	}
	if len(deaths) == 0 {
		return nil
	}
	sort.SliceStable(deaths, func(i, j int) bool { return deaths[i].Tick < deaths[j].Tick })

	rep := &model.PositioningReport{
		Deaths:     len(deaths),
		Thresholds: positioningThresholds(),
	}
	for _, d := range deaths {
		if d.HighRisk {
			rep.HighRiskCount++
		}
		if d.ExposedLines >= 2 {
			rep.ExposedDeaths++
		}
		if d.Isolated {
			rep.IsolatedDeaths++
		}
		if d.CoverEvaluated && !d.CoverUtility {
			rep.NoCoverDeaths++
		}
	}
	if rep.Deaths > 0 {
		rep.HighRiskRate = round3(float64(rep.HighRiskCount) / float64(rep.Deaths))
	}
	rep.HotZones = hotZones(deaths, anchors)
	return rep
}

// positioningThresholds 把具名常量原样写进报告（报告必须能解释按什么阈值算的）。
func positioningThresholds() model.PositioningThresholds {
	return model.PositioningThresholds{
		ExposureMaxDistance:   ExposureMaxDistance,
		ExposureConeHalfAngle: ExposureConeHalfAngle,
		IsolatedTeammateDist:  IsolatedTeammateDist,
		CoverRadius:           CoverRadius,
		CoverWindowSec:        CoverWindowSec,
		HotZoneRadius:         HotZoneRadius,
	}
}

// hotZones 对焦点死亡坐标做确定性贪心聚类：按 tick 顺序，落入首个
// 中心距离 <= HotZoneRadius 的簇（用簇当前中心判断）并更新中心为成员均值，否则新建簇。
// 输出按死亡次数降序、并列按首次 tick 升序，取前 MaxHotZones 个。
func hotZones(deaths []model.PositionDeathAnalysis, anchors map[string][]model.Vec3) []model.HotZone {
	type cluster struct {
		sumX, sumY, sumZ float64
		rounds, ticks    []int
	}
	var cs []*cluster
	for _, d := range deaths {
		placed := false
		for _, cl := range cs {
			n := float64(len(cl.ticks))
			center := model.Vec3{X: cl.sumX / n, Y: cl.sumY / n, Z: cl.sumZ / n}
			if dist3(center, d.Pos) <= HotZoneRadius {
				cl.sumX += d.Pos.X
				cl.sumY += d.Pos.Y
				cl.sumZ += d.Pos.Z
				cl.rounds = append(cl.rounds, d.Round)
				cl.ticks = append(cl.ticks, d.Tick)
				placed = true
				break // 只进首个命中的簇（贪心、确定性）
			}
		}
		if !placed {
			cs = append(cs, &cluster{
				sumX: d.Pos.X, sumY: d.Pos.Y, sumZ: d.Pos.Z,
				rounds: []int{d.Round}, ticks: []int{d.Tick},
			})
		}
	}

	out := make([]model.HotZone, 0, len(cs))
	for _, cl := range cs {
		n := float64(len(cl.ticks))
		pos := model.Vec3{
			X: round1(cl.sumX / n),
			Y: round1(cl.sumY / n),
			Z: round1(cl.sumZ / n),
		}
		out = append(out, model.HotZone{
			Pos: pos, PosLabel: posLabel(pos, anchors),
			Deaths: len(cl.ticks),
			Rounds: append([]int(nil), cl.rounds...),
			Ticks:  append([]int(nil), cl.ticks...),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Deaths != out[j].Deaths {
			return out[i].Deaths > out[j].Deaths
		}
		return out[i].Ticks[0] < out[j].Ticks[0]
	})
	if len(out) > MaxHotZones {
		out = out[:MaxHotZones]
	}
	return out
}

// ---------- 对枪（每次焦点参与的对枪） ----------

// buildDuels 生成焦点玩家本回合参与的对枪明细（按接敌 start tick 升序）。
func buildDuels(c *roundCtx, duels []model.DuelFact) []model.DuelAnalysis {
	if c.focusID == "" || c.round == nil || len(duels) == 0 {
		return nil
	}
	ordered := append([]model.DuelFact(nil), duels...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].StartTick < ordered[j].StartTick })
	var out []model.DuelAnalysis
	for _, d := range ordered {
		f, o := duelSides(d, c.focusID)
		if f == nil {
			continue
		}
		out = append(out, buildDuel(c, d, f, o))
	}
	return out
}

// duelSides 取接敌中焦点一侧与对手一侧；焦点不参与返回 nil, nil。
func duelSides(d model.DuelFact, focusID string) (*model.DuelPlayerFact, *model.DuelPlayerFact) {
	a, b := d.A, d.B
	switch {
	case focusID != "" && a.SteamID64 == focusID:
		return &a, &b
	case focusID != "" && b.SteamID64 == focusID:
		return &b, &a
	}
	return nil, nil
}

// buildDuel 把一条接敌事实换算成焦点视角的规则指标。
func buildDuel(c *roundCtx, d model.DuelFact, f, o *model.DuelPlayerFact) model.DuelAnalysis {
	tick := d.StartTick
	if tick <= 0 {
		tick = firstPositive(f.FirstShotTick, o.FirstShotTick, d.KillTick)
	}
	rt := d.RoundTime
	if rt <= 0 {
		rt = roundTime(tick, c.round.StartTick, c.rate)
	}
	preAimRaw := 0.0
	// 预瞄可评估 = 有接敌瞬间快照（hurt），或焦点确实开过火（kill-only 降级的兜底）。
	measured := d.Contact == "hurt" || f.FirstShotTick > 0
	if measured {
		target := o.Pos
		if f.TargetPos.X != 0 || f.TargetPos.Y != 0 || f.TargetPos.Z != 0 {
			target = f.TargetPos
		}
		preAimRaw = yawDelta(f.Yaw, bearingDeg(f.Pos, target))
	}
	da := model.DuelAnalysis{
		Round:              c.round.Number,
		Tick:               tick,
		RoundTime:          rt,
		OpponentID:         o.SteamID64,
		OpponentName:       o.Name,
		OpponentSide:       o.Side,
		Contact:            d.Contact,
		FocusYaw:           f.Yaw,
		FocusPitch:         f.Pitch,
		FocusSpeed:         f.Speed,
		FocusSpeedValid:    f.SpeedValid,
		PreAimMeasured:     measured,
		PreAimDeltaDeg:     round1(preAimRaw),
		FocusFirstShotTick: f.FirstShotTick,
		OppFirstShotTick:   o.FirstShotTick,
		FocusShots:         f.Shots,
		OppShots:           o.Shots,
		FocusHits:          f.Hits,
		OppHits:            o.Hits,
		FocusDamage:        f.Damage,
		OppDamage:          o.Damage,
	}
	da.HasBothShots = f.FirstShotTick > 0 && o.FirstShotTick > 0
	if da.HasBothShots {
		// model.DuelAnalysis.FirstShotDiffSec 冻结语义：正数 = 焦点更晚开枪。
		da.FirstShotDiffSec = round2(float64(f.FirstShotTick-o.FirstShotTick) / c.rate)
	}
	// 焦点没开火的对枪不评预瞄/急停：那是「没接火」或「被单方面打死」，不是预瞄失败。
	da.PreAimFailed = measured && preAimRaw > PreAimBadDeg
	da.MovingShot = measured && f.SpeedValid && f.Speed > MovingFireSpeedUPS
	switch {
	case d.KillerID != "" && d.KillerID == c.focusID:
		da.Outcome = "focus_kill"
	case d.VictimID != "" && d.VictimID == c.focusID:
		da.Outcome = "focus_death"
	default:
		da.Outcome = "no_kill"
	}
	switch {
	case d.FirstHurtBy == "":
		da.FirstHitter = "none"
	case d.FirstHurtBy == c.focusID:
		da.FirstHitter = "focus"
	default:
		da.FirstHitter = "opponent"
	}
	da.Verdict = duelVerdict(da)
	return da
}

// duelVerdict 生成中文事实句（含 tick、预瞄差、速度、先手、命中、结果）。
// 数据缺失时写明「不可测/未开火」，不用 0 冒充事实；同一事实只写一次。
func duelVerdict(da model.DuelAnalysis) string {
	var b strings.Builder
	fmt.Fprintf(&b, "第%d回合 [tick %d] 对 %s 对枪：", da.Round, da.Tick, labels.Or(da.OpponentName, "对手"))
	if !da.PreAimMeasured {
		b.WriteString("无接敌瞬间快照，不评估预瞄/急停；")
	} else {
		fmt.Fprintf(&b, "预瞄偏差 %.1f°（阈值 %.0f°）；", da.PreAimDeltaDeg, PreAimBadDeg)
		switch {
		case da.FocusFirstShotTick == 0:
			b.WriteString("焦点未开火，急停不可测；")
		case da.FocusSpeedValid:
			fmt.Fprintf(&b, "开火速度 %.0f u/s（阈值 %.0f）；", da.FocusSpeed, MovingFireSpeedUPS)
		default:
			b.WriteString("开火速度不可测（速度采样无效）；")
		}
	}
	switch da.FirstHitter {
	case "focus":
		b.WriteString("焦点先手命中；")
	case "opponent":
		b.WriteString("对手先手命中；")
	}
	switch {
	case da.HasBothShots:
		switch {
		case da.FirstShotDiffSec > 0:
			fmt.Fprintf(&b, "焦点晚开火 %.2fs；", da.FirstShotDiffSec)
		case da.FirstShotDiffSec < 0:
			fmt.Fprintf(&b, "焦点早开火 %.2fs；", -da.FirstShotDiffSec)
		default:
			b.WriteString("双方同 tick 开火；")
		}
	case da.OppFirstShotTick == 0 && da.FocusFirstShotTick > 0:
		b.WriteString("对手未开火；")
	case da.FocusFirstShotTick == 0 && da.OppFirstShotTick == 0:
		switch {
		case da.FirstHitter != "none":
			b.WriteString("开火事件未采到（以伤害判定接敌）；")
		case da.Outcome != "no_kill":
			b.WriteString("开火事件未采到（以击杀事实判定接敌）；")
		default:
			b.WriteString("双方均未开火；")
		}
	}
	if da.PreAimMeasured && da.FocusFirstShotTick > 0 {
		fmt.Fprintf(&b, "命中 %d/%d", da.FocusHits, da.FocusShots)
		if da.OppShots > 0 {
			fmt.Fprintf(&b, "（对手 %d/%d）", da.OppHits, da.OppShots)
		}
		b.WriteString("；")
	} else if da.OppShots > 0 {
		fmt.Fprintf(&b, "对手命中 %d/%d；", da.OppHits, da.OppShots)
	}
	switch da.Outcome {
	case "focus_kill":
		b.WriteString("结果击杀对手。")
	case "focus_death":
		b.WriteString("结果被击杀。")
	default:
		b.WriteString("结果窗口内无击杀。")
	}
	return b.String()
}

// firstPositive 返回第一个 > 0 的值；全为 0 时返回 0。
func firstPositive(vals ...int) int {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return 0
}

// buildDuelingReport 汇总全场对枪指标与阈值校准分布；无焦点对枪返回 nil。
func buildDuelingReport(m *model.Match, rounds []model.RoundAnalysis, focusID string) *model.DuelingReport {
	var duels []model.DuelAnalysis
	for _, ra := range rounds {
		duels = append(duels, ra.Duels...)
	}
	if len(duels) == 0 {
		return nil
	}
	rep := &model.DuelingReport{
		Total:      len(duels),
		Thresholds: model.DuelThresholds{PreAimBadDeg: PreAimBadDeg, MovingFireSpeedUPS: MovingFireSpeedUPS},
	}
	preAim := make([]float64, 0, len(duels))
	for _, d := range duels {
		if d.PreAimMeasured {
			rep.Measured++
			preAim = append(preAim, d.PreAimDeltaDeg)
			if d.PreAimFailed {
				rep.PreAimFailures++
			}
		}
		if d.FocusFirstShotTick > 0 {
			rep.Fired++
		}
		if d.FirstHitter == "focus" {
			rep.FirstHitterFocus++
		}
		if d.Contact == "kill" {
			rep.KillOnly++
		}
		if d.MovingShot {
			rep.MovingShots++
		}
		switch d.Outcome {
		case "focus_kill":
			rep.FocusKills++
			// 先手胜：焦点必须真的开过火，且（对手没开火 或 焦点更早开火）。
			// 没有焦点开火事件的击杀（手雷/刀/事件缺失）不能算「先手胜」。
			if d.FocusFirstShotTick > 0 &&
				(d.OppFirstShotTick == 0 || d.FocusFirstShotTick < d.OppFirstShotTick) {
				rep.FirstShotWins++
			}
		case "focus_death":
			rep.FocusDeaths++
		}
	}
	// 预瞄差分布：全部焦点对枪。
	rep.Distribution.PreAimSamples = len(preAim)
	rep.Distribution.PreAimDeltaDeg = percentiles(preAim)

	// 开枪速度分布：优先用焦点玩家全部有效 ShotFact；不足 MinShotSpeedSamples 条时
	// 退回对枪快照速度（同样只取 SpeedValid 的样本），样本数如实写回。
	speeds := shotSpeeds(m, focusID)
	if len(speeds) < MinShotSpeedSamples {
		if snapshot := duelSpeeds(duels); len(snapshot) > 0 {
			speeds = snapshot
		}
	}
	rep.Distribution.ShotSpeedSamples = len(speeds)
	rep.Distribution.ShotSpeedUPS = percentiles(speeds)
	return rep
}

// shotSpeeds 取焦点玩家全部有效（SpeedValid）开枪速度样本。
func shotSpeeds(m *model.Match, focusID string) []float64 {
	if m == nil || focusID == "" {
		return nil
	}
	var out []float64
	for i := range m.Rounds {
		for _, s := range m.Rounds[i].Shots {
			if s.ShooterID == focusID && s.SpeedValid {
				out = append(out, s.Speed)
			}
		}
	}
	return out
}

// duelSpeeds 取对枪快照里焦点一侧的有效速度样本（分布不足 10 条时的退回口径）。
func duelSpeeds(duels []model.DuelAnalysis) []float64 {
	var out []float64
	for _, d := range duels {
		if d.FocusSpeedValid {
			out = append(out, d.FocusSpeed)
		}
	}
	return out
}
