package report

import (
	"strings"
	"testing"

	"cs2-demo-review/internal/labels"
	"cs2-demo-review/internal/model"
)

// sampleAnalysis 构造一场 10:13 负的分析数据，带赛后判决 Call。
// 丢分：3 teammate、5 yours、7 split、14 split。纯队友 1/4，不能说主因不在你。
func sampleAnalysis() *model.Analysis {
	return &model.Analysis{
		Map:   "de_mirage",
		Focus: model.FocusSummary{Name: "taoma", SteamID64: "76561198000000001"},
		Story: model.StorySummary{
			FinalScore:      model.Score{T: 10, CT: 13},
			FocusTeamScore:  10,
			OpponentScore:   13,
			FirstHalfScore:  model.Score{T: 5, CT: 7},
			SecondHalfScore: model.Score{T: 5, CT: 6},
			FocusWonRounds:  []int{1, 2, 4, 6, 8, 9, 10, 12, 13},
			FocusLostRounds: []int{3, 5, 7, 14},
			WonByBuy:        map[string]int{"full": 7, "force": 2},
			LostByBuy:       map[string]int{"full": 2, "force": 2},
			KeySwings:       []string{"第 11-14 回合连丢"},
		},
		LostRoundCauses: []model.CauseStat{
			{Tag: model.CauseTradeFail, Label: "补枪失败", Count: 3, Rounds: []int{3, 7, 14}, Fraction: 0.75},
			{Tag: model.CauseFirstBloodGift, Label: "首杀白给", Count: 1, Rounds: []int{5}, Fraction: 0.25},
		},
		Suspects: []model.Suspect{
			{Round: 14, TickRange: [2]int{140000, 148000}, Tags: []string{model.CauseClutchThrow}, Reason: "有利残局丢分", ScoreHint: 1.0},
		},
		GoodRounds: []model.GoodRound{
			{Round: 8, Reason: "焦点玩家 1v2 残局获胜", Highlight: "残局双杀，HP 剩 12", Tick: 88000},
		},
		Rounds: []model.RoundAnalysis{
			lostCall(3, model.CallTeammate, &model.RoundCall{
				Kind: model.CallTeammate, VictimName: "队友x", KillerName: "y", Weapon: "AK-47", Tick: 30100,
			}),
			lostCall(5, model.CallYours, &model.RoundCall{
				Kind: model.CallYours, VictimName: "taoma", KillerName: "y", Weapon: "AWP", Tick: 50120,
			}),
			lostCall(7, model.CallSplit, &model.RoundCall{
				Kind: model.CallSplit, VictimName: "队友m", KillerName: "z", Weapon: "M4A1", Tick: 70500,
				FocusKillerName: "y", FocusWeapon: "AK-47", FocusTick: 72000,
			}),
			lostCall(14, model.CallSplit, &model.RoundCall{
				Kind: model.CallSplit, VictimName: "队友n", KillerName: "y", Weapon: "AK-47", Tick: 140500,
				FocusKillerName: "y", FocusWeapon: "AK-47", FocusTick: 142000,
			}),
		},
	}
}

func lostCall(number int, _ string, call *model.RoundCall) model.RoundAnalysis {
	return model.RoundAnalysis{
		Number:    number,
		Winner:    model.SideCT,
		FocusWon:  false,
		EndReason: "elimination",
		Half:      "H1",
		FocusBuy:  "force",
		Call:      call,
		WinCost:   0.5,
		TickRange: [2]int{number * 10000, number*10000 + 6000},
	}
}

func sampleInterpret() string {
	return strings.Join([]string{
		"**一句话结论**：13:9 赢了，但补枪掉链子——3 个回合没补上枪。",
		"",
		"## 追问建议",
		"- 第 14 回合我为什么不转点？",
		"- 第 5 回合我为什么白给首杀？",
		"- 强起局应该怎么打？",
	}, "\n")
}

