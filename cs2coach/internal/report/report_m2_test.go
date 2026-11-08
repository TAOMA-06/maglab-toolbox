package report

import (
	"strings"
	"testing"

	"cs2-demo-review/internal/model"
)

// m2Analysis 在 sampleAnalysis 上挂 M2 站位/对枪事实（不依赖真实 demo / tactics）。
func m2Analysis() *model.Analysis {
	a := sampleAnalysis()
	a.Positioning = &model.PositioningReport{
		Deaths:         5,
		HighRiskCount:  3,
		HighRiskRate:   0.6,
		ExposedDeaths:  3,
		NoCoverDeaths:  2,
		IsolatedDeaths: 1,
		HotZones: []model.HotZone{
			{PosLabel: "A 点附近", Pos: model.Vec3{X: 100, Y: 200}, Deaths: 2, Rounds: []int{3, 7}, Ticks: []int{30100, 70500}},
			{PosLabel: "B 点附近", Pos: model.Vec3{X: 900, Y: 800}, Deaths: 2, Rounds: []int{5, 14}, Ticks: []int{50120, 142000}},
			{PosLabel: "中路", Pos: model.Vec3{X: 500, Y: 500}, Deaths: 1, Rounds: []int{11}, Ticks: []int{110000}},
		},
		Thresholds: model.PositioningThresholds{
			ExposureMaxDistance: 2500, ExposureConeHalfAngle: 45, IsolatedTeammateDist: 800,
			CoverRadius: 200, CoverWindowSec: 10, HotZoneRadius: 400,
		},
	}
	a.Dueling = &model.DuelingReport{
		Total: 6, Measured: 6, Fired: 6, PreAimFailures: 3, MovingShots: 2, FirstShotWins: 2,
		FocusKills: 3, FocusDeaths: 3,
		Distribution: model.MetricDistribution{
			PreAimSamples: 6,
			PreAimDeltaDeg: model.Percentiles{
				Min: 5, P50: 15.25, P75: 22.05, P90: 29.34, P95: 31.2, Max: 31.2,
			},
			ShotSpeedSamples: 4,
			ShotSpeedUPS: model.Percentiles{
				Min: 0, P50: 50, P75: 151.5, P90: 182, P95: 182, Max: 182,
			},
		},
		Thresholds: model.DuelThresholds{PreAimBadDeg: 15, MovingFireSpeedUPS: 100},
	}

	// 站位死亡：3 与 7 高危；5 与 14 非高危。故意乱序放入，验证报告按回合升序。
	a.Rounds[2].Positioning = &model.PositionDeathAnalysis{
		Round: 7, Tick: 70500, PosLabel: "A 点附近",
		ExposedLines: 3, NearestTeammateDist: 400, CoverUtility: true, CoverType: "smoke",
		HighRisk: true,
		// RiskReasons 为空：报告必须自己补「视锥近似」口径，不能编理由。
	}
	a.Rounds[0].Positioning = &model.PositionDeathAnalysis{
		Round: 3, Tick: 30100, PosLabel: "A 点附近",
		ExposedLines: 2, NearestTeammateDist: 620, NearestTeammateName: "队友B",
		HighRisk: true,
		RiskReasons: []string{
			"暴露枪线 2 条（视锥近似），最近敌 距离 620 units（右前）",
			"死亡前 10s 无己方烟/火覆盖该位置",
		},
	}
	a.Rounds[1].Positioning = &model.PositionDeathAnalysis{
		Round: 5, Tick: 50120, PosLabel: "B 点附近",
		ExposedLines: 1, NearestTeammateDist: 1200, Isolated: true,
		CoverUtility: true, CoverType: "smoke",
	}
	a.Rounds[3].Positioning = &model.PositionDeathAnalysis{
		Round: 14, Tick: 142000, PosLabel: "B 点附近",
		ExposedLines: 0, NearestTeammateDist: 900, Isolated: true,
	}

	// 对枪：预瞄差故意乱序，验证报告按 PreAimDeltaDeg 降序、回合升序。
	a.Rounds[0].Duels = []model.DuelAnalysis{
		{Round: 3, Tick: 30500, PreAimMeasured: true, OpponentName: "Q", PreAimDeltaDeg: 8, FocusSpeed: 40, FocusSpeedValid: true,
			HasBothShots: true, FirstShotDiffSec: -0.05, FocusShots: 3, FocusHits: 2, Outcome: "focus_kill"},
		{Round: 3, Tick: 33000, PreAimMeasured: true, OpponentName: "U", PreAimDeltaDeg: 5, FocusSpeed: 30, FocusSpeedValid: true,
			HasBothShots: true, FirstShotDiffSec: 0.1, FocusShots: 1, FocusHits: 0, Outcome: "no_kill"},
	}
	a.Rounds[1].Duels = []model.DuelAnalysis{
		{Round: 5, Tick: 50500, PreAimMeasured: true, OpponentName: "R", PreAimDeltaDeg: 23.4, FocusSpeed: 182, FocusSpeedValid: true,
			HasBothShots: true, FirstShotDiffSec: 0.08, FocusShots: 3, FocusHits: 1, Outcome: "focus_death",
			PreAimFailed: true, MovingShot: true,
			Verdict: "第5回合 [tick 50500] 对 R 对枪：预瞄偏差 23.4°，速度 182 u/s，命中 1/3，结果被击杀。"},
	}
	a.Rounds[2].Duels = []model.DuelAnalysis{
		{Round: 7, Tick: 70900, PreAimMeasured: true, OpponentName: "S", PreAimDeltaDeg: 31.2, FocusShots: 2, FocusHits: 0,
			Outcome: "focus_death", PreAimFailed: true},
	}
	a.Rounds[3].Duels = []model.DuelAnalysis{
		{Round: 14, Tick: 142500, PreAimMeasured: true, OpponentName: "T", PreAimDeltaDeg: 12, FocusSpeed: 60, FocusSpeedValid: true,
			HasBothShots: true, FirstShotDiffSec: 0.02, FocusShots: 2, FocusHits: 1, Outcome: "no_kill"},
		{Round: 14, Tick: 146000, PreAimMeasured: true, OpponentName: "V", PreAimDeltaDeg: 18.5, FocusSpeed: 120, FocusSpeedValid: true,
			HasBothShots: true, FirstShotDiffSec: 0.6, FocusShots: 2, FocusHits: 0,
			Outcome: "focus_death", PreAimFailed: true, MovingShot: true},
	}
	return a
}

