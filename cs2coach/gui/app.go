// Package gui 是 cs2coach 的桌面图形前端（Wails v2）后端绑定层。
//
// 与 cmd/cs2coach 共用同一 module，直接 import internal/* 各层：
// parse（事实）→ tactics（归因）→ coach（解读）→ report（渲染）→ qa（追问）。
// 本层只做编排、会话状态与 Wails 桥接，不产生任何数字或事实。
package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"cs2-demo-review/internal/coach"
	"cs2-demo-review/internal/model"
	"cs2-demo-review/internal/parse"
	"cs2-demo-review/internal/qa"
	"cs2-demo-review/internal/report"
	"cs2-demo-review/internal/tactics"
)

// LLM 模式（前端步骤 3 的三选一）。
const (
	// ModeFacts 纯事实：不调用任何 LLM，只输出规则层事实报告。
	ModeFacts = "facts"
	// ModeOllama 本地 Ollama：走其 OpenAI 兼容端点，数据不出本机。
	ModeOllama = "ollama"
	// ModeAPI 外部 API：Anthropic 官方 / OpenAI 兼容（DeepSeek、Kimi…）。
	ModeAPI = "api"

	// defaultOllamaURL 是 Ollama 默认的 OpenAI 兼容端点。
	defaultOllamaURL = "http://localhost:11434/v1"
	// defaultOllamaModel 是本地默认模型（中文解读质量较好的小模型）。
	defaultOllamaModel = "qwen2.5:7b"

	// progressEvent 是进度事件的 Wails 事件名；
	// 前端用 window.runtime.EventsOn("run:progress", cb) 订阅（Go → JS 回调）。
	progressEvent = "run:progress"

	// reportFile / markdownFile / matchFile / analysisFile / promptFile 是输出产物名（与 CLI 一致）。
	reportFile   = "report.html"
	markdownFile = "report.md"
	matchFile    = "match.json"
	analysisFile = "analysis.json"
	promptFile   = "prompt.txt"

	// heartbeatInterval 是长任务（解析/LLM）估算进度的刷新间隔。
	heartbeatInterval = 700 * time.Millisecond
)

// Settings 是图形版 LLM 设置（存本机，不进仓库）。
type Settings struct {
	Mode        string `json:"mode"`        // facts | ollama | api（上次选择）
	Provider    string `json:"provider"`    // anthropic | openai（空=跟随环境）
	APIKey      string `json:"apiKey"`      // 外部 API Key（只存本机）
	BaseURL     string `json:"baseURL"`     // 外部 API 自定义网关
	Model       string `json:"model"`       // 外部 API 模型
	OllamaURL   string `json:"ollamaURL"`   // 本地 Ollama 地址（含 /v1）
	OllamaModel string `json:"ollamaModel"` // 本地 Ollama 模型
	OutDir      string `json:"outDir"`      // 上次输出目录
	UseLLM      bool   `json:"useLLM"`      // 兼容旧配置：追问是否默认走 LLM
}

// PlayerItem 是前端玩家下拉项。
type PlayerItem struct {
	Name      string `json:"name"`
	SteamID64 string `json:"steamID64"`
	Focus     bool   `json:"focus"`
}

// ProbeInfo 是步骤 1/2 的 demo 预检结果：文件信息 + 玩家名单 + POV 判定。
type ProbeInfo struct {
	Path      string       `json:"path"`
	FileName  string       `json:"fileName"`
	SizeBytes int64        `json:"sizeBytes"`
	SizeText  string       `json:"sizeText"`
	ModTime   string       `json:"modTime"`
	Players   []PlayerItem `json:"players"`
	IsPOV     bool         `json:"isPOV"`
	POVName   string       `json:"povName"`
}

// Progress 是一条进度事件（Go → JS 回调 / GetProgress 轮询）。
type Progress struct {
	Stage     string `json:"stage"`   // probe | parse | analyze | interpret | report | done | error
	Percent   int    `json:"percent"` // 0-100（长任务内为估算值，按耗时推进）
	Message   string `json:"message"`
	ElapsedMS int64  `json:"elapsedMs"`
	Failed    bool   `json:"failed"`
}

// CauseRow 是丢分原因行。
type CauseRow struct {
	Label   string `json:"label"`
	Tag     string `json:"tag"`
	Count   int    `json:"count"`
	Percent string `json:"percent"`
	Rounds  string `json:"rounds"`
}

// SuspectRow 是可疑回合行。
type SuspectRow struct {
	Round  int     `json:"round"`
	Tags   string  `json:"tags"`
	Reason string  `json:"reason"`
	Cost   float64 `json:"cost"`
	Tick   int     `json:"tick"`
}

// GoodRow 是好回合行。
type GoodRow struct {
	Round     int    `json:"round"`
	Reason    string `json:"reason"`
	Highlight string `json:"highlight"`
}

