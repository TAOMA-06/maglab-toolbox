package report

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"cs2-demo-review/internal/model"
	"cs2-demo-review/internal/tactics"
)

// ErrNilAnalysis 是 Analysis 为空时的错误。
var ErrNilAnalysis = errors.New("报告渲染失败：Analysis 为空")

// factsOnlyNote 是未配置 LLM 时的纯事实版注记。
const factsOnlyNote = "未配置 LLM，本报告为纯事实版"

// 报告第一屏不再引用 labels.CauseAction 套话；丢分原因频次也不再渲染，
// LostRoundCauses 仍留在 analysis.json。

// genericQuestions 是完全没有归因数据时的通用追问示例（只引导复盘方向，不发明事实）。
var genericQuestions = []string{
	"这一场我最该先改的一个问题是什么？",
	"丢分回合里哪类原因最高频，下把先练哪一项？",
	"好回合里的做法怎么复制到下一把？",
}

// 分开按再死间隔的两种提示（各最多出现一次）。
const (
	splitHintClose = "隔了不到或等于 10 秒又死的，下次就改这一下：别再把自己送进去。先倒的那下你照旧。"
	splitHintFar   = "隔了超过 10 秒才死的，不是同一下，不要求你改送进去。"
)

// tagQuestions 是无 LLM 时按归因标签生成的追问示例。
// 补枪失败 / 道具缺口已从判决里排除，不再当追问示例。
var tagQuestions = map[string]string{
	model.CauseFirstBloodGift: "我为什么会白给首杀？",
	model.CauseEcoForce:       "强起局应该怎么打才不白给？",
	model.CauseClutchThrow:    "这个残局我该怎么打？",
	model.CauseManAdvLost:     "人数优势为什么会丢？",
}

// followUpHint 是页底追问入口文案（md/html 共用；不再教人打 CLI）。
const followUpHint = "接着问用窗口里的「追问」，不用打命令。"

// M2 固定文案：Markdown / HTML 共用同一份，避免两套事实各写一遍。
const (
	missingPositioningText = "本场未采到站位数据（需要 M2 解析字段）。"
	missingDuelingText     = "本场未采到对枪数据（需要 M2 解析字段）。"
	noHotZoneText          = "本场无死亡热区数据。"
	noHighRiskText         = "本场无命中高危规则的死亡（最近队友超过 800，且死亡前没有己方烟/火罩住落点；残局不算）。"
	noDuelRowsText         = "本场无对枪明细。"
	unknownPosLabel        = "未标注点位"
	unknownOpponent        = "未知对手"
)

// page 是 Markdown / HTML 共用的页面数据（从 Analysis + interpret 组装）。
type page struct {
	Title       string
	Map         string
	FocusName   string
	Score       string
	FactsOnly   bool
	Verdict     verdictView // 第一屏：整场句 + 分组判决行
	GoodRounds  []goodCard
	Positioning positioningView // M2：站位分析（事实 + 可选的教练解读）
	Dueling     duelingView     // M2：对枪分析（事实 + 可选的教练解读）
	LabBaseline *LabBaseline    // 可选的实验室基准；nil 时显示「还没有实验室基准」
	CliExample  string
	Questions   []string
	HowTo       string // demo_gototick 回看方法（玩家操作指南）
}

// verdictView 是赛后判决第一屏（Markdown / HTML 同一份文字）。
// 顺序：整场句 → 对枪条 → 分开提示 → 要你改的(yours) → 分开的(split) → teammate/unknown。
type verdictView struct {
	Lead      string   // 整场开场句
	Duel      string   // 第一屏对枪条（移动中开枪）
	HintClose string   // 有紧接着的分开时，一次提示
	HintFar   string   // 有隔开了的分开时，一次提示
	Yours     []string // 「要你改的」
	Split     []string // 「分开的」事实清单
	Other     []string // teammate / unknown，不加小标题
}

// empty 表示本场没有任何丢分判决行。
func (v verdictView) empty() bool {
	return len(v.Yours) == 0 && len(v.Split) == 0 && len(v.Other) == 0
}

// LabLine 返回「对枪分析」标题正下方的那一句（nil 安全）。
func (p *page) LabLine() string {
	if p == nil {
		return "还没有实验室基准"
	}
	return LabLine(p.LabBaseline)
}

