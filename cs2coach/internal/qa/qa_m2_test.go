package qa

import (
	"strings"
	"testing"

	"cs2-demo-review/internal/model"
)

// testM2Analysis 在 testAnalysis 上挂站位/对枪数据（回合 2 站位、回合 3 对枪）。
func testM2Analysis() *model.Analysis {
	a := testAnalysis()
	a.Positioning = &model.PositioningReport{
		Deaths: 2, HighRiskCount: 1, HighRiskRate: 0.5,
		Thresholds: model.PositioningThresholds{
			ExposureMaxDistance: 2500, ExposureConeHalfAngle: 45, IsolatedTeammateDist: 800,
		},
	}
	a.Dueling = &model.DuelingReport{
		Total: 1, PreAimFailures: 1, MovingShots: 1,
		Thresholds: model.DuelThresholds{PreAimBadDeg: 15, MovingFireSpeedUPS: 100},
	}
	a.Rounds[1].Positioning = &model.PositionDeathAnalysis{
		Round: 2, Tick: 2100, PosLabel: "A 点附近",
		ExposedLines: 2, NearestTeammateDist: 620, HighRisk: true,
		RiskReasons: []string{"暴露枪线 2 条（视锥近似），最近敌 距离 620 units（右前）"},
	}
	a.Rounds[2].Duels = []model.DuelAnalysis{
		{Round: 3, Tick: 2800, PreAimMeasured: true, FocusFirstShotTick: 2800, OpponentName: "王五", PreAimDeltaDeg: 23.4, FocusSpeed: 182,
			FocusSpeedValid: true, HasBothShots: true, FirstShotDiffSec: 0.08,
			FocusShots: 3, FocusHits: 1, Outcome: "focus_death", PreAimFailed: true, MovingShot: true},
	}
	return a
}

// TestMatchKeywordsM2 站位/对枪口语词必须命中对应标签。
func TestMatchKeywordsM2(t *testing.T) {
	tests := []struct {
		name     string
		question string
		want     string
	}{
		{"站位", "我站位是不是有问题", "positioning"},
		{"死位", "这个死位怎么来的", "positioning"},
		{"枪线", "枪线太多了", "positioning"},
		{"暴露", "我总是暴露", "positioning"},
		{"预瞄", "预瞄差怎么办", "dueling"},
		{"急停", "急停没停稳怎么练", "dueling"},
		{"对枪", "对枪总是输", "dueling"},
		{"准星", "准星放哪", "dueling"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags, hits := matchKeywords(tt.question)
			found := false
			for _, tag := range tags {
				if tag == tt.want {
					found = true
				}
			}
			if !found {
				t.Errorf("%q 应命中 %q，got tags=%v hits=%v", tt.question, tt.want, tags, hits)
			}
		})
	}
}

// TestRetrieveM2Rounds 检索到站位/对枪回合时，RoundAnalysis 里的 M2 数据随切片返回。
func TestRetrieveM2Rounds(t *testing.T) {
	a := testM2Analysis()

	pos := Retrieve(a, "我站位是不是有问题")
	if len(pos.Rounds) != 1 || pos.Rounds[0] != 2 {
		t.Fatalf("站位问题应检索到回合 2，got %v", pos.Rounds)
	}
	if pos.Analyses[0].Positioning == nil || pos.Analyses[0].Positioning.Tick != 2100 {
		t.Errorf("切片应带 RoundAnalysis.Positioning，got %+v", pos.Analyses[0].Positioning)
	}

	duel := Retrieve(a, "预瞄差怎么办")
	if len(duel.Rounds) != 1 || duel.Rounds[0] != 3 {
		t.Fatalf("预瞄问题应检索到回合 3，got %v", duel.Rounds)
	}
	if len(duel.Analyses[0].Duels) != 1 || duel.Analyses[0].Duels[0].Tick != 2800 {
		t.Errorf("切片应带 RoundAnalysis.Duels，got %+v", duel.Analyses[0].Duels)
	}
}

// TestAnswerFactsM2 无 LLM 降级回答也要给出站位/对枪事实，且带 tick。
func TestAnswerFactsM2(t *testing.T) {
	a := testM2Analysis()
	pos := AnswerFacts(a, Retrieve(a, "我站位是不是有问题"))
	for _, want := range []string{
		"- 站位：[tick 2100] A 点附近",
		"暴露枪线 2 条（视锥近似）",
		"高危=true",
		"可用引用",
		"2100",
	} {
		if !strings.Contains(pos, want) {
			t.Errorf("站位降级回答缺少 %q：\n%s", want, pos)
		}
	}

	duel := AnswerFacts(a, Retrieve(a, "第3回合预瞄差怎么办"))
	for _, want := range []string{
		"- 对枪：[tick 2800] 对 王五",
		"预瞄偏差 23.4°（超阈值）",
		"速度 182 u/s（有效）",
		"先手差 +0.08 秒",
		"命中 1/3",
		"结果 焦点被击杀",
		"2800",
	} {
		if !strings.Contains(duel, want) {
			t.Errorf("对枪降级回答缺少 %q：\n%s", want, duel)
		}
	}
}

// TestRoundTicksIncludeM2 可引用 tick 收集包含站位/对枪 tick。
func TestRoundTicksIncludeM2(t *testing.T) {
	a := testM2Analysis()
	ticks := roundTicks(a.Rounds[1])
	if !containsInt(ticks, 2100) {
		t.Errorf("roundTicks 缺少站位 tick 2100：%v", ticks)
	}
	ticks = roundTicks(a.Rounds[2])
	if !containsInt(ticks, 2800) {
		t.Errorf("roundTicks 缺少对枪 tick 2800：%v", ticks)
	}
}

func containsInt(nums []int, want int) bool {
	for _, n := range nums {
		if n == want {
			return true
		}
	}
	return false
}