// AnalyzeSummary 是归因步骤摘要（前端表格用）。
type AnalyzeSummary struct {
	FocusWon   int          `json:"focusWon"`
	FocusLost  int          `json:"focusLost"`
	Causes     []CauseRow   `json:"causes"`
	Suspects   []SuspectRow `json:"suspects"`
	KeySwings  []string     `json:"keySwings"`
	GoodRounds []GoodRow    `json:"goodRounds"`
}

// RunResult 是一次「解析 → 归因 → 报告」的完整结果。
type RunResult struct {
	Map          string         `json:"map"`
	TotalRounds  int            `json:"totalRounds"`
	ScoreT       int            `json:"scoreT"`
	ScoreCT      int            `json:"scoreCT"`
	FocusName    string         `json:"focusName"`
	Mode         string         `json:"mode"`
	OutDir       string         `json:"outDir"`
	ReportHTML   string         `json:"reportHTML"`   // 报告 HTML 绝对路径
	ReportMD     string         `json:"reportMD"`     // 报告 Markdown 绝对路径
	MatchJSON    string         `json:"matchJSON"`    // 事实层 JSON 路径
	AnalysisJSON string         `json:"analysisJSON"` // 归因 JSON 路径
	PromptFile   string         `json:"promptFile"`   // 降级时落盘的提示词路径
	HTML         string         `json:"html"`         // 报告 HTML 内容（iframe srcdoc）
	Markdown     string         `json:"markdown"`     // 报告 Markdown 内容
	Summary      AnalyzeSummary `json:"summary"`
	Warning      string         `json:"warning"` // 降级/校验提示（空=无）
	Interpreted  bool           `json:"interpreted"`
	DurationMS   int64          `json:"durationMs"`
}

// App 是 Wails 绑定对象：全部 GUI 操作入口。
type App struct {
	ctx context.Context

	mu         sync.Mutex // 保护会话状态（match/analysis/interpret/...）
	runMu      sync.Mutex // 保证同一时刻只有一个分析任务
	workDir    string
	settings   Settings
	match      *model.Match
	analysis   *model.Analysis
	system     string
	user       string
	interpret  string
	demoPath   string
	outDir     string
	mode       string
	reportHTML string
	reportMD   string

	progressMu sync.Mutex
	progress   Progress
	runStart   time.Time
}

// NewApp 构造 App（workDir 存会话产物与设置）。
func NewApp() *App {
	return &App{workDir: workDir(), mode: ModeFacts}
}

// Startup 是 Wails 生命周期回调：加载本机设置并应用为环境变量。
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	s, err := loadSettings()
	if err == nil {
		a.settings = normalizeSettings(s)
		applySettings(a.settings)
	} else {
		a.settings = normalizeSettings(Settings{})
	}
	// 文件拖拽：main.go 里开启 DragAndDrop.EnableFileDrop 后才会收到事件。
	runtime.OnFileDrop(ctx, func(x, y int, paths []string) { a.HandleFileDrop(paths) })
}

// WorkDir 返回会话产物目录（供前端展示路径）。
func (a *App) WorkDir() string { return a.workDir }

// ---------- 设置 ----------

// GetSettings 返回当前设置（只在本机内存/本机文件，不外发）。
func (a *App) GetSettings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.settings
	s.Mode = normalizeMode(s.Mode)
	return normalizeSettings(s)
}

// SaveSettings 保存设置到本机并应用为环境变量。
func (a *App) SaveSettings(s Settings) error {
	s = normalizeSettings(s)
	s.Provider = strings.ToLower(strings.TrimSpace(s.Provider))
	if s.Provider != "anthropic" && s.Provider != "openai" {
		s.Provider = ""
	}
	s.Mode = normalizeMode(s.Mode)
	a.mu.Lock()
	a.settings = s
	a.mu.Unlock()
	applySettings(s)
	return saveSettings(s)
}

// CheckLLM 检查某个模式是否可用（不产生 token 消耗）：
// Ollama 会请求 /v1/models 确认服务与模型；外部 API 只校验配置是否齐全。
func (a *App) CheckLLM(mode string) (string, error) {
	mode = normalizeMode(mode)
	if mode == ModeFacts {
		return "纯事实模式：不调用任何 LLM，数据完全留在本机。", nil
	}
	if mode == ModeOllama {
		base, model := a.ollamaConfig()
		models, err := listOllamaModels(base)
		if err != nil {
			return "", err
		}
		for _, m := range models {
			if m == model || strings.HasPrefix(m, model+":") {
				return fmt.Sprintf("Ollama 已就绪：%s（模型 %s）。", base, model), nil
			}
		}
		if len(models) == 0 {
			return "", fmt.Errorf("Ollama 在 %s 运行但没有已下载模型：请先执行 ollama pull %s", base, model)
		}
		return "", fmt.Errorf("Ollama 缺少模型 %q（已有：%s）：请执行 ollama pull %s",
			model, strings.Join(models, "、"), model)
	}
	client, err := a.newLLMClient(ModeAPI)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("外部 API 配置可用：%s（调用时才产生费用）。", client.Model()), nil
}

// ---------- 步骤 1：选 demo / 预检 ----------