// positioningView 是「站位分析」两节共用的渲染数据。
// HasData 为 false 时只显示 Missing；事实字段全部来自 Analysis，不发明。
type positioningView struct {
	HasData  bool
	Missing  string
	Summary  string
	HotZones []hotZoneRow
	HighRisk []posDeathRow
	Coach    string // interpret 里抽取出的「## 站位…」正文，可为空
}

// hotZoneRow 是死亡热区的一行（标签 + 坐标 + 回合/tick 证据）。
type hotZoneRow struct {
	Label  string
	Pos    string
	Deaths int
	Rounds string
	Ticks  []int
	Line   string // 与 Markdown 完全一致的事实行（HTML 共用同一份文本）
}

// posDeathRow 是一条高危站位死亡（回合/tick 锚点 + 量化理由）。
type posDeathRow struct {
	Round int
	Tick  int
	Text  string // 点位 + 暴露/掩护/队友/理由，Markdown 与 HTML 共用
}

// duelingView 是「对枪分析」两节共用的渲染数据。
type duelingView struct {
	HasData bool
	Missing string
	Summary string
	Rows    []duelRow
	Coach   string
}

// duelRow 是一次对枪明细（回合/tick 锚点 + 指标事实）。
type duelRow struct {
	Round int
	Tick  int
	Text  string // 「对 Q：预瞄偏差 …」；Markdown 与 HTML 共用
}

// goodCard 是值得保留的好回合。
type goodCard struct {
	Number    int
	Reason    string
	Highlight string
	Tick      int
}

// buildPage 从 Analysis 与 interpret 组装页面数据。
// interpret 为空时产出纯事实版；所有事实字段只取自 Analysis，不发明。
func buildPage(a *model.Analysis, interpret string) (*page, error) {
	return buildPageWithBaseline(a, interpret, nil)
}

// buildPageWithBaseline 在 buildPage 基础上多带一条可选的实验室基准。
func buildPageWithBaseline(a *model.Analysis, interpret string, baseline *LabBaseline) (*page, error) {
	if a == nil {
		return nil, ErrNilAnalysis
	}
	interpret = strings.TrimSpace(interpret)
	p := &page{
		Map:         a.Map,
		FocusName:   a.Focus.Name,
		FactsOnly:   interpret == "",
		Score:       fmt.Sprintf("%d:%d", a.Story.FocusTeamScore, a.Story.OpponentScore),
		Verdict:     buildVerdict(a),
		GoodRounds:  buildGoodRounds(a),
		Positioning: buildPositioning(a, interpret),
		Dueling:     buildDueling(a, interpret),
		LabBaseline: baseline,
	}
	if p.Map == "" {
		p.Map = a.Meta.Map
	}
	if p.FocusName == "" {
		p.FocusName = a.Meta.FocusPlayerName
	}
	p.Title = fmt.Sprintf("教练复盘 · %s %s · 焦点玩家 %s", p.Map, p.Score, p.FocusName)

	p.CliExample = followUpHint
	p.Questions = buildQuestions(a, interpret)
	p.HowTo = howToText
	return p, nil
}

// howToText 是 demo_gototick 回看方法（全仓唯一出处，md/html 共用）。
const howToText = "回看方法：复制 tick（如 106601），在 CS2 里打开该 demo 回放（playdemo），" +
	"先在设置里开启开发者控制台，然后在控制台输入 demo_gototick 106601 即可跳到对应时刻。"

