package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGuiPipelineHeadless 不开窗口，直调 App 方法走完 选文件→选人→运行→报告→追问 全链路。
func TestGuiPipelineHeadless(t *testing.T) {
	const demo = "../testdata/demos/s2/s2.dem"
	a := NewApp()

	probe, err := a.Probe(demo)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(probe.Players) == 0 {
		t.Fatal("Probe 应返回玩家名单")
	}
	if probe.FileName != "s2.dem" || probe.SizeBytes == 0 {
		t.Errorf("文件信息异常：%+v", probe)
	}
	if probe.IsPOV {
		t.Error("s2.dem 是 GOTV，不应判为 POV")
	}

	players, err := a.Players(demo)
	if err != nil {
		t.Fatalf("Players: %v", err)
	}
	if len(players) != len(probe.Players) {
		t.Errorf("Players 数量不一致：%d vs %d", len(players), len(probe.Players))
	}

	// 非 POV 不指定玩家应报错。
	if _, err := a.Run(demo, "", ModeFacts, t.TempDir()); err == nil {
		t.Error("非 POV 无焦点应报错")
	}
	// 文件不存在应报错。
	if _, err := a.Run("../testdata/demos/nope.dem", "", ModeFacts, t.TempDir()); err == nil {
		t.Error("不存在的文件应报错")
	}

	out := t.TempDir()
	res, err := a.Run(demo, "Appl3Thr33", ModeFacts, out)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.TotalRounds == 0 {
		t.Error("归因回合数为 0")
	}
	if res.Summary.FocusWon+res.Summary.FocusLost == 0 {
		t.Error("归因摘要为空")
	}
	if res.Interpreted {
		t.Error("纯事实模式不应有 LLM 解读")
	}
	for _, p := range []string{res.ReportHTML, res.ReportMD, res.AnalysisJSON, res.MatchJSON} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("产物缺失 %s: %v", p, err)
		}
	}
	if filepath.Dir(res.ReportHTML) != out {
		t.Errorf("报告未写入指定输出目录：%s", res.ReportHTML)
	}
	if !strings.Contains(res.HTML, "教练复盘") {
		t.Error("报告 HTML 内容异常")
	}
	if !strings.Contains(res.Markdown, "回合") {
		t.Error("报告 Markdown 内容异常")
	}

	page, err := a.GetReportHTML()
	if err != nil || page != res.HTML {
		t.Errorf("GetReportHTML 应返回本次报告：%v", err)
	}
	md, err := a.GetReportMarkdown()
	if err != nil || md != res.Markdown {
		t.Errorf("GetReportMarkdown 应返回本次报告：%v", err)
	}

	// 复用缓存换模式重出报告（不重新解析）。
	if _, err := a.RegenerateReport(ModeFacts, out); err != nil {
		t.Errorf("RegenerateReport: %v", err)
	}

	ans, err := a.Ask("", "残局问题")
	if err != nil {
		t.Errorf("Ask: %v", err)
	} else if !strings.Contains(ans, "回合") {
		t.Errorf("Ask 纯事实回答异常: %.80s", ans)
	}
	if _, err := a.Ask("", "   "); err == nil {
		t.Error("空问题应报错")
	}
	if _, err := a.Ask("/tmp/other.dem", "第 1 回合怎么了"); err == nil {
		t.Error("换 demo 追问应提示先分析")
	}
}

// TestRunFactsModeProducesFallback 确认 LLM 不可用时仍产出纯事实报告而非失败。
func TestNormalizeModeAndSizes(t *testing.T) {
	if got := normalizeMode("OLLAMA"); got != ModeOllama {
		t.Errorf("normalizeMode(OLLAMA)=%q", got)
	}
	if got := normalizeMode("nonsense"); got != ModeFacts {
		t.Errorf("未知模式应回落 facts，得到 %q", got)
	}
	if got := humanSize(1536); got != "1.5 KB" {
		t.Errorf("humanSize=%q", got)
	}
	if got := sanitizeName("../../etc/passwd"); strings.Contains(got, "..") || strings.Contains(got, "/") {
		t.Errorf("sanitizeName 未清理路径：%q", got)
	}
}
