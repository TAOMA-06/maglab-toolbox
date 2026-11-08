package model

import (
	"encoding/json"
	"testing"
)

// 数据契约冒烟：类型 JSON 往返 + 阵营常量互异（重构护栏，非行为测试）。
func TestContractSmoke(t *testing.T) {
	if SideT == SideCT || SideT == SideDraw || SideCT == SideDraw {
		t.Error("Side 常量必须互异")
	}
	m := Match{
		Meta:    MatchMeta{Map: "de_mirage", TickRate: 64, FocusPlayer: "1"},
		Players: []PlayerInfo{{SteamID64: 1, Name: "焦点"}},
		Rounds: []Round{{
			Number: 1, Winner: SideT, Half: "H1",
			Economy: EconomySnapshot{},
		}},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var back Match
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Meta.Map != "de_mirage" || len(back.Rounds) != 1 || back.Rounds[0].Winner != SideT {
		t.Error("Match JSON 往返不一致")
	}

	a := Analysis{
		Map:   "de_mirage",
		Focus: FocusSummary{SteamID64: "1", Name: "焦点", SideStats: map[string]PlayerMatchStats{}},
		Story: StorySummary{WonByBuy: map[string]int{}, LostByBuy: map[string]int{}},
	}
	raw2, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var back2 Analysis
	if err := json.Unmarshal(raw2, &back2); err != nil {
		t.Fatal(err)
	}
	if back2.Focus.Name != "焦点" {
		t.Error("Analysis JSON 往返不一致")
	}
}
