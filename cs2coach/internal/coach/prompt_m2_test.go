package coach

import (
	"strings"
	"testing"

	"cs2-demo-review/internal/model"
)

// testM2Analysis 在 testAnalysis 上挂 M2 站位/对枪事实（回合 2/3 都在 tick 区间内）。
func testM2Analysis() *model.Analysis {
	a := testAnalysis()
	a.Positioning = &model.PositioningReport{
		Deaths: 3, HighRiskCount: 2, HighRiskRate: 2.0 / 3.0,
		ExposedDeaths: 2, NoCoverDeaths: 1, IsolatedDeaths: 1,
		HotZones: []model.HotZone{
			{PosLabel: "A 点附近", Pos: model.Vec3{X: 100, Y: 200}, Deaths: 2, Rounds: []int{2, 3}, Ticks: []int{5100, 12000}},
		},
		Thresholds: model.PositioningThresholds{
			ExposureMaxDistance: 2500, ExposureConeHalfAngle: 45, IsolatedTeammateDist: 800,
			CoverRadius: 200, CoverWindowSec: 10, HotZoneRadius: 400,
		},
	}
	a.Dueling = &model.DuelingReport{
		Total: 3, Measured: 3, Fired: 3, PreAimFailures: 2, MovingShots: 1, FirstShotWins: 1,
		FocusKills: 1, FocusDeaths: 2,
		Distribution: model.MetricDistribution{
			PreAimSamples: 3,
			PreAimDeltaDeg: model.Percentiles{
				Min: 5, P50: 18, P75: 23, P90: 29, P95: 29, Max: 29,
			},
			ShotSpeedSamples: 3,
			ShotSpeedUPS: model.Percentiles{
				Min: 10, P50: 80, P75: 160, P90: 220, P95: 220, Max: 220,
			},
		},
		Thresholds: model.DuelThresholds{PreAimBadDeg: 15, MovingFireSpeedUPS: 100},
	}
	a.Rounds[1].Positioning = &model.PositionDeathAnalysis{
		Round: 2, Tick: 5100, PosLabel: "A 点附近",
		ExposedLines: 2, NearestTeammateDist: 620, HighRisk: true,
		RiskReasons: []string{"暴露枪线 2 条（视锥近似），最近敌 距离 620 units（右前）"},
	}
	a.Rounds[1].Duels = []model.DuelAnalysis{
		{Round: 2, Tick: 6000, PreAimMeasured: true, OpponentName: "王五", PreAimDeltaDeg: 23.4, FocusSpeed: 182,
			FocusSpeedValid: true, HasBothShots: true, FirstShotDiffSec: 0.08,
			FocusShots: 3, FocusHits: 1, Outcome: "focus_death", PreAimFailed: true, MovingShot: true},
		{Round: 2, Tick: 6200, PreAimMeasured: true, OpponentName: "李四", PreAimDeltaDeg: 5, FocusSpeed: 40,
			FocusSpeedValid: true, HasBothShots: true, FirstShotDiffSec: -0.2,
			FocusShots: 2, FocusHits: 1, Outcome: "focus_kill"},
	}
	a.Rounds[2].Duels = []model.DuelAnalysis{
		{Round: 3, Tick: 12000, PreAimMeasured: true, OpponentName: "赵六", PreAimDeltaDeg: 29, FocusSpeed: 220,
			FocusSpeedValid: true, HasBothShots: true, FirstShotDiffSec: 0.3,
			FocusShots: 2, FocusHits: 0, Outcome: "focus_death", PreAimFailed: true, MovingShot: true},
	}
	return a
}

// TestReportPromptM2System system 固定结构追加站位/对枪解读，且在正确位置。
func TestReportPromptM2System(t *testing.T) {
	system, _ := ReportPrompt(testM2Analysis())
	for _, want := range []string{"## 站位解读", "## 对枪解读", "视锥近似", "禁写「敌人能看到你」"} {
		if !strings.Contains(system, want) {
			t.Errorf("system 缺少 M2 要素 %q", want)
		}
	}
	iGood := strings.Index(system, "## 值得保留的好回合")
	iPos := strings.Index(system, "## 站位解读")
	iDuel := strings.Index(system, "## 对枪解读")
	iAsk := strings.Index(system, "## 追问建议")
	if !(iGood >= 0 && iGood < iPos && iPos < iDuel && iDuel < iAsk) {
		t.Errorf("M2 小节位置错误：好回合@%d 站位@%d 对枪@%d 追问@%d", iGood, iPos, iDuel, iAsk)
	}
}

