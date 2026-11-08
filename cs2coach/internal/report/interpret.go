package report

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// interpret 文本来自 coach 层（LLM）。本文件只做「提取」，绝不改写、不发明：
// 拿得到就用，拿不到就退回规则层事实。

// roundHeadRe 匹配 LLM 报告里以回合号开头的小节标题行（best-effort）。
// 形如：「### 回合 14」「**回合 14**」「第 14 回合」「回合 14」等。
var roundHeadRe = regexp.MustCompile(`^\s{0,3}(?:#{1,6}[ \t]*)?(?:\*\*|__)?\s*(?:第\s*([0-9]+)\s*回合|回合\s*([0-9]+))`)

// sectionHeadRe 匹配各级 Markdown 标题（用于切段边界，如「## 值得保留的好回合」）。
var sectionHeadRe = regexp.MustCompile(`^\s{0,3}#{1,6}[ \t]`)

// oneLinerRe 定位「一句话结论」标记行。
var oneLinerRe = regexp.MustCompile(`一句话结论`)

// extractOneLiner 从 interpret 中取「一句话结论」。
// 只认带标记的结论句；找不到返回空串，由调用方退回事实摘要（不发明）。
func extractOneLiner(interpret string) string {
	lines := strings.Split(interpret, "\n")
	for i, line := range lines {
		loc := oneLinerRe.FindStringIndex(line)
		if loc == nil {
			continue
		}
		rest := strings.TrimSpace(line[loc[1]:])
		rest = strings.Trim(rest, "*_：: 　")
		if rest != "" {
			return rest
		}
		// 标记行本身没写内容：取其后第一个非空、非标题行
		for j := i + 1; j < len(lines) && j < i+4; j++ {
			next := strings.TrimSpace(lines[j])
			if next == "" || strings.HasPrefix(next, "#") {
				if next == "" {
					continue
				}
				break
			}
			return cleanBullet(next)
		}
	}
	return ""
}

// roundHeadingNum 判断一行是否为回合小节标题（best-effort），返回回合号。
// 必须像标题（#/** 开头或「回合 N」后缀很短）且不是问句/列表项——
// 否则「第 14 回合我为什么不转点？」这类追问文本会被误当成小节标题。
func roundHeadingNum(line string) (int, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.ContainsAny(trimmed, "？?") {
		return 0, false
	}
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "+ ") || strings.HasPrefix(trimmed, "* ") {
		return 0, false
	}
	loc := roundHeadRe.FindStringSubmatchIndex(line)
	if loc == nil {
		return 0, false
	}
	m := roundHeadRe.FindStringSubmatch(line)
	numStr := m[1]
	if numStr == "" {
		numStr = m[2]
	}
	num, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, false
	}
	headish := strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "**") || strings.HasPrefix(trimmed, "__")
	// 不带 #/** 的行要求「回合 N」后缀很短（如「回合 14 · 5v3 被翻」），长句视为正文
	if !headish && utf8.RuneCountInString(line[loc[1]:]) > 24 {
		return 0, false
	}
	return num, true
}

// headingTail 取标题行「回合 N」之后的同行正文（如「### 回合 14：开局白给」）。
func headingTail(line string) string {
	loc := roundHeadRe.FindStringIndex(line)
	if loc == nil {
		return ""
	}
	rest := strings.TrimSpace(line[loc[1]:])
	rest = strings.TrimLeft(rest, "*_：:·—-\t ")
	return strings.TrimSpace(rest)
}

// roundSection 取 interpret 中第 n 回合的小节文本（best-effort）。
// 找不到返回空串。
func roundSection(interpret string, n int) string {
	lines := strings.Split(interpret, "\n")
	start, end := -1, len(lines)
	for i, line := range lines {
		num, isHead := roundHeadingNum(line)
		if !isHead {
			// 小节在别的标题处截止
			if start >= 0 && sectionHeadRe.MatchString(line) {
				end = i
				break
			}
			continue
		}
		if start < 0 {
			if num == n {
				start = i
			}
			continue
		}
		// 已进入目标小节后遇到任何回合小节标题即截止
		end = i
		break
	}
	if start < 0 {
		return ""
	}
	body := lines[start:end]
	if len(body) == 0 {
		return ""
	}
	// 标题行可能带同行正文（如「### 回合 14：开局白给」），保留冒号后的部分
	out := make([]string, 0, len(body))
	if rest := headingTail(lines[start]); rest != "" {
		out = append(out, rest)
	}
	out = append(out, body[1:]...)
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// 三段关键词（LLM 输出结构里的固定小节名，best-effort 匹配）。
var (
	whatMarkers   = []string{"发生了什么", "事实"}
	whyMarkers    = []string{"为什么错", "解释", "为什么"}
	actionMarkers = []string{"下把怎么改", "怎么改", "建议", "处方", "动作"}
)

// markerKind 根据段首关键词把一行归入 what/why/action/空。
// 只认段首出现的标记——正文里出现「解释/建议」就把后续内容错分段是提取事故。
func markerKind(line string) string {
	s := strings.TrimSpace(line)
	s = strings.TrimLeft(s, "#*_- \t【[")
	for _, m := range whatMarkers {
		if strings.HasPrefix(s, m) {
			return "what"
		}
	}
	for _, m := range whyMarkers {
		if strings.HasPrefix(s, m) {
			return "why"
		}
	}
	for _, m := range actionMarkers {
		if strings.HasPrefix(s, m) {
			return "action"
		}
	}
	return ""
}

// cleanBullet 去掉列表/加粗标记，保留正文。
func cleanBullet(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "-*+ \t")
	s = strings.Trim(s, "*_ \t")
	s = strings.TrimSpace(s)
	return s
}