func TestMarkdownVerdictFactsOnly(t *testing.T) {
	got, err := Markdown(sampleAnalysis(), "")
	if err != nil {
		t.Fatalf("Markdown() error = %v", err)
	}

	wantSubstrings := []struct {
		name string
		in   string
	}{
		{"标题", "教练复盘 · de_mirage 10:13 · 焦点玩家 taoma"},
		{"判决标记", "**判决**："},
		{"不能说主因", "这场输了，不能说主因不在你"},
		{"计数", "纯队友的有 1 分，队友先倒你后来又死的有 2 分，你先死的有 1 分"},
		{"分开提示", splitHint},
		{"要你改的标题", "### 要你改的"},
		{"分开的标题", "### 分开的"},
		{"回合5你的", "回合 5 是你的。你先被 y 用 AWP 打死（tick 50120）。下次这下别先死。demo_gototick 50120"},
		{"回合7分开", "回合 7 分开。队友m 先被 z 用 M4A1 打死（tick 70500），那下不是你的。你后来被 y 用 AK-47 打死（tick 72000）。demo_gototick 72000"},
		{"回合3队友", "回合 3 是队友的"},
		{"纯事实版注记", factsOnlyNote},
		{"好回合标题", "## 值得保留的好回合"},
		{"好回合内容", "焦点玩家 1v2 残局获胜"},
		{"站位分析", "## 站位分析"},
		{"对枪分析", "## 对枪分析"},
		{"追问入口", "## 追问入口"},
		{"CLI 示例", `cs2coach ask <analysis.json> "第3回合我为什么不转点？"`},
	}
	for _, w := range wantSubstrings {
		if !strings.Contains(got, w.in) {
			t.Errorf("Markdown 缺少 %s（%q）", w.name, w.in)
		}
	}
	for _, bad := range []string{
		"主要丢分原因",
		"## 丢分回合 Top3",
		"## 丢分原因频次",
		"那下是你的",
		"**下把怎么改",
		"**为什么错",
		"开局对枪前先架默认位",
		"贴墙架补枪位",
	} {
		if strings.Contains(got, bad) {
			t.Errorf("Markdown 不应再出现 %q", bad)
		}
	}
	if strings.Count(got, "别再把自己送进去") != 1 {
		t.Errorf("提示句应只出现一次，实际 %d", strings.Count(got, "别再把自己送进去"))
	}
}

func TestMarkdownVerdictOrderByRound(t *testing.T) {
	got, err := Markdown(sampleAnalysis(), "")
	if err != nil {
		t.Fatalf("Markdown() error = %v", err)
	}
	iYoursH := strings.Index(got, "### 要你改的")
	i5 := strings.Index(got, "回合 5 是你的")
	iSplitH := strings.Index(got, "### 分开的")
	i7 := strings.Index(got, "回合 7 分开")
	i14 := strings.Index(got, "回合 14 分开")
	i3 := strings.Index(got, "回合 3 是队友的")
	if iYoursH < 0 || i5 < 0 || iSplitH < 0 || i7 < 0 || i14 < 0 || i3 < 0 {
		t.Fatalf("判决行缺失：要你改的@%d 5@%d 分开的@%d 7@%d 14@%d 3@%d",
			iYoursH, i5, iSplitH, i7, i14, i3)
	}
	// 顺序：要你改的 → yours → 分开的 → split 升序 → teammate（无小标题）
	if !(iYoursH < i5 && i5 < iSplitH && iSplitH < i7 && i7 < i14 && i14 < i3) {
		t.Errorf("应按 要你改的/yours → 分开的/split → other，实际 %d %d %d %d %d %d",
			iYoursH, i5, iSplitH, i7, i14, i3)
	}
}

func TestMarkdownInterpretCannotCoverVerdict(t *testing.T) {
	got, err := Markdown(sampleAnalysis(), sampleInterpret())
	if err != nil {
		t.Fatalf("Markdown() error = %v", err)
	}
	if !strings.Contains(got, "这场输了，不能说主因不在你") {
		t.Error("有 interpret 时规则判决仍须打头")
	}
	if strings.Contains(got, "补枪掉链子") {
		t.Error("LLM 一句话结论不得盖掉判决开场")
	}
	if strings.Contains(got, factsOnlyNote) {
		t.Error("带 interpret 时不应出现纯事实版注记")
	}
	if !strings.Contains(got, "第 14 回合我为什么不转点？") {
		t.Error("interpret 追问仍应保留")
	}
}

func TestHTMLVerdict(t *testing.T) {
	a := sampleAnalysis()
	a.Focus.Name = `<script>alert(1)</script>`
	got, err := HTML(a, "")
	if err != nil {
		t.Fatalf("HTML() error = %v", err)
	}
	if strings.Contains(got, `<script>alert(1)</script>`) {
		t.Error("HTML 未转义焦点玩家名")
	}
	if !strings.Contains(got, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("HTML 应包含转义后的焦点玩家名")
	}
	for _, in := range []string{
		"判决",
		"这场输了，不能说主因不在你",
		"要你改的",
		"分开的",
		"回合 5 是你的",
		"回合 7 分开",
		"demo_gototick 72000",
		"值得保留的好回合",
		"#06090e",
	} {
		if !strings.Contains(got, in) {
			t.Errorf("HTML 缺少 %q", in)
		}
	}
	for _, bad := range []string{"http://", "https://", "<link ", "下把怎么改", "丢分回合 Top3", "丢分原因频次", "那下是你的"} {
		if strings.Contains(got, bad) {
			t.Errorf("HTML 不应包含 %q", bad)
		}
	}
}