// TestMarkdownM2Facts 纯事实版必须包含两节、热区、带 tick 的高危站位与对枪明细。
func TestMarkdownM2Facts(t *testing.T) {
	got, err := Markdown(m2Analysis(), "")
	if err != nil {
		t.Fatalf("Markdown() error = %v", err)
	}

	wantSubstrings := []struct {
		name string
		in   string
	}{
		{"站位标题", "## 站位分析"},
		{"对枪标题", "## 对枪分析"},
		{"站位汇总", "焦点死亡 5 次，高危 3 次（60.0%）"},
		{"站位规则", "下次别离队友这么远"},
		{"站位暴露说明", "单独不算高危"},
		{"站位阈值", "暴露距离 2500 units / 暴露半角 45° / 孤立队友间距 800 units / 掩护半径 200 units（死亡前 10 秒）"},
		{"热区标题", "### 死亡热区 Top3"},
		{"热区行", "A 点附近（坐标(100, 200)）· 2 次死亡 · 回合 3、7（tick 30100、70500）"},
		{"高危标题", "### 高危站位"},
		{"高危行-回合3", "回合 3 [tick 30100] demo_gototick 30100 A 点附近：暴露枪线 2 条（视锥近似）"},
		{"高危行-回合7", "回合 7 [tick 70500] demo_gototick 70500 A 点附近：暴露枪线 3 条（视锥近似）"},
		{"对枪汇总", "对枪 6 次（预瞄可评估 6 次，无接敌快照 0 次；焦点开火 6 次，先手命中 0 次；焦点击杀 3 / 阵亡 3），预瞄超阈值 3 次，被侧身或背身 0 次，移动中开枪 2 次"},
		{"对枪阈值", "阈值：预瞄偏差 15° / 开火速度 100 u/s"},
		{"对枪明细标题", "### 对枪明细"},
		{"对枪明细-预瞄最差", "回合 7 [tick 70900] 对 S：预瞄偏差 31.2°（超阈值）"},
		{"对枪明细-无效速度", "速度 0 u/s（无效）"},
		{"对枪明细-未双方开火", "未双方开火"},
		{"Verdict 归一化", "回合 5 [tick 50500] 对 R：预瞄偏差 23.4°，速度 182 u/s，命中 1/3，结果被击杀。"},
	}
	for _, w := range wantSubstrings {
		if !strings.Contains(got, w.in) {
			t.Errorf("Markdown 站位/对枪事实缺少 %s（%q）", w.name, w.in)
		}
	}

	// 章节顺序：判决 → 好回合 → 站位分析 → 对枪分析 → 追问入口（无丢分原因频次）。
	order := []string{"**判决**：", "## 值得保留的好回合", "## 站位分析", "## 对枪分析", "## 追问入口"}
	last := -1
	for _, s := range order {
		i := strings.Index(got, s)
		if i < 0 {
			t.Fatalf("缺少章节 %q", s)
		}
		if i < last {
			t.Errorf("章节顺序错误：%q 出现在 %d，前一节在 %d", s, i, last)
		}
		last = i
	}
	if strings.Contains(got, "丢分原因频次") {
		t.Error("报告不应再输出丢分原因频次")
	}
}