// HandleFileDrop 处理 Wails 文件拖放（main.go 的 OnFileDrop 回调），把路径推给前端。
func (a *App) HandleFileDrop(paths []string) {
	if a.ctx == nil || len(paths) == 0 {
		return
	}
	p := paths[0]
	if !strings.EqualFold(filepath.Ext(p), ".dem") {
		runtime.EventsEmit(a.ctx, "demo:dropped:error", "请拖入 .dem 录像文件")
		return
	}
	runtime.EventsEmit(a.ctx, "demo:dropped", p)
}

// SelectDemo 弹出系统文件框让用户选 .dem。
func (a *App) SelectDemo() (string, error) {
	return runtime.OpenFileDialog(a.ctxOrBackground(), runtime.OpenDialogOptions{
		Title: "选择 CS2 demo 文件",
		Filters: []runtime.FileFilter{
			{DisplayName: "CS2 录像 (*.dem)", Pattern: "*.dem"},
			{DisplayName: "所有文件 (*.*)", Pattern: "*.*"},
		},
	})
}

// SelectOutDir 弹出目录框选择产物输出目录。
func (a *App) SelectOutDir() (string, error) {
	a.mu.Lock()
	def := a.outDir
	if def == "" {
		def = a.settings.OutDir
	}
	a.mu.Unlock()
	return runtime.OpenDirectoryDialog(a.ctxOrBackground(), runtime.OpenDialogOptions{
		Title:            "选择报告输出目录",
		DefaultDirectory: def,
	})
}

// Probe 预检 demo（步骤 1→2）：文件信息 + 玩家名单 + 是否 POV。
// 只读玩家名单，不做归因；非 POV 录像必须由用户选焦点玩家。
func (a *App) Probe(path string) (ProbeInfo, error) {
	var info ProbeInfo
	p := strings.TrimSpace(path)
	if p == "" {
		return info, fmt.Errorf("请先选择 .dem 文件")
	}
	st, err := os.Stat(p)
	if err != nil {
		return info, fmt.Errorf("无法读取文件：%w", err)
	}
	if st.IsDir() {
		return info, fmt.Errorf("这是一个文件夹，请选择 .dem 录像文件")
	}
	info.Path = p
	info.FileName = filepath.Base(p)
	info.SizeBytes = st.Size()
	info.SizeText = humanSize(st.Size())
	info.ModTime = st.ModTime().Format("2006-01-02 15:04")

	pr, err := parse.Probe(p)
	if err != nil {
		return info, err
	}
	info.Players = playerItems(pr.Players, pr.POVPlayer)
	info.IsPOV = pr.POVPlayer != ""
	info.POVName = pr.POVPlayerName
	return info, nil
}

// Players 只返回 demo 里的玩家列表（步骤 2 独立调用）。
func (a *App) Players(path string) ([]PlayerItem, error) {
	info, err := a.Probe(path)
	if err != nil {
		return nil, err
	}
	return info.Players, nil
}

// ---------- 步骤 4：一键运行（parse → analyze → report） ----------

// Run 跑完整管线，并通过 "run:progress" 事件把进度推给前端。
//
// player 为空表示跟随 POV 自动识别（非 POV 会报错并提示选人）；
// mode 取 facts / ollama / api；outDir 为空时用默认目录（工作目录/runs/<demo 名>）。
// LLM 失败不吞错：报告仍产出纯事实版，失败原因写进 RunResult.Warning。
func (a *App) Run(path, player, mode, outDir string) (RunResult, error) {
	start := time.Now()
	mode = normalizeMode(mode)
	res := RunResult{Mode: mode}

	p := strings.TrimSpace(path)
	if p == "" {
		return res, fmt.Errorf("请先选择 .dem 文件")
	}
	st, err := os.Stat(p)
	if err != nil {
		return res, fmt.Errorf("无法读取 demo 文件：%w", err)
	}
	if st.IsDir() {
		return res, fmt.Errorf("请选择 .dem 录像文件，而不是文件夹")
	}
	if !a.runMu.TryLock() {
		return res, fmt.Errorf("已有分析任务在运行，请等待它完成")
	}
	defer a.runMu.Unlock()

	out := strings.TrimSpace(outDir)
	if out == "" {
		out = a.DefaultOutDir(p)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return res, fmt.Errorf("创建输出目录失败：%w", err)
	}

	a.beginRun(p, out, mode)

	// ---- 1/3 解析 ----
	opts := parse.Options{}
	if pl := strings.TrimSpace(player); pl != "" {
		if isSteamID(pl) {
			opts.FocusSteamID = pl
		} else {
			opts.FocusName = pl
		}
	}
	a.emit("parse", 6, "解析 demo 事件流（大文件需要几十秒）…")
	stop := a.progressTicker(6, 46, "parse", "解析 demo 事件流")
	m, perr := parse.Parse(p, opts)
	stop()
	if perr != nil {
		a.emitFailed("parse", perr)
		return res, fmt.Errorf("解析失败：%w", perr)
	}
	a.mu.Lock()
	a.match = m
	a.mu.Unlock()
	a.emit("analyze", 50, fmt.Sprintf("解析完成：%s / %d 回合，开始归因…",
		orDefault(m.Meta.Map, "未知地图"), m.Meta.TotalRounds))

	// ---- 2/3 归因 ----
	stop = a.progressTicker(50, 66, "analyze", "规则层归因丢分回合")
	an, aerr := tactics.Analyze(m, tactics.Options{})
	stop()
	if aerr != nil {
		a.emitFailed("analyze", aerr)
		return res, fmt.Errorf("归因失败：%w", aerr)
	}
	a.mu.Lock()
	a.analysis = an
	a.mu.Unlock()

	// ---- 3/3 解读 + 报告 ----
	res, rerr := a.finishReport(mode, out, start)
	if rerr != nil {
		a.emitFailed("report", rerr)
		return res, rerr
	}
	return res, nil
}

