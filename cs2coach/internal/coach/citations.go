package coach

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"cs2-demo-review/internal/model"
)

// placeholder 是无证据论断被删除后的替换标记。
const placeholder = "【无证据，已删】"

// 引用抽取正则（契约）。
//
// 本地小模型不会严格照抄格式，实测常见写法都按同一契约校验——认得出就必须校验，
// 不能因为写法不同就悄悄放行：
//
//	回合：回合 3 / 回合3 / 回合 #3 / 第 3 回合 / 3 回合 / 回合 2-3 / 第 3 局 / round 3 / R3 / [R3]
//	tick：tick 5100 / tick:5100 / tick=5100 / tick #5100 / demo_gototick 5100 / demo_goto_tick 5100
//	杂项：全角数字（５１００）、全角冒号、千分位（5,100）
//
// 唯一需要回避的歧义是武器名「R8 Revolver / R8 左轮」——它长得像 R8 回合引用，单独排除。
var (
	// reRoundAfter 匹配「回合 3」「回合3」「回合 #3」；「回合 2-3」两端都算引用。
	reRoundAfter = regexp.MustCompile(`回合\s*#?\s*(\d+)(?:\s*[-~到至]\s*(\d+))?`)
	// reRoundBefore 匹配「第 3 回合」「3 回合」反序写法。
	reRoundBefore = regexp.MustCompile(`(\d+)\s*回合`)
	// reRoundOrdinal 匹配「第 3 局」「第 3 小局」（口语里的「局」就是回合）。
	reRoundOrdinal = regexp.MustCompile(`第\s*(\d+)\s*小?局`)
	// reRoundR 匹配「R3」「r 3」「[R3]」；\b 保证不会命中 AK-47 / M4A1 这类字母数字串。
	reRoundR = regexp.MustCompile(`(?i)\bR\s*(\d{1,3})\b`)
	// reRevolver 用于排除武器名 R8 Revolver / R8 左轮。
	reRevolver = regexp.MustCompile(`(?i)^\s*(revolver|左轮)`)
	// reTick 匹配「tick 5100」「tick: 5100」「tick=5100」「tick #5100」。
	// \b 防止命中 demo_gototick 的尾部；至少 2 位数字，避免把「tick 数」当引用。
	reTick = regexp.MustCompile(`(?i)\btick\s*[:=#]?\s*(\d{2,8})`)
	// reGotoTick 匹配「demo_gototick 5100」以及下划线被写坏/写成空格的变体。
	reGotoTick = regexp.MustCompile(`(?i)demo[_\s]*goto[_\s]*tick\s*[:=#]?\s*(\d{2,8})`)
)

// advisoryHard 是建议类措辞：只要命中且整句没有任何引用，就按「无证据建议」删除。
//
// 在契约四词（建议/应该/需要/必须）之外补了小模型的高频说法：
// 推荐 / 务必 / 记得 / 下次 / 试试 / 最好 / 切记 / 应当。
var advisoryHard = []string{
	"建议", "应该", "需要", "必须",
	"推荐", "务必", "记得", "下次", "试试", "最好", "切记", "应当",
}

// yaoNonAdvisory 是裸「要」的非建议构词（重要/要点/要求…）。
// 建议句判定时先把这些构词整词屏蔽，只认剩下的裸「要」（如「要多补枪」），
// 避免「这一点很重要」类事实句被误删。
var yaoNonAdvisory = []string{
	"要不是", "要不然", "要素", "要害", "要塞", "要紧", "要领", "要挟", "要么",
	"要职", "要员", "要目", "要义", "要闻", "要道", "要冲", "要隘", "要地", "要津",
	"要诀", "要略", "要求", "要点",
	"重要", "需要", "必要", "主要", "次要", "首要", "简要", "摘要", "纲要", "提要",
	"扼要", "概要", "将要", "只要", "机要",
}