// TestMarkdownM2DuelSortOrder 对枪明细按预瞄差降序（并列按回合升序）。
func TestMarkdownM2DuelSortOrder(t *testing.T) {
	got, err := Markdown(m2Analysis(), "")
	if err != nil {
		t.Fatalf("Markdown() error = %v", err)
	}
	ticks := []string{"tick 70900", "tick 50500", "tick 146000", "tick 142500", "tick 30500", "tick 33000"}
	last := -1
	for _, tick := range ticks {
		i := strings.Index(got, tick)
		if i < 0 {
			t.Fatalf("对枪明细缺少 %s", tick)
		}
		if i < last {
			t.Errorf("对枪明细顺序错误：%s 出现在 %d，前一条在 %d", tick, i, last)
		}
		last = i
	}
}

// TestMarkdownM2MissingData 没有 M2 数据时只写固定缺失文案，不编造。
func TestMarkdownM2MissingData(t *testing.T) {
	got, err := Markdown(sampleAnalysis(), "")
	if err != nil {
		t.Fatalf("Markdown() error = %v", err)
	}
	for _, in := range []string{missingPositioningText, missingDuelingText, "## 站位分析", "## 对枪分析"} {
		if !strings.Contains(got, in) {
			t.Errorf("缺少缺失数据文案：%q", in)
		}
	}
	if strings.Contains(got, "暴露枪线") {
		t.Error("没有站位数据时不应出现暴露枪线等编造事实")
	}
}

// TestHTMLM2Facts HTML 与 Markdown 共用 page 事实，关键字符串必须一致。
func TestHTMLM2Facts(t *testing.T) {
	got, err := HTML(m2Analysis(), "")
	if err != nil {
		t.Fatalf("HTML() error = %v", err)
	}
	for _, in := range []string{
		"站位分析", "对枪分析",
		"焦点死亡 5 次，高危 3 次（60.0%）",
		"下次别离队友这么远",
		"单独不算高危",
		"暴露距离 2500 units / 暴露半角 45° / 孤立队友间距 800 units / 掩护半径 200 units（死亡前 10 秒）",
		"A 点附近", "2 次死亡", "tick 30100", "tick 70500",
		"回合 3 [tick 30100] demo_gototick 30100 A 点附近",
		"回合 7 [tick 70900] 对 S：预瞄偏差 31.2°（超阈值）",
		"暴露枪线 3 条（视锥近似）",
		"对枪 6 次（预瞄可评估 6 次，无接敌快照 0 次；焦点开火 6 次，先手命中 0 次；焦点击杀 3 / 阵亡 3）",
		"预瞄超阈值 3 次，被侧身或背身 0 次",
		"预瞄偏差 15° / 开火速度 100 u/s",
		"预瞄偏差 31.2°（超阈值）",
	} {
		if !strings.Contains(got, in) {
			t.Errorf("HTML 站位/对枪事实缺少 %q", in)
		}
	}
}

// TestHTMLM2MissingData HTML 同样输出缺失文案。
func TestHTMLM2MissingData(t *testing.T) {
	got, err := HTML(sampleAnalysis(), "")
	if err != nil {
		t.Fatalf("HTML() error = %v", err)
	}
	for _, in := range []string{missingPositioningText, missingDuelingText} {
		if !strings.Contains(got, in) {
			t.Errorf("HTML 缺少缺失数据文案：%q", in)
		}
	}
}