// buildVerdict 生成第一屏判决：规则开场永远打头，LLM 不得盖掉。
// 丢分行按「要你改的 / 分开的 / 其余」分组，同类内按回合号升序。
func buildVerdict(a *model.Analysis) verdictView {
	v := verdictView{
		Lead: tactics.MatchVerdictLead(a.Story.FocusTeamScore, a.Story.OpponentScore, a.Rounds),
	}
	var yours, split, other []model.RoundAnalysis
	for _, ra := range a.Rounds {
		if ra.FocusWon || ra.Winner == model.SideDraw {
			continue
		}
		kind := model.CallUnknown
		if ra.Call != nil {
			kind = ra.Call.Kind
		}
		switch kind {
		case model.CallYours:
			yours = append(yours, ra)
		case model.CallSplit:
			split = append(split, ra)
		default:
			other = append(other, ra)
		}
	}
	sort.SliceStable(yours, func(i, j int) bool { return yours[i].Number < yours[j].Number })
	sort.SliceStable(split, func(i, j int) bool { return split[i].Number < split[j].Number })
	sort.SliceStable(other, func(i, j int) bool { return other[i].Number < other[j].Number })
	v.Duel = formatDuelTip(a.Dueling)
	var hasClose, hasFar bool
	for _, ra := range split {
		if ra.Call == nil || !ra.Call.GapKnown {
			continue
		}
		if ra.Call.GapClose {
			hasClose = true
		} else {
			hasFar = true
		}
	}
	if hasClose {
		v.HintClose = splitHintClose
	}
	if hasFar {
		v.HintFar = splitHintFar
	}
	for _, ra := range yours {
		v.Yours = append(v.Yours, formatCallLine(ra))
	}
	for _, ra := range split {
		v.Split = append(v.Split, formatCallLine(ra))
	}
	for _, ra := range other {
		v.Other = append(v.Other, formatCallLine(ra))
	}
	return v
}

// formatDuelTip 第一屏对枪条：只用已有的 MovingShots / Total / 速度阈值。
func formatDuelTip(d *model.DuelingReport) string {
	if d == nil || d.Total == 0 {
		return "这场没有可计的对枪。"
	}
	if d.MovingShots == 0 {
		return fmt.Sprintf("移动中开枪 0/%d。这场不要求你改急停。", d.Total)
	}
	speed := int(d.Thresholds.MovingFireSpeedUPS)
	return fmt.Sprintf("对枪这一条：移动中开枪 %d/%d（开火速度超过 %d u/s）。", d.MovingShots, d.Total, speed)
}

// formatCallLine 渲染单个丢分回合的判决行（Markdown / HTML 共用）。
// split 只写事实，不再复读「那下是你的」。
func formatCallLine(ra model.RoundAnalysis) string {
	n := ra.Number
	c := ra.Call
	if c == nil || c.Kind == model.CallUnknown {
		return fmt.Sprintf("回合 %d 看不出是谁先倒。这分不要求你改。", n)
	}
	switch c.Kind {
	case model.CallYours:
		return fmt.Sprintf("回合 %d 是你的。你先被 %s 用 %s 打死（tick %d）。下次这下别先死。demo_gototick %d",
			n, c.KillerName, c.Weapon, c.Tick, c.Tick)
	case model.CallTeammate:
		return fmt.Sprintf("回合 %d 是队友的。%s 先被 %s 用 %s 打死（tick %d），你没再死。下次照旧。demo_gototick %d",
			n, c.VictimName, c.KillerName, c.Weapon, c.Tick, c.Tick)
	case model.CallSplit:
		body := fmt.Sprintf("%s 先被 %s 用 %s 打死（tick %d），那下不是你的。你后来被 %s 用 %s 打死（tick %d）。",
			c.VictimName, c.KillerName, c.Weapon, c.Tick, c.FocusKillerName, c.FocusWeapon, c.FocusTick)
		if !c.GapKnown {
			return fmt.Sprintf("回合 %d 分开。%s看不出隔了多久，这分不要求你改送进去。demo_gototick %d",
				n, body, c.FocusTick)
		}
		sec := fmt.Sprintf("%.1f", c.GapSec)
		if c.GapClose {
			return fmt.Sprintf("回合 %d 分开，紧接着（隔了 %s 秒）。%s下次这下别再送进去。demo_gototick %d",
				n, sec, body, c.FocusTick)
		}
		return fmt.Sprintf("回合 %d 分开，隔开了（隔了 %s 秒）。%s隔太久，这分不要求你改送进去。demo_gototick %d",
			n, sec, body, c.FocusTick)
	default:
		return fmt.Sprintf("回合 %d 看不出是谁先倒。这分不要求你改。", n)
	}
}

// buildGoodRounds 组装「值得保留的好回合」。
func buildGoodRounds(a *model.Analysis) []goodCard {
	out := make([]goodCard, 0, len(a.GoodRounds))
	for _, g := range a.GoodRounds {
		out = append(out, goodCard{Number: g.Round, Reason: g.Reason, Highlight: g.Highlight, Tick: g.Tick})
	}
	return out
}

