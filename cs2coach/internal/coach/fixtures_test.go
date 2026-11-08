package coach

import (
	"cs2-demo-review/internal/model"
)

// testAnalysis 手工构造一份最小可用的 Analysis fixture（不依赖 .dem / tactics）。
// 回合号 1..3 连续（与 ValidateCitations 的「N ∈ 1..len(rounds)」契约一致）。
func testAnalysis() *model.Analysis {
	return &model.Analysis{
		Meta: model.MatchMeta{
			Map:             "de_mirage",
			TickRate:        64,
			FocusPlayer:     "76561198000000001",
			FocusPlayerName: "张三",
			TotalRounds:     3,
		},
		Map: "de_mirage",
		Focus: model.FocusSummary{
			SteamID64: "76561198000000001",
			Name:      "张三",
			Sides: []model.FocusSideNote{
				{Half: "H1", Side: model.SideT},
				{Half: "H2", Side: model.SideCT},
			},
			Stats: model.PlayerMatchStats{
				RoundsPlayed: 3, Kills: 20, Deaths: 15, Assists: 5, KDR: 1.33,
				Damage: 2000, ADR: 80.5, FirstKills: 4, FirstDeaths: 6,
				TradedDeaths: 3, UntradedDeaths: 4, TradeKills: 5,
				ClutchWins: 1, ClutchLosses: 2, MultikillRounds: 3,
				UtilThrown: 12, EnemiesFlashed: 8,
			},
			SideStats: map[string]model.PlayerMatchStats{
				"T":  {RoundsPlayed: 2, Kills: 12, Deaths: 8},
				"CT": {RoundsPlayed: 1, Kills: 8, Deaths: 7},
			},
		},
		Story: model.StorySummary{
			FinalScore:      model.Score{T: 13, CT: 9},
			FocusTeamScore:  13,
			OpponentScore:   9,
			FirstHalfScore:  model.Score{T: 8, CT: 7},
			SecondHalfScore: model.Score{T: 5, CT: 2},
			FocusWonRounds:  []int{1},
			FocusLostRounds: []int{2, 3},
			WonByBuy:        map[string]int{"full": 1},
			LostByBuy:       map[string]int{"force": 1, "eco": 1},
			KeySwings:       []string{"第 2-3 回合连丢 2 分"},
		},
		LostRoundCauses: []model.CauseStat{
			{Tag: model.CauseFirstBloodGift, Label: "首杀白给", Count: 1, Rounds: []int{2}, Fraction: 0.5},
			{Tag: model.CauseTradeFail, Label: "补枪失败", Count: 1, Rounds: []int{2}, Fraction: 0.5},
			{Tag: model.CauseUtilityGap, Label: "道具缺口", Count: 1, Rounds: []int{3}, Fraction: 0.5},
		},
		Suspects: []model.Suspect{
			{
				Round:     2,
				TickRange: [2]int{5001, 9000},
				Tags:      []string{model.CauseFirstBloodGift, model.CauseTradeFail},
				Reason:    "首杀白给后未补枪",
				ScoreHint: 0.6,
			},
			{
				Round:     3,
				TickRange: [2]int{9001, 13000},
				Tags:      []string{model.CauseUtilityGap},
				Reason:    "交火前道具落后",
				ScoreHint: 0.35,
			},
		},
		GoodRounds: []model.GoodRound{
			{Round: 1, Reason: "焦点玩家残局获胜", Highlight: "1v2 残局拆包胜", Tick: 4800},
		},
		Rounds: []model.RoundAnalysis{
			{
				Number: 1, Winner: model.SideT, FocusWon: true,
				ScoreAfter: model.Score{T: 1, CT: 0}, EndReason: "elimination", Half: "H1",
				Buys:              map[model.Side]string{model.SideT: "full", model.SideCT: "full"},
				FocusBuy:          "full",
				WinCost:           0,
				TickRange:         [2]int{1000, 5000},
				UtilBeforeContact: map[model.Side]int{model.SideT: 3, model.SideCT: 2},
				Evidence: []model.Evidence{
					{Tick: 1100, RoundTime: 1.5, Kind: "kill", Text: "张三用 AK-47 击杀 李四（回合 1 首杀）"},
					{Tick: 4800, RoundTime: 58.0, Kind: "bomb", Text: "张三 1v2 残局拆包获胜"},
				},
			},
			{
				Number: 2, Winner: model.SideCT, FocusWon: false,
				ScoreAfter: model.Score{T: 1, CT: 1}, EndReason: "elimination", Half: "H1",
				Buys:        map[model.Side]string{model.SideT: "force", model.SideCT: "full"},
				FocusBuy:    "force",
				WinCost:     0.6,
				Suspect:     true,
				SuspectTags: []string{model.CauseFirstBloodGift, model.CauseTradeFail},
				TickRange:   [2]int{5001, 9000},
				Opening: &model.OpeningInfo{
					Tick: 5100, RoundTime: 2.0, KillerID: "76561198000000009", KillerName: "王五",
					KillerSide: model.SideCT, VictimID: "76561198000000001", VictimName: "张三",
					VictimSide: model.SideT, Weapon: "M4A1-S", TradedWithinWindow: false,
					FocusRole: "victim",
				},
				Trades: []model.TradeEvent{
					{Tick: 5200, VictimID: "76561198000000001", VictimName: "张三", VictimSide: model.SideT,
						KilledID: "76561198000000009", KilledName: "王五", AvengerID: "76561198000000002",
						AvengerName: "赵六", AvengerSide: model.SideT, Success: false, DelaySec: 8.0},
				},
				FocusDeath: &model.FocusDeathInfo{
					Tick: 5100, RoundTime: 2.0, KillerName: "王五", KillerSide: model.SideCT,
					Weapon: "M4A1-S", Headshot: true, Traded: false, PosLabel: "A 点附近",
					Pos: model.Vec3{X: 100, Y: 200}, UtilityHeld: 2, IsFirstDeath: true,
				},
				ManAdv: []model.ManAdvSpan{
					{StartTick: 5300, EndTick: 7000, AdvSide: model.SideT, AdvDelta: 1, Threw: true},
				},
				UtilBeforeContact: map[model.Side]int{model.SideT: 1, model.SideCT: 4},
				Causes:            []string{model.CauseFirstBloodGift, model.CauseTradeFail},
				CauseDetail:       map[string]string{model.CauseFirstBloodGift: "焦点玩家首死且未被补（tick 5100）", model.CauseTradeFail: "8 秒内没补掉凶手（tick 5200）"},
				Evidence: []model.Evidence{
					{Tick: 5100, RoundTime: 2.0, Kind: "kill", Text: "王五用 M4A1-S 爆头击杀 张三（首杀）"},
					{Tick: 5200, RoundTime: 10.0, Kind: "kill", Text: "赵六补枪未成功，延迟 8 秒"},
					{Tick: 5300, RoundTime: 12.0, Kind: "economy", Text: "T 强起（force）撞 CT 全枪"},
				},
			},
			{
				Number: 3, Winner: model.SideCT, FocusWon: false,
				ScoreAfter: model.Score{T: 1, CT: 2}, EndReason: "time", Half: "H1",
				Buys:        map[model.Side]string{model.SideT: "eco", model.SideCT: "full"},
				FocusBuy:    "eco",
				WinCost:     0.35,
				Suspect:     true,
				SuspectTags: []string{model.CauseUtilityGap},
				TickRange:   [2]int{9001, 13000},
				Clutch: &model.ClutchInfo{
					StartTick: 12000, EndTick: 12800, Situation: "1v2", FocusInClutch: true,
					FocusSide: model.SideT, Result: "lost", WinnerSide: model.SideCT, Advantageous: false,
					HPFocus: 30, HPOppMin: 80,
				},
				UtilBeforeContact: map[model.Side]int{model.SideT: 0, model.SideCT: 5},
				Causes:            []string{model.CauseUtilityGap},
				CauseDetail:       map[string]string{model.CauseUtilityGap: "交火前道具 0 : 5"},
				Evidence: []model.Evidence{
					{Tick: 9100, RoundTime: 1.5, Kind: "economy", Text: "T 全队 eco，装备值远低于 CT"},
					{Tick: 12000, RoundTime: 46.0, Kind: "alive", Text: "进入 1v2 残局"},
				},
			},
		},
	}
}

// testQASlice 手工构造一份检索切片。
func testQASlice() *model.QASlice {
	a := testAnalysis()
	return &model.QASlice{
		Question:    "第 2 回合我为什么先死？",
		Rounds:      []int{2},
		Analyses:    []model.RoundAnalysis{a.Rounds[1]},
		ExtraFacts:  []model.Evidence{{Tick: 5100, RoundTime: 2.0, Kind: "kill", Text: "王五用 M4A1-S 爆头击杀 张三"}},
		KeywordHits: []string{"opening", "首杀"},
	}
}