// TestM2CoachSectionExtraction interpret 里的「## 站位/对枪」正文被抽取到对应事实节。
func TestM2CoachSectionExtraction(t *testing.T) {
	interpret := strings.Join([]string{
		"**一句话结论**：13:9 赢了。",
		"## 站位解读",
		"第 3 回合 [tick 30100] 站位太靠前，先躲第一枪线。",
		"## 对枪解读",
		"第 5 回合 [tick 50500] 预瞄没到位，先停稳再开枪。",
		"## 追问建议",
		"- 第 3 回合我该站哪？",
	}, "\n")

	md, err := Markdown(m2Analysis(), interpret)
	if err != nil {
		t.Fatalf("Markdown() error = %v", err)
	}
	for _, in := range []string{
		"**教练解读**",
		"站位太靠前，先躲第一枪线。",
		"预瞄没到位，先停稳再开枪。",
	} {
		if !strings.Contains(md, in) {
			t.Errorf("Markdown 缺少抽取的教练解读 %q", in)
		}
	}
	// 解读正文不能混进纯事实清单：高危站位仍来自 facts。
	if !strings.Contains(md, "回合 7 [tick 70500] demo_gototick 70500 A 点附近：暴露枪线 3 条（视锥近似）") {
		t.Error("抽取 interpret 后事实行被破坏")
	}

	htmlOut, err := HTML(m2Analysis(), interpret)
	if err != nil {
		t.Fatalf("HTML() error = %v", err)
	}
	if !strings.Contains(htmlOut, "教练解读") || !strings.Contains(htmlOut, "站位太靠前") {
		t.Error("HTML 缺少抽取的教练解读")
	}
}

// TestM2CoachSectionEscaped HTML 中 interpret 文本必须转义，不能注入标签/事实。
func TestM2CoachSectionEscaped(t *testing.T) {
	interpret := "## 站位解读\n<script>alert('x')</script>\n"
	got, err := HTML(m2Analysis(), interpret)
	if err != nil {
		t.Fatalf("HTML() error = %v", err)
	}
	if strings.Contains(got, "<script>alert") {
		t.Error("HTML 未转义 interpret 中的 script 标签")
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Error("HTML 应包含转义后的 interpret 文本")
	}
}

// TestExtractNamedSection 直接测小节抽取（含边界）。
func TestExtractNamedSection(t *testing.T) {
	interpret := strings.Join([]string{
		"**一句话结论**：x",
		"## 站位解读",
		"站位正文 A",
		"",
		"### 站位子项",
		"站位正文 B",
		"## 对枪解读",
		"对枪正文",
		"## 追问建议",
		"问题",
	}, "\n")
	tests := []struct {
		name    string
		keyword string
		want    string
	}{
		{"站位小节", "站位", "站位正文 A\n\n### 站位子项\n站位正文 B"},
		{"对枪小节", "对枪", "对枪正文"},
		{"不存在", "经济", ""},
		{"空文本", "站位", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := interpret
			if tt.name == "空文本" {
				in = ""
			}
			if got := extractNamedSection(in, tt.keyword); got != tt.want {
				t.Errorf("extractNamedSection(%q) = %q, want %q", tt.keyword, got, tt.want)
			}
		})
	}
}

// TestNoM2DataDoesNotRenderCoachSection 无 M2 事实时，即使 interpret 里有
// 「站位/对枪」小节也不能渲染：否则 LLM 可以绕过「无数据不结论」的架构锁。
func TestNoM2DataDoesNotRenderCoachSection(t *testing.T) {
	a := sampleAnalysis() // 不带 Positioning / Dueling
	interpret := "## 站位解读\n- 编造的站位结论 回合1 [tick 1]\n\n## 对枪解读\n- 编造的对枪结论 回合1 [tick 1]"
	md, err := Markdown(a, interpret)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(md, "编造的站位结论") || strings.Contains(md, "编造的对枪结论") {
		t.Errorf("无 M2 数据时不应渲染教练解读：\n%s", md)
	}
	htmlOut, err := HTML(a, interpret)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(htmlOut, "编造的站位结论") || strings.Contains(htmlOut, "编造的对枪结论") {
		t.Errorf("无 M2 数据时 HTML 不应渲染教练解读")
	}
}