// joinRounds 用中文顿号连接回合号列表。
func joinRounds(rs []int) string {
	if len(rs) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		parts = append(parts, strconv.Itoa(r))
	}
	return strings.Join(parts, "、")
}

// ---------------------------------------------------------------------------
// M2：站位分析 / 对枪分析（事实只来自 Analysis，interpret 只做抽取）
// ---------------------------------------------------------------------------

// buildPositioning 组装「站位分析」事实 + interpret 里的教练解读。
// a.Positioning 为空或没有死亡样本时不编造，只给固定缺失文案。
func buildPositioning(a *model.Analysis, interpret string) positioningView {
	v := positioningView{
		Missing: missingPositioningText,
		Coach:   extractNamedSection(interpret, "站位"),
	}
	r := a.Positioning
	if r == nil || r.Deaths == 0 {
		return v
	}
	v.HasData = true
	v.Summary = positionSummary(*r)

	for i, hz := range r.HotZones {
		if i >= 3 {
			break // 契约：报告只展示 Top3
		}
		label := orText(hz.PosLabel, unknownPosLabel)
		pos := posText(hz.Pos)
		rounds := joinRounds(hz.Rounds)
		ticks := joinTickList(hz.Ticks)
		v.HotZones = append(v.HotZones, hotZoneRow{
			Label:  label,
			Pos:    pos,
			Deaths: hz.Deaths,
			Rounds: rounds,
			Ticks:  hz.Ticks,
			Line:   fmt.Sprintf("%s（%s）· %d 次死亡 · 回合 %s（tick %s）", label, pos, hz.Deaths, rounds, ticks),
		})
	}
	v.HighRisk = highRiskRows(a.Rounds, 5)
	return v
}

// positionSummary 生成站位汇总行：死亡数 / 高危比例 / 规则说明 / 阈值。
func positionSummary(r model.PositioningReport) string {
	rate := r.HighRiskRate
	if rate <= 0 && r.HighRiskCount > 0 && r.Deaths > 0 {
		rate = float64(r.HighRiskCount) / float64(r.Deaths)
	}
	iso := "800"
	th := r.Thresholds
	if th.IsolatedTeammateDist > 0 {
		iso = formatNum(th.IsolatedTeammateDist)
	}
	line := fmt.Sprintf("焦点死亡 %d 次，高危 %d 次（%s）。高危只算最近队友超过 %s、且死亡前没有己方烟/火罩住落点；残局不算。下次别离队友这么远。暴露枪线仍是视锥近似，不算墙，单独不算高危",
		r.Deaths, r.HighRiskCount, formatPct(rate), iso)
	if th.ExposureMaxDistance > 0 || th.ExposureConeHalfAngle > 0 || th.IsolatedTeammateDist > 0 {
		line += fmt.Sprintf("；阈值：暴露距离 %s units / 暴露半角 %s° / 孤立队友间距 %s units / 掩护半径 %s units（死亡前 %s 秒）",
			formatNum(th.ExposureMaxDistance),
			formatNum(th.ExposureConeHalfAngle),
			formatNum(th.IsolatedTeammateDist),
			formatNum(th.CoverRadius),
			formatNum(th.CoverWindowSec))
	} else {
		line += "；阈值：未提供"
	}
	return line
}

// highRiskRows 从各回合的 Positioning 里取高危死亡（按回合升序，最多 limit 条）。
func highRiskRows(rounds []model.RoundAnalysis, limit int) []posDeathRow {
	type item struct {
		round int
		p     model.PositionDeathAnalysis
	}
	var items []item
	for _, ra := range rounds {
		if ra.Positioning == nil || !ra.Positioning.HighRisk {
			continue
		}
		round := ra.Number
		if round == 0 {
			round = ra.Positioning.Round
		}
		items = append(items, item{round: round, p: *ra.Positioning})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].round != items[j].round {
			return items[i].round < items[j].round
		}
		return items[i].p.Tick < items[j].p.Tick
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	out := make([]posDeathRow, 0, len(items))
	for _, it := range items {
		out = append(out, posDeathRow{
			Round: it.round,
			Tick:  it.p.Tick,
			Text:  posDeathText(it.p),
		})
	}
	return out
}