// RegenerateReport 复用已缓存的分析结果，切换 LLM 模式重新生成解读与报告
// （避免为了换解读而重解析几十 MB 的 demo）。
func (a *App) RegenerateReport(mode, outDir string) (RunResult, error) {
	mode = normalizeMode(mode)
	if !a.runMu.TryLock() {
		return RunResult{Mode: mode}, fmt.Errorf("已有分析任务在运行，请等待它完成")
	}
	defer a.runMu.Unlock()

	a.mu.Lock()
	hasAnalysis := a.analysis != nil
	out := a.outDir
	a.mu.Unlock()
	if !hasAnalysis {
		return RunResult{Mode: mode}, fmt.Errorf("请先完成一次分析（步骤 4）")
	}
	if s := strings.TrimSpace(outDir); s != "" {
		out = s
	}
	if out == "" {
		return RunResult{Mode: mode}, fmt.Errorf("输出目录为空，请重新运行分析")
	}
	a.mu.Lock()
	a.mode = mode
	a.mu.Unlock()
	return a.finishReport(mode, out, time.Now())
}

// finishReport 生成解读，再委托 renderAndSave 落盘。
func (a *App) finishReport(mode, outDir string, start time.Time) (RunResult, error) {
	a.mu.Lock()
	an := a.analysis
	a.mu.Unlock()
	if an == nil {
		return RunResult{Mode: mode, OutDir: outDir}, fmt.Errorf("请先完成解析与归因")
	}
	interpret, warning := a.generateInterpret(mode, an)
	return a.renderAndSave(mode, outDir, interpret, warning, start)
}

// renderAndSave 用给定解读渲染报告并写全部产物（interpret 为空=纯事实版）。
func (a *App) renderAndSave(mode, outDir, interpret, warning string, start time.Time) (RunResult, error) {
	res := RunResult{Mode: mode, OutDir: outDir}
	a.mu.Lock()
	an, m := a.analysis, a.match
	a.mu.Unlock()
	if an == nil {
		return res, fmt.Errorf("请先完成解析与归因")
	}

	res.Interpreted = strings.TrimSpace(interpret) != ""

	a.mu.Lock()
	a.interpret = interpret
	a.mu.Unlock()

	a.emit("report", 90, "渲染报告（Markdown + HTML）…")
	md, err := report.Markdown(an, interpret)
	if err != nil {
		return res, fmt.Errorf("渲染 Markdown 失败：%w", err)
	}
	html, err := report.HTML(an, interpret)
	if err != nil {
		return res, fmt.Errorf("渲染 HTML 失败：%w", err)
	}

	mdPath := filepath.Join(outDir, markdownFile)
	htmlPath := filepath.Join(outDir, reportFile)
	if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
		return res, fmt.Errorf("写入 report.md 失败：%w", err)
	}
	if err := os.WriteFile(htmlPath, []byte(html), 0o644); err != nil {
		return res, fmt.Errorf("写入 report.html 失败：%w", err)
	}
	if err := tactics.SaveJSON(an, filepath.Join(outDir, analysisFile)); err != nil {
		return res, fmt.Errorf("写入 analysis.json 失败：%w", err)
	}
	matchPath := filepath.Join(outDir, matchFile)
	if m != nil {
		raw, jerr := json.MarshalIndent(m, "", "  ")
		if jerr != nil {
			return res, fmt.Errorf("序列化 match.json 失败：%w", jerr)
		}
		if werr := os.WriteFile(matchPath, raw, 0o644); werr != nil {
			return res, fmt.Errorf("写入 match.json 失败：%w", werr)
		}
	}

	res.Map = an.Map
	res.TotalRounds = len(an.Rounds)
	res.ScoreT = an.Story.FinalScore.T
	res.ScoreCT = an.Story.FinalScore.CT
	res.FocusName = an.Focus.Name
	res.ReportHTML = htmlPath
	res.ReportMD = mdPath
	res.AnalysisJSON = filepath.Join(outDir, analysisFile)
	res.MatchJSON = matchPath
	res.HTML = html
	res.Markdown = md
	res.Warning = warning
	res.Summary = buildSummary(an)
	res.DurationMS = time.Since(start).Milliseconds()

	// LLM 请求过但没拿到解读：落盘提示词，方便人工粘贴给任意 LLM。
	if mode != ModeFacts && !res.Interpreted {
		a.mu.Lock()
		system, user := a.system, a.user
		a.mu.Unlock()
		if system != "" || user != "" {
			p := filepath.Join(outDir, promptFile)
			if err := os.WriteFile(p, []byte(system+"\n\n---\n\n"+user), 0o644); err == nil {
				res.PromptFile = p
				res.Warning = strings.TrimSpace(res.Warning + " 提示词已落盘，可粘贴给任意 LLM 后用「回填解读」导入。")
			}
		}
	}

	a.mu.Lock()
	a.reportHTML, a.reportMD = html, md
	a.mu.Unlock()

	a.emit("done", 100, fmt.Sprintf("完成：%s %d 回合，用时 %.1fs",
		orDefault(an.Map, "未知地图"), len(an.Rounds), time.Since(start).Seconds()))
	return res, nil
}

