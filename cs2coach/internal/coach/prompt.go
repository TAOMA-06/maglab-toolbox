package coach

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"cs2-demo-review/internal/knowledge"
	"cs2-demo-review/internal/model"
)

// 提示词预算：面向本地 17-20GB 量级的小模型。
//
// 小模型的失败模式不是「知道得太少」，而是「被太多行带偏」：上下文里每个字段都会
// 被它当成可以引用的素材，字段越多，自造 tick / 编造事件的概率越高。因此这里做三件事：
//  1. system 用「编号硬规则 + 可照抄的输出模板」，结构靠模板而不是靠描述；
//  2. user 只留教练报告真正会用到的切片，砍掉内部打分、重复字段、已被结构化事实覆盖的证据；
//  3. user 末尾追加「可用引用」清单，把 R/T 收窄成有限候选（就近约束最有效）。
const (
	// maxEvidencePerRound 是每个回合切片最多列出的「剩余证据」条数（按重要度挑选）。
	maxEvidencePerRound = 5
	// maxCitationTicks 是「可用引用」里每回合最多列出的 tick 数。
	maxCitationTicks = 6
	// maxChecklistRounds 是「可用引用」最多列出的回合数（防止整场 24 回合把尾段撑爆）。
	maxChecklistRounds = 6
)

// 游戏知识（通用常识，写进提示词；不是本场数据，禁止当事实引用）。
const (
	// economyKnowledge 是 knowledge 包取不到数据时的兜底经济常识（压缩版）。
	economyKnowledge = `- 胜方 +$3250；T 下包胜 / CT 拆包胜 +$3500；T 下包全队 +$800（该回合输了也发）；现金上限 $16000。
- 败方 $1400 + $500×连败，梯度 1400/1900/2400/2900/3400（封顶 $3400），赢一回合即清零。
- MR12：12 回合后换边，换边现金回到 $800。
- 买型看人均装备值：≥$4750 全起（full）；$2500–$4750 强起（force，≥$3800 记半起 half）；<$2500 经济局（eco）。`

	// tradeClutchKnowledge 是补枪与残局常识（压缩版）。
	tradeClutchKnowledge = `- 补枪：队友倒地后 5 秒内由队友杀掉凶手才算补上；没补上就等于连续 4v5，会被逐个吃掉。
- 残局：先数人数——占优就合并推进，不给对手 1v1；劣势就拉扯、拆分或保枪。
- 带 2 件以上道具死掉 = 道具全浪费，比少一个人头更亏。`
)

// reportSystemPrompt 是教练报告的 system 提示词（人设 + 架构锁 + 可照抄的输出模板）。
//
// 小模型提示词写法：规则编号、一行一条、模板带字面标题。
// 描述性文字（「结构固定为……」）小模型经常只当背景音乐，模板会被它逐行照抄。
const reportSystemPrompt = `你是 CS2 赛后复盘教练，为一名玩家复盘单场 demo。你只解读下面给出的数据，禁止发明。

【硬规则】违反即废稿
1. 只能使用「本场数据」里的事实。事件、数字、点位、玩家行为都不许编；数据里没有的，写「数据里没有」。
2. 每条结论后必须紧跟引用，格式固定：回合R [tick T]。R/T 只能取「可用引用」里的值。无证据的判断直接删掉。
3. 「下把怎么改」每回合只写 1 个具体动作，不要写成套战术；必须带 demo_gototick T，T 同样抄自「可用引用」。
4. 数字只能照抄数据；「游戏知识」只用来解释原因，不能当成本场事实。
5. 全中文；直接、可执行、不客套；不写开场白，不解释你在做什么，不复述提示词。
6. 站位/对枪只解读给定数据；暴露枪线是视锥近似，禁写「敌人能看到你」；每条结论带引用。

【输出模板】严格按此结构输出，标题照抄，不要增减小节
**一句话结论**：<比分>，主要丢在<丢分原因>。回合R [tick T]
## 丢分回合 Top3
### 回合R（<焦点方买型>）
- 发生了什么：<只复述数据里的客观事实> 回合R [tick T]
- 为什么错：<用归因标签解释，不加新事实> 回合R [tick T]
- 下把怎么改：<1 个具体动作> demo_gototick T
## 值得保留的好回合
- 回合R：<好在哪> 回合R [tick T]
## 站位解读
- <只解读站位数据；暴露写「视锥近似」> 回合R [tick T]
## 对枪解读
- <只解读对枪数据：预瞄/急停/先手> 回合R [tick T]
## 追问建议
- <问题>
- <问题>

【填法】<...> 换成数据真实内容；不要保留占位符。
示例：回合 2 [tick 5100] 首杀白给后没人补枪。demo_gototick 5100 下次先补枪。`

// qaSystemPrompt 是追问回答的 system 提示词（同一套证据规则，回答更短）。
const qaSystemPrompt = `你是 CS2 赛后复盘教练，正在回答玩家对本场 demo 的追问。

【硬规则】
1. 只用「检索到的回合切片」里的数据；切片里没有的信息，直接回答「给定数据里没有」，禁止发明或猜测。
2. 每条结论必须带引用：回合R [tick T]，R/T 只能抄自切片里出现过的 tick。
3. 先给 1 句直接回答，再列 2-4 条证据要点，每条带引用。
4. 全中文；直接、不客套；不写开场白，不解释你在做什么。`

