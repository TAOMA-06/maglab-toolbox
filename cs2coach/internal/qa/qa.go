// Package qa 是追问层：从问题中抽取回合号/关键词，返回数据切片（纯规则检索）。
package qa

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"cs2-demo-review/internal/labels"
	"cs2-demo-review/internal/model"
)

// 回合号正则：中文「第 N 回合」/「N 回合」/「第 N 局」、英文 round N / R N。
// 中文数字（「第二回合」「十二回合」）单列一条，见 cnNumeral。
var roundPatterns = []*regexp.Regexp{
	regexp.MustCompile(`第?\s*(\d+)\s*(?:回合|小?局)`),
	regexp.MustCompile(`(?i)round\s*#?\s*(\d+)`),
	regexp.MustCompile(`(?i)\bR\s*(\d+)\b`),
	regexp.MustCompile(`第?\s*([一二三四五六七八九十两]+)\s*(?:回合|小?局)`),
}

// tick 引用正则：问题里直接给 tick（「tick 5100 那波」「demo_gototick 5200」）时，
// 反查所属回合——这是追问里最精确的定位方式，比关键词可靠。
var tickPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)demo[_\s]*goto[_\s]*tick\s*[:=#]?\s*(\d{2,8})`),
	regexp.MustCompile(`(?i)\btick\s*[:=#]?\s*(\d{2,8})`),
}

// cnDigits 是中文数字字符表（「两」按 2 处理，口语常用）。
var cnDigits = map[rune]int{
	'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4,
	'五': 5, '六': 6, '七': 7, '八': 8, '九': 9,
}

// kwRule 是一组关键词 → 归因标签的映射。
// except 里的构词先屏蔽再匹配，避免「火力/火车」被当成道具话题。
type kwRule struct {
	tag    string
	words  []string
	except []string
}

// keywordRules 按匹配优先级排列（中文关键词 + 英文标签词）。
// 词表覆盖口语说法：小模型/玩家提问很少用「标准术语」，多是「先死」「白给」「一换一」。
var keywordRules = []kwRule{
	{tag: "clutch", words: []string{
		"残局", "单挑", "1v", "1 v", "1打", "一打", "以一敌", "最后一人", "只剩我", "只剩一个",
		"少打多", "兜底", "clutch",
	}},
	{tag: "man_adv", words: []string{
		"人数优势", "多打少", "人数占优", "优势局", "人数领先", "多一个人", "man advantage",
	}},
	{tag: "opening", words: []string{
		"首杀", "首死", "开局", "第一个死", "先死", "白给", "对枪", "交火", "第一枪",
		"first blood", "first", "entry", "opening",
	}},
	{tag: "trade", words: []string{
		"补枪", "补掉", "换人", "一换一", "互换", "换命", "trade",
	}},
	{tag: "utility", words: []string{
		"道具", "闪光", "烟雾", "燃烧", "燃烧瓶", "燃烧弹", "火", "火瓶", "雷", "手雷",
		"高爆", "he", "致盲", "闪白", "烟", "utility", "flash", "smoke", "molotov", "incendiary",
	}, except: []string{"火力", "火车", "火爆"}},
	{tag: "economy", words: []string{
		"经济", "买", "起枪", "起狙", "强起", "半起", "全起", "没钱", "存款", "手枪局",
		"长枪局", "保枪", "装备值", "eco", "force", "full", "buy",
	}},
	{tag: "bomb", words: []string{
		"下包", "拆包", "包点", "炸弹", "c4", "拆弹", "假拆", "安放", "bomb", "plant", "defuse",
	}},
	{tag: "positioning", words: []string{
		"站位", "死位", "枪线", "暴露", "高危站位", "死亡热区", "暴露枪线",
	}},
	{tag: "dueling", words: []string{
		"预瞄", "急停", "对枪", "准星", "预瞄差", "开火速度", "开挖速度", "开枪速度", "停枪",
	}},
}