// generateInterpret 按模式生成教练解读；返回解读文本与降级提示（空=一切正常）。
func (a *App) generateInterpret(mode string, an *model.Analysis) (string, string) {
	system, user := coach.ReportPrompt(an)
	a.mu.Lock()
	a.system, a.user = system, user
	a.mu.Unlock()

	if mode == ModeFacts {
		return "", ""
	}

	client, err := a.newLLMClient(mode)
	if err != nil {
		return "", fmt.Sprintf("LLM 不可用（%v）。已生成纯事实版报告。", err)
	}
	a.emit("interpret", 70, fmt.Sprintf("调用 %s 生成教练解读（可能需要 1-3 分钟）…", client.Model()))
	stop := a.progressTicker(70, 88, "interpret", "LLM 生成解读中")
	raw, cerr := client.Complete(a.ctxOrBackground(), system, user)
	stop()
	if cerr != nil {
		return "", fmt.Sprintf("LLM 调用失败（%v）。已生成纯事实版报告。", cerr)
	}
	cleaned, cites := coach.ValidateCitations(raw, an)
	if strings.TrimSpace(cleaned) == "" {
		return "", "LLM 输出未通过证据校验（架构锁：只解读不发明）。已生成纯事实版报告。"
	}
	if len(cites) > 0 {
		return cleaned, fmt.Sprintf("证据校验删除/标注了 %d 处无证据论断，报告已按净化后文本生成。", len(cites))
	}
	return cleaned, ""
}

// ---------- 步骤 5：报告 ----------

// GetReportHTML 返回当前报告 HTML（自包含，前端放 iframe srcdoc）。
func (a *App) GetReportHTML() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.reportHTML != "" {
		return a.reportHTML, nil
	}
	if a.analysis == nil {
		return "", fmt.Errorf("请先完成一次分析（步骤 4）")
	}
	return report.HTML(a.analysis, a.interpret)
}

// GetReportMarkdown 返回当前报告 Markdown。
func (a *App) GetReportMarkdown() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.reportMD != "" {
		return a.reportMD, nil
	}
	if a.analysis == nil {
		return "", fmt.Errorf("请先完成一次分析（步骤 4）")
	}
	return report.Markdown(a.analysis, a.interpret)
}

// OpenReportHTML 用系统浏览器打开已落盘的报告。
func (a *App) OpenReportHTML() (string, error) {
	a.mu.Lock()
	an, interpret, out := a.analysis, a.interpret, a.outDir
	a.mu.Unlock()
	if an == nil {
		return "", fmt.Errorf("请先完成一次分析（步骤 4）")
	}
	if out == "" {
		return "", fmt.Errorf("输出目录为空，请先运行分析")
	}
	html, err := report.HTML(an, interpret)
	if err != nil {
		return "", err
	}
	p := filepath.Join(out, reportFile)
	if err := os.WriteFile(p, []byte(html), 0o644); err != nil {
		return "", err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(p)}
	runtime.BrowserOpenURL(a.ctxOrBackground(), u.String())
	return p, nil
}

// OpenOutDir 在系统文件管理器中打开输出目录。
func (a *App) OpenOutDir() (string, error) {
	a.mu.Lock()
	out := a.outDir
	a.mu.Unlock()
	if out == "" {
		return "", fmt.Errorf("输出目录为空，请先运行分析")
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(out)}
	runtime.BrowserOpenURL(a.ctxOrBackground(), u.String())
	return out, nil
}

