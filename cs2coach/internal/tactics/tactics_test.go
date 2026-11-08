package tactics

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"cs2-demo-review/internal/model"
)

const testFocus = "76561198000000001"
const (
	focusU64 uint64 = 76561198000000001
	mateU64  uint64 = 76561198000000002
	mateBU64 uint64 = 76561198000000003
	e1U64    uint64 = 76561198000000009
	e2U64    uint64 = 76561198000000010
	e3U64    uint64 = 76561198000000011
)

func sid(u uint64) string { return fmt.Sprint(u) }

// testMatch 构造一场 4 回合的最小 Match：T 方焦点，开局/补枪/买型/残局全覆盖。
func testMatch() *model.Match {
	p := func(id uint64, name string, side model.Side) model.RoundPlayer {
		return model.RoundPlayer{SteamID64: id, Name: name, Side: side, AliveAtEnd: true}
	}
	kill := func(tick int, kid, kname string, kside model.Side, vid, vname string, vside model.Side, weapon string) model.KillEvent {
		return model.KillEvent{
			Tick: tick, RoundTime: float64(tick) / 64,
			KillerID: kid, KillerName: kname, KillerSide: kside,
			VictimID: vid, VictimName: vname, VictimSide: vside,
			Weapon: weapon, WeaponClass: "Rifle",
			VictimPos: model.Vec3{X: 100, Y: 200}, KillerPos: model.Vec3{X: 0, Y: 0},
		}
	}
	econ := func(buyT, buyCT string) model.EconomySnapshot {
		ev := map[string]int{"full": 6000, "force": 3000, "half": 4000, "eco": 1000}
		mkSide := func(buy string) model.SideEconomy {
			v := ev[buy]
			players := make([]model.PlayerEconomy, 5)
			for i := range players {
				players[i] = model.PlayerEconomy{SteamID64: uint64(100 + i), EquipValue: v}
			}
			spent := 0
			if buy != "eco" {
				spent = 2000
			}
			return model.SideEconomy{Money: 8000, EquipValueFreezeEnd: v * 5, MoneySpent: spent, Players: players}
		}
		return model.EconomySnapshot{T: mkSide(buyT), CT: mkSide(buyCT)}
	}
	mkRound := func(n int, winner model.Side, half string, econ model.EconomySnapshot,
		players []model.RoundPlayer, kills []model.KillEvent, util []model.UtilityEvent, alive []model.AliveCount) model.Round {
		return model.Round{
			Number: n, StartTick: n * 10000, EndTick: n*10000 + 6000,
			Winner: winner, EndReason: "elimination",
			ScoreAfter: model.Score{T: n, CT: 0}, Half: half,
			Economy: econ, Players: players, Kills: kills, Utility: util, Alive: alive,
		}
	}
	t1 := p(focusU64, "焦点", model.SideT)
	t2 := p(mateU64, "队友A", model.SideT)
	t3 := p(mateBU64, "队友B", model.SideT)
	c1 := p(e1U64, "敌1", model.SideCT)

	// R1：焦点方胜，焦点 2 杀含首杀。
	r1t1 := t1
	r1t1.Kills, r1t1.FirstKill = 2, true
	alive1 := []model.AliveCount{{Tick: 10000, TAlive: 5, CTAlive: 5}, {Tick: 10100, TAlive: 5, CTAlive: 4}, {Tick: 10200, TAlive: 5, CTAlive: 3}}
	r1 := mkRound(1, model.SideT, "H1", econ("full", "full"),
		[]model.RoundPlayer{r1t1, t2, c1},
		[]model.KillEvent{
			kill(10100, testFocus, "焦点", model.SideT, sid(e1U64), "敌1", model.SideCT, "AK-47"),
			kill(10200, testFocus, "焦点", model.SideT, sid(e2U64), "敌2", model.SideCT, "AK-47"),
		},
		[]model.UtilityEvent{{Tick: 10050, Type: "flash", ThrowerID: testFocus, ThrowerSide: model.SideT}},
		alive1)

	// R2：对方拿首杀（杀焦点），8s 后才被补 → first_blood_gift + trade_fail；T force 撞 CT full → eco_force。
	r2t1 := t1
	r2t1.Deaths, r2t1.FirstDeath, r2t1.DeathTick, r2t1.UtilityAtDeath = 1, true, 20100, 0
	alive2 := []model.AliveCount{{Tick: 20000, TAlive: 5, CTAlive: 5}, {Tick: 20100, TAlive: 4, CTAlive: 5}, {Tick: 20612, TAlive: 4, CTAlive: 4}}
	r2 := mkRound(2, model.SideCT, "H1", econ("force", "full"),
		[]model.RoundPlayer{r2t1, t2, c1},

		[]model.KillEvent{
			kill(20100, sid(e1U64), "敌1", model.SideCT, testFocus, "焦点", model.SideT, "M4A1"),
			kill(20612, sid(mateU64), "队友A", model.SideT, sid(e1U64), "敌1", model.SideCT, "AK-47"), // 512 tick 后 ≈ 8s
		},
		[]model.UtilityEvent{
			{Tick: 20050, Type: "smoke", ThrowerID: sid(e1U64), ThrowerSide: model.SideCT},
			{Tick: 20051, Type: "flash", ThrowerID: sid(e1U64), ThrowerSide: model.SideCT},
			{Tick: 20052, Type: "he", ThrowerID: sid(e1U64), ThrowerSide: model.SideCT},
		},
		alive2)

	// R3：平局（ openings 无补枪也不归因）。
	r3 := mkRound(3, model.SideDraw, "H1", econ("eco", "full"),
		[]model.RoundPlayer{t1, t2, c1},
		[]model.KillEvent{kill(30100, sid(e1U64), "敌1", model.SideCT, sid(mateU64), "队友A", model.SideT, "AWP")},
		nil,
		[]model.AliveCount{{Tick: 30000, TAlive: 5, CTAlive: 5}, {Tick: 30100, TAlive: 4, CTAlive: 5}})

	// R4：残局 1v2 被翻（焦点存活到最后，T 5v5 开局连死 4 人剩焦点）。
	r4t1 := t1
	r4t1.DeathTick = 0
	alive4 := []model.AliveCount{
		{Tick: 40000, TAlive: 5, CTAlive: 5},
		{Tick: 40100, TAlive: 4, CTAlive: 5}, {Tick: 40200, TAlive: 3, CTAlive: 5},
		{Tick: 40300, TAlive: 2, CTAlive: 5}, {Tick: 40400, TAlive: 1, CTAlive: 5},
		{Tick: 40500, TAlive: 1, CTAlive: 2}, {Tick: 40600, TAlive: 1, CTAlive: 1},
	}
	r4 := mkRound(4, model.SideCT, "H2", econ("full", "full"),
		[]model.RoundPlayer{r4t1, t2, t3, c1},
		[]model.KillEvent{
			kill(40100, sid(e1U64), "敌1", model.SideCT, sid(mateU64), "队友A", model.SideT, "M4A1"),
			kill(40400, sid(e1U64), "敌1", model.SideCT, sid(mateBU64), "队友B", model.SideT, "M4A1"),
			kill(40500, testFocus, "焦点", model.SideT, sid(e2U64), "敌2", model.SideCT, "AK-47"),
			kill(40600, sid(e3U64), "敌3", model.SideCT, testFocus, "焦点", model.SideT, "AK-47"),
		},
		[]model.UtilityEvent{{Tick: 40050, Type: "smoke", ThrowerID: testFocus, ThrowerSide: model.SideT}},
		alive4)

	return &model.Match{
		Meta:    model.MatchMeta{Map: "de_mirage", TickRate: 64, TotalRounds: 4, FocusPlayer: testFocus, FocusPlayerName: "焦点"},
		Players: []model.PlayerInfo{{SteamID64: 1, Name: "焦点"}},
		Rounds:  []model.Round{r1, r2, r3, r4},
	}
}