// posDeathText 拼一条高危站位事实：点位 + 暴露枪线（视锥近似）+ 量化理由。
// 优先使用 tactics 生成的 RiskReasons（已带数字）；缺失时才用原始字段兜底。
func posDeathText(p model.PositionDeathAnalysis) string {
	label := orText(p.PosLabel, unknownPosLabel)
	exposed := p.ExposedLines
	if exposed == 0 {
		exposed = len(p.ExposedBy)
	}
	reasons := append([]string(nil), p.RiskReasons...)
	if exposed > 0 {
		joined := strings.Join(reasons, "")
		// 契约：暴露枪线必须写「视锥近似」，不得写成「敌人能看到你」。
		if !strings.Contains(joined, "视锥近似") {
			reasons = append([]string{fmt.Sprintf("暴露枪线 %d 条（视锥近似）", exposed)}, reasons...)
		}
	}
	if len(reasons) > 0 {
		return label + "：" + strings.Join(reasons, "；")
	}
	// RiskReasons 缺失时用原始事实兜底，不写主观形容词。
	parts := []string{}
	if exposed > 0 {
		parts = append(parts, fmt.Sprintf("暴露枪线 %d 条（视锥近似）", exposed))
	}
	if len(p.ExposedBy) > 0 {
		if e := nearestExposed(p.ExposedBy); e != nil {
			parts = append(parts, fmt.Sprintf("最近暴露 %s 距离 %s units（%s）",
				orText(e.Name, "未知敌人"), formatNum(e.Distance), orText(e.Sector, "方位未知")))
		}
	}
	parts = append(parts, teammateText(p))
	if p.CoverUtility {
		parts = append(parts, "有己方烟/火覆盖（"+orText(p.CoverType, "道具")+"）")
	} else {
		parts = append(parts, "死亡前无己方烟/火覆盖")
	}
	return label + "：" + strings.Join(parts, "；")
}

// nearestExposed 返回距离最近的暴露敌人（仅用于展示事实）。
func nearestExposed(items []model.ExposedEnemy) *model.ExposedEnemy {
	if len(items) == 0 {
		return nil
	}
	best := &items[0]
	for i := 1; i < len(items); i++ {
		if items[i].Distance < best.Distance {
			best = &items[i]
		}
	}
	return best
}

// teammateText 渲染最近队友距离（名字/阈值事实可选）。
func teammateText(p model.PositionDeathAnalysis) string {
	text := fmt.Sprintf("最近队友距离 %s units", formatNum(p.NearestTeammateDist))
	if p.NearestTeammateName != "" {
		text += "（" + p.NearestTeammateName + "）"
	}
	if p.Isolated {
		text += "，孤立"
	}
	return text
}

// buildDueling 组装「对枪分析」事实 + interpret 里的教练解读。
func buildDueling(a *model.Analysis, interpret string) duelingView {
	v := duelingView{
		Missing: missingDuelingText,
		Coach:   extractNamedSection(interpret, "对枪"),
	}
	d := a.Dueling
	if d == nil || d.Total == 0 {
		return v
	}
	v.HasData = true
	v.Summary = duelSummary(*d)
	if d.Measured == 0 {
		v.Missing = "本场未采到可评估的对枪数据（缺少接敌瞬间快照）。"
		return v
	}

	duels := collectDuels(a.Rounds)
	for _, duel := range pickDuelRows(duels, 10, 6) {
		v.Rows = append(v.Rows, duelRow{
			Round: duel.Round,
			Tick:  duel.Tick,
			Text:  duelText(duel),
		})
	}
	return v
}

// pickDuelRows 从按预瞄差降序的对枪里挑报告行：
// 至少 minFired 条「焦点开火」（预瞄+急停都有数，CP-B 要求 >=5），
// 剩余名额给焦点未开火的最差预瞄，避免最刺眼的「被侧身」案例被挤掉。
func pickDuelRows(all []model.DuelAnalysis, maxRows, minFired int) []model.DuelAnalysis {
	var fired, unfired []model.DuelAnalysis
	for _, d := range all {
		if d.FocusFirstShotTick > 0 {
			fired = append(fired, d)
		} else {
			unfired = append(unfired, d)
		}
	}
	var out []model.DuelAnalysis
	nf := minFired
	if nf > len(fired) {
		nf = len(fired)
	}
	out = append(out, fired[:nf]...)
	remaining := maxRows - len(out)
	nu := remaining
	if nu > len(unfired) {
		nu = len(unfired)
	}
	out = append(out, unfired[:nu]...)
	// 名额还有剩（例如焦点开火很少）：补剩下的开火条目。
	if len(out) < maxRows {
		extra := maxRows - len(out)
		rest := fired[nf:]
		if extra > len(rest) {
			extra = len(rest)
		}
		out = append(out, rest[:extra]...)
	}
	return out
}