// SavePromptOnly 落盘 prompt.txt，返回路径（人工粘贴给任意 LLM 用）。
func (a *App) SavePromptOnly() (string, error) {
	a.mu.Lock()
	an, out := a.analysis, a.outDir
	a.mu.Unlock()
	if an == nil {
		return "", fmt.Errorf("请先完成一次分析（步骤 4）")
	}
	if out == "" {
		out = a.workDir
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	system, user := coach.ReportPrompt(an)
	p := filepath.Join(out, promptFile)
	if err := os.WriteFile(p, []byte(system+"\n\n---\n\n"+user), 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// SelectInterpretFile 弹出文件框让用户选解读文本文件。
func (a *App) SelectInterpretFile() (string, error) {
	return runtime.OpenFileDialog(a.ctxOrBackground(), runtime.OpenDialogOptions{
		Title:   "选择 LLM 解读文本文件",
		Filters: []runtime.FileFilter{{DisplayName: "文本文件 (*.txt;*.md)", Pattern: "*.txt;*.md"}},
	})
}

// LoadInterpretFile 读取外部解读文件（prompt-only 人工流程回填），校验后重出报告。
func (a *App) LoadInterpretFile(path string) (RunResult, error) {
	var res RunResult
	a.mu.Lock()
	an, out := a.analysis, a.outDir
	a.mu.Unlock()
	if an == nil {
		return res, fmt.Errorf("请先完成一次分析（步骤 4）")
	}
	if strings.TrimSpace(path) == "" {
		return res, fmt.Errorf("请选择解读文本文件")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return res, fmt.Errorf("解读文件读取失败：%v", err)
	}
	cleaned, cites := coach.ValidateCitations(string(raw), an)
	if strings.TrimSpace(cleaned) == "" {
		return res, fmt.Errorf("解读文件未通过证据校验（没有可用的带引用内容）")
	}
	if out == "" {
		out = a.workDir
	}
	a.mu.Lock()
	a.mode = "import"
	a.mu.Unlock()
	res, err = a.renderAndSave("import", out, cleaned, "", time.Now())
	if err != nil {
		return res, err
	}
	if len(cites) > 0 {
		res.Warning = fmt.Sprintf("证据校验删除/标注了 %d 处无证据论断。", len(cites))
	}
	return res, nil
}

// ---------- 步骤 6：追问 ----------

// Ask 回答追问。走当前会话的 LLM 模式（facts 则纯事实）；
// LLM 不可用/失败时自动降级为纯事实回答并明确标注，不编造。
func (a *App) Ask(path, question string) (string, error) {
	q := strings.TrimSpace(question)
	if q == "" {
		return "", fmt.Errorf("问题不能为空")
	}
	if err := a.ensureSession(path); err != nil {
		return "", err
	}
	a.mu.Lock()
	an, mode := a.analysis, a.mode
	a.mu.Unlock()
	if an == nil {
		return "", fmt.Errorf("请先完成一次分析（步骤 4）")
	}

	slice := qa.Retrieve(an, q)
	facts := qa.AnswerFacts(an, slice)
	if mode != ModeOllama && mode != ModeAPI {
		return facts, nil
	}

	client, err := a.newLLMClient(mode)
	if err != nil {
		return "（LLM 不可用，以下是纯事实回答）\n" + facts, nil
	}
	system, user := coach.QAPrompt(an, slice)
	raw, cerr := client.Complete(a.ctxOrBackground(), system, user)
	if cerr != nil {
		return "（LLM 调用失败，以下是纯事实回答）\n" + facts, nil
	}
	cleaned, cites := coach.ValidateCitations(raw, an)
	if strings.TrimSpace(cleaned) == "" {
		return "（LLM 回答未通过证据校验，以下是纯事实回答）\n" + facts, nil
	}
	if len(cites) > 0 {
		cleaned += fmt.Sprintf("\n\n> 证据校验：删除/标注了 %d 处无证据论断。", len(cites))
	}
	return cleaned, nil
}

// ensureSession 确认当前会话有可用的分析结果；重启后可从输出目录的
// analysis.json 恢复，避免为一次追问重解析整场 demo。
func (a *App) ensureSession(demoPath string) error {
	a.mu.Lock()
	ok := a.analysis != nil && (demoPath == "" || a.demoPath == "" || demoPath == a.demoPath)
	a.mu.Unlock()
	if ok {
		return nil
	}
	p := strings.TrimSpace(demoPath)
	if p == "" {
		return fmt.Errorf("请先完成一次分析（步骤 4）")
	}
	an, err := tactics.LoadJSON(filepath.Join(a.DefaultOutDir(p), analysisFile))
	if err != nil {
		return fmt.Errorf("请先完成一次分析（步骤 4）")
	}
	a.mu.Lock()
	a.analysis = an
	a.demoPath = p
	a.outDir = a.DefaultOutDir(p)
	a.mode = ModeFacts
	a.reportHTML, a.reportMD = "", ""
	a.mu.Unlock()
	return nil
}

// ---------- 进度 ----------

// GetProgress 返回最近一条进度（事件丢失时前端可轮询兜底）。
func (a *App) GetProgress() Progress {
	a.progressMu.Lock()
	defer a.progressMu.Unlock()
	p := a.progress
	if !a.runStart.IsZero() {
		p.ElapsedMS = time.Since(a.runStart).Milliseconds()
	}
	return p
}

// emit 推送一条进度事件给前端（无窗口/单测直调时静默跳过）。
func (a *App) emit(stage string, percent int, msg string) {
	p := Progress{Stage: stage, Percent: percent, Message: msg}
	a.progressMu.Lock()
	a.progress = p
	elapsed := time.Since(a.runStart).Milliseconds()
	a.progressMu.Unlock()
	p.ElapsedMS = elapsed
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, progressEvent, p)
}

// emitFailed 推送失败进度。
func (a *App) emitFailed(stage string, err error) {
	p := Progress{Stage: stage, Percent: 100, Message: err.Error(), Failed: true}
	a.progressMu.Lock()
	a.progress = p
	p.ElapsedMS = time.Since(a.runStart).Milliseconds()
	a.progressMu.Unlock()
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, progressEvent, p)
	}
}