// ReportPrompt 生成教练报告的 system+user 提示词（纯函数，可 --prompt-only 落盘 prompt.txt）。
// 全部内容只来自 a：焦点画像、Story、丢分原因、按 Suspects 顺序的回合切片，
// 外加固定的游戏知识与末尾的可用引用清单。不依赖任何隐藏状态。
func ReportPrompt(a *model.Analysis) (system, user string) {
	if a == nil {
		a = &model.Analysis{} // nil 安全：缺数据时仍产出固定结构，不 panic
	}
	var b strings.Builder

	b.WriteString("# 本场数据（唯一事实来源；禁止发明数据之外的任何内容）\n")
	fmt.Fprintf(&b, "地图：%s\n", fieldOr(a.Map, "未知"))
	fmt.Fprintf(&b, "焦点玩家：%s\n\n", focusLine(a))

	writeScoreline(&b, a)
	writeFocusStats(&b, a)
	writeLostCauses(&b, a)
	writePositioningFacts(&b, a)
	writeDuelingFacts(&b, a)
	writeSuspectSlices(&b, a)
	writeGoodRounds(&b, a)
	writeGameKnowledge(&b, a.Map)
	writeCitationChecklist(&b, a)

	return reportSystemPrompt, b.String()
}

// QAPrompt 生成追问回答的 system+user 提示词（纯函数）。
// 上下文 = 检索切片 slice；同一套证据规则，回答必须带引用。
func QAPrompt(a *model.Analysis, slice *model.QASlice) (system, user string) {
	if a == nil {
		a = &model.Analysis{} // nil 安全：缺数据时仍产出固定结构，不 panic
	}
	var b strings.Builder

	fmt.Fprintf(&b, "# 追问\n%s\n", fieldOr(questionOf(slice), "（空问题）"))
	fmt.Fprintf(&b, "\n# 焦点玩家\n%s\n", focusLine(a))

	if slice != nil && len(slice.KeywordHits) > 0 {
		fmt.Fprintf(&b, "\n# 检索命中关键词\n%s\n", strings.Join(slice.KeywordHits, "、"))
	}

	b.WriteString("\n# 检索到的回合切片（唯一事实来源）\n")
	if slice == nil || len(slice.Analyses) == 0 {
		b.WriteString("\n（没有检索到回合数据；回答「给定数据里没有」，禁止编造。）\n")
	} else {
		for i := range slice.Analyses {
			b.WriteString("\n")
			writeRoundSlice(&b, slice.Analyses[i], i+1, nil)
		}
	}

	// ExtraFacts 与切片证据大量重复（qa.filterEvidence 就是从 ra.Evidence 里取的），
	// 逐条去重后再拼，避免同一事实在上下文里出现两遍——小模型会把它当成两条独立证据。
	if extra := dedupeFacts(slice); len(extra) > 0 {
		b.WriteString("\n# 补充证据（ExtraFacts）\n")
		writeEvidence(&b, extra)
	}

	writeGameKnowledge(&b, a.Map)
	writeQACitationHint(&b, slice)

	return qaSystemPrompt, b.String()
}

// questionOf 安全取问题文本。
func questionOf(slice *model.QASlice) string {
	if slice == nil {
		return ""
	}
	return slice.Question
}

// focusLine 焦点玩家一行摘要（不含「焦点玩家」前缀，调用方自带标签）。
func focusLine(a *model.Analysis) string {
	return fmt.Sprintf("%s（SteamID64 %s，地图 %s）",
		fieldOr(a.Focus.Name, "未知"), fieldOr(a.Focus.SteamID64, "未知"), fieldOr(a.Map, "未知"))
}

// fieldOr 空值兜底。
func fieldOr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// sortedIntKeys 返回 map 键的字典序（提示词输出要稳定可复现）。
func sortedIntKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// writeScoreline 写比分与走势（只保留模型会写进「一句话结论」的字段）。
func writeScoreline(b *strings.Builder, a *model.Analysis) {
	st := a.Story
	b.WriteString("## 比分与走势\n")
	fmt.Fprintf(b, "- 最终：T %d - CT %d（焦点方 %d : %d 对手）\n",
		st.FinalScore.T, st.FinalScore.CT, st.FocusTeamScore, st.OpponentScore)
	// 0 回合（无数据）时不写半场与买型：空字段只会变成幻觉素材。
	if st.FirstHalfScore.T+st.FirstHalfScore.CT+st.SecondHalfScore.T+st.SecondHalfScore.CT > 0 {
		fmt.Fprintf(b, "- 半场：上半场 T %d - CT %d；下半场 T %d - CT %d\n",
			st.FirstHalfScore.T, st.FirstHalfScore.CT, st.SecondHalfScore.T, st.SecondHalfScore.CT)
	}
	if len(st.WonByBuy) > 0 || len(st.LostByBuy) > 0 {
		fmt.Fprintf(b, "- 按买型胜负：%s\n", renderBuyRecord(st.WonByBuy, st.LostByBuy))
	}
	for _, swing := range st.KeySwings {
		fmt.Fprintf(b, "- 关键节点：%s\n", swing)
	}
}

// writeFocusStats 写焦点画像（全场 + 分边，零值字段省略）。
func writeFocusStats(b *strings.Builder, a *model.Analysis) {
	b.WriteString("\n## 焦点玩家数据\n")
	fmt.Fprintf(b, "- 全场：%s\n", compactStats(a.Focus.Stats))
	for _, key := range orderedSideNames(a.Focus.SideStats) {
		fmt.Fprintf(b, "- 分边 %s：%s\n", key, compactStats(a.Focus.SideStats[key]))
	}
}

