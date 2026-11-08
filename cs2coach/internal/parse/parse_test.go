package parse

import (
	"strings"
	"testing"

	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/common"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/events"

	"cs2-demo-review/internal/model"
)

func TestRoundEndReasonString(t *testing.T) {
	tests := []struct {
		name string
		in   events.RoundEndReason
		want string
	}{
		{"炸包爆炸", events.RoundEndReasonTargetBombed, "bomb_exploded"},
		{"拆包成功", events.RoundEndReasonBombDefused, "bomb_defused"},
		{"CT 消灭对方", events.RoundEndReasonCTWin, "elimination"},
		{"T 消灭对方", events.RoundEndReasonTerroristsWin, "elimination"},
		{"守住时间", events.RoundEndReasonTargetSaved, "time"},
		{"平局", events.RoundEndReasonDraw, "draw"},
		{"T 投降", events.RoundEndReasonTerroristsSurrender, "surrender"},
		{"CT 投降", events.RoundEndReasonCTSurrender, "surrender"},
		{"其余-游戏开始", events.RoundEndReasonGameStart, "other"},
		{"其余-人质", events.RoundEndReasonHostagesRescued, "other"},
		{"其余-进行中", events.RoundEndReasonStillInProgress, "other"},
		{"其余-T已下包", events.RoundEndReasonTerroristsPlanted, "other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := roundEndReasonString(tt.in); got != tt.want {
				t.Errorf("roundEndReasonString(%d) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSideFromTeamAndWinnerSide(t *testing.T) {
	sideTests := []struct {
		in   common.Team
		want model.Side
	}{
		{common.TeamTerrorists, model.SideT},
		{common.TeamCounterTerrorists, model.SideCT},
		{common.TeamSpectators, model.SideNone},
		{common.TeamUnassigned, model.SideNone},
	}
	for _, tt := range sideTests {
		if got := sideFromTeam(tt.in); got != tt.want {
			t.Errorf("sideFromTeam(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}

	winnerTests := []struct {
		in   common.Team
		want model.Side
	}{
		{common.TeamTerrorists, model.SideT},
		{common.TeamCounterTerrorists, model.SideCT},
		{common.TeamSpectators, model.SideDraw},
		{common.TeamUnassigned, model.SideDraw},
	}
	for _, tt := range winnerTests {
		if got := winnerSide(tt.in); got != tt.want {
			t.Errorf("winnerSide(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPlayerSide(t *testing.T) {
	tests := []struct {
		name string
		in   *common.Player
		want model.Side
	}{
		{"nil 玩家", nil, model.SideNone},
		{"T 方玩家", &common.Player{Team: common.TeamTerrorists}, model.SideT},
		{"CT 方玩家", &common.Player{Team: common.TeamCounterTerrorists}, model.SideCT},
		{
			"Team 未分配时退回 TeamState",
			func() *common.Player {
				ts := common.NewTeamState(common.TeamCounterTerrorists, nil, nil)
				return &common.Player{Team: common.TeamUnassigned, TeamState: &ts}
			}(),
			model.SideCT,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := playerSide(tt.in); got != tt.want {
				t.Errorf("playerSide() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGrenadeTypeString(t *testing.T) {
	tests := []struct {
		in   common.EquipmentType
		want string
	}{
		{common.EqFlash, "flash"},
		{common.EqSmoke, "smoke"},
		{common.EqHE, "he"},
		{common.EqMolotov, "molotov"},
		{common.EqIncendiary, "incendiary"},
		{common.EqDecoy, "decoy"},
		{common.EqAK47, "other"},
		{common.EqUnknown, "other"},
	}
	for _, tt := range tests {
		if got := grenadeTypeString(tt.in); got != tt.want {
			t.Errorf("grenadeTypeString(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCountGrenades(t *testing.T) {
	tests := []struct {
		name string
		in   []*common.Equipment
		want int
	}{
		{"空列表", nil, 0},
		{
			"纯投掷物",
			[]*common.Equipment{
				{Type: common.EqFlash}, {Type: common.EqFlash}, {Type: common.EqSmoke},
				{Type: common.EqHE}, {Type: common.EqMolotov}, {Type: common.EqIncendiary}, {Type: common.EqDecoy},
			},
			7,
		},
		{
			"枪械不计",
			[]*common.Equipment{
				{Type: common.EqAK47}, {Type: common.EqDeagle}, {Type: common.EqKevlar},
				{Type: common.EqKnife}, {Type: common.EqBomb}, {Type: common.EqUnknown},
			},
			0,
		},
		{
			"混装",
			[]*common.Equipment{
				{Type: common.EqAK47}, {Type: common.EqSmoke}, nil, {Type: common.EqHE},
			},
			2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countGrenades(tt.in); got != tt.want {
				t.Errorf("countGrenades() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestWeaponNames(t *testing.T) {
	got := weaponNames([]*common.Equipment{
		{Type: common.EqAK47},
		{Type: common.EqFlash},
		nil,
		{Type: common.EqSmoke},
		{Type: common.EqFlash},
	})
	want := []string{"AK-47", "Flashbang", "Flashbang", "Smoke Grenade"}
	if len(got) != len(want) {
		t.Fatalf("weaponNames() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("weaponNames()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestWeaponClassName(t *testing.T) {
	tests := []struct {
		in   common.EquipmentClass
		want string
	}{
		{common.EqClassPistols, "pistol"},
		{common.EqClassSMG, "smg"},
		{common.EqClassHeavy, "heavy"},
		{common.EqClassRifle, "rifle"},
		{common.EqClassEquipment, "equipment"},
		{common.EqClassGrenade, "grenade"},
		{common.EqClassUnknown, "unknown"},
	}
	for _, tt := range tests {
		if got := weaponClassName(tt.in); got != tt.want {
			t.Errorf("weaponClassName(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBombsiteString(t *testing.T) {
	tests := []struct {
		in   events.Bombsite
		want string
	}{
		{events.BombsiteA, "A"},
		{events.BombsiteB, "B"},
		{events.BomsiteUnknown, ""},
	}
	for _, tt := range tests {
		if got := bombsiteString(tt.in); got != tt.want {
			t.Errorf("bombsiteString(%q) = %q, want %q", string(tt.in), got, tt.want)
		}
	}
}

func TestMapNameFromPath(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"/tmp/de_mirage-2026.dem", "de_mirage-2026"},
		{"match.dem", "match"},
		{`C:\demos\game1.DEM`, "game1"},
		{"noext", "noext"},
	}
	for _, tt := range tests {
		if got := mapNameFromPath(tt.in); got != tt.want {
			t.Errorf("mapNameFromPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResolveFocus(t *testing.T) {
	players := []playerRef{
		{steamID: 1001, name: "Alice"},
		{steamID: 1002, name: "bob"},
		{steamID: 1003, name: "Bob"},
		{steamID: 1004, name: "Carol"},
	}
	pov := &playerRef{steamID: 1004, name: "Carol"}

	tests := []struct {
		name    string
		opts    Options
		pov     *playerRef
		wantID  uint64
		wantErr string // 空 = 期望成功；非空 = 错误信息需包含该子串
	}{
		{
			name:   "SteamID 优先于名字",
			opts:   Options{FocusSteamID: "1001", FocusName: "Carol"},
			wantID: 1001,
		},
		{
			name:   "名字忽略大小写精确匹配",
			opts:   Options{FocusName: "aLiCe"},
			wantID: 1001,
		},
		{
			name:    "SteamID 非法",
			opts:    Options{FocusSteamID: "abc"},
			wantErr: "十进制",
		},
		{
			name:    "SteamID 不存在",
			opts:    Options{FocusSteamID: "9999"},
			wantErr: "可用玩家名单",
		},
		{
			name:    "同名歧义报错",
			opts:    Options{FocusName: "bob"},
			wantErr: "同名",
		},
		{
			name:    "名字不存在",
			opts:    Options{FocusName: "nobody"},
			wantErr: "未找到名为",
		},
		{
			name:   "无指定时用 POV 玩家",
			opts:   Options{},
			pov:    pov,
			wantID: 1004,
		},
		{
			name:    "无指定且无 POV 报错列名单",
			opts:    Options{},
			pov:     nil,
			wantErr: "Alice (1001)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveFocus(tt.opts, players, tt.pov)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("resolveFocus() = (%+v, nil), 期望错误包含 %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("错误信息 %q 不包含 %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveFocus() 报错: %v", err)
			}
			if got.steamID != tt.wantID {
				t.Errorf("resolveFocus() = %d, want %d", got.steamID, tt.wantID)
			}
		})
	}
}

func TestAggregateSideEconomy(t *testing.T) {
	tests := []struct {
		name        string
		players     []model.PlayerEconomy
		equipStart  int
		equipFreeze int
		moneySpent  int
		wantMoney   int
	}{
		{
			name:      "空名单",
			players:   nil,
			wantMoney: 0,
		},
		{
			name: "现金合计为回合开始全员现金之和",
			players: []model.PlayerEconomy{
				{Money: 800}, {Money: 3200}, {Money: 1500},
			},
			equipStart:  1000,
			equipFreeze: 5200,
			moneySpent:  2700,
			wantMoney:   5500,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aggregateSideEconomy(tt.players, tt.equipStart, tt.equipFreeze, tt.moneySpent)
			if got.Money != tt.wantMoney {
				t.Errorf("Money = %d, want %d", got.Money, tt.wantMoney)
			}
			if got.EquipValueRoundStart != tt.equipStart {
				t.Errorf("EquipValueRoundStart = %d, want %d", got.EquipValueRoundStart, tt.equipStart)
			}
			if got.EquipValueFreezeEnd != tt.equipFreeze {
				t.Errorf("EquipValueFreezeEnd = %d, want %d", got.EquipValueFreezeEnd, tt.equipFreeze)
			}
			if got.MoneySpent != tt.moneySpent {
				t.Errorf("MoneySpent = %d, want %d", got.MoneySpent, tt.moneySpent)
			}
			if len(got.Players) != len(tt.players) {
				t.Errorf("Players 长度 = %d, want %d", len(got.Players), len(tt.players))
			}
		})
	}
}

func TestHalfTracker(t *testing.T) {
	// 回合 1-12=H1、13-24=H2、25-27=OT1、28-30=OT2（每加时半场 3 回合）。
	want := map[int]string{
		1: "H1", 12: "H1",
		13: "H2", 24: "H2",
		25: "OT1", 27: "OT1",
		28: "OT2", 30: "OT2",
		31: "OT3", 33: "OT3",
		34: "OT4",
	}
	var h halfTracker
	for r := 1; r <= 34; r++ {
		got := h.beginRound()
		if w, ok := want[r]; ok && got != w {
			t.Errorf("回合 %d 半场 = %s, want %s", r, got, w)
		}
	}
}

func TestHalfTrackerSideSwitchEvent(t *testing.T) {
	var h halfTracker
	// 标准赛制：换边事件在第 13 回合 RoundStart 后到达，不应重复推进。
	for r := 1; r <= 13; r++ {
		got := h.beginRound()
		if r == 13 && got != "H2" {
			t.Fatalf("回合 13 半场 = %s, want H2", got)
		}
	}
	if got := h.noteSideSwitch(); got != "H2" {
		t.Errorf("换边事件后半场 = %s, want H2（不应二次推进）", got)
	}

	// 非标准 demo：第 5 回合就换边 → 事件兜底推进到 H2 并改写标签。
	var h2 halfTracker
	for r := 1; r <= 5; r++ {
		h2.beginRound()
	}
	if got := h2.noteSideSwitch(); got != "H2" {
		t.Errorf("提前换边事件后半场 = %s, want H2", got)
	}
}

func TestFinalizeRoundScore(t *testing.T) {
	acc := newAccumulator("x.dem", Options{})
	rb := &roundBuilder{
		players:      map[*common.Player]*model.RoundPlayer{},
		moneyAtStart: map[*common.Player]int{},
	}
	rb.round = model.Round{Number: 1, Winner: model.SideT}
	acc.cur = rb
	acc.finalizeRound()
	if acc.tScore != 1 || acc.ctScore != 0 {
		t.Fatalf("比分累计 = %d:%d, want 1:0", acc.tScore, acc.ctScore)
	}
	if acc.rounds[0].ScoreAfter != (model.Score{T: 1, CT: 0}) {
		t.Errorf("ScoreAfter = %+v, want {1 0}", acc.rounds[0].ScoreAfter)
	}

	rb2 := &roundBuilder{
		players:      map[*common.Player]*model.RoundPlayer{},
		moneyAtStart: map[*common.Player]int{},
	}
	rb2.round = model.Round{Number: 2, Winner: model.SideCT}
	acc.cur = rb2
	acc.finalizeRound()
	if acc.rounds[1].ScoreAfter != (model.Score{T: 1, CT: 1}) {
		t.Errorf("ScoreAfter = %+v, want {1 1}", acc.rounds[1].ScoreAfter)
	}

	// 平局不加分。
	rb3 := &roundBuilder{
		players:      map[*common.Player]*model.RoundPlayer{},
		moneyAtStart: map[*common.Player]int{},
	}
	rb3.round = model.Round{Number: 3, Winner: model.SideDraw}
	acc.cur = rb3
	acc.finalizeRound()
	if acc.rounds[2].ScoreAfter != (model.Score{T: 1, CT: 1}) {
		t.Errorf("ScoreAfter = %+v, want {1 1}", acc.rounds[2].ScoreAfter)
	}
}