// progressTicker 在无法上报细粒度进度的长任务里按耗时估算推进百分比；
// 返回的 stop 会等 goroutine 退出，避免结束事件被迟到的 tick 覆盖。
func (a *App) progressTicker(from, to int, stage, msg string) func() {
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		t := time.NewTicker(heartbeatInterval)
		defer t.Stop()
		start := time.Now()
		p := from
		for {
			select {
			case <-stopCh:
				return
			case <-t.C:
				if p < to-1 {
					p++
				}
				a.emit(stage, p, fmt.Sprintf("%s（%ds）", msg, int(time.Since(start).Seconds())))
			}
		}
	}()
	return func() {
		close(stopCh)
		<-doneCh
	}
}

// beginRun 重置会话并记录本次运行参数。
func (a *App) beginRun(demoPath, outDir, mode string) {
	a.mu.Lock()
	a.match = nil
	a.analysis = nil
	a.interpret = ""
	a.system, a.user = "", ""
	a.demoPath = demoPath
	a.outDir = outDir
	a.mode = mode
	a.reportHTML, a.reportMD = "", ""
	a.mu.Unlock()

	a.progressMu.Lock()
	a.progress = Progress{Stage: "start", Percent: 0, Message: "开始…"}
	a.runStart = time.Now()
	a.progressMu.Unlock()
}

// ---------- LLM 客户端 ----------

// newLLMClient 按模式构造 coach.Client。
// Ollama 走其 OpenAI 兼容端点，密钥为占位值（Ollama 不校验）。
func (a *App) newLLMClient(mode string) (coach.Client, error) {
	switch normalizeMode(mode) {
	case ModeAPI:
		return coach.NewClientFromEnv()
	case ModeOllama:
		base, model := a.ollamaConfig()
		models, err := listOllamaModels(base)
		if err != nil {
			return nil, err
		}
		if !hasModel(models, model) {
			return nil, fmt.Errorf("Ollama 缺少模型 %q（已有：%s）：请执行 ollama pull %s",
				model, strings.Join(models, "、"), model)
		}
		return newClientWithEnv(map[string]string{
			"CS2COACH_LLM_PROVIDER": "openai",
			"CS2COACH_LLM_BASE_URL": base,
			"CS2COACH_LLM_MODEL":    model,
			"CS2COACH_LLM_API_KEY":  "ollama-local",
		})
	default:
		return nil, fmt.Errorf("纯事实模式不调用 LLM")
	}
}

// ollamaConfig 返回规范化后的 Ollama 端点与模型。
func (a *App) ollamaConfig() (string, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	base := strings.TrimSpace(a.settings.OllamaURL)
	if base == "" {
		base = defaultOllamaURL
	}
	base = strings.TrimRight(base, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	model := strings.TrimSpace(a.settings.OllamaModel)
	if model == "" {
		model = defaultOllamaModel
	}
	return base, model
}

// newClientWithEnv 临时注入环境变量构造客户端后立刻还原（客户端已快照配置）。
func newClientWithEnv(env map[string]string) (coach.Client, error) {
	type saved struct {
		val string
		ok  bool
	}
	prev := make(map[string]saved, len(env))
	for k, v := range env {
		old, ok := os.LookupEnv(k)
		prev[k] = saved{old, ok}
		_ = os.Setenv(k, v)
	}
	client, err := coach.NewClientFromEnv()
	for k, s := range prev {
		if s.ok {
			_ = os.Setenv(k, s.val)
		} else {
			_ = os.Unsetenv(k)
		}
	}
	return client, err
}

// listOllamaModels 请求 {base}/models 确认 Ollama 在跑并列出本地模型。
func listOllamaModels(base string) ([]string, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(strings.TrimRight(base, "/") + "/models")
	if err != nil {
		return nil, fmt.Errorf("连不上本地 Ollama（%s）：请确认已运行 ollama serve", base)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Ollama 返回 HTTP %d：请检查地址 %s", resp.StatusCode, base)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("Ollama 响应无法解析：%v", err)
	}
	out := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		out = append(out, m.ID)
	}
	return out, nil
}

// hasModel 判断模型名是否在列表中（兼容 name 与 name:tag 写法）。
func hasModel(models []string, want string) bool {
	for _, m := range models {
		if m == want || strings.HasPrefix(m, want+":") || strings.HasPrefix(want, m+":") {
			return true
		}
	}
	return false
}

// ---------- 小工具 ----------