// compactStats 渲染紧凑版选手汇总：零值字段整段省略（小模型会把 0 也写成事实）。
func compactStats(s model.PlayerMatchStats) string {
	parts := []string{fmt.Sprintf("%d 回合", s.RoundsPlayed)}
	switch {
	case s.Assists > 0:
		parts = append(parts, fmt.Sprintf("K/D/A %d/%d/%d", s.Kills, s.Deaths, s.Assists))
	case s.Kills+s.Deaths > 0:
		parts = append(parts, fmt.Sprintf("K/D %d/%d", s.Kills, s.Deaths))
	}
	if s.KDR > 0 {
		parts = append(parts, fmt.Sprintf("KDR %.2f", s.KDR))
	}
	if s.ADR > 0 {
		parts = append(parts, fmt.Sprintf("ADR %.1f", s.ADR))
	}
	if s.FirstKills+s.FirstDeaths > 0 {
		parts = append(parts, fmt.Sprintf("首杀 %d / 首死 %d", s.FirstKills, s.FirstDeaths))
	}
	if s.TradeKills > 0 {
		parts = append(parts, fmt.Sprintf("补枪击杀 %d", s.TradeKills))
	}
	if s.UntradedDeaths > 0 {
		parts = append(parts, fmt.Sprintf("无人补死亡 %d", s.UntradedDeaths))
	}
	if s.ClutchWins+s.ClutchLosses > 0 {
		parts = append(parts, fmt.Sprintf("残局 %d 胜 %d 负", s.ClutchWins, s.ClutchLosses))
	}
	if s.MultikillRounds > 0 {
		parts = append(parts, fmt.Sprintf("多杀回合 %d", s.MultikillRounds))
	}
	if s.UtilThrown > 0 {
		parts = append(parts, fmt.Sprintf("道具 %d", s.UtilThrown))
	}
	if s.EnemiesFlashed > 0 {
		parts = append(parts, fmt.Sprintf("闪敌 %d", s.EnemiesFlashed))
	}
	return strings.Join(parts, " | ")
}

// sideOrder 是边的展示顺序：T 在前、CT 在后，其余按字典序。
// 固定顺序让提示词可复现，也符合「先看自己这边」的阅读习惯。
func sideOrder(side string) int {
	switch side {
	case "T":
		return 0
	case "CT":
		return 1
	default:
		return 2
	}
}