func TestNilAnalysis(t *testing.T) {
	if _, err := Markdown(nil, ""); err == nil {
		t.Error("Markdown(nil) 应返回错误")
	} else if !strings.Contains(err.Error(), "Analysis 为空") {
		t.Errorf("错误信息应为中文，got %q", err)
	}
	if _, err := HTML(nil, ""); err == nil {
		t.Error("HTML(nil) 应返回错误")
	}
}

func TestEmptyAnalysisStillRenders(t *testing.T) {
	got, err := Markdown(&model.Analysis{}, "")
	if err != nil {
		t.Fatalf("Markdown(空) error = %v", err)
	}
	for _, in := range []string{"**判决**：", "这场没有要你改的。", "值得保留的好回合", "站位分析", "对枪分析", "追问入口"} {
		if !strings.Contains(got, in) {
			t.Errorf("空 Analysis 缺少 %q", in)
		}
	}
	if strings.Contains(got, "丢分原因频次") {
		t.Error("空 Analysis 也不应再渲染丢分原因频次")
	}
}

func TestExtractOneLiner(t *testing.T) {
	tests := []struct {
		name      string
		interpret string
		want      string
	}{
		{"空", "", ""},
		{"同行冒号", "**一句话结论**：比分 13:9，输在补枪。", "比分 13:9，输在补枪。"},
		{"换行给出", "## 报告\n**一句话结论**\n输在残局。\n", "输在残局。"},
		{"无标记不发明", "这是一份没有标记的文本。", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractOneLiner(tt.interpret); got != tt.want {
				t.Errorf("extractOneLiner() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTagLabelFallback(t *testing.T) {
	tests := []struct {
		tag  string
		want string
	}{
		{model.CauseTradeFail, "补枪失败"},
		{model.CauseClutchThrow, "残局决策"},
		{"unknown_tag", "unknown_tag"},
	}
	for _, tt := range tests {
		if got := labels.CauseLabel(tt.tag); got != tt.want {
			t.Errorf("CauseLabel(%q) = %q, want %q", tt.tag, got, tt.want)
		}
	}
}

func TestMarkdownDrawRoundNotInVerdict(t *testing.T) {
	a := sampleAnalysis()
	draw := lostCall(9, model.CallUnknown, &model.RoundCall{Kind: model.CallUnknown})
	draw.Winner = model.SideDraw
	a.Rounds = append(a.Rounds, draw)
	got, err := Markdown(a, "")
	if err != nil {
		t.Fatalf("Markdown() error = %v", err)
	}
	if strings.Contains(got, "回合 9 ") {
		t.Error("平局回合 9 不应进入判决列表")
	}
}

func TestBuildQuestionsAlwaysTwoToThree(t *testing.T) {
	tests := []struct {
		name      string
		a         *model.Analysis
		interpret string
		wantFirst string
	}{
		{"无任何数据兜底 2 条", &model.Analysis{}, "", "这一场我最该先改的一个问题是什么？"},
		{"interpret 只给 1 条时补齐", &model.Analysis{}, "## 追问建议\n- 第 14 回合我为什么不转点？\n", "第 14 回合我为什么不转点？"},
		{"按归因标签生成", sampleAnalysis(), "", "第14回合这个残局我该怎么打？"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qs := buildQuestions(tt.a, tt.interpret)
			if len(qs) < 2 || len(qs) > 3 {
				t.Fatalf("buildQuestions() 返回 %d 条 %v，应在 2-3 条之间", len(qs), qs)
			}
			if !strings.Contains(qs[0], tt.wantFirst) {
				t.Errorf("qs[0] = %q，应包含 %q", qs[0], tt.wantFirst)
			}
		})
	}
}

func TestFormatCallLine(t *testing.T) {
	ra := model.RoundAnalysis{
		Number: 6,
		Call: &model.RoundCall{
			Kind: model.CallSplit, VictimName: "DONK", KillerName: "翻斗社会图图", Weapon: "SSG 08", Tick: 32279,
			FocusKillerName: "twisstzz-CS", FocusWeapon: "Desert Eagle", FocusTick: 32596,
		},
	}
	got := formatCallLine(ra)
	want := "回合 6 分开。DONK 先被 翻斗社会图图 用 SSG 08 打死（tick 32279），那下不是你的。你后来被 twisstzz-CS 用 Desert Eagle 打死（tick 32596）。demo_gototick 32596"
	if got != want {
		t.Errorf("formatCallLine =\n%q\nwant\n%q", got, want)
	}
}
