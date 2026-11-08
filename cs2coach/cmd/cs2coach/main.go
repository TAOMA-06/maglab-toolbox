// cs2coach 是 CS2 AI 分析教练的命令行入口。
//
// 三层管线：parse（.dem → 事实 JSON）→ tactics（规则归因）→ coach/report（LLM 解读 + 报告）。
// 用法见 README.md；设计文档见 work-kb/projects/cs2-demo-review/design.md。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cs2-demo-review/internal/coach"
	"cs2-demo-review/internal/model"
	"cs2-demo-review/internal/parse"
	"cs2-demo-review/internal/qa"
	"cs2-demo-review/internal/report"
	"cs2-demo-review/internal/tactics"
)

const usageText = `cs2coach — CS2 AI 分析教练（赛后复盘，不是统计网站）

用法：
  cs2coach parse   <demo.dem> [-o match.json] [--player 名字|SteamID64]
  cs2coach analyze <match.json> [-o analysis.json]
  cs2coach report  <analysis.json> [--out-dir 目录] [--llm] [--prompt-only] [--interpret 解读.txt]
  cs2coach ask     <analysis.json> "问题"     （或 -i 进入交互追问）
  cs2coach run     <demo.dem> [--out-dir 目录] [--player ...] [--llm] [--prompt-only] [--interpret 解读.txt]
  cs2coach players <match.json>               （列出玩家，帮 --player 选人）
  cs2coach metrics <analysis.json> [more...]  （输出 M2 预瞄/急停指标分布，供阈值校准）
  cs2coach export  <demo.dem|目录> -o samples.jsonl [--player ...] [--recursive]
                                              （导出每回合训练样本 JSONL）

退出码：0 成功；2 参数错；3 解析失败；4 LLM 不可用（未配置或调用失败，但本次要求了 LLM 解读）。
 --interpret：把 --prompt-only 落盘的 prompt.txt 粘贴给任意 LLM 后，把拿到的解读存成文本文件，
   再用本参数重新生成报告（报告将内嵌该解读并过证据校验）。
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, usageText)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "parse":
		return cmdParse(rest)
	case "analyze":
		return cmdAnalyze(rest)
	case "report":
		return cmdReport(rest)
	case "ask":
		return cmdAsk(rest)
	case "run":
		return cmdRun(rest)
	case "players":
		return cmdPlayers(rest)
	case "metrics":
		return cmdMetrics(rest)
	case "export":
		return cmdExport(rest)
	case "-h", "--help", "help":
		fmt.Print(usageText)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "未知命令：%s\n\n%s", cmd, usageText)
		return 2
	}
}

// cmdParse 解析 .dem 为事实 JSON。
func cmdParse(args []string) int {
	fs := flag.NewFlagSet("parse", flag.ContinueOnError)
	args = reorderArgs(args, nil)
	out := fs.String("o", "match.json", "输出 JSON 路径")
	player := fs.String("player", "", "焦点玩家（名字或 SteamID64）；省略则自动选 POV 玩家")
	if code := parseFlags(fs, args); code >= 0 {
		return code
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "用法：cs2coach parse <demo.dem> [-o match.json] [--player ...]")
		return 2
	}
	opts := parse.Options{}
	if s := strings.TrimSpace(*player); s != "" {
		if isSteamID(s) {
			opts.FocusSteamID = s
		} else {
			opts.FocusName = s
		}
	}
	m, err := parse.Parse(fs.Arg(0), opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "解析失败：%v\n", err)
		return 3
	}
	if err := writeJSON(*out, m); err != nil {
		fmt.Fprintf(os.Stderr, "写入失败：%v\n", err)
		return 3
	}
	fmt.Printf("解析完成：%s（%d 回合，比分 %d:%d）→ %s\n",
		m.Meta.Map, m.Meta.TotalRounds, m.Meta.FinalScore.T, m.Meta.FinalScore.CT, *out)
	if m.Meta.FocusPlayerName != "" {
		fmt.Printf("焦点玩家：%s (%s)\n", m.Meta.FocusPlayerName, m.Meta.FocusPlayer)
	}
	return 0
}

// cmdAnalyze 运行战术层规则引擎。
func cmdAnalyze(args []string) int {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	args = reorderArgs(args, nil)
	out := fs.String("o", "analysis.json", "输出 JSON 路径")
	if code := parseFlags(fs, args); code >= 0 {
		return code
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "用法：cs2coach analyze <match.json> [-o analysis.json]")
		return 2
	}
	m, err := loadMatch(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取失败：%v\n", err)
		return 3
	}
	a, err := tactics.Analyze(m, tactics.Options{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "归因失败：%v\n", err)
		return 3
	}
	if err := tactics.SaveJSON(a, *out); err != nil {
		fmt.Fprintf(os.Stderr, "写入失败：%v\n", err)
		return 3
	}
	fmt.Printf("归因完成：%d 回合，丢分原因频次 → %s\n", len(a.Rounds), *out)
	for _, c := range a.LostRoundCauses {
		fmt.Printf("  %s：%d 次（%.0f%%）回合 %v\n", c.Label, c.Count, c.Fraction*100, c.Rounds)
	}
	return 0
}

// cmdReport 生成一页教练报告（Markdown + 本地 HTML）。
func cmdReport(args []string) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	args = reorderArgs(args, map[string]bool{"llm": true, "prompt-only": true})
	outDir := fs.String("out-dir", "out", "输出目录")
	useLLM := fs.Bool("llm", false, "调用 LLM 生成教练解读")
	promptOnly := fs.Bool("prompt-only", false, "只落盘提示词（人工粘贴给任意 LLM）")
	interpretPath := fs.String("interpret", "", "外部解读文件（--prompt-only 人工流程的回填口）")
	labBaselinePath := fs.String("lab-baseline", "", "实验室基准文件（maglab-baseline.json）；不传则报告显示「还没有实验室基准」")
	if code := parseFlags(fs, args); code >= 0 {
		return code
	}
	if *useLLM && *promptOnly {
		fmt.Fprintln(os.Stderr, "--llm 与 --prompt-only 互斥，只能二选一")
		return 2
	}
	if *interpretPath != "" && (*useLLM || *promptOnly) {
		fmt.Fprintln(os.Stderr, "--interpret 与 --llm/--prompt-only 互斥：回填时只需 --interpret")
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "用法：cs2coach report <analysis.json> [--out-dir dir] [--llm] [--prompt-only] [--interpret 解读.txt]")
		return 2
	}
	a, err := loadAnalysis(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取失败：%v\n", err)
		return 3
	}
	return produceReport(a, *outDir, *useLLM, *promptOnly, *interpretPath, *labBaselinePath)
}

// cmdRun 一条龙：parse → analyze → report。
func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	args = reorderArgs(args, map[string]bool{"llm": true, "prompt-only": true})
	outDir := fs.String("out-dir", "out", "输出目录")
	player := fs.String("player", "", "焦点玩家（名字或 SteamID64）")
	useLLM := fs.Bool("llm", false, "调用 LLM 生成教练解读")
	promptOnly := fs.Bool("prompt-only", false, "只落盘提示词")
	interpretPath := fs.String("interpret", "", "外部解读文件（--prompt-only 人工流程的回填口）")
	labBaselinePath := fs.String("lab-baseline", "", "实验室基准文件（maglab-baseline.json）；不传则报告显示「还没有实验室基准」")
	if code := parseFlags(fs, args); code >= 0 {
		return code
	}
	if *useLLM && *promptOnly {
		fmt.Fprintln(os.Stderr, "--llm 与 --prompt-only 互斥，只能二选一")
		return 2
	}
	if *interpretPath != "" && (*useLLM || *promptOnly) {
		fmt.Fprintln(os.Stderr, "--interpret 与 --llm/--prompt-only 互斥：回填时只需 --interpret")
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "用法：cs2coach run <demo.dem> [--out-dir dir] [--player ...] [--llm] [--prompt-only] [--interpret 解读.txt]")
		return 2
	}
	opts := parse.Options{}
	if s := strings.TrimSpace(*player); s != "" {
		if isSteamID(s) {
			opts.FocusSteamID = s
		} else {
			opts.FocusName = s
		}
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "创建目录失败：%v\n", err)
		return 3
	}
	fmt.Println("① 解析 demo …")
	m, err := parse.Parse(fs.Arg(0), opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "解析失败：%v\n", err)
		return 3
	}
	matchPath := filepath.Join(*outDir, "match.json")
	if err := writeJSON(matchPath, m); err != nil {
		fmt.Fprintf(os.Stderr, "写入失败：%v\n", err)
		return 3
	}
	fmt.Printf("   %s：%d 回合，比分 %d:%d\n", m.Meta.Map, m.Meta.TotalRounds, m.Meta.FinalScore.T, m.Meta.FinalScore.CT)

	fmt.Println("② 规则归因 …")
	a, err := tactics.Analyze(m, tactics.Options{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "归因失败：%v\n", err)
		return 3
	}
	analysisPath := filepath.Join(*outDir, "analysis.json")
	if err := tactics.SaveJSON(a, analysisPath); err != nil {
		fmt.Fprintf(os.Stderr, "写入失败：%v\n", err)
		return 3
	}

	fmt.Println("③ 生成报告 …")
	code := produceReport(a, *outDir, *useLLM, *promptOnly, *interpretPath, *labBaselinePath)
	if code == 0 || code == 4 {
		fmt.Printf("\n完成。产物在 %s/：report.md report.html analysis.json match.json\n", *outDir)
		fmt.Println("追问：" + askHint(a, analysisPath))
	}
	return code
}

// askHint 生成追问示例（按 Suspects/Top3 顺序取最亏回合，不再硬编码回合号）。
func askHint(a *model.Analysis, analysisPath string) string {
	roundNo := 1
	if len(a.Suspects) > 0 {
		roundNo = a.Suspects[0].Round
	} else {
		for _, ra := range a.Rounds {
			if !ra.FocusWon && ra.Winner != model.SideDraw {
				roundNo = ra.Number
				break
			}
		}
	}
	return fmt.Sprintf(`cs2coach ask %s "第%d回合我为什么不转点？"`, analysisPath, roundNo)
}

// produceReport 统一的报告产出逻辑（report 与 run 共用）。
// interpretPath 非空时读入外部解读文件（--prompt-only 人工流程的回填口）。
// labBaselinePath 非空时读入实验室基准文件；不传则不找文件，报告显示「还没有实验室基准」。
// useLLM 要求 LLM 但不可用（未配置/调用失败）时：仍产出纯事实报告，但返回 4，
// 让脚本能区分「真解读」与「降级版」。
func produceReport(a *model.Analysis, outDir string, useLLM, promptOnly bool, interpretPath string, labBaselinePath string) int {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "创建目录失败：%v\n", err)
		return 3
	}
	system, user := coach.ReportPrompt(a)
	promptPath := filepath.Join(outDir, "prompt.txt")
	interpret := ""
	llmWanted := useLLM || interpretPath != "" // 本次是否要求了 LLM 解读（含 --interpret 回填）

	writePrompt := func() {
		if err := os.WriteFile(promptPath, []byte(system+"\n\n---\n\n"+user), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "警告：提示词落盘失败：%v\n", err)
		}
	}

	switch {
	case promptOnly:
		writePrompt()
		fmt.Printf("提示词已落盘：%s\n三步回填：1) 粘贴给任意 LLM；2) 把解读存成文件（如 interpret.txt）；\n3) 用 report/run --interpret interpret.txt --out-dir %s 重新生成报告。\n", promptPath, outDir)
	case interpretPath != "":
		raw, err := os.ReadFile(interpretPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "解读文件读取失败：%v\n", err)
			return 3
		}
		cleaned, cites := coach.ValidateCitations(string(raw), a)
		if len(cites) > 0 {
			fmt.Printf("证据校验：删除/标注 %d 处无证据论断\n", len(cites))
		}
		interpret = cleaned
		writePrompt()
	case useLLM:
		client, err := coach.NewClientFromEnv()
		if err != nil {
			if errors.Is(err, coach.ErrNotConfigured) {
				fmt.Fprintln(os.Stderr, "未配置 LLM：请设置 CS2COACH_LLM_API_KEY 或 ANTHROPIC_API_KEY（或改用 --prompt-only）")
			} else {
				fmt.Fprintf(os.Stderr, "LLM 初始化失败：%v\n", err)
			}
			writePrompt()
			break
		}
		fmt.Printf("调用 LLM（%s）…\n", client.Model())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		raw, err := client.Complete(ctx, system, user)
		if err != nil {
			fmt.Fprintf(os.Stderr, "LLM 调用失败：%v\n（报告已降级为纯事实版，退出码 4 标记本次无 LLM 解读；提示词已存 %s）\n", err, promptPath)
			writePrompt()
			// 降级为纯事实报告，保证产出可用；但用退出码 4 明确标记。
			break
		}
		cleaned, cites := coach.ValidateCitations(raw, a)
		if len(cites) > 0 {
			fmt.Printf("证据校验：删除/标注 %d 处无证据论断\n", len(cites))
		}
		interpret = cleaned
		writePrompt()
		if err := os.WriteFile(filepath.Join(outDir, "report_interpret.txt"), []byte(cleaned), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "警告：解读落盘失败：%v\n", err)
		}
	default:
		// 无 LLM：纯事实版
	}

	var labBaseline *report.LabBaseline
	if strings.TrimSpace(labBaselinePath) != "" {
		b, err := report.LoadLabBaselineFile(strings.TrimSpace(labBaselinePath))
		if err != nil {
			fmt.Fprintf(os.Stderr, "实验室基准无效：%v（报告仍按“还没有实验室基准”生成）\n", err)
		} else {
			labBaseline = b
		}
	}

	md, err := report.MarkdownWithBaseline(a, interpret, labBaseline)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Markdown 渲染失败：%v\n", err)
		return 3
	}
	html, err := report.HTMLWithBaseline(a, interpret, labBaseline)
	if err != nil {
		fmt.Fprintf(os.Stderr, "HTML 渲染失败：%v\n", err)
		return 3
	}
	if err := os.WriteFile(filepath.Join(outDir, "report.md"), []byte(md), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "写入失败：%v\n", err)
		return 3
	}
	if err := os.WriteFile(filepath.Join(outDir, "report.html"), []byte(html), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "写入失败：%v\n", err)
		return 3
	}
	fmt.Printf("报告已生成：%s/report.md 与 report.html（%s）\n", outDir, reportMode(llmWanted, promptOnly, interpretPath, interpret))
	if llmWanted && interpret == "" {
		// 要求了 LLM 解读但最终没有：纯事实降级版，用 4 标记。
		return 4
	}
	return 0
}

func reportMode(useLLM, promptOnly bool, interpretPath, interpret string) string {
	switch {
	case promptOnly:
		return "提示词模式"
	case interpretPath != "" && interpret != "":
		return "含外部解读文件，已过证据校验"
	case useLLM && interpret != "":
		return "含 LLM 解读，已过证据校验"
	case useLLM:
		return "LLM 不可用，已降级为纯事实版（退出码 4）"
	default:
		return "纯事实版"
	}
}

// cmdAsk 追问入口（单次或 -i 交互）。
func cmdAsk(args []string) int {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	args = reorderArgs(args, map[string]bool{"i": true, "llm": true})
	interactive := fs.Bool("i", false, "交互模式")
	useLLM := fs.Bool("llm", false, "调用 LLM 回答（默认纯事实回答）")
	if code := parseFlags(fs, args); code >= 0 {
		return code
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "用法：cs2coach ask <analysis.json> \"问题\"  或  cs2coach ask <analysis.json> -i")
		return 2
	}
	a, err := loadAnalysis(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取失败：%v\n", err)
		return 3
	}

	var client coach.Client
	llmBroken := false // --llm 被要求但 LLM 不可用（单次问答按退出码 4 标记）
	if *useLLM {
		client, err = coach.NewClientFromEnv()
		if err != nil {
			if errors.Is(err, coach.ErrNotConfigured) {
				fmt.Fprintln(os.Stderr, "未配置 LLM，将用纯事实回答（配置 CS2COACH_LLM_API_KEY 后可用 --llm）")
			} else {
				fmt.Fprintf(os.Stderr, "LLM 初始化失败：%v\n", err)
			}
			client = nil
			llmBroken = true
		}
	}

	answer := func(question string) bool {
		slice := qa.Retrieve(a, question)
		if client == nil {
			fmt.Println(qa.AnswerFacts(a, slice))
			return !llmBroken
		}
		system, user := coach.QAPrompt(a, slice)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		raw, err := client.Complete(ctx, system, user)
		if err != nil {
			fmt.Fprintf(os.Stderr, "LLM 调用失败：%v\n降级为纯事实回答（退出码 4 标记本次无 LLM 解读）：\n", err)
			fmt.Println(qa.AnswerFacts(a, slice))
			return false
		}
		cleaned, cites := coach.ValidateCitations(raw, a)
		if len(cites) > 0 {
			fmt.Printf("（证据校验：删除/标注 %d 处）\n", len(cites))
		}
		fmt.Println(cleaned)
		return true
	}

	if !*interactive {
		if fs.NArg() < 2 {
			fmt.Fprintln(os.Stderr, "用法：cs2coach ask <analysis.json> \"问题\"")
			return 2
		}
		if !answer(strings.Join(fs.Args()[1:], " ")) {
			return 4
		}
		return 0
	}

	fmt.Println("交互追问（输入问题；exit/quit/q/退出 或 Ctrl-D 结束）：")
	next, scanErr := newLineScanner(os.Stdin)
	for {
		fmt.Print("> ")
		line, ok := next()
		if !ok {
			if err := scanErr(); err != nil {
				fmt.Fprintf(os.Stderr, "读取输入失败：%v\n", err)
				return 3
			}
			return 0
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" || line == "q" || line == "退出" {
			return 0
		}
		answer(line)
		fmt.Println()
	}
}

// cmdPlayers 列出 match.json 里的玩家。
func cmdPlayers(args []string) int {
	fs := flag.NewFlagSet("players", flag.ContinueOnError)
	args = reorderArgs(args, nil)
	if code := parseFlags(fs, args); code >= 0 {
		return code
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "用法：cs2coach players <match.json>")
		return 2
	}
	m, err := loadMatch(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "players 读取失败：%v\n", err)
		return 3
	}
	if len(m.Players) == 0 {
		fmt.Fprintln(os.Stderr, "警告：match.json 中没有玩家（文件可能为空对局或格式不对）")
		return 3
	}
	fmt.Println("玩家名单（--player 可用名字或 SteamID64，名字含空格/特殊字符时加引号，SteamID 更可靠）：")
	for _, p := range m.Players {
		mark := ""
		if fmt.Sprint(p.SteamID64) == m.Meta.FocusPlayer {
			mark = "  ← 当前焦点"
		}
		fmt.Printf("  %-24s  %s%s\n", p.Name, formatSteamID(p.SteamID64), mark)
	}
	return 0
}

// parseFlags 解析 flag：成功返回 -1；-h/--help 已打印用法，返回 0；其他错误返回 2。
func parseFlags(fs *flag.FlagSet, args []string) int {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	return -1
}

// ---------- 小工具 ----------

func isSteamID(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func formatSteamID(id uint64) string {
	return fmt.Sprint(id)
}

func writeJSON(path string, v any) error {
	data, err := marshalIndent(v)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o644)
}

func loadMatch(path string) (*model.Match, error) {
	var m model.Match
	if err := unmarshalFile(path, &m); err != nil {
		return nil, err
	}
	if len(m.Rounds) == 0 {
		return nil, fmt.Errorf("match.json 无效：0 回合（文件可能损坏或不是 parse 产物）")
	}
	return &m, nil
}

func loadAnalysis(path string) (*model.Analysis, error) {
	var a model.Analysis
	if err := unmarshalFile(path, &a); err != nil {
		return nil, err
	}
	if len(a.Rounds) == 0 {
		return nil, fmt.Errorf("analysis.json 无效：0 回合（文件可能损坏或不是 analyze 产物）")
	}
	return &a, nil
}