// TestReportPromptM2User user 里加入站位/对枪数据块（汇总/阈值/热区/高危/分布/明细）。
func TestReportPromptM2User(t *testing.T) {
	_, user := ReportPrompt(testM2Analysis())
	for _, want := range []string{
		"## 站位数据",
		"## 对枪数据",
		"焦点死亡 3 次，高危 2 次（66.7%）",
		"暴露距离 2500 units / 暴露半角 45° / 孤立队友间距 800 units",
		"A 点附近（坐标(100, 200)）2 次死亡",
		"回合 2 [tick 5100] A 点附近：暴露枪线 2 条（视锥近似）",
		"对枪 3 次（预瞄可评估 3 次，无接敌快照 0 次），焦点开火 3 次，先手命中 0 次；预瞄超阈值 2 次，移动中开枪 1 次",
		"预瞄偏差 15° / 开火速度 100 u/s",
		"预瞄差 P50 18° / P75 23° / P90 29°",
		"开火速度 P50 80 / P75 160 / P90 220 u/s",
		"回合 2 [tick 6000] 对 王五：预瞄偏差 23.4°",
		"暴露枪线是视锥近似",
		"高危不含单独的暴露枪线",
	} {
		if !strings.Contains(user, want) {
			t.Errorf("user 缺少 M2 要素 %q", want)
		}
	}
}

// TestReportPromptM2Missing 没有 M2 数据时明示缺失，不让模型编造。
func TestReportPromptM2Missing(t *testing.T) {
	_, user := ReportPrompt(testAnalysis())
	for _, want := range []string{
		"（本场未采到站位数据；不要写站位结论）",
		"（本场未采到对枪数据；不要写对枪结论）",
	} {
		if !strings.Contains(user, want) {
			t.Errorf("缺少 M2 缺失文案 %q", want)
		}
	}
}

// TestWriteRoundSliceM2Facts 回合切片里加入该回合站位/对枪事实，每条带 tick。
func TestWriteRoundSliceM2Facts(t *testing.T) {
	a := testM2Analysis()
	var b strings.Builder
	writeRoundSlice(&b, a.Rounds[1], 1, nil)
	out := b.String()
	for _, want := range []string{
		"- [tick 5100] 站位：A 点附近：暴露枪线 2 条（视锥近似）",
		"- [tick 6000] 对枪：预瞄偏差 23.4°",
		"- [tick 6200] 对枪：预瞄偏差 5.0°",
		"速度 182 u/s（有效）",
		"先手差 +0.08 s",
		"命中 1/3",
		"结果 焦点被击杀",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("回合切片缺少 %q：\n%s", want, out)
		}
	}
}

// TestCitationTicksIncludeM2 可引用 tick 清单包含站位/对枪 tick，供 ValidateCitations 校验。
func TestCitationTicksIncludeM2(t *testing.T) {
	a := testM2Analysis()
	ticks := citationTicks(a.Rounds[1])
	for _, want := range []int{5100, 6000, 6200} {
		found := false
		for _, tick := range ticks {
			if tick == want {
				found = true
			}
		}
		if !found {
			t.Errorf("citationTicks 缺少 M2 tick %d：%v", want, ticks)
		}
	}

	// 新增 prompt 文本必须仍能通过证据校验（回合 2 区间 5001~9000）。
	if _, errs := ValidateCitations("回合 2 [tick 6000] 预瞄偏差 23.4°。", a); len(errs) != 0 {
		t.Errorf("合法 M2 引用被误删：%+v", errs)
	}
	if _, errs := ValidateCitations("回合 2 [tick 99999] 预瞄偏差 23.4°。", a); len(errs) == 0 {
		t.Error("非法 M2 tick 应被 ValidateCitations 拦下")
	}
}