// splitSegments 把回合小节切成 三段（发生了什么 / 为什么错 / 下把怎么改）。
// 拿不到的段返回空。
func splitSegments(section string) (what, why []string, action string) {
	cur := ""
	var whatBuf, whyBuf, actionBuf []string
	for _, line := range strings.Split(section, "\n") {
		if kind := markerKind(line); kind != "" {
			// 标记行里冒号后的文本算该段第一条内容
			cur = kind
			rest := line
			if idx := strings.IndexAny(rest, "：:"); idx >= 0 {
				// 冒号可能是全角（3 字节），按 rune 宽度跳过
				_, size := utf8.DecodeRuneInString(rest[idx:])
				rest = rest[idx+size:]
			} else {
				rest = ""
			}
			if v := cleanBullet(rest); v != "" {
				switch cur {
				case "what":
					whatBuf = append(whatBuf, v)
				case "why":
					whyBuf = append(whyBuf, v)
				case "action":
					actionBuf = append(actionBuf, v)
				}
			}
			continue
		}
		v := cleanBullet(line)
		if v == "" {
			continue
		}
		switch cur {
		case "what":
			whatBuf = append(whatBuf, v)
		case "why":
			whyBuf = append(whyBuf, v)
		case "action":
			actionBuf = append(actionBuf, v)
		}
	}
	if len(actionBuf) > 0 {
		action = actionBuf[0] // 处方只要 1 条
	}
	return whatBuf, whyBuf, action
}

// headingRe 匹配 Markdown 标题并捕获级别与标题正文（允许 # 后没有空格）。
var headingRe = regexp.MustCompile(`^\s{0,3}(#{1,6})\s*(.*)$`)

// extractNamedSection 从 interpret 里抽取标题含 keyword 的小节正文（best-effort）。
// 用于 M2 的「## 站位解读」「## 对枪解读」：只取正文，不改写、不发明。
// 小节截止于下一个同级或更高级标题；找不到返回空串。
func extractNamedSection(interpret, keyword string) string {
	if strings.TrimSpace(interpret) == "" || keyword == "" {
		return ""
	}
	lines := strings.Split(interpret, "\n")
	start, level := -1, 0
	for i, line := range lines {
		m := headingRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if start < 0 {
			if strings.Contains(m[2], keyword) {
				start, level = i+1, len(m[1])
			}
			continue
		}
		// 已进入目标小节：遇到同级或更高级标题即截止。
		if len(m[1]) <= level {
			return strings.TrimSpace(strings.Join(lines[start:i], "\n"))
		}
	}
	if start < 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines[start:], "\n"))
}

// extractQuestions 从 interpret 的追问建议里取 2-3 个问题示例。
// 找不到返回空，由调用方按数据生成。
func extractQuestions(interpret string) []string {
	lines := strings.Split(interpret, "\n")
	inSection := false
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if sectionHeadRe.MatchString(line) || strings.HasPrefix(trimmed, "**追问") ||
			strings.HasPrefix(trimmed, "追问建议") || strings.HasPrefix(trimmed, "问题建议") {
			inSection = strings.Contains(line, "追问") || strings.Contains(line, "问题")
			continue
		}
		if !inSection {
			continue
		}
		v := cleanBullet(trimmed)
		v = strings.Trim(v, `"“”'‘’`)
		if v == "" {
			continue
		}
		if strings.ContainsAny(v, "？?") {
			out = append(out, v)
			if len(out) >= 3 {
				break
			}
		}
	}
	return out
}