// reportSectionLabels 是输出模板里「必须带引用」的小节标题。
//
// 为什么需要这条：通用建议句规则只抓「应该/必须」类措辞，抓不到
// 「为什么错：他站位太靠前」这种纯解释型幻觉——而它恰恰是小模型最常写的东西。
// 模板是固定契约，所以按小节标签直接要求引用是安全的。
const (
	labelWhatHappened = "发生了什么"
	labelWhyWrong     = "为什么错"
	labelHowToFix     = "下把怎么改"
)

// citeRef 是一处引用（回合或 tick）及其在行内的字节位置。
type citeRef struct {
	kind  string // "round" | "tick"
	value int
	pos   int
}

// ValidateCitations 校验报告里的回合/tick 引用是否存在于 Analysis，剔除无证据论断（架构锁：LLM 只解读不发明）。
//
// 规则（按顺序执行，命中即整行替换成【无证据，已删】并记 CitationError）：
//  1. 模板段落漏引用：「发生了什么」「为什么错」没有引用；「下把怎么改」没有 tick。
//  2. 引用非法：回合号 ∉ [1, len(rounds)]；tick 不落在对应回合 TickRange 放宽
//     ±2×tick_rate 的区间内（tick_rate 缺失按 64 计）。
//  3. 建议句（含 建议/应该/需要/必须/推荐/务必/记得/下次/最好 等，或裸「要」；
//     「重要」等构词不算）若整句没有任何引用，删除该句；Markdown 标题行是结构骨架，豁免。
//
// 净化前会先剥掉首尾整行的 Markdown 代码围栏（小模型习惯把整篇包在 ```markdown 里）。
// 返回净化后的文本与全部 CitationError（Line 以剥围栏后的文本为准，1 起）。
func ValidateCitations(report string, a *model.Analysis) (string, []CitationError) {
	lines := strings.Split(stripCodeFences(report), "\n")
	var errs []CitationError
	for i, line := range lines {
		lineNo := i + 1
		if reason := structuralRefReason(line); reason != "" {
			lines[i] = replaceWithPlaceholder(line)
			errs = append(errs, CitationError{Line: lineNo, Text: line, Why: reason})
			continue
		}
		if reason := validateLineRefs(line, a); reason != "" {
			lines[i] = replaceWithPlaceholder(line)
			errs = append(errs, CitationError{Line: lineNo, Text: line, Why: reason})
			continue
		}
		cleaned, lineErrs := stripUncitedAdvisory(line, lineNo)
		lines[i] = cleaned
		errs = append(errs, lineErrs...)
	}
	return strings.Join(lines, "\n"), errs
}

// stripCodeFences 去掉首尾整行的 Markdown 代码围栏（``` / ```markdown）。
// 只处理首尾：中间的围栏是模型自己的排版，不属于契约范围。
func stripCodeFences(report string) string {
	lines := strings.Split(report, "\n")
	for len(lines) > 0 && isFenceLine(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && isFenceLine(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// isFenceLine 判断整行是否是代码围栏。
func isFenceLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "```")
}

// structuralRefReason 按输出模板的小节契约校验引用：
//   - 「发生了什么」「为什么错」必须有引用（回合或 tick）；
//   - 「下把怎么改」必须带 tick（[tick T] 或 demo_gototick T），回合号不够。
//
// 标题行（# 开头）只做结构，不参与该检查。
func structuralRefReason(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return ""
	}
	refs := extractRefs(line)
	hasTick := false
	for _, r := range refs {
		if r.kind == "tick" {
			hasTick = true
			break
		}
	}
	if strings.Contains(line, labelHowToFix) && !hasTick && !strings.Contains(line, "demo_gototick") {
		return "「下把怎么改」没有 tick 引用（必须带 demo_gototick T）"
	}
	if len(refs) == 0 && (strings.Contains(line, labelWhatHappened) || strings.Contains(line, labelWhyWrong)) {
		return "模板段落缺少引用（架构锁：每条结论必须带 回合R [tick T]）"
	}
	return ""
}