// normalizeMode 归一化 LLM 模式，未知值回落到纯事实。
func normalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ModeOllama:
		return ModeOllama
	case ModeAPI:
		return ModeAPI
	default:
		return ModeFacts
	}
}

// normalizeSettings 补齐设置默认值（不改动用户已填内容）。
func normalizeSettings(s Settings) Settings {
	s.Mode = normalizeMode(s.Mode)
	if strings.TrimSpace(s.OllamaURL) == "" {
		s.OllamaURL = defaultOllamaURL
	}
	if strings.TrimSpace(s.OllamaModel) == "" {
		s.OllamaModel = defaultOllamaModel
	}
	s.Provider = strings.ToLower(strings.TrimSpace(s.Provider))
	s.APIKey = strings.TrimSpace(s.APIKey)
	s.BaseURL = strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	s.Model = strings.TrimSpace(s.Model)
	s.OutDir = strings.TrimSpace(s.OutDir)
	return s
}

// DefaultOutDir 返回某个 demo 的默认输出目录：workDir/runs/<demo 名>。
func (a *App) DefaultOutDir(demoPath string) string {
	base := strings.TrimSuffix(filepath.Base(demoPath), filepath.Ext(demoPath))
	base = sanitizeName(base)
	if base == "" {
		base = "run"
	}
	return filepath.Join(a.workDir, "runs", base)
}

// humanSize 把字节数格式化为便于阅读的大小。
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// playerItems 把解析层玩家信息转成前端下拉项。
func playerItems(players []model.PlayerInfo, pov string) []PlayerItem {
	out := make([]PlayerItem, 0, len(players))
	for _, p := range players {
		id := fmt.Sprint(p.SteamID64)
		out = append(out, PlayerItem{Name: p.Name, SteamID64: id, Focus: id == pov})
	}
	return out
}

// buildSummary 从 Analysis 提取前端摘要。
func buildSummary(an *model.Analysis) AnalyzeSummary {
	out := AnalyzeSummary{
		FocusWon:  len(an.Story.FocusWonRounds),
		FocusLost: len(an.Story.FocusLostRounds),
		KeySwings: an.Story.KeySwings,
	}
	for _, c := range an.LostRoundCauses {
		rounds := make([]string, 0, len(c.Rounds))
		for _, n := range c.Rounds {
			rounds = append(rounds, fmt.Sprint(n))
		}
		out.Causes = append(out.Causes, CauseRow{
			Label: c.Label, Tag: c.Tag, Count: c.Count,
			Percent: fmt.Sprintf("%.0f%%", c.Fraction*100),
			Rounds:  strings.Join(rounds, "、"),
		})
	}
	for _, s := range an.Suspects {
		out.Suspects = append(out.Suspects, SuspectRow{
			Round: s.Round, Tags: strings.Join(s.Tags, "、"),
			Reason: s.Reason, Cost: s.ScoreHint, Tick: s.TickRange[0],
		})
	}
	for _, g := range an.GoodRounds {
		out.GoodRounds = append(out.GoodRounds, GoodRow{Round: g.Round, Reason: g.Reason, Highlight: g.Highlight})
	}
	return out
}

// isSteamID 判断字符串是否全为数字（SteamID64）。
func isSteamID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// sanitizeName 过滤文件名里的路径分隔符，避免输出目录逃逸。
func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, string(filepath.Separator), "-")
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "\\", "-")
	s = strings.ReplaceAll(s, "..", "-")
	return s
}

// orDefault 空字符串时返回默认值。
func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// workDir 返回会话产物目录：macOS ~/Library/Application Support/cs2coach，
// Windows %APPDATA%/cs2coach，Linux ~/.config/cs2coach。
func workDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "cs2coach")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// settingsPath 返回设置文件路径。
func settingsPath() string { return filepath.Join(workDir(), "settings.json") }

// loadSettings 读取本机设置。
func loadSettings() (Settings, error) {
	var s Settings
	raw, err := os.ReadFile(settingsPath())
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, err
	}
	return s, nil
}

// saveSettings 写本机设置（0600：只允许当前用户读）。
func saveSettings(s Settings) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath(), raw, 0o600)
}

// applySettings 把设置应用为环境变量（供 coach.NewClientFromEnv 读取）。
// 只覆盖非空项；APIKey 为空时不动现有环境变量。
func applySettings(s Settings) {
	if s.Provider != "" {
		_ = os.Setenv("CS2COACH_LLM_PROVIDER", s.Provider)
	}
	if s.APIKey != "" {
		_ = os.Setenv("CS2COACH_LLM_API_KEY", s.APIKey)
	}
	if s.BaseURL != "" {
		_ = os.Setenv("CS2COACH_LLM_BASE_URL", s.BaseURL)
	}
	if s.Model != "" {
		_ = os.Setenv("CS2COACH_LLM_MODEL", s.Model)
	}
}

// ctxOrBackground 取请求上下文（单测直调时 Startup 未执行，兜底 Background）。
func (a *App) ctxOrBackground() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}