// duelSummary 生成对枪汇总行：总数 / 预瞄失败 / 移动开枪 / 阈值。
func duelSummary(d model.DuelingReport) string {
	unmeasured := d.Total - d.Measured
	if unmeasured < 0 {
		unmeasured = 0
	}
	line := fmt.Sprintf("对枪 %d 次（预瞄可评估 %d 次，无接敌快照 %d 次；焦点开火 %d 次，先手命中 %d 次；焦点击杀 %d / 阵亡 %d），预瞄超阈值 %d 次，被侧身或背身 %d 次，移动中开枪 %d 次",
		d.Total, d.Measured, unmeasured, d.Fired, d.FirstHitterFocus, d.FocusKills, d.FocusDeaths, d.PreAimFailures, d.Flanked, d.MovingShots)
	th := d.Thresholds
	if th.PreAimBadDeg > 0 || th.MovingFireSpeedUPS > 0 {
		line += fmt.Sprintf("；阈值：预瞄偏差 %s° / 开火速度 %s u/s",
			formatNum(th.PreAimBadDeg), formatNum(th.MovingFireSpeedUPS))
	} else {
		line += "；阈值：未提供"
	}
	if d.KillOnly > 0 {
		line += fmt.Sprintf("；其中 %d 次仅有击杀快照（缺枪械伤害事件或非枪械击杀，预瞄为击杀瞬间口径）", d.KillOnly)
	}
	if d.Fired < 5 {
		line += fmt.Sprintf("；注意：焦点开火的对枪仅 %d 次，不足 CP-B 的 5 次展示量", d.Fired)
	}
	return line
}

// collectDuels 汇总全部回合的对枪，按预瞄偏差降序、回合升序排列。
func collectDuels(rounds []model.RoundAnalysis) []model.DuelAnalysis {
	var out []model.DuelAnalysis
	for _, ra := range rounds {
		for _, d := range ra.Duels {
			if d.Round == 0 {
				d.Round = ra.Number
			}
			if d.Tick <= 0 {
				continue // 没有 tick 证据的条目不上报告（架构锁）
			}
			if !d.PreAimMeasured {
				continue // 焦点未开火：没有预瞄/急停可评，避免把 0° 当成「预瞄完美」
			}
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].PreAimDeltaDeg != out[j].PreAimDeltaDeg {
			return out[i].PreAimDeltaDeg > out[j].PreAimDeltaDeg
		}
		if out[i].Round != out[j].Round {
			return out[i].Round < out[j].Round
		}
		return out[i].Tick < out[j].Tick
	})
	return out
}

// duelText 渲染一次对枪明细（「对 Q：预瞄偏差 …」）。
// Verdict 非空时优先使用（先剥掉行首重复的回合/tick/对手前缀），
// 确保整行仍以调用方统一添加的「回合 R [tick T]」开头。
func duelText(d model.DuelAnalysis) string {
	opp := orText(d.OpponentName, unknownOpponent)
	head := "对 " + opp + "："
	if v := normalizeVerdict(d.Verdict, opp); v != "" {
		return head + v
	}
	if !d.PreAimMeasured {
		return head + fmt.Sprintf("焦点未开火，不评估预瞄/急停；结果 %s", outcomeCN(d.Outcome))
	}
	speed := fmt.Sprintf("速度 %s u/s（无效）", formatNum(d.FocusSpeed))
	if d.FocusSpeedValid {
		speed = fmt.Sprintf("速度 %s u/s（有效）", formatNum(d.FocusSpeed))
	}
	timing := "未双方开火"
	if d.HasBothShots {
		if d.FirstShotDiffSec >= 0 {
			timing = fmt.Sprintf("先手差 +%s s", formatNum(d.FirstShotDiffSec))
		} else {
			timing = fmt.Sprintf("先手差 %s s", formatNum(d.FirstShotDiffSec))
		}
	}
	return fmt.Sprintf("%s预瞄偏差 %s°（%s），%s，%s，命中 %d/%d，结果 %s",
		head, formatNum(d.PreAimDeltaDeg), preAimStatus(d.PreAimFailed),
		speed, timing, d.FocusHits, d.FocusShots, outcomeCN(d.Outcome))
}