// validateLineRefs 校验一行内全部引用；合法返回空串，否则返回中文原因（多条以「；」连接）。
func validateLineRefs(line string, a *model.Analysis) string {
	refs := extractRefs(line)
	if len(refs) == 0 {
		return ""
	}
	nRounds := 0
	if a != nil {
		nRounds = len(a.Rounds)
	}
	var reasons []string
	seen := make(map[string]bool, len(refs))
	addReason := func(format string, args ...any) {
		s := fmt.Sprintf(format, args...)
		if seen[s] {
			return
		}
		seen[s] = true
		reasons = append(reasons, s)
	}
	for _, ref := range refs {
		if ref.kind == "round" {
			if ref.value < 1 || ref.value > nRounds {
				addReason("回合 %d 不存在（本场共 %d 回合）", ref.value, nRounds)
			}
			continue
		}
		// tick 引用：优先与行内最近的回合引用配对校验。
		r := associateRound(ref, refs, a)
		if r == nil {
			if a != nil && !tickInAnyRound(ref.value, a) {
				addReason("tick %d 不在任何回合区间内", ref.value)
			}
			continue
		}
		lo, hi, widen := widenedTickRange(r, a)
		if ref.value < lo || ref.value > hi {
			addReason("tick %d 不在回合 %d 的 tick 区间 %d~%d（已放宽 ±%d）",
				ref.value, r.Number, lo, hi, widen)
		}
	}
	return strings.Join(reasons, "；")
}

// extractRefs 抽取一行内的回合/tick 引用，按出现位置排序。
func extractRefs(line string) []citeRef {
	line = normalizeCitationText(line)
	var refs []citeRef
	add := func(kind string, value, pos int) {
		refs = append(refs, citeRef{kind: kind, value: value, pos: pos})
	}
	for _, m := range reRoundAfter.FindAllStringSubmatchIndex(line, -1) {
		add("round", atoiSlice(line, m[2], m[3]), m[0])
		if m[4] >= 0 {
			add("round", atoiSlice(line, m[4], m[5]), m[4])
		}
	}
	for _, m := range reRoundBefore.FindAllStringSubmatchIndex(line, -1) {
		add("round", atoiSlice(line, m[2], m[3]), m[0])
	}
	for _, m := range reRoundOrdinal.FindAllStringSubmatchIndex(line, -1) {
		add("round", atoiSlice(line, m[2], m[3]), m[0])
	}
	for _, m := range reRoundR.FindAllStringSubmatchIndex(line, -1) {
		if isWeaponR8(line, m) {
			continue // 「R8 Revolver」是武器名，不是回合引用
		}
		add("round", atoiSlice(line, m[2], m[3]), m[0])
	}
	for _, re := range []*regexp.Regexp{reGotoTick, reTick} {
		for _, m := range re.FindAllStringSubmatchIndex(line, -1) {
			add("tick", atoiSlice(line, m[2], m[3]), m[0])
		}
	}
	// 稳定排序：同位置先 round 后 tick（sortRefs 按 pos）。
	sortRefs(refs)
	return refs
}

// atoiSlice 取捕获组数字，越界/非法返回 0。
func atoiSlice(s string, lo, hi int) int {
	if lo < 0 || hi > len(s) || lo >= hi {
		return 0
	}
	v, err := strconv.Atoi(s[lo:hi])
	if err != nil {
		return 0
	}
	return v
}

// isWeaponR8 判断 R\d+ 形态是否是武器名 R8 Revolver / R8 左轮。
// m 是 reRoundR 的整段匹配下标；只看匹配之后的文本。
func isWeaponR8(line string, m []int) bool {
	if line[m[2]:m[3]] != "8" {
		return false
	}
	return reRevolver.MatchString(line[m[1]:])
}