func TestAnalyzeBasic(t *testing.T) {
	a, err := Analyze(testMatch(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Rounds) != 4 {
		t.Fatalf("回合数 = %d want 4", len(a.Rounds))
	}
	// R1 焦点胜：无归因，WinCost 0。
	if len(a.Rounds[0].Causes) != 0 || a.Rounds[0].WinCost != 0 {
		t.Errorf("R1 不应有归因: %+v", a.Rounds[0])
	}
	if !a.Rounds[0].FocusWon {
		t.Error("R1 焦点方应获胜")
	}
	// R2：三个标签齐了。
	want := map[string]bool{model.CauseFirstBloodGift: true, model.CauseTradeFail: true, model.CauseEcoForce: true}
	for _, tag := range a.Rounds[1].Causes {
		delete(want, tag)
	}
	for tag := range want {
		t.Errorf("R2 缺少归因 %s, 实际 %v", tag, a.Rounds[1].Causes)
	}
	if a.Rounds[1].WinCost != 0.6 {
		t.Errorf("R2 WinCost = %.2f want 0.6 (多标签取 max)", a.Rounds[1].WinCost)
	}
	if a.Rounds[1].Opening == nil || a.Rounds[1].Opening.TradedWithinWindow {
		t.Error("R2 首杀 8s 后被补，应判未在窗口内")
	}
	// R3 平局：无归因。
	if len(a.Rounds[2].Causes) != 0 {
		t.Errorf("平局不应归因: %v", a.Rounds[2].Causes)
	}
	// 每回合 ≥3 条证据。
	for _, ra := range a.Rounds {
		if len(ra.Evidence) < 3 {
			t.Errorf("R%d 证据 %d 条 < 3", ra.Number, len(ra.Evidence))
		}
	}
}

func TestClassifyBuy(t *testing.T) {
	cases := []struct {
		name  string
		avg   float64
		spent int
		want  string
	}{
		{"full 线上", 4750, 0, "full"},
		{"force", 3000, 2000, "force"},
		{"half", 4000, 2000, "half"},
		{"eco 低均值", 1000, 0, "eco"},
		{"攒钱不花按 eco", 3000, 0, "eco"},
	}
	for _, c := range cases {
		if got := classifyBuy(c.avg, c.spent); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}

func TestTradeWindowBoundary(t *testing.T) {
	mkKill := func(tick int, kid string, kside model.Side, vid string, vside model.Side) model.KillEvent {
		return model.KillEvent{Tick: tick, KillerID: kid, KillerSide: kside, VictimID: vid, VictimSide: vside}
	}
	// 5s 窗口 @64tick = 320 tick：恰好 320 算成功，321 算失败。
	kills := []model.KillEvent{
		mkKill(1000, "A", model.SideCT, "B", model.SideT),
		mkKill(1320, "B2", model.SideT, "A", model.SideCT),
	}
	c := &roundCtx{window: 5.0, rate: 64}
	tr := buildTrades(c, kills, model.SideCT)
	if len(tr) != 2 || !tr[0].Success {
		t.Errorf("320 tick 应判补枪成功: %+v", tr)
	}
	kills[1].Tick = 1321
	tr = buildTrades(c, kills, model.SideCT)
	if tr[0].Success {
		t.Errorf("321 tick 应判补枪失败: %+v", tr)
	}
}

func TestSuspectsOrderAndStory(t *testing.T) {
	a, err := Analyze(testMatch(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Suspects 按 WinCost 降序：R2(0.6) 应在 R4 之前（若 R4 有嫌疑）。
	for i := 1; i < len(a.Suspects); i++ {
		if a.Suspects[i-1].ScoreHint < a.Suspects[i].ScoreHint {
			t.Errorf("Suspects 未按 WinCost 降序: %+v", a.Suspects)
			break
		}
	}
	if len(a.Suspects) == 0 || a.Suspects[0].Round != 2 {
		t.Errorf("Top1 可疑应为 R2, 实际 %+v", a.Suspects)
	}
	if len(a.Story.FocusWonRounds) != 1 || len(a.Story.FocusLostRounds) != 2 {
		t.Errorf("胜负回合统计错: won=%v lost=%v", a.Story.FocusWonRounds, a.Story.FocusLostRounds)
	}
	if len(a.GoodRounds) == 0 || a.GoodRounds[0].Round != 1 {
		t.Errorf("好回合应为 R1: %+v", a.GoodRounds)
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	a, err := Analyze(testMatch(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "analysis.json")
	if err := SaveJSON(a, path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if len(raw) == 0 || raw[0] != '{' {
		t.Error("JSON 落盘格式异常")
	}
	b, err := LoadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Rounds) != len(a.Rounds) || b.Focus.Name != a.Focus.Name {
		t.Error("LoadJSON 回读不一致")
	}
}

func TestAnalyzeNil(t *testing.T) {
	if _, err := Analyze(nil, Options{}); err == nil {
		t.Error("nil Match 应报错")
	}
}

func TestAnalyzeGhostFocus(t *testing.T) {
	m := testMatch()
	m.Meta.FocusPlayer = "00000000000000000" // 整场没出现
	if _, err := Analyze(m, Options{}); err == nil {
		t.Error("幽灵焦点（整场未出现）应报错，而不是静默记丢分")
	}
}

func TestEcoSweepCause(t *testing.T) {
	// 焦点方 eco 撞对方 full，且焦点方 0 杀 → eco_force（强行接团被横扫）。
	mkSide := func(v int) model.SideEconomy {
		players := make([]model.PlayerEconomy, 5)
		for i := range players {
			players[i] = model.PlayerEconomy{SteamID64: uint64(100 + i), EquipValue: v}
		}
		spent := 0
		if v > 1000 {
			spent = 2000
		}
		return model.SideEconomy{Money: 8000, EquipValueFreezeEnd: v * 5, MoneySpent: spent, Players: players}
	}
	m := &model.Match{
		Meta:    model.MatchMeta{Map: "de_mirage", TickRate: 64, FocusPlayer: testFocus, FocusPlayerName: "焦点"},
		Players: []model.PlayerInfo{{SteamID64: focusU64, Name: "焦点"}},
		Rounds: []model.Round{{
			Number: 1, StartTick: 10000, EndTick: 16000, Winner: model.SideCT,
			EndReason: "elimination", ScoreAfter: model.Score{T: 0, CT: 1}, Half: "H1",
			Economy: model.EconomySnapshot{T: mkSide(1000), CT: mkSide(6000)},
			Players: []model.RoundPlayer{
				{SteamID64: focusU64, Name: "焦点", Side: model.SideT, Deaths: 1, DeathTick: 10100, FirstDeath: true},
			},
			Kills: []model.KillEvent{{
				Tick: 10100, KillerID: sid(e1U64), KillerName: "敌1", KillerSide: model.SideCT,
				VictimID: testFocus, VictimName: "焦点", VictimSide: model.SideT, Weapon: "AK-47",
			}},
			Alive: []model.AliveCount{{Tick: 10000, TAlive: 5, CTAlive: 5}, {Tick: 10100, TAlive: 4, CTAlive: 5}},
		}},
	}
	a, err := Analyze(m, Options{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tag := range a.Rounds[0].Causes {
		if tag == model.CauseEcoForce {
			found = true
		}
	}
	if !found {
		t.Errorf("eco 撞 full 被横扫应有 eco_force，实际 %v", a.Rounds[0].Causes)
	}
}
