package report

import (
	"fmt"
	"strings"
)

// renderMarkdown 把页面数据渲染成一页 Markdown 教练报告。
func renderMarkdown(p *page) string {
	var b strings.Builder

	// 1. 标题
	fmt.Fprintf(&b, "# %s\n\n", p.Title)

	// 2. 第一屏：赛后判决（整场句 + 分组行）
	b.WriteString("**判决**：")
	b.WriteString(p.Verdict.Lead)
	b.WriteString("\n\n")
	if p.Verdict.Hint != "" {
		b.WriteString(p.Verdict.Hint)
		b.WriteString("\n\n")
	}
	if p.Verdict.empty() {
		b.WriteString("本场没有丢分回合。\n\n")
	} else {
		if len(p.Verdict.Yours) > 0 {
			b.WriteString("### 要你改的\n\n")
			for _, line := range p.Verdict.Yours {
				b.WriteString(line)
				b.WriteString("\n\n")
			}
		}
		if len(p.Verdict.Split) > 0 {
			b.WriteString("### 分开的\n\n")
			for _, line := range p.Verdict.Split {
				b.WriteString(line)
				b.WriteString("\n\n")
			}
		}
		for _, line := range p.Verdict.Other {
			b.WriteString(line)
			b.WriteString("\n\n")
		}
	}

	if p.FactsOnly {
		fmt.Fprintf(&b, "> %s；配置 LLM 后可得教练解读。\n\n", factsOnlyNote)
	}

	// 3. 值得保留的好回合
	b.WriteString("## 值得保留的好回合\n\n")
	if len(p.GoodRounds) == 0 {
		b.WriteString("本场未识别出值得保留的好回合。\n\n")
	}
	for _, g := range p.GoodRounds {
		fmt.Fprintf(&b, "### 回合 %d · tick %d\n\n", g.Number, g.Tick)
		if g.Reason != "" {
			fmt.Fprintf(&b, "- %s\n", g.Reason)
		}
		if g.Highlight != "" {
			fmt.Fprintf(&b, "- 高光：%s\n", g.Highlight)
		}
		fmt.Fprintf(&b, "\n定位：[tick %d] · demo_gototick %d\n\n", g.Tick, g.Tick)
	}

	// 4. M2：站位分析 / 对枪分析（纯事实，与 HTML 共用 page 数据）
	// 丢分原因频次不再渲染；LostRoundCauses 仍在 analysis.json。
	writePositioningMD(&b, p.Positioning)
	writeDuelingMD(&b, p.Dueling, p.LabLine())

	// 5. 追问入口
	b.WriteString("## 追问入口\n\n")
	b.WriteString("用 CLI 继续追问（检索该回合数据回答）：\n\n")
	fmt.Fprintf(&b, "```\n%s\n```\n\n", p.CliExample)
	if len(p.Questions) > 0 {
		b.WriteString("示例问题：\n\n")
		for _, q := range p.Questions {
			fmt.Fprintf(&b, "- %s\n", q)
		}
		b.WriteString("\n")
	}
	// 回看方法：tick 拿到后去哪输入，全仓只写这一次。
	b.WriteString("## 如何回看\n\n")
	fmt.Fprintf(&b, "%s\n\n", p.HowTo)

	b.WriteString("---\n\n")
	b.WriteString("事实与证据由规则层（tactics）生成；LLM 只解读不发明——证据引用架构锁。工具：cs2coach。\n")
	return b.String()
}

// writePositioningMD 渲染「站位分析」事实节（可选教练解读追加在事实之后）。
func writePositioningMD(b *strings.Builder, v positioningView) {
	b.WriteString("## 站位分析\n\n")
	if !v.HasData {
		b.WriteString(v.Missing)
		b.WriteString("\n\n")
	} else {
		b.WriteString(v.Summary)
		b.WriteString("\n\n")
		b.WriteString("### 死亡热区 Top3\n\n")
		if len(v.HotZones) == 0 {
			b.WriteString(noHotZoneText)
			b.WriteString("\n\n")
		} else {
			for _, hz := range v.HotZones {
				fmt.Fprintf(b, "- %s\n", hz.Line)
			}
			b.WriteString("\n")
		}
		b.WriteString("### 高危站位\n\n")
		if len(v.HighRisk) == 0 {
			b.WriteString(noHighRiskText)
			b.WriteString("\n\n")
		} else {
			for _, row := range v.HighRisk {
				fmt.Fprintf(b, "- 回合 %d [tick %d] %s\n", row.Round, row.Tick, row.Text)
			}
			b.WriteString("\n")
		}
	}
	if v.HasData {
		writeCoachMD(b, v.Coach)
	}
}

// writeDuelingMD 渲染「对枪分析」事实节（可选教练解读追加在事实之后）。
// labLine 是「对枪分析」标题正下方的实验室基准行（合法时带数字，否则「还没有实验室基准」）。
func writeDuelingMD(b *strings.Builder, v duelingView, labLine string) {
	b.WriteString("## 对枪分析\n\n")
	if labLine != "" {
		b.WriteString(labLine)
		b.WriteString("\n\n")
	}
	if !v.HasData {
		b.WriteString(v.Missing)
		b.WriteString("\n\n")
	} else {
		b.WriteString(v.Summary)
		b.WriteString("\n\n")
		b.WriteString("### 对枪明细\n\n")
		if len(v.Rows) == 0 {
			b.WriteString(noDuelRowsText)
			b.WriteString("\n\n")
		} else {
			for _, row := range v.Rows {
				fmt.Fprintf(b, "- 回合 %d [tick %d] %s\n", row.Round, row.Tick, row.Text)
			}
			b.WriteString("\n")
		}
	}
	if v.HasData {
		writeCoachMD(b, v.Coach)
	}
}

// writeCoachMD 追加 LLM 解读正文（只做 Markdown 拼装，不改写内容）。
func writeCoachMD(b *strings.Builder, coach string) {
	coach = strings.TrimSpace(coach)
	if coach == "" {
		return
	}
	b.WriteString("**教练解读**\n\n")
	b.WriteString(coach)
	b.WriteString("\n\n")
}