// tagEvidenceKinds 是标签 → 相关证据 kind，用于 ExtraFacts 过滤。
var tagEvidenceKinds = map[string][]string{
	"clutch":      {"alive", "kill"},
	"man_adv":     {"alive", "kill"},
	"opening":     {"kill"},
	"trade":       {"kill"},
	"utility":     {"utility"},
	"economy":     {"economy", "buy"},
	"bomb":        {"bomb"},
	"positioning": {"kill"},
	"dueling":     {"kill"},
}

// reCountVs 匹配「5打3」「4v2」这类人数写法。
var reCountVs = regexp.MustCompile(`([1-5])\s*[v打]\s*([1-5])`)

const (
	// MaxRounds 是无显式回合号时按关键词取的回合上限。
	MaxRounds = 3
	// MaxExplicitRounds 是显式指定回合号时的上限（防整场塞进 prompt）。
	MaxExplicitRounds = 6
	// MaxExtraFacts 是 ExtraFacts 上限（round-robin 分配，保证后回合证据不被饿死）。
	MaxExtraFacts = 12
)

// Retrieve 从问题中抽取回合号/关键词/tick，返回数据切片。
// 有显式回合号 → 直接取对应回合（超范围的丢弃，上限 MaxExplicitRounds 个）；
// 只给 tick → 反查 tick 所在回合；
// 两者都没有 → 按关键词命中最多的回合（并列取 WinCost 高的）取前 3。
// 空问题直接返回空切片（不编造相关性）。
func Retrieve(a *model.Analysis, question string) *model.QASlice {
	slice := &model.QASlice{Question: question}
	if a == nil {
		return slice
	}
	if strings.TrimSpace(question) == "" {
		return slice
	}
	byNumber := map[int]model.RoundAnalysis{}
	valid := map[int]bool{}
	for _, ra := range a.Rounds {
		byNumber[ra.Number] = ra
		valid[ra.Number] = true
	}
	rounds := extractRounds(question, valid)
	rounds = mergeRounds(rounds, roundsFromTicks(a, question))
	tags, hits := matchKeywords(question)
	slice.KeywordHits = hits

	if len(rounds) > 0 {
		if len(rounds) > MaxExplicitRounds {
			rounds = rounds[:MaxExplicitRounds]
		}
		for _, n := range rounds {
			if ra, ok := byNumber[n]; ok {
				slice.Rounds = append(slice.Rounds, n)
				slice.Analyses = append(slice.Analyses, ra)
			}
		}
	} else {
		for _, ra := range topByKeywords(a.Rounds, tags) {
			slice.Rounds = append(slice.Rounds, ra.Number)
			slice.Analyses = append(slice.Analyses, ra)
		}
	}
	slice.ExtraFacts = filterEvidence(slice.Analyses, tags)
	return slice
}

// mergeRounds 合并两组回合号（去重、升序），显式回合号优先且保持靠前。
func mergeRounds(a, b []int) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(a)+len(b))
	for _, n := range append(append([]int(nil), a...), b...) {
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	if len(a) == 0 {
		sort.Ints(out)
	}
	return out
}