// sortRefs 按位置排序引用（小函数便于测试）。
func sortRefs(refs []citeRef) {
	for i := 1; i < len(refs); i++ {
		for j := i; j > 0 && refs[j].pos < refs[j-1].pos; j-- {
			refs[j], refs[j-1] = refs[j-1], refs[j]
		}
	}
}

// normalizeCitationText 统一全角数字/标点与千分位逗号，让
// 「[tick ５１００]」「tick：5,100」「（R３）」和 ASCII 写法走同一组正则。
//
// 位置只用于同一行内比较先后（associateRound），字节长度变化不影响结论。
func normalizeCitationText(s string) string {
	if !needsNormalize(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= '０' && r <= '９':
			b.WriteRune('0' + (r - '０'))
		case r == '：':
			b.WriteByte(':')
		case r == '，':
			b.WriteByte(',')
		case r == '（':
			b.WriteByte('(')
		case r == '）':
			b.WriteByte(')')
		case r == '～' || r == '－' || r == '—':
			b.WriteByte('-')
		case r == '＃':
			b.WriteByte('#')
		case r == '　':
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return stripDigitSeparators(b.String())
}

// needsNormalize 判断是否含需要归一化的字符（纯 ASCII 快路径）。
func needsNormalize(s string) bool {
	for _, r := range s {
		switch {
		case r > 127:
			return true
		case r == ',':
			return true
		}
	}
	return false
}

// stripDigitSeparators 去掉数字之间的逗号（5,100 → 5100），保留中文逗号语境。
func stripDigitSeparators(s string) string {
	if !strings.Contains(s, ",") {
		return s
	}
	rs := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range rs {
		if r == ',' && i > 0 && i+1 < len(rs) && isDigitRune(rs[i-1]) && isDigitRune(rs[i+1]) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isDigitRune 判断 rune 是否 ASCII 数字。
func isDigitRune(r rune) bool { return r >= '0' && r <= '9' }

// associateRound 为 tick 引用配对回合：取行内最近的前向回合引用，
// 没有则取最近的后向回合引用；行内无回合引用返回 nil（改用全量区间兜底校验）。
func associateRound(tick citeRef, refs []citeRef, a *model.Analysis) *model.RoundAnalysis {
	if a == nil {
		return nil
	}
	bestPos, found := 0, false
	for _, ref := range refs {
		if ref.kind != "round" {
			continue
		}
		if ref.pos <= tick.pos {
			if !found || ref.pos > bestPos {
				bestPos, found = ref.pos, true
			}
		}
	}
	if !found {
		for _, ref := range refs {
			if ref.kind != "round" {
				continue
			}
			if !found || ref.pos < bestPos {
				bestPos, found = ref.pos, true
			}
		}
	}
	if !found {
		return nil
	}
	// 回合号 → 回合数据：按 Number 字段配对（切片乱序也不能配错 TickRange）。
	for _, ref := range refs {
		if ref.kind == "round" && ref.pos == bestPos {
			return roundByNumber(a, ref.value)
		}
	}
	return nil
}

// roundByNumber 按回合号取回合数据：优先匹配 Number 字段，
// 缺失/不匹配时按 1..N 位次兜底（契约：回合号从 1 递增）。
func roundByNumber(a *model.Analysis, n int) *model.RoundAnalysis {
	if a == nil || n < 1 || n > len(a.Rounds) {
		return nil
	}
	for i := range a.Rounds {
		if a.Rounds[i].Number == n {
			return &a.Rounds[i]
		}
	}
	r := a.Rounds[n-1]
	return &r
}

// tickInAnyRound 判断 tick 是否落在任意回合的放宽区间（行内无回合引用时的兜底）。
func tickInAnyRound(tick int, a *model.Analysis) bool {
	for i := range a.Rounds {
		lo, hi, _ := widenedTickRange(&a.Rounds[i], a)
		if tick >= lo && tick <= hi {
			return true
		}
	}
	return false
}

// widenedTickRange 返回回合 TickRange 放宽 ±2×tick_rate 后的区间。
// tick_rate 缺失（≤0）时按 64 计（CS2 常见值），放宽 128。
func widenedTickRange(r *model.RoundAnalysis, a *model.Analysis) (lo, hi, widen int) {
	tickRate := 64.0
	if a != nil && a.Meta.TickRate > 0 {
		tickRate = a.Meta.TickRate
	}
	widen = int(2 * tickRate)
	lo = r.TickRange[0] - widen
	hi = r.TickRange[1] + widen
	return lo, hi, widen
}

// stripUncitedAdvisory 删除一行内没有任何引用的建议句；行内 Markdown 标题（# 开头）豁免。
// 返回处理后的行与逐句 CitationError。整句被删光时整行替换为占位标记。
func stripUncitedAdvisory(line string, lineNo int) (string, []CitationError) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return line, nil
	}
	sentences := splitSentences(line)
	var errs []CitationError
	kept := make([]string, 0, len(sentences))
	for _, s := range sentences {
		if strings.TrimSpace(s) == "" {
			continue
		}
		if isUncitedAdvisory(s) {
			errs = append(errs, CitationError{
				Line: lineNo,
				Text: s,
				Why:  "无引用的建议句（架构锁：无证据的判断直接删）",
			})
			continue
		}
		kept = append(kept, s)
	}
	if len(errs) == 0 {
		return line, nil
	}
	if len(kept) == 0 {
		return replaceWithPlaceholder(line), errs
	}
	return strings.Join(kept, ""), errs
}

// isUncitedAdvisory 判断句子是否为无引用的建议句：
// 含建议措辞（见 advisoryHard）或裸「要」（「重要」等构词不算），
// 且不含「回合」语境、也不含任何 tick/demo_gototick 引用。
func isUncitedAdvisory(sentence string) bool {
	if !hasAdviceWord(sentence) {
		return false
	}
	// 有证据锚点就保留：回合N / 第N回合 / 第N局 / R3 / tick / demo_gototick 任意一种。
	// 早先只认「回合」二字，导致「第3局应该先架点」这类合法引用被当成无引用建议删掉。
	if len(extractRefs(sentence)) > 0 {
		return false
	}
	if strings.Contains(sentence, "回合") {
		return false
	}
	normalized := normalizeCitationText(sentence)
	if reTick.MatchString(normalized) || reGotoTick.MatchString(normalized) {
		return false
	}
	return true
}

// hasAdviceWord 判断句子是否含建议措辞：advisoryHard 任意命中，
// 或屏蔽非建议构词后仍有裸「要」。
func hasAdviceWord(sentence string) bool {
	for _, w := range advisoryHard {
		if strings.Contains(sentence, w) {
			return true
		}
	}
	masked := sentence
	for _, w := range yaoNonAdvisory {
		masked = strings.ReplaceAll(masked, w, "\x00")
	}
	return strings.Contains(masked, "要")
}

// splitSentences 按中英文句末标点切分句子，标点保留在句尾。
func splitSentences(line string) []string {
	var out []string
	var cur strings.Builder
	for _, r := range line {
		cur.WriteRune(r)
		switch r {
		case '。', '！', '？', '；', '!', '?', ';':
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// replaceWithPlaceholder 保留行首缩进与 Markdown 列表前缀，正文替换为占位标记。
func replaceWithPlaceholder(line string) string {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	indent := line[:i]
	rest := line[i:]
	prefix := ""
	for _, p := range []string{"- ", "* ", "+ ", "> "} {
		if strings.HasPrefix(rest, p) {
			prefix = p
			break
		}
	}
	// "1. " 形式的有序列表前缀。
	if prefix == "" {
		j := 0
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if j > 0 && strings.HasPrefix(rest[j:], ". ") {
			prefix = rest[:j+2]
		}
	}
	return indent + prefix + placeholder
}