// reVerdictLead 匹配 Verdict 行首的「第7回合 [tick 88213]」引用前缀。
var reVerdictLead = regexp.MustCompile(
	`^\s*(?:第\s*\d+\s*回合|回合\s*\d+)\s*(?:\[\s*(?:tick|demo_gototick)\s*[:=#]?\s*\d+\s*\])?\s*[：:，,。.、·\-—]?\s*`)

// normalizeVerdict 剥掉 Verdict 行首重复的引用与「对 X 对枪：」前缀。
// 只做显示归一化，不改写任何数字/结论。
func normalizeVerdict(verdict, opp string) string {
	v := strings.TrimSpace(verdict)
	if v == "" {
		return ""
	}
	v = reVerdictLead.ReplaceAllString(v, "")
	for _, prefix := range []string{
		"对 " + opp + " 对枪：", "对" + opp + "对枪：",
		"对 " + opp + "：", "对" + opp + "：",
	} {
		if strings.HasPrefix(v, prefix) {
			v = strings.TrimSpace(strings.TrimPrefix(v, prefix))
			break
		}
	}
	return strings.TrimSpace(v)
}

// preAimStatus 渲染预瞄是否超阈值（阈值本身写在汇总行，不在这里重复）。
func preAimStatus(failed bool) string {
	if failed {
		return "超阈值"
	}
	return "阈值内"
}

// outcomeCN 把对枪结果映射成中文事实（不发明第三种结果）。
func outcomeCN(outcome string) string {
	switch outcome {
	case "focus_kill":
		return "焦点击杀"
	case "focus_death":
		return "焦点被击杀"
	case "no_kill":
		return "无人阵亡"
	default:
		if strings.TrimSpace(outcome) == "" {
			return "未知结果"
		}
		return outcome
	}
}

// posText 渲染坐标（Markdown / HTML 共用）。
func posText(p model.Vec3) string {
	return fmt.Sprintf("坐标(%s, %s)", formatNum(p.X), formatNum(p.Y))
}

// formatNum 渲染数字：整数不带小数点，小数保留原值（23.4 不会变成 23.40）。
func formatNum(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// orText 空字符串兜底（report 包内统一用这个，避免与 coach 的同名 helper 混淆）。
func orText(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// formatPct 渲染比例（0.667 → 66.7%）。
func formatPct(rate float64) string {
	return fmt.Sprintf("%.1f%%", rate*100)
}

// joinTickList 用中文顿号连接 tick 列表。
func joinTickList(ticks []int) string {
	if len(ticks) == 0 {
		return "未记录"
	}
	parts := make([]string, 0, len(ticks))
	for _, t := range ticks {
		parts = append(parts, strconv.Itoa(t))
	}
	return strings.Join(parts, "、")
}

// buildQuestions 取 2-3 个追问示例：LLM 的追问建议优先（≥2 条时原样用），
// 不足时按归因标签补齐，仍不足用通用示例兜底——始终保证 2-3 条。
func buildQuestions(a *model.Analysis, interpret string) []string {
	out := extractQuestions(interpret)
	if len(out) >= 2 {
		return out
	}
	seenTmpl := map[string]bool{} // 按模板文本去重：同类问题只出现一次
	add := func(q string) {
		if len(out) >= 3 {
			return
		}
		out = append(out, q)
	}
	addTag := func(round int, tag string) {
		tmpl, ok := tagQuestions[tag]
		if !ok || seenTmpl[tmpl] {
			return
		}
		seenTmpl[tmpl] = true
		add(fmt.Sprintf("第%d回合%s", round, tmpl))
	}
	for _, s := range a.Suspects {
		for _, tag := range s.Tags {
			addTag(s.Round, tag)
		}
	}
	for _, r := range a.Rounds {
		for _, tag := range r.Causes {
			addTag(r.Number, tag)
		}
	}
	for _, q := range genericQuestions {
		if len(out) >= 2 {
			break
		}
		add(q) // 通用示例不带回合号
	}
	return out
}