// extractRounds 抽取问题中的回合号（去重、升序，只保留真实存在的编号）。
func extractRounds(question string, valid map[int]bool) []int {
	seen := map[int]bool{}
	var out []int
	for _, re := range roundPatterns {
		for _, m := range re.FindAllStringSubmatch(question, -1) {
			n, ok := parseRoundNumber(m[1])
			if !ok {
				continue
			}
			if valid[n] && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Ints(out)
	return out
}

// parseRoundNumber 解析阿拉伯数字或中文数字的回合号。
func parseRoundNumber(raw string) (int, bool) {
	if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
		return n, true
	}
	return cnNumeral(raw)
}

// cnNumeral 解析 1..99 的中文数字（一 / 十 / 十二 / 二十 / 二十四）。
func cnNumeral(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	total, cur, seen := 0, 0, false
	for _, r := range s {
		if r == '十' {
			if cur == 0 {
				cur = 1
			}
			total += cur * 10
			cur = 0
			seen = true
			continue
		}
		d, ok := cnDigits[r]
		if !ok {
			return 0, false
		}
		cur, seen = d, true
	}
	if !seen {
		return 0, false
	}
	return total + cur, true
}

// roundsFromTicks 把问题里出现的 tick 反查成回合号（按 TickRange 放宽 ±2×tick_rate）。
func roundsFromTicks(a *model.Analysis, question string) []int {
	var ticks []int
	for _, re := range tickPatterns {
		for _, m := range re.FindAllStringSubmatch(question, -1) {
			if v, err := strconv.Atoi(m[1]); err == nil {
				ticks = append(ticks, v)
			}
		}
	}
	var out []int
	for _, tick := range ticks {
		if n, ok := roundByTick(a, tick); ok {
			out = append(out, n)
		}
	}
	return out
}

// roundByTick 返回包含该 tick 的回合号（优先精确区间，其次放宽区间）。
func roundByTick(a *model.Analysis, tick int) (int, bool) {
	if a == nil {
		return 0, false
	}
	for _, ra := range a.Rounds {
		if tick >= ra.TickRange[0] && tick <= ra.TickRange[1] {
			return ra.Number, true
		}
	}
	tickRate := 64.0
	if a.Meta.TickRate > 0 {
		tickRate = a.Meta.TickRate
	}
	widen := int(2 * tickRate)
	for _, ra := range a.Rounds {
		if tick >= ra.TickRange[0]-widen && tick <= ra.TickRange[1]+widen {
			return ra.Number, true
		}
	}
	return 0, false
}

// matchKeywords 匹配关键词，返回命中的标签与命中词（标签在前、原词在后，如 ["opening","首杀"]）。
func matchKeywords(question string) (tags []string, hits []string) {
	seen := map[string]bool{}
	add := func(tag, word string) {
		if seen[tag] {
			return
		}
		seen[tag] = true
		tags = append(tags, tag)
		hits = append(hits, tag, word)
	}
	for _, r := range keywordRules {
		lower := strings.ToLower(question)
		// 先屏蔽会产生假命中的构词（「火力」不是道具话题）。
		for _, ex := range r.except {
			lower = strings.ReplaceAll(lower, strings.ToLower(ex), "\x00")
		}
		for _, w := range r.words {
			lw := strings.ToLower(w)
			// ASCII 词按词边界匹配（两侧非 [a-z0-9] 即命中，如 "1v2""trade失败""eco局"）；
			// 中文词子串直查。避免 "he" 误命中其他单词。
			if isASCIIWord(lw) {
				if matchWordBoundary(lower, lw) {
					add(r.tag, w)
					break
				}
				continue
			}
			if strings.Contains(lower, lw) {
				add(r.tag, w)
				break
			}
		}
	}
	// 人数写法（5打3 / 3v5）单独归类：多的一方在前 = 人数优势话题，少的一方在前 = 残局话题。
	if tag, hit, ok := matchCountTag(question); ok {
		add(tag, hit)
	}
	return tags, hits
}

// matchCountTag 按「5打3」这类人数写法归类。焦点方阵营不可知，只看提问的话题走向。
func matchCountTag(question string) (tag, hit string, ok bool) {
	m := reCountVs.FindStringSubmatch(question)
	if m == nil {
		return "", "", false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	switch {
	case a > b:
		return "man_adv", m[0], true
	case a < b:
		return "clutch", m[0], true
	}
	return "", "", false
}

// isASCIIWord 判断是否为纯 ASCII 单词（需要词边界匹配）。
func isASCIIWord(s string) bool {
	for _, r := range s {
		if r > 127 || !(r == ' ' || r == '-' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return strings.ContainsFunc(s, func(r rune) bool { return r >= 'a' && r <= 'z' })
}

// roundKeywordScore 计算某回合命中了几个关键词类别。
func roundKeywordScore(ra model.RoundAnalysis, tags []string) int {
	score := 0
	for _, t := range tags {
		switch t {
		case "clutch":
			if ra.Clutch != nil {
				score++
			}
		case "man_adv":
			if len(ra.ManAdv) > 0 {
				score++
			}
		case "opening":
			if ra.Opening != nil {
				score++
			}
		case "trade":
			if len(ra.Trades) > 0 {
				score++
			}
		case "utility":
			sum := 0
			for _, v := range ra.UtilBeforeContact {
				sum += v
			}
			if sum > 0 {
				score++
			}
		case "economy":
			score++ // 每回合都有经济快照
		case "bomb":
			for _, e := range ra.Evidence {
				if e.Kind == "bomb" {
					score++
					break
				}
			}
		case "positioning":
			if ra.Positioning != nil {
				score++
			}
		case "dueling":
			if len(ra.Duels) > 0 {
				score++
			}
		}
	}
	return score
}

// topByKeywords 无显式回合号时：按关键词命中数（并列 WinCost）取前 3；
// 无关键词时直接按 WinCost 取前 3（丢分最可惜的回合）。
func topByKeywords(rounds []model.RoundAnalysis, tags []string) []model.RoundAnalysis {
	// 无关键词时只看丢分回合（胜/平局 WinCost 为 0，不应被当成"最可惜"）。
	var pool []model.RoundAnalysis
	if len(tags) == 0 {
		for _, ra := range rounds {
			if !ra.FocusWon && ra.Winner != model.SideDraw {
				pool = append(pool, ra)
			}
		}
	} else {
		pool = append([]model.RoundAnalysis(nil), rounds...)
	}
	cp := pool
	if len(tags) == 0 {
		sort.SliceStable(cp, func(i, j int) bool {
			if cp[i].WinCost != cp[j].WinCost {
				return cp[i].WinCost > cp[j].WinCost
			}
			return cp[i].Number < cp[j].Number
		})
	} else {
		type scored struct {
			ra    model.RoundAnalysis
			score int
		}
		var ss []scored
		for _, ra := range cp {
			ss = append(ss, scored{ra: ra, score: roundKeywordScore(ra, tags)})
		}
		sort.SliceStable(ss, func(i, j int) bool {
			if ss[i].score != ss[j].score {
				return ss[i].score > ss[j].score
			}
			if ss[i].ra.WinCost != ss[j].ra.WinCost {
				return ss[i].ra.WinCost > ss[j].ra.WinCost
			}
			return ss[i].ra.Number < ss[j].ra.Number
		})
		cp = cp[:0]
		for _, s := range ss {
			// 零命中的回合不算「相关」：宁可返回空（答「给定数据里没有」），
			// 也不要把不相关回合塞进上下文让模型硬编故事。
			if s.score == 0 {
				continue
			}
			cp = append(cp, s.ra)
		}
	}
	if len(cp) > MaxRounds {
		cp = cp[:MaxRounds]
	}
	return cp
}

// filterEvidence 取选中回合中与关键词相关的证据（无关键词则全取，上限 12 条）。
func filterEvidence(analyses []model.RoundAnalysis, tags []string) []model.Evidence {
	allowed := map[string]bool{}
	for _, t := range tags {
		for _, k := range tagEvidenceKinds[t] {
			allowed[k] = true
		}
	}
	// 各回合证据先按关键词过滤，再 round-robin 取（保证后回合关键证据不被饿死）。
	filtered := make([][]model.Evidence, len(analyses))
	for i, ra := range analyses {
		for _, e := range ra.Evidence {
			if len(tags) > 0 && !allowed[e.Kind] {
				continue
			}
			filtered[i] = append(filtered[i], e)
		}
	}
	var out []model.Evidence
	for k := 0; len(out) < MaxExtraFacts; k++ {
		added := false
		for i := range filtered {
			if k < len(filtered[i]) {
				out = append(out, filtered[i][k])
				added = true
				if len(out) >= MaxExtraFacts {
					return out
				}
			}
		}
		if !added {
			break
		}
	}
	return out
}

// matchWordBoundary 在文本中按词边界查找 ASCII 词（两侧不是 [a-z0-9] 即命中）。
func matchWordBoundary(text, word string) bool {
	for i := 0; i+len(word) <= len(text); i++ {
		if text[i:i+len(word)] != word {
			continue
		}
		beforeOK := i == 0 || !isWordByte(text[i-1])
		afterOK := i+len(word) == len(text) || !isWordByte(text[i+len(word)])
		if beforeOK && afterOK {
			return true
		}
	}
	return false
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}

// AnswerFacts 无 LLM 时的降级回答：把切片整理成事实清单（只复述数据，不发明）。
//
// 格式面向「可读 + 可回溯」：每个回合带 [tick 起始] 锚点、每条事实带自己的 tick，
// 末尾给出「可用引用」，读者可以直接把 回合R [tick T] 粘进 demo_gototick 回放。
func AnswerFacts(a *model.Analysis, slice *model.QASlice) string {
	if a == nil {
		return "没有可用的复盘数据。"
	}
	var b strings.Builder
	q := ""
	if slice != nil {
		q = strings.TrimSpace(slice.Question)
	}
	if q == "" {
		q = "（空问题）"
	}
	fmt.Fprintf(&b, "问题：%s\n", q)
	if slice == nil || len(slice.Analyses) == 0 {
		b.WriteString("给定数据里没有相关回合。\n")
		return b.String()
	}
	fmt.Fprintf(&b, "相关回合：%s\n", labels.JoinInts(slice.Rounds))
	if len(slice.KeywordHits) > 0 {
		fmt.Fprintf(&b, "命中关键词：%s\n", strings.Join(slice.KeywordHits, "、"))
	}
	for _, ra := range slice.Analyses {
		b.WriteString("\n")
		writeRoundFacts(&b, ra)
	}
	if len(slice.ExtraFacts) > 0 {
		b.WriteString("\n补充证据：\n")
		for _, e := range slice.ExtraFacts {
			fmt.Fprintf(&b, "- [tick %d] %s\n", e.Tick, e.Text)
		}
	}
	writeCitationHint(&b, slice)
	return b.String()
}

// writeRoundFacts 写单个回合的事实清单（每条事实自带 tick 锚点）。
func writeRoundFacts(b *strings.Builder, ra model.RoundAnalysis) {
	result := "焦点方胜"
	switch {
	case ra.Winner == model.SideDraw:
		result = "平局（无胜方）"
	case !ra.FocusWon:
		result = "焦点方丢分"
	}
	fmt.Fprintf(b, "## 回合 %d [tick %d]（%s，半场 %s）\n",
		ra.Number, ra.TickRange[0], result, labels.Or(ra.Half, "未知"))
	fmt.Fprintf(b, "- 结果：胜方 %s，结束原因 %s，比分 T %d - CT %d\n",
		string(ra.Winner), labels.Or(ra.EndReason, "未知"), ra.ScoreAfter.T, ra.ScoreAfter.CT)
	fmt.Fprintf(b, "- 买型：T-%s / CT-%s，焦点方 %s\n",
		labels.Or(ra.Buys[model.SideT], "未知"), labels.Or(ra.Buys[model.SideCT], "未知"), labels.Or(ra.FocusBuy, "未知"))
	if op := ra.Opening; op != nil {
		trade := "未被补枪"
		if op.TradedWithinWindow {
			trade = fmt.Sprintf("被 %s 补掉", labels.Or(op.TradeByName, "队友"))
		}
		fmt.Fprintf(b, "- 开局对枪：[tick %d] %s 用 %s 击杀 %s；%s\n",
			op.Tick, named(op.KillerName, op.KillerSide), labels.Or(op.Weapon, "未知"),
			named(op.VictimName, op.VictimSide), trade)
	}
	for _, t := range ra.Trades {
		status := "补枪成功"
		if !t.Success {
			status = "补枪失败"
		}
		fmt.Fprintf(b, "- 补枪：[tick %d] %s 死后 %.1f 秒 %s 击杀 %s（%s）\n",
			t.Tick, labels.Or(t.VictimName, "未知"), t.DelaySec, labels.Or(t.AvengerName, "未知"), labels.Or(t.KilledName, "未知"), status)
	}
	if fd := ra.FocusDeath; fd != nil {
		flags := []string{fmt.Sprintf("被补=%v", fd.Traded), fmt.Sprintf("死时剩 %d 个道具", fd.UtilityHeld)}
		if fd.IsFirstDeath {
			flags = append(flags, "首死")
		}
		if fd.Headshot {
			flags = append(flags, "爆头")
		}
		fmt.Fprintf(b, "- 焦点死亡：[tick %d] 被 %s 用 %s 击杀于 %s；%s\n",
			fd.Tick, labels.Or(fd.KillerName, "未知"), labels.Or(fd.Weapon, "未知"),
			labels.Or(fd.PosLabel, "未知"), strings.Join(flags, "，"))
	}
	if cl := ra.Clutch; cl != nil {
		fmt.Fprintf(b, "- 残局：[tick %d] %s（%d ~ %d），结果 %s，胜方 %s\n",
			cl.StartTick, cl.Situation, cl.StartTick, cl.EndTick, cl.Result, string(cl.WinnerSide))
	}
	for _, sp := range ra.ManAdv {
		state := "保持优势"
		if sp.Threw {
			state = "被翻盘"
		}
		fmt.Fprintf(b, "- 人数优势：[tick %d] %s +%d（%d ~ %d），%s\n",
			sp.StartTick, string(sp.AdvSide), sp.AdvDelta, sp.StartTick, sp.EndTick, state)
	}
	// M2：站位与对枪事实（每条带 tick，无数据时不写）。
	if p := ra.Positioning; p != nil {
		exposed := p.ExposedLines
		if exposed == 0 {
			exposed = len(p.ExposedBy)
		}
		line := fmt.Sprintf("- 站位：[tick %d] %s；暴露枪线 %d 条（视锥近似）；最近队友 %s units；掩护道具=%v；高危=%v",
			p.Tick, labels.Or(p.PosLabel, "未标注点位"), exposed,
			qaNum(p.NearestTeammateDist), p.CoverUtility, p.HighRisk)
		if reasons := strings.Join(p.RiskReasons, "；"); reasons != "" {
			line += "；理由：" + reasons
		}
		b.WriteString(line + "\n")
	}
	for _, d := range ra.Duels {
		if d.Tick <= 0 {
			continue
		}
		if !d.PreAimMeasured {
			fmt.Fprintf(b, "- 对枪：[tick %d] 对 %s；无接敌瞬间快照，不评估预瞄/急停；结果 %s\n",
				d.Tick, labels.Or(d.OpponentName, "未知对手"), qaOutcome(d.Outcome))
			continue
		}
		speed := "焦点未开火，急停不可测"
		hits := "焦点未开火"
		if d.FocusFirstShotTick > 0 {
			hits = fmt.Sprintf("命中 %d/%d", d.FocusHits, d.FocusShots)
			speed = fmt.Sprintf("%s u/s（无效）", qaNum(d.FocusSpeed))
			if d.FocusSpeedValid {
				speed = fmt.Sprintf("%s u/s（有效）", qaNum(d.FocusSpeed))
			}
		}
		timing := ""
		if d.HasBothShots {
			timing = fmt.Sprintf("；先手差 %+.2f 秒", d.FirstShotDiffSec)
		}
		fmt.Fprintf(b, "- 对枪：[tick %d] 对 %s；预瞄偏差 %s°（%s）；速度 %s%s；%s；结果 %s\n",
			d.Tick, labels.Or(d.OpponentName, "未知对手"), qaNum(d.PreAimDeltaDeg),
			qaPreAimStatus(d.PreAimFailed), speed, timing, hits, qaOutcome(d.Outcome))
	}
	if len(ra.Causes) > 0 {
		parts := make([]string, 0, len(ra.Causes))
		for _, tag := range ra.Causes {
			if d := ra.CauseDetail[tag]; d != "" {
				parts = append(parts, labels.CauseLabel(tag)+"："+d)
			} else {
				parts = append(parts, labels.CauseLabel(tag))
			}
		}
		fmt.Fprintf(b, "- 归因：%s\n", strings.Join(parts, "；"))
	}
	// 一行可直接复制的引用，让读者知道这段话该怎么标注证据。
	fmt.Fprintf(b, "- 引用写法：回合 %d [tick %d]\n", ra.Number, ra.TickRange[0])
}

// named 渲染「名字（边）」；边为空时只写名字（避免出现「（）」）。
func named(name string, side model.Side) string {
	n := labels.Or(name, "未知")
	if strings.TrimSpace(string(side)) == "" {
		return n
	}
	return n + "（" + string(side) + "）"
}

// writeCitationHint 在回答末尾列出可引用 tick（对应回合区间内，可直接回放验证）。
func writeCitationHint(b *strings.Builder, slice *model.QASlice) {
	b.WriteString("\n可用引用（tick 均在对应回合区间内）：\n")
	for _, ra := range slice.Analyses {
		ticks := roundTicks(ra)
		if len(ticks) == 0 {
			fmt.Fprintf(b, "- 回合 %d：区间 %d ~ %d\n", ra.Number, ra.TickRange[0], ra.TickRange[1])
			continue
		}
		fmt.Fprintf(b, "- 回合 %d：%s（区间 %d ~ %d）\n", ra.Number, joinTicks(ticks), ra.TickRange[0], ra.TickRange[1])
	}
}

// roundTicks 收集回合内可用 tick（结构化事实优先，去重、升序，最多 6 个）。
func roundTicks(ra model.RoundAnalysis) []int {
	seen := map[int]bool{}
	var out []int
	add := func(tick int) {
		if tick <= 0 || seen[tick] {
			return
		}
		seen[tick] = true
		out = append(out, tick)
	}
	if op := ra.Opening; op != nil {
		add(op.Tick)
	}
	for _, t := range ra.Trades {
		add(t.Tick)
	}
	if fd := ra.FocusDeath; fd != nil {
		add(fd.Tick)
	}
	if cl := ra.Clutch; cl != nil {
		add(cl.StartTick)
	}
	if p := ra.Positioning; p != nil {
		add(p.Tick)
	}
	for _, d := range ra.Duels {
		add(d.Tick)
	}
	for _, sp := range ra.ManAdv {
		add(sp.StartTick)
	}
	for _, e := range ra.Evidence {
		add(e.Tick)
	}
	sort.Ints(out)
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

// joinTicks 把 tick 列表渲染成「5100、5200」。
func joinTicks(nums []int) string {
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, "、")
}

// qaNum 渲染数字（整数不带小数点，其余保留 1 位）。
func qaNum(v float64) string {
	r := math.Round(v*10) / 10
	if r == math.Trunc(r) {
		return strconv.FormatInt(int64(r), 10)
	}
	return strconv.FormatFloat(r, 'f', 1, 64)
}

// qaPreAimStatus 渲染预瞄是否超阈值。
func qaPreAimStatus(failed bool) string {
	if failed {
		return "超阈值"
	}
	return "阈值内"
}

// qaOutcome 把对枪结果映射成中文事实。
func qaOutcome(outcome string) string {
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