// orderedSideNames 返回 map[string]T 的键，按 T、CT、字典序排列（map 遍历要稳定）。
func orderedSideNames[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if sideOrder(out[i]) != sideOrder(out[j]) {
			return sideOrder(out[i]) < sideOrder(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// orderedSideKeys 返回 map[Side]T 的键，按 T、CT、字典序排列。
func orderedSideKeys[T any](m map[model.Side]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, string(k))
	}
	sort.Slice(out, func(i, j int) bool {
		if sideOrder(out[i]) != sideOrder(out[j]) {
			return sideOrder(out[i]) < sideOrder(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// renderBuyRecord 渲染按买型的胜负记录。
func renderBuyRecord(won, lost map[string]int) string {
	var parts []string
	for _, buy := range sortedIntKeys(won) {
		parts = append(parts, fmt.Sprintf("%s 胜 %d", buy, won[buy]))
	}
	for _, buy := range sortedIntKeys(lost) {
		parts = append(parts, fmt.Sprintf("%s 负 %d", buy, lost[buy]))
	}
	return strings.Join(parts, "；")
}

// writeLostCauses 写丢分原因频次。
func writeLostCauses(b *strings.Builder, a *model.Analysis) {
	b.WriteString("\n## 丢分原因频次（回答「输在哪类回合」）\n")
	if len(a.LostRoundCauses) == 0 {
		b.WriteString("- （无归因数据）\n")
		return
	}
	for i, cs := range a.LostRoundCauses {
		fmt.Fprintf(b, "%d. %s（%s）：%d 次，占丢分 %.0f%%，回合 %s\n",
			i+1, cs.Label, cs.Tag, cs.Count, cs.Fraction*100, joinInts(cs.Rounds))
	}
}

// writePositioningFacts 写「站位数据」块（汇总 / 阈值 / 热区 / 高危死亡）。
// 只写规则层已有的事实；旧 analysis.json 没有 M2 字段时明示缺失，禁止模型补故事。
func writePositioningFacts(b *strings.Builder, a *model.Analysis) {
	b.WriteString("\n## 站位数据（规则层；只可解读，不可发明）\n")
	r := a.Positioning
	if r == nil || r.Deaths == 0 {
		b.WriteString("- （本场未采到站位数据；不要写站位结论）\n")
		return
	}
	rate := r.HighRiskRate
	if rate <= 0 && r.HighRiskCount > 0 && r.Deaths > 0 {
		rate = float64(r.HighRiskCount) / float64(r.Deaths)
	}
	fmt.Fprintf(b, "- 汇总：焦点死亡 %d 次，高危 %d 次（%s）；暴露枪线≥2 %d 次，无掩护 %d 次，孤立 %d 次\n",
		r.Deaths, r.HighRiskCount, pctText(rate), r.ExposedDeaths, r.NoCoverDeaths, r.IsolatedDeaths)
	th := r.Thresholds
	if th.ExposureMaxDistance > 0 || th.ExposureConeHalfAngle > 0 || th.IsolatedTeammateDist > 0 {
		fmt.Fprintf(b, "- 阈值：暴露距离 %s units / 暴露半角 %s° / 孤立队友间距 %s units\n",
			numText(th.ExposureMaxDistance), numText(th.ExposureConeHalfAngle), numText(th.IsolatedTeammateDist))
	} else {
		b.WriteString("- 阈值：（旧 analysis.json 未提供）\n")
	}
	if len(r.HotZones) > 0 {
		b.WriteString("- 死亡热区：\n")
		for i, hz := range r.HotZones {
			if i >= 3 {
				break
			}
			ticks := "未记录"
			if len(hz.Ticks) > 0 {
				ticks = joinTicks(hz.Ticks)
			}
			fmt.Fprintf(b, "  - %s（坐标(%s, %s)）%d 次死亡，回合 %s，tick %s\n",
				fieldOr(hz.PosLabel, "未标注点位"), numText(hz.Pos.X), numText(hz.Pos.Y),
				hz.Deaths, joinInts(hz.Rounds), ticks)
		}
	}
	rows := coachHighRiskRows(a.Rounds, 5)
	if len(rows) > 0 {
		b.WriteString("- 高危站位（每条都可引用）：\n")
		for _, row := range rows {
			fmt.Fprintf(b, "  - 回合 %d [tick %d] %s\n", row.round, row.tick, row.text)
		}
	}
	b.WriteString("- 注意：暴露枪线是视锥近似，不是视线遮挡；不许写「敌人能看到你」。\n")
}

// coachHighRiskRow 是提示词里的一条高危死亡事实。
type coachHighRiskRow struct {
	round int
	tick  int
	text  string
}

// coachHighRiskRows 取高危死亡（按回合升序，最多 limit 条）。
func coachHighRiskRows(rounds []model.RoundAnalysis, limit int) []coachHighRiskRow {
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
	out := make([]coachHighRiskRow, 0, len(items))
	for _, it := range items {
		out = append(out, coachHighRiskRow{round: it.round, tick: it.p.Tick, text: coachPosText(it.p)})
	}
	return out
}

// coachPosText 拼一条站位事实（点位 + 暴露口径 + tactics 理由）。
func coachPosText(p model.PositionDeathAnalysis) string {
	label := fieldOr(p.PosLabel, "未标注点位")
	exposed := p.ExposedLines
	if exposed == 0 {
		exposed = len(p.ExposedBy)
	}
	reasons := append([]string(nil), p.RiskReasons...)
	if exposed > 0 && !strings.Contains(strings.Join(reasons, ""), "视锥近似") {
		reasons = append([]string{fmt.Sprintf("暴露枪线 %d 条（视锥近似）", exposed)}, reasons...)
	}
	if len(reasons) > 0 {
		return label + "：" + strings.Join(reasons, "；")
	}
	return fmt.Sprintf("%s：暴露枪线 %d 条（视锥近似）；最近队友距离 %s units；有掩护道具=%s",
		label, exposed, numText(p.NearestTeammateDist), boolCN(p.CoverUtility))
}

// writeDuelingFacts 写「对枪数据」块（汇总 / 阈值 / 分布 / 明细）。
func writeDuelingFacts(b *strings.Builder, a *model.Analysis) {
	b.WriteString("\n## 对枪数据（规则层；只可解读，不可发明）\n")
	d := a.Dueling
	if d == nil || d.Total == 0 {
		b.WriteString("- （本场未采到对枪数据；不要写对枪结论）\n")
		return
	}
	unmeasured := d.Total - d.Measured
	if unmeasured < 0 {
		unmeasured = 0
	}
	fmt.Fprintf(b, "- 汇总：对枪 %d 次（预瞄可评估 %d 次，无接敌快照 %d 次），焦点开火 %d 次，先手命中 %d 次；预瞄超阈值 %d 次，移动中开枪 %d 次，先手胜 %d，焦点击杀 %d / 阵亡 %d\n",
		d.Total, d.Measured, unmeasured, d.Fired, d.FirstHitterFocus, d.PreAimFailures, d.MovingShots, d.FirstShotWins, d.FocusKills, d.FocusDeaths)
	th := d.Thresholds
	if th.PreAimBadDeg > 0 || th.MovingFireSpeedUPS > 0 {
		fmt.Fprintf(b, "- 阈值：预瞄偏差 %s° / 开火速度 %s u/s\n",
			numText(th.PreAimBadDeg), numText(th.MovingFireSpeedUPS))
	} else {
		b.WriteString("- 阈值：（旧 analysis.json 未提供）\n")
	}
	if d.KillOnly > 0 {
		fmt.Fprintf(b, "- 数据降级：其中 %d 次仅有击杀快照（缺枪械伤害事件或非枪械击杀，预瞄为击杀瞬间口径），不要当成完整接敌数据。\n", d.KillOnly)
	}
	dist := d.Distribution
	fmt.Fprintf(b, "- 分布：预瞄差 P50 %s° / P75 %s° / P90 %s°（%d 样本）；开火速度 P50 %s / P75 %s / P90 %s u/s（%d 样本）\n",
		numText(dist.PreAimDeltaDeg.P50), numText(dist.PreAimDeltaDeg.P75), numText(dist.PreAimDeltaDeg.P90),
		dist.PreAimSamples,
		numText(dist.ShotSpeedUPS.P50), numText(dist.ShotSpeedUPS.P75), numText(dist.ShotSpeedUPS.P90),
		dist.ShotSpeedSamples)
	rows := coachDuelRows(a, 10)
	if len(rows) == 0 {
		b.WriteString("- 明细：（无焦点开火的对枪明细，预瞄/急停不可评估）\n")
		return
	}
	b.WriteString("- 明细（按预瞄差降序，最多 10 条）：\n")
	for _, row := range rows {
		fmt.Fprintf(b, "  - 回合 %d [tick %d] %s\n", row.Round, row.Tick, row.Text)
	}
}

// pickCoachDuelRows 与 report 层同口径：至少 minFired 条焦点开火的对枪，
// 剩余名额给焦点未开火的最差预瞄（避免最刺眼的被侧身案例被挤掉）。
func pickCoachDuelRows(all []model.DuelAnalysis, maxRows, minFired int) []model.DuelAnalysis {
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

// coachDuelRow 是提示词里的一条对枪明细（回合/tick 锚点 + 指标文本）。
type coachDuelRow struct {
	Round int
	Tick  int
	Text  string
}

// coachDuelRows 汇总对枪明细（按预瞄偏差降序、回合升序，最多 limit 条）。
func coachDuelRows(a *model.Analysis, limit int) []coachDuelRow {
	threshold := 0.0
	if a != nil && a.Dueling != nil {
		threshold = a.Dueling.Thresholds.PreAimBadDeg
	}
	var out []model.DuelAnalysis
	if a != nil {
		for _, ra := range a.Rounds {
			for _, d := range ra.Duels {
				if d.Round == 0 {
					d.Round = ra.Number
				}
				if d.Tick <= 0 {
					continue
				}
				if !d.PreAimMeasured {
					continue // 没有接敌快照：预瞄/急停都不可评，别喂给模型当「预瞄失败」
				}
				out = append(out, d)
			}
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
	if limit > 0 && len(out) > limit {
		out = pickCoachDuelRows(out, limit, 5)
	}
	rows := make([]coachDuelRow, 0, len(out))
	for _, d := range out {
		opp := fieldOr(d.OpponentName, "未知对手")
		rows = append(rows, coachDuelRow{
			Round: d.Round,
			Tick:  d.Tick,
			Text:  "对 " + opp + "：" + duelFactText(d, threshold),
		})
	}
	return rows
}

// duelFactText 渲染对枪指标（优先用 tactics 的 Verdict，剥掉重复前缀）。
func duelFactText(d model.DuelAnalysis, preAimThreshold float64) string {
	opp := fieldOr(d.OpponentName, "未知对手")
	if v := strings.TrimSpace(d.Verdict); v != "" {
		v = coachVerdictPrefix.ReplaceAllString(v, "")
		for _, prefix := range []string{
			"对 " + opp + " 对枪：", "对" + opp + "对枪：",
			"对 " + opp + "：", "对" + opp + "：",
		} {
			if strings.HasPrefix(v, prefix) {
				v = strings.TrimSpace(strings.TrimPrefix(v, prefix))
				break
			}
		}
		if v != "" {
			return v
		}
	}
	speed := fmt.Sprintf("%s u/s（无效）", numText(d.FocusSpeed))
	if d.FocusSpeedValid {
		speed = fmt.Sprintf("%s u/s（有效）", numText(d.FocusSpeed))
	}
	timing := "未双方开火"
	if d.HasBothShots {
		timing = fmt.Sprintf("先手差 %+.2f s", d.FirstShotDiffSec)
	}
	if d.PreAimMeasured {
		preAim := fmt.Sprintf("预瞄偏差 %.1f°", d.PreAimDeltaDeg)
		if preAimThreshold > 0 {
			preAim += fmt.Sprintf("（阈值 %s°）", numText(preAimThreshold))
		}
		return fmt.Sprintf("%s，速度 %s，%s，命中 %d/%d，结果 %s",
			preAim, speed, timing, d.FocusHits, d.FocusShots, outcomeText(d.Outcome))
	}
	return fmt.Sprintf("焦点未开火，不评估预瞄/急停；结果 %s", outcomeText(d.Outcome))
}

// coachVerdictPrefix 匹配 Verdict 行首「第7回合 [tick 88213]」引用前缀。
var coachVerdictPrefix = regexp.MustCompile(
	`^\s*(?:第\s*\d+\s*回合|回合\s*\d+)\s*(?:\[\s*(?:tick|demo_gototick)\s*[:=#]?\s*\d+\s*\])?\s*[：:，,。.、·\-—]?\s*`)

// outcomeText 把对枪结果映射成中文事实。
func outcomeText(outcome string) string {
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

// numText 渲染数字（整数不带小数点）。
func numText(v float64) string {
	r := math.Round(v*10) / 10
	if r == math.Trunc(r) {
		return strconv.FormatInt(int64(r), 10)
	}
	return strconv.FormatFloat(r, 'f', 1, 64)
}

// pctText 渲染比例（0.667 → 66.7%）。
func pctText(rate float64) string {
	return fmt.Sprintf("%.1f%%", rate*100)
}

// writeSuspectSlices 按 Suspects 顺序写回合切片（Top3 报告顺序即此顺序）。
func writeSuspectSlices(b *strings.Builder, a *model.Analysis) {
	b.WriteString("\n## 丢分回合切片（顺序即报告 Top3 顺序）\n")
	if len(a.Suspects) == 0 {
		b.WriteString("\n（无可疑回合切片。）\n")
		return
	}
	byNumber := make(map[int]model.RoundAnalysis, len(a.Rounds))
	for _, r := range a.Rounds {
		byNumber[r.Number] = r
	}
	for i, sus := range a.Suspects {
		r, ok := byNumber[sus.Round]
		if !ok {
			// 数据不一致时只给索引信息，不编造回合内容。
			fmt.Fprintf(b, "\n### 切片 %d：回合 %d（无回合详情）\n- 可疑标签：%s\n- 理由：%s\n- tick 区间：%d ~ %d（demo_gototick %d）\n",
				i+1, sus.Round, strings.Join(sus.Tags, "、"), sus.Reason,
				sus.TickRange[0], sus.TickRange[1], sus.TickRange[0])
			continue
		}
		b.WriteString("\n")
		writeRoundSlice(b, r, i+1, &sus)
	}
}

// writeRoundSlice 渲染单个回合切片（RoundAnalysis + 结构化事实 + 剩余证据）。
//
// 顺序按小模型的阅读习惯：结果 → 买型 → 可疑 → tick 区间 → 事实（每条自带 tick）→ 归因。
// 内部打分（WinCost）不写进提示词：它对解读没有帮助，却经常被模型当成「本场事实」复述。
func writeRoundSlice(b *strings.Builder, r model.RoundAnalysis, idx int, sus *model.Suspect) {
	// 平局（Winner=draw）不标「焦点方丢分」——丢分/胜是事实，不能由 FocusWon 猜。
	result := "焦点方胜"
	switch {
	case r.Winner == model.SideDraw:
		result = "平局（无胜方）"
	case !r.FocusWon:
		result = "焦点方丢分"
	}
	fmt.Fprintf(b, "### 切片 %d：回合 %d（%s，半场 %s）\n", idx, r.Number, result, fieldOr(r.Half, "-"))
	fmt.Fprintf(b, "- 结果：胜方 %s，结束原因 %s，比分 T %d - CT %d\n",
		string(r.Winner), fieldOr(r.EndReason, "未知"), r.ScoreAfter.T, r.ScoreAfter.CT)
	if line := buyLine(r.Buys); line != "" {
		fmt.Fprintf(b, "- 买型（经济事实）：%s；焦点方 %s\n", line, fieldOr(r.FocusBuy, "未知"))
	}
	if line := utilLine(r.UtilBeforeContact); line != "" {
		fmt.Fprintf(b, "- 交火前道具数：%s\n", line)
	}
	if sus != nil {
		fmt.Fprintf(b, "- 可疑标签：%s；理由：%s\n", strings.Join(sus.Tags, "、"), sus.Reason)
	}
	fmt.Fprintf(b, "- tick 区间：%d ~ %d（回放入口 demo_gototick %d）\n", r.TickRange[0], r.TickRange[1], r.TickRange[0])

	writeRoundFacts(b, r)

	if len(r.Causes) > 0 {
		causeParts := make([]string, 0, len(r.Causes))
		for _, tag := range r.Causes {
			detail := ""
			if r.CauseDetail != nil {
				detail = r.CauseDetail[tag]
			}
			if detail == "" {
				causeParts = append(causeParts, tag)
				continue
			}
			causeParts = append(causeParts, fmt.Sprintf("%s——%s", tag, detail))
		}
		fmt.Fprintf(b, "- 归因标签：%s\n", strings.Join(causeParts, "；"))
	}

	if evs := residualEvidence(r); len(evs) > 0 {
		b.WriteString("- 其他证据：\n")
		writeEvidence(b, evs)
	}
}

// writeRoundFacts 写结构化事实（开局对枪 / 补枪 / 焦点死亡 / 残局 / 人数优势）。
// 每条都带自己的 tick，方便模型直接抄成「回合R [tick T]」。
func writeRoundFacts(b *strings.Builder, r model.RoundAnalysis) {
	if op := r.Opening; op != nil {
		var flags []string
		if role := focusRoleCN(op.FocusRole); role != "" {
			flags = append(flags, "焦点="+role)
		}
		flags = append(flags, "5 秒内被补="+boolCN(op.TradedWithinWindow))
		fmt.Fprintf(b, "- [tick %d] 开局对枪：%s（%s）用 %s 击杀 %s（%s）；%s\n",
			op.Tick, op.KillerName, string(op.KillerSide), fieldOr(op.Weapon, "未知武器"),
			op.VictimName, string(op.VictimSide), strings.Join(flags, "，"))
	}
	for _, t := range r.Trades {
		status := "补枪成功"
		if !t.Success {
			status = "补枪失败"
		}
		fmt.Fprintf(b, "- [tick %d] 补枪：%s 死后 %.1f 秒 %s 击杀 %s（%s）\n",
			t.Tick, t.VictimName, t.DelaySec, t.AvengerName, t.KilledName, status)
	}
	if d := r.FocusDeath; d != nil {
		flags := []string{}
		if d.Headshot {
			flags = append(flags, "爆头")
		}
		if d.ThroughSmoke {
			flags = append(flags, "穿烟")
		}
		if d.Traded {
			flags = append(flags, "被队友补")
		}
		if d.IsFirstDeath {
			flags = append(flags, "首死")
		}
		suffix := ""
		if len(flags) > 0 {
			suffix = "；" + strings.Join(flags, "、")
		}
		fmt.Fprintf(b, "- [tick %d] 焦点死亡（回合内 %.1f 秒）：被 %s（%s）用 %s 击杀于 %s；死时剩 %d 个道具%s\n",
			d.Tick, d.RoundTime, fieldOr(d.KillerName, "未知/世界"), string(d.KillerSide),
			fieldOr(d.Weapon, "未知武器"), fieldOr(d.PosLabel, "未知点位"), d.UtilityHeld, suffix)
	}
	if c := r.Clutch; c != nil {
		flags := []string{"焦点在残局=" + boolCN(c.FocusInClutch)}
		if c.HPFocus > 0 || c.HPOppMin > 0 {
			flags = append(flags, fmt.Sprintf("血量 %d vs %d", c.HPFocus, c.HPOppMin))
		}
		fmt.Fprintf(b, "- [tick %d] 残局：%s（%d ~ %d），结果 %s，胜方 %s，开局占优=%s；%s\n",
			c.StartTick, c.Situation, c.StartTick, c.EndTick, c.Result, string(c.WinnerSide),
			boolCN(c.Advantageous), strings.Join(flags, "，"))
	}
	for _, sp := range r.ManAdv {
		// 只有「被翻盘」或人数差 ≥2 的优势区间才有解读价值，其余是噪声。
		if !sp.Threw && sp.AdvDelta < 2 {
			continue
		}
		state := "保持到回合结束"
		if sp.Threw {
			state = "被翻盘"
		}
		fmt.Fprintf(b, "- 人数优势：%s +%d（%d ~ %d），%s\n",
			string(sp.AdvSide), sp.AdvDelta, sp.StartTick, sp.EndTick, state)
	}
	// M2：站位与对枪事实（每条带 tick，模型可直接抄成 回合R [tick T]）。
	if p := r.Positioning; p != nil {
		fmt.Fprintf(b, "- [tick %d] 站位：%s\n", p.Tick, coachPosText(*p))
	}
	for _, d := range r.Duels {
		if d.Tick <= 0 {
			continue
		}
		fmt.Fprintf(b, "- [tick %d] 对枪：%s\n", d.Tick, duelFactText(d, 0))
	}
}

// residualEvidence 返回没有被结构化事实覆盖的证据（按 tick 去重、按重要度截断）。
// 开局对枪/焦点死亡/补枪事件已经在 writeRoundFacts 里以更精确的形式出现过，
// 再列一遍会让同一事实在上下文里出现两次。
func residualEvidence(r model.RoundAnalysis) []model.Evidence {
	covered := make(map[int]bool, len(r.Trades)+2)
	if r.Opening != nil {
		covered[r.Opening.Tick] = true
	}
	if r.FocusDeath != nil {
		covered[r.FocusDeath.Tick] = true
	}
	for _, t := range r.Trades {
		covered[t.Tick] = true
	}
	// M2 站位/对枪事实已在 writeRoundFacts 里以更精确的指标形式出现。
	if r.Positioning != nil {
		covered[r.Positioning.Tick] = true
	}
	for _, d := range r.Duels {
		if d.Tick > 0 {
			covered[d.Tick] = true
		}
	}
	var out []model.Evidence
	seen := make(map[int]string, len(r.Evidence))
	for _, e := range r.Evidence {
		if covered[e.Tick] {
			continue
		}
		// 存活序列（alive）只是「进入 1v2 残局」这类过程记录，残局/人数优势事实
		// 已经用更精确的形式写过一遍，同区间内的 alive 证据不再重复喂给模型。
		if e.Kind == "alive" && inAdvantageSpan(r, e.Tick) {
			continue
		}
		// 同 tick 同文重复（parse 层偶发）只留一条。
		if prev, ok := seen[e.Tick]; ok && prev == e.Text {
			continue
		}
		seen[e.Tick] = e.Text
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		wi, wj := evidenceWeight(out[i].Kind), evidenceWeight(out[j].Kind)
		if wi != wj {
			return wi < wj
		}
		return out[i].Tick < out[j].Tick
	})
	if len(out) > maxEvidencePerRound {
		out = out[:maxEvidencePerRound]
	}
	return out
}

// inAdvantageSpan 判断 tick 是否落在本回合残局/人数优势的区间内。
func inAdvantageSpan(r model.RoundAnalysis, tick int) bool {
	in := func(start, end int) bool { return start > 0 && end >= start && tick >= start && tick <= end }
	if c := r.Clutch; c != nil && in(c.StartTick, c.EndTick) {
		return true
	}
	for _, sp := range r.ManAdv {
		if in(sp.StartTick, sp.EndTick) {
			return true
		}
	}
	return false
}

// evidenceWeight 是证据重要度（越小越重要），用于截断时保住击杀/下包/经济。
func evidenceWeight(kind string) int {
	switch kind {
	case "kill", "bomb":
		return 0
	case "economy", "alive":
		return 1
	case "utility", "buy":
		return 2
	default:
		return 3
	}
}

// writeEvidence 渲染证据列表。
func writeEvidence(b *strings.Builder, evs []model.Evidence) {
	if len(evs) == 0 {
		b.WriteString("- （无）\n")
		return
	}
	for _, e := range evs {
		fmt.Fprintf(b, "  - [tick %d]（%.1f 秒）[%s] %s\n", e.Tick, e.RoundTime, fieldOr(e.Kind, "fact"), e.Text)
	}
}

// writeGoodRounds 写值得保留的好回合。
func writeGoodRounds(b *strings.Builder, a *model.Analysis) {
	b.WriteString("\n## 值得保留的好回合（规则层选出）\n")
	if len(a.GoodRounds) == 0 {
		b.WriteString("- （无）\n")
		return
	}
	for _, g := range a.GoodRounds {
		fmt.Fprintf(b, "- 回合 %d：%s；高光：%s（[tick %d]，demo_gototick %d）\n",
			g.Round, g.Reason, g.Highlight, g.Tick, g.Tick)
	}
}

// writeCitationChecklist 在 user 末尾列出「可用引用」，把 R/T 收窄为有限候选。
//
// 小模型最常犯的错不是「不想引用」，而是「记不住 tick」：给一份可抄的清单，
// 比在 system 里重复十遍「不许编造」有效。放在最后一段是因为模型对末尾指令最敏感。
func writeCitationChecklist(b *strings.Builder, a *model.Analysis) {
	b.WriteString("\n# 可用引用（R 和 T 只能从这里抄，不许新造）\n")
	byNumber := make(map[int]model.RoundAnalysis, len(a.Rounds))
	for _, r := range a.Rounds {
		byNumber[r.Number] = r
	}

	rows, suspectRows := 0, 0
	for _, sus := range a.Suspects {
		if rows >= maxChecklistRounds {
			break
		}
		r, ok := byNumber[sus.Round]
		if !ok {
			continue
		}
		if ticks := citationTicks(r); len(ticks) > 0 {
			fmt.Fprintf(b, "- 回合 %d：tick %s（区间 %d ~ %d）\n", r.Number, joinTicks(ticks), r.TickRange[0], r.TickRange[1])
		} else {
			fmt.Fprintf(b, "- 回合 %d：只有区间 %d ~ %d，没有单独 tick\n", r.Number, r.TickRange[0], r.TickRange[1])
		}
		rows++
		suspectRows++
	}
	for _, g := range a.GoodRounds {
		if rows >= maxChecklistRounds {
			break
		}
		if g.Tick <= 0 {
			continue // 没有高光 tick 的好回合不列（列了也没法引用）
		}
		fmt.Fprintf(b, "- 好回合 %d：tick %d\n", g.Round, g.Tick)
		rows++
	}
	if rows == 0 {
		b.WriteString("- （没有可引用回合；只能回答「数据里没有」）\n")
		return
	}
	fmt.Fprintf(b, "规则：Top3 按上面顺序写 %d 个回合（不足 3 个就写实际个数）；tick 只许抄清单里的数字；清单外的回合只写回合号、不要写 tick。\n",
		suspectRows)
}

// citationTicks 收集一个回合内可被引用的 tick（结构化事实优先，去重、升序、截断）。
func citationTicks(r model.RoundAnalysis) []int {
	seen := map[int]bool{}
	var out []int
	add := func(tick int) {
		if tick <= 0 || seen[tick] {
			return
		}
		seen[tick] = true
		out = append(out, tick)
	}
	if op := r.Opening; op != nil {
		add(op.Tick)
	}
	for _, t := range r.Trades {
		add(t.Tick)
	}
	if d := r.FocusDeath; d != nil {
		add(d.Tick)
	}
	if c := r.Clutch; c != nil {
		add(c.StartTick)
	}
	if p := r.Positioning; p != nil {
		add(p.Tick)
	}
	for _, d := range r.Duels {
		add(d.Tick)
	}
	for _, e := range r.Evidence {
		add(e.Tick)
	}
	sort.Ints(out)
	if len(out) > maxCitationTicks {
		out = out[:maxCitationTicks]
	}
	return out
}

// writeQACitationHint 在追问提示词末尾列出切片里的可引用 tick。
func writeQACitationHint(b *strings.Builder, slice *model.QASlice) {
	b.WriteString("\n# 可引用 tick（只能抄这里）\n")
	if slice == nil || len(slice.Analyses) == 0 {
		b.WriteString("- （无；只能回答「给定数据里没有」）\n")
		return
	}
	for _, ra := range slice.Analyses {
		if ticks := citationTicks(ra); len(ticks) > 0 {
			fmt.Fprintf(b, "- 回合 %d：%s\n", ra.Number, joinTicks(ticks))
			continue
		}
		fmt.Fprintf(b, "- 回合 %d：只有区间 %d ~ %d\n", ra.Number, ra.TickRange[0], ra.TickRange[1])
	}
}

// dedupeFacts 去掉与切片证据重复的 ExtraFacts（同 tick + 同文本）。
func dedupeFacts(slice *model.QASlice) []model.Evidence {
	if slice == nil || len(slice.ExtraFacts) == 0 {
		return nil
	}
	type key struct {
		tick int
		text string
	}
	seen := make(map[key]bool)
	for _, ra := range slice.Analyses {
		for _, e := range ra.Evidence {
			seen[key{e.Tick, e.Text}] = true
		}
	}
	out := make([]model.Evidence, 0, len(slice.ExtraFacts))
	for _, e := range slice.ExtraFacts {
		k := key{e.Tick, e.Text}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, e)
	}
	return out
}

// writeGameKnowledge 拼游戏知识段（单源：knowledge 包内嵌数据）。
// knowledge 取不到时回退到硬编码常量，保证提示词不断档。
// 只注入压缩版经济常识：完整价格表对小模型是噪声，会稀释「只能引用数据」这条主指令。
func writeGameKnowledge(b *strings.Builder, mapName string) {
	econ := economyKnowledge
	if brief, err := knowledge.EconomyBrief(); err == nil && strings.TrimSpace(brief) != "" {
		econ = strings.TrimSpace(brief)
	}
	b.WriteString("\n# 游戏知识（通用常识，只能用来解释，不是本场事实）\n")
	b.WriteString(econ)
	b.WriteString("\n")
	b.WriteString(tradeClutchKnowledge)
	b.WriteString("\n")
	if brief := knowledge.MapBrief(mapName); strings.TrimSpace(brief) != "" {
		fmt.Fprintf(b, "- 地图 %s 默认：%s\n", mapName, strings.ReplaceAll(strings.TrimSpace(brief), "\n", " "))
	}
}

// buyLine 渲染双方买型（T / CT 字典序）。
func buyLine(buys map[model.Side]string) string {
	if len(buys) == 0 {
		return ""
	}
	parts := make([]string, 0, len(buys))
	for _, side := range orderedSideKeys(buys) {
		parts = append(parts, fmt.Sprintf("%s %s", side, fieldOr(buys[model.Side(side)], "未知")))
	}
	return strings.Join(parts, " / ")
}

// utilLine 渲染交火前道具数；双方都是 0 时返回空串（没有信息量）。
func utilLine(m map[model.Side]int) string {
	if len(m) == 0 {
		return ""
	}
	sum := 0
	for _, v := range m {
		sum += v
	}
	if sum == 0 {
		return ""
	}
	parts := make([]string, 0, len(m))
	for _, side := range orderedSideKeys(m) {
		parts = append(parts, fmt.Sprintf("%s %d", side, m[model.Side(side)]))
	}
	return strings.Join(parts, " / ")
}

// focusRoleCN 把 FocusRole 翻成中文；none/空 返回空串（不写进提示词）。
func focusRoleCN(role string) string {
	switch role {
	case "killer":
		return "凶手"
	case "victim":
		return "被击杀"
	case "assist":
		return "助攻"
	default:
		return ""
	}
}

// boolCN 布尔值中文渲染（模型抄「是/否」比抄 true/false 更稳）。
func boolCN(v bool) string {
	if v {
		return "是"
	}
	return "否"
}

// joinTicks 把 tick 列表渲染成「5100、5200」（不带方括号，避免和引用格式撞车）。
func joinTicks(nums []int) string {
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = fmt.Sprintf("%d", n)
	}
	return strings.Join(parts, "、")
}

// joinInts 把整数列表渲染成 [1, 2, 3]。
func joinInts(nums []int) string {
	if len(nums) == 0 {
		return "[]"
	}
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = fmt.Sprintf("%d", n)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
