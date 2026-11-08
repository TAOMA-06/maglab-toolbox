package tactics

import (
	"strings"
	"testing"

	"cs2-demo-review/internal/model"
)

func TestJudgeRoundCallKinds(t *testing.T) {
	focus := testFocus
	mate := sid(mateU64)
	enemy := sid(e1U64)
	kill := func(tick int, kid, kname, vid, vname string, vside model.Side, weapon string) model.KillEvent {
		return model.KillEvent{
			Tick: tick, KillerID: kid, KillerName: kname, KillerSide: model.SideCT,
			VictimID: vid, VictimName: vname, VictimSide: vside, Weapon: weapon,
		}
	}

	tests := []struct {
		name string
		ks   []model.KillEvent
		want string
	}{
		{
			name: "yours 焦点先死",
			ks: []model.KillEvent{
				kill(100, enemy, "敌1", focus, "焦点", model.SideT, "AK-47"),
			},
			want: model.CallYours,
		},
		{
			name: "teammate 队友先死焦点没死",
			ks: []model.KillEvent{
				kill(100, enemy, "敌1", mate, "队友A", model.SideT, "M4A1"),
				kill(200, enemy, "敌1", sid(mateBU64), "队友B", model.SideT, "M4A1"),
			},
			want: model.CallTeammate,
		},
		{
			name: "split 队友先死焦点后来又死",
			ks: []model.KillEvent{
				kill(100, enemy, "敌1", mate, "队友A", model.SideT, "SSG 08"),
				kill(200, enemy, "敌2", focus, "焦点", model.SideT, "Desert Eagle"),
			},
			want: model.CallSplit,
		},
		{
			name: "unknown 我方没人死",
			ks: []model.KillEvent{
				kill(100, focus, "焦点", enemy, "敌1", model.SideCT, "AK-47"),
			},
			want: model.CallUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := judgeRoundCall(tt.ks, model.SideT, focus)
			if got == nil || got.Kind != tt.want {
				t.Fatalf("kind = %v, want %s", got, tt.want)
			}
			if tt.want == model.CallSplit {
				if got.VictimName != "队友A" || got.Weapon != "SSG 08" || got.Tick != 100 {
					t.Errorf("首死者字段不对: %+v", got)
				}
				if got.FocusKillerName != "敌2" || got.FocusWeapon != "Desert Eagle" || got.FocusTick != 200 {
					t.Errorf("焦点死亡字段不对: %+v", got)
				}
			}
			if tt.want == model.CallYours {
				if got.KillerName != "敌1" || got.Weapon != "AK-47" || got.Tick != 100 {
					t.Errorf("yours 字段不对: %+v", got)
				}
			}
		})
	}
}

func TestMatchVerdictLeadHalfThreshold(t *testing.T) {
	mk := func(kind string) model.RoundAnalysis {
		return model.RoundAnalysis{
			FocusWon: false,
			Winner:   model.SideCT,
			Call:     &model.RoundCall{Kind: kind},
		}
	}
	// 7/13 纯队友 → 主因不在你
	rounds7 := make([]model.RoundAnalysis, 0, 13)
	for i := 0; i < 7; i++ {
		rounds7 = append(rounds7, mk(model.CallTeammate))
	}
	for i := 0; i < 6; i++ {
		rounds7 = append(rounds7, mk(model.CallYours))
	}
	lead7 := MatchVerdictLead(10, 13, rounds7)
	if !strings.Contains(lead7, "这场输了，主因不在你") {
		t.Errorf("7/13 应写主因不在你，got %q", lead7)
	}
	if strings.Contains(lead7, "不能说主因不在你") {
		t.Errorf("7/13 不应写不能说，got %q", lead7)
	}

	// 6/13 纯队友 → 不能说主因不在你
	rounds6 := make([]model.RoundAnalysis, 0, 13)
	for i := 0; i < 6; i++ {
		rounds6 = append(rounds6, mk(model.CallTeammate))
	}
	for i := 0; i < 7; i++ {
		rounds6 = append(rounds6, mk(model.CallSplit))
	}
	lead6 := MatchVerdictLead(10, 13, rounds6)
	if !strings.Contains(lead6, "不能说主因不在你") {
		t.Errorf("6/13 应写不能说主因不在你，got %q", lead6)
	}
	// 刚好一半也不过：6*2 == 12，不满足 >
	if MainCauseNotYou(CountCalls(rounds6)) {
		t.Error("6/13 刚好不到一半，MainCauseNotYou 应为 false")
	}
}

func TestFillRoundCallsOnLostRounds(t *testing.T) {
	m := testMatch()
	a, err := Analyze(m, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// testMatch R2 是焦点方丢分且焦点首死 → yours
	var r2 *model.RoundAnalysis
	for i := range a.Rounds {
		if a.Rounds[i].Number == 2 {
			r2 = &a.Rounds[i]
			break
		}
	}
	if r2 == nil {
		t.Fatal("缺回合 2")
	}
	if r2.FocusWon {
		t.Fatal("回合 2 应是丢分")
	}
	if r2.Call == nil || r2.Call.Kind != model.CallYours {
		t.Fatalf("回合 2 Call = %+v, want yours", r2.Call)
	}
	// 赢的回合 Call 为空
	for _, ra := range a.Rounds {
		if ra.FocusWon && ra.Call != nil {
			t.Errorf("赢的回合 %d 不应有 Call", ra.Number)
		}
	}
}
