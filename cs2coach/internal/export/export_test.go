package export

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"cs2-demo-review/internal/model"
	"cs2-demo-review/internal/tactics"
)

const testFocusID = "76561198000000001"

const (
	focusU64 uint64 = 76561198000000001
	mateU64  uint64 = 76561198000000002
	mateBU64 uint64 = 76561198000000003
	e1U64    uint64 = 76561198000000009
	e2U64    uint64 = 76561198000000010
	e3U64    uint64 = 76561198000000011
)

// econSide 构造单边经济快照（5 人，便于 tactics 按人均算买型）。
func econSide(money, equipTotal, spent int) model.SideEconomy {
	players := make([]model.PlayerEconomy, 5)
	for i := range players {
		players[i] = model.PlayerEconomy{
			SteamID64:  uint64(1000 + i),
			EquipValue: equipTotal / 5,
		}
	}
	return model.SideEconomy{
		Money:               money,
		EquipValueFreezeEnd: equipTotal,
		MoneySpent:          spent,
		Players:             players,
	}
}

// testMatch 手工构造 3 回合 Match（不依赖真实 demo）：
//   - R1：T 方焦点，满枪 vs 强起，首杀被补，下包爆炸；
//   - R2：T 方焦点 eco 撞 full，焦点首死未补（丢分归因标签齐全）；
//   - R3：换边后焦点在 CT，半起 vs eco，下包被拆，CT 人数优势 + 1v1 残局。
func testMatch() *model.Match {
	player := func(id uint64, name string, side model.Side) model.RoundPlayer {
		return model.RoundPlayer{SteamID64: id, Name: name, Side: side, AliveAtEnd: true}
	}
	kill := func(tick int, killerID uint64, killerName string, killerSide model.Side,
		victimID uint64, victimName string, victimSide model.Side, weapon string) model.KillEvent {
		return model.KillEvent{
			Tick: tick, RoundTime: float64(tick) / 64,
			KillerID: testID(killerID), KillerName: killerName, KillerSide: killerSide,
			VictimID: testID(victimID), VictimName: victimName, VictimSide: victimSide,
			Weapon: weapon, WeaponClass: "Rifle",
		}
	}
	util := func(tick int, typ string, side model.Side, heDamage int) model.UtilityEvent {
		return model.UtilityEvent{Tick: tick, RoundTime: float64(tick) / 64, Type: typ, ThrowerSide: side, HEDamage: heDamage}
	}

	// R1：tick 10000~10800，T 胜（炸弹爆炸）。
	r1 := model.Round{
		Number: 1, StartTick: 10000, EndTick: 10800,
		Winner: model.SideT, EndReason: "bomb_exploded", ScoreAfter: model.Score{T: 1, CT: 0}, Half: "H1",
		Economy: model.EconomySnapshot{
			T:  econSide(12000, 24000, 4500), // 人均 4800 → full
			CT: econSide(9000, 15000, 3000),  // 人均 3000 且有花费 → force
		},
		Players: []model.RoundPlayer{
			player(focusU64, "焦点", model.SideT),
			player(mateU64, "队友A", model.SideT),
			player(e1U64, "敌1", model.SideCT),
			player(e2U64, "敌2", model.SideCT),
		},
		Kills: []model.KillEvent{
			kill(10060, focusU64, "焦点", model.SideT, e1U64, "敌1", model.SideCT, "AK-47"), // 首杀
			kill(10080, e2U64, "敌2", model.SideCT, focusU64, "焦点", model.SideT, "M4A1"),  // 20 tick 补掉首杀凶手
		},
		Utility: []model.UtilityEvent{
			util(10010, "flash", model.SideT, 0),
			util(10020, "smoke", model.SideT, 0),
			util(10030, "he", model.SideT, 27),
			util(10040, "flash", model.SideCT, 0),
			util(10050, "molotov", model.SideCT, 0),
			util(10100, "incendiary", model.SideCT, 0), // 交火后，计入 molotov_count
			util(10120, "he", model.SideCT, 13),
			util(10140, "decoy", model.SideCT, 0), // decoy 不计入任何计数
		},
		Bomb: []model.BombEvent{
			{Tick: 10300, RoundTime: 4.6875, Type: "planted", Site: "A"},
			{Tick: 10700, RoundTime: 10.9375, Type: "exploded"},
		},
		Alive: []model.AliveCount{
			{Tick: 10000, TAlive: 5, CTAlive: 5},
			{Tick: 10060, TAlive: 5, CTAlive: 4},
			{Tick: 10080, TAlive: 4, CTAlive: 4},
		},
	}

	// R2：tick 20000~20900，CT 胜（团灭）。
	r2Focus := player(focusU64, "焦点", model.SideT)
	r2Focus.AliveAtEnd, r2Focus.Deaths, r2Focus.FirstDeath, r2Focus.DeathTick = false, 1, true, 20100
	r2 := model.Round{
		Number: 2, StartTick: 20000, EndTick: 20900,
		Winner: model.SideCT, EndReason: "elimination", ScoreAfter: model.Score{T: 1, CT: 1}, Half: "H1",
		Economy: model.EconomySnapshot{
			T:  econSide(2000, 5000, 0),      // 人均 1000 → eco
			CT: econSide(10000, 25000, 4500), // 人均 5000 → full
		},
		Players: []model.RoundPlayer{
			r2Focus,
			player(mateU64, "队友A", model.SideT),
			player(e1U64, "敌1", model.SideCT),
			player(e2U64, "敌2", model.SideCT),
		},
		Kills: []model.KillEvent{
			kill(20100, e1U64, "敌1", model.SideCT, focusU64, "焦点", model.SideT, "M4A1"), // 焦点首死，无人补
			kill(20800, e1U64, "敌1", model.SideCT, mateU64, "队友A", model.SideT, "M4A1"),
		},
		Utility: []model.UtilityEvent{
			util(20010, "flash", model.SideT, 0),
			util(20020, "flash", model.SideCT, 0),
			util(20030, "he", model.SideCT, 40),
			util(20040, "smoke", model.SideCT, 0),
		},
		Alive: []model.AliveCount{
			{Tick: 20000, TAlive: 5, CTAlive: 5},
			{Tick: 20100, TAlive: 4, CTAlive: 5},
			{Tick: 20800, TAlive: 3, CTAlive: 5},
		},
	}

	// R3：tick 30000~31200，换边后焦点在 CT，CT 胜（拆包）。
	r3 := model.Round{
		Number: 3, StartTick: 30000, EndTick: 31200,
		Winner: model.SideCT, EndReason: "bomb_defused", ScoreAfter: model.Score{T: 1, CT: 2}, Half: "H2",
		Economy: model.EconomySnapshot{
			T:  econSide(8000, 10000, 0),     // 人均 2000 → eco
			CT: econSide(12000, 22500, 4000), // 人均 4500 且有花费 → half
		},
		Players: []model.RoundPlayer{
			player(focusU64, "焦点", model.SideCT),
			player(mateU64, "队友A", model.SideCT),
			player(mateBU64, "队友B", model.SideCT),
			player(e1U64, "敌1", model.SideT),
			player(e2U64, "敌2", model.SideT),
			player(e3U64, "敌3", model.SideT),
		},
		Kills: []model.KillEvent{
			kill(30100, e1U64, "敌1", model.SideT, mateU64, "队友A", model.SideCT, "AK-47"), // 首杀（T）
			kill(30200, focusU64, "焦点", model.SideCT, e1U64, "敌1", model.SideT, "M4A1"),  // 100 tick 补掉首杀凶手
			kill(30400, focusU64, "焦点", model.SideCT, e2U64, "敌2", model.SideT, "M4A1"),
		},
		Utility: []model.UtilityEvent{
			util(30010, "smoke", model.SideT, 0),
			util(30020, "flash", model.SideT, 0),
			util(30030, "flash", model.SideCT, 0),
			util(30040, "molotov", model.SideCT, 0),
		},
		Bomb: []model.BombEvent{
			{Tick: 30400, RoundTime: 6.25, Type: "planted", Site: "B"},
			{Tick: 30800, RoundTime: 12.5, Type: "defused", Site: "B"},
		},
		Alive: []model.AliveCount{
			{Tick: 30000, TAlive: 5, CTAlive: 5},
			{Tick: 30100, TAlive: 3, CTAlive: 5}, // CT（焦点方）+2 开始
			{Tick: 30720, TAlive: 3, CTAlive: 5}, // +2 持续 800 tick = 12.5s
			{Tick: 30900, TAlive: 1, CTAlive: 1}, // 1v1 残局
		},
	}

	return &model.Match{
		Meta: model.MatchMeta{
			SourceFile: "test_demo.dem", Map: "de_mirage", TickRate: 64,
			TotalRounds: 3, FocusPlayer: testFocusID, FocusPlayerName: "焦点",
		},
		Players: []model.PlayerInfo{{SteamID64: focusU64, Name: "焦点"}},
		Rounds:  []model.Round{r1, r2, r3},
	}
}

// testID 把 SteamID64 转成 model 里使用的十进制字符串。
func testID(id uint64) string {
	return strconv.FormatUint(id, 10)
}

func TestSamplesFromMatch(t *testing.T) {
	m := testMatch()
	a, err := tactics.Analyze(m, tactics.Options{})
	if err != nil {
		t.Fatalf("tactics.Analyze: %v", err)
	}
	got, err := SamplesFromMatch(m, a)
	if err != nil {
		t.Fatalf("SamplesFromMatch: %v", err)
	}
	if len(got) != len(m.Rounds) {
		t.Fatalf("样本数 = %d，want %d", len(got), len(m.Rounds))
	}

	want := []Sample{
		{
			SourceFile: "test_demo.dem", Map: "de_mirage", TickRate: 64, FocusPlayer: testFocusID,
			RoundNumber: 1, Half: "H1", ScoreBefore: model.Score{T: 0, CT: 0},

			TMoney: 12000, CTMoney: 9000,
			TEquipFreezeEnd: 24000, CTEquipFreezeEnd: 15000,
			TMoneySpent: 4500, CTMoneySpent: 3000,
			FocusBuy: "full", EnemyBuy: "force",

			HasFirstKill: true, FirstKillerSide: "T", FirstVictimSide: "CT",
			FirstKillRoundTime: 0.9375, TradedWithinWindow: true,

			FocusUtilBeforeContact: 3, EnemyUtilBeforeContact: 2,
			FlashCount: 2, SmokeCount: 1, HECount: 2, MolotovCount: 2, HEDamageTotal: 40,

			StartTAlive: 5, StartCTAlive: 5, ClutchEntered: false, ClutchSituation: "",
			ManAdvantageSpans: []ManAdvantageSpan{},

			Planted: true, PlantSite: "A", Defused: false, Exploded: true, PlantRoundTime: 4.6875,

			WinnerSide: "T", FocusWon: true, EndReason: "bomb_exploded", WinCost: 0, CauseTags: []string{},
		},
		{
			SourceFile: "test_demo.dem", Map: "de_mirage", TickRate: 64, FocusPlayer: testFocusID,
			RoundNumber: 2, Half: "H1", ScoreBefore: model.Score{T: 1, CT: 0},

			TMoney: 2000, CTMoney: 10000,
			TEquipFreezeEnd: 5000, CTEquipFreezeEnd: 25000,
			TMoneySpent: 0, CTMoneySpent: 4500,
			FocusBuy: "eco", EnemyBuy: "full",

			HasFirstKill: true, FirstKillerSide: "CT", FirstVictimSide: "T",
			FirstKillRoundTime: 1.5625, TradedWithinWindow: false,

			FocusUtilBeforeContact: 1, EnemyUtilBeforeContact: 3,
			FlashCount: 2, SmokeCount: 1, HECount: 1, MolotovCount: 0, HEDamageTotal: 40,

			StartTAlive: 5, StartCTAlive: 5, ClutchEntered: false, ClutchSituation: "",
			ManAdvantageSpans: []ManAdvantageSpan{},

			Planted: false, PlantSite: "", Defused: false, Exploded: false, PlantRoundTime: 0,

			WinnerSide: "CT", FocusWon: false, EndReason: "elimination",
		},
		{
			SourceFile: "test_demo.dem", Map: "de_mirage", TickRate: 64, FocusPlayer: testFocusID,
			RoundNumber: 3, Half: "H2", ScoreBefore: model.Score{T: 1, CT: 1},

			TMoney: 8000, CTMoney: 12000,
			TEquipFreezeEnd: 10000, CTEquipFreezeEnd: 22500,
			TMoneySpent: 0, CTMoneySpent: 4000,
			FocusBuy: "half", EnemyBuy: "eco",

			HasFirstKill: true, FirstKillerSide: "T", FirstVictimSide: "CT",
			FirstKillRoundTime: 1.5625, TradedWithinWindow: true,

			FocusUtilBeforeContact: 2, EnemyUtilBeforeContact: 2,
			FlashCount: 2, SmokeCount: 1, HECount: 0, MolotovCount: 1, HEDamageTotal: 0,

			StartTAlive: 5, StartCTAlive: 5, ClutchEntered: true, ClutchSituation: "1v1",
			ManAdvantageSpans: []ManAdvantageSpan{
				{StartTick: 30100, EndTick: 30900, AdvSide: "CT", AdvDelta: 2, Threw: false},
			},

			Planted: true, PlantSite: "B", Defused: true, Exploded: false, PlantRoundTime: 6.25,

			WinnerSide: "CT", FocusWon: true, EndReason: "bomb_defused", WinCost: 0, CauseTags: []string{},
		},
	}
	// 归因标签/WinCost 是 tactics 层产物：这里只验证原样透传，不在 export 测试里重复规则阈值。
	for i := range want {
		want[i].WinCost = a.Rounds[i].WinCost
		want[i].CauseTags = append([]string{}, a.Rounds[i].Causes...)
	}

	cases := []struct {
		name string
		idx  int
	}{
		{"回合1 下包爆炸/首杀被补/满枪对强起", 0},
		{"回合2 eco 撞 full/焦点首死未补/丢分标签", 1},
		{"回合3 换边后 CT/拆包/人数优势与 1v1 残局", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !reflect.DeepEqual(got[tc.idx], want[tc.idx]) {
				t.Errorf("样本不一致:\n got = %+v\nwant = %+v", got[tc.idx], want[tc.idx])
			}
		})
	}

	// 丢分回合必须有归因标签和正 WinCost，且与 Analysis 一致。
	if len(got[1].CauseTags) == 0 {
		t.Error("回合2 焦点方丢分，cause_tags 不应为空")
	}
	if got[1].WinCost <= 0 {
		t.Errorf("回合2 win_cost = %v，want > 0", got[1].WinCost)
	}
	if got[1].WinCost != a.Rounds[1].WinCost {
		t.Errorf("win_cost = %v，want %v（Analysis 原值）", got[1].WinCost, a.Rounds[1].WinCost)
	}
	hasFirstBlood := false
	for _, tag := range got[1].CauseTags {
		if tag == model.CauseFirstBloodGift {
			hasFirstBlood = true
		}
	}
	if !hasFirstBlood {
		t.Errorf("回合2 cause_tags = %v，want 含 %s", got[1].CauseTags, model.CauseFirstBloodGift)
	}
}

func TestSamplesFromMatchErrors(t *testing.T) {
	m := testMatch()
	a, err := tactics.Analyze(m, tactics.Options{})
	if err != nil {
		t.Fatalf("tactics.Analyze: %v", err)
	}
	noFocus := *m
	noFocus.Meta.FocusPlayer = ""

	cases := []struct {
		name     string
		match    *model.Match
		analysis *model.Analysis
		wantSub  string
	}{
		{"Match 为空", nil, a, "Match 为空"},
		{"Analysis 为空", m, nil, "Analysis 为空"},
		{"未确定焦点玩家", &noFocus, a, "未确定焦点玩家"},
		{"Analysis 缺回合", m, &model.Analysis{Rounds: a.Rounds[:1]}, "缺少 Analysis 数据"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SamplesFromMatch(tc.match, tc.analysis)
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("err = %q，want 含 %q", err, tc.wantSub)
			}
		})
	}
}

func TestStartAlive(t *testing.T) {
	cases := []struct {
		name   string
		round  *model.Round
		wantT  int
		wantCT int
	}{
		{
			"Alive 首条优先",
			&model.Round{
				Alive:   []model.AliveCount{{Tick: 100, TAlive: 5, CTAlive: 4}},
				Players: []model.RoundPlayer{{Side: model.SideT}},
			},
			5, 4,
		},
		{
			"无 Alive 时按 RoundPlayer 阵营计数",
			&model.Round{Players: []model.RoundPlayer{
				{SteamID64: 1, Side: model.SideT}, {SteamID64: 2, Side: model.SideT},
				{SteamID64: 3, Side: model.SideCT}, {SteamID64: 4, Side: model.SideCT},
				{SteamID64: 5, Side: model.SideCT},
			}},
			2, 3,
		},
		{"都为空返回 0", &model.Round{}, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotT, gotCT := startAlive(tc.round)
			if gotT != tc.wantT || gotCT != tc.wantCT {
				t.Errorf("startAlive = (%d, %d)，want (%d, %d)", gotT, gotCT, tc.wantT, tc.wantCT)
			}
		})
	}
}

// TestSampleJSONShape 锁定 JSONL 契约：字段齐全、数组字段不为 null、score_before 为 {t,ct}。
func TestSampleJSONShape(t *testing.T) {
	m := testMatch()
	a, err := tactics.Analyze(m, tactics.Options{})
	if err != nil {
		t.Fatalf("tactics.Analyze: %v", err)
	}
	samples, err := SamplesFromMatch(m, a)
	if err != nil {
		t.Fatalf("SamplesFromMatch: %v", err)
	}
	line, err := json.Marshal(samples[0])
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(line, &obj); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	wantKeys := []string{
		"source_file", "map", "tick_rate", "focus_player",
		"round_number", "half", "score_before",
		"t_money", "ct_money", "t_equip_freeze_end", "ct_equip_freeze_end",
		"t_money_spent", "ct_money_spent", "focus_buy", "enemy_buy",
		"has_first_kill", "first_killer_side", "first_victim_side",
		"first_kill_round_time", "traded_within_window",
		"focus_util_before_contact", "enemy_util_before_contact",
		"flash_count", "smoke_count", "he_count", "molotov_count", "he_damage_total",
		"start_t_alive", "start_ct_alive", "clutch_entered", "clutch_situation", "man_advantage_spans",
		"planted", "plant_site", "defused", "exploded", "plant_round_time",
		"winner_side", "focus_won", "end_reason", "win_cost", "cause_tags",
	}
	for _, k := range wantKeys {
		if _, ok := obj[k]; !ok {
			t.Errorf("缺少字段 %q", k)
		}
	}
	if len(obj) != len(wantKeys) {
		t.Errorf("字段数 = %d，want %d（多出未约定字段）", len(obj), len(wantKeys))
	}
	for _, k := range []string{"man_advantage_spans", "cause_tags"} {
		if string(obj[k]) == "null" {
			t.Errorf("%q 不应为 null（应为空数组）", k)
		}
	}
	var score map[string]int
	if err := json.Unmarshal(obj["score_before"], &score); err != nil {
		t.Fatalf("score_before 不是 {t,ct} 对象: %v", err)
	}
	if _, ok := score["t"]; !ok {
		t.Error("score_before 缺少 t")
	}
	if _, ok := score["ct"]; !ok {
		t.Error("score_before 缺少 ct")
	}
}

func TestCollectDemos(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	write("a.dem")
	write("b.DEM")
	write("c.txt")
	write("sub/d.dem")
	write("sub/e.txt")
	write("dir.dem/inside.dem") // 目录名带 .dem，不应被当成文件

	cases := []struct {
		name      string
		path      string
		recursive bool
		want      []string
	}{
		{
			"单文件原样返回（不校验扩展名）",
			filepath.Join(dir, "c.txt"), false,
			[]string{filepath.Join(dir, "c.txt")},
		},
		{
			"目录只收顶层 .dem（大小写不敏感）",
			dir, false,
			[]string{filepath.Join(dir, "a.dem"), filepath.Join(dir, "b.DEM")},
		},
		{
			"递归收子目录 .dem",
			dir, true,
			[]string{
				filepath.Join(dir, "a.dem"),
				filepath.Join(dir, "b.DEM"),
				filepath.Join(dir, "dir.dem", "inside.dem"),
				filepath.Join(dir, "sub", "d.dem"),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CollectDemos(tc.path, tc.recursive)
			if err != nil {
				t.Fatalf("CollectDemos: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("CollectDemos = %v，want %v", got, tc.want)
			}
		})
	}

	if _, err := CollectDemos(filepath.Join(dir, "missing"), false); err == nil {
		t.Error("不存在路径应返回 error")
	}
	if _, err := CollectDemos(t.TempDir(), true); err != nil {
		t.Errorf("空目录不应 error: %v", err)
	}
}

// TestRunSkipsFailedDemos 验证批量导出：坏文件跳过、好文件照常写入、汇总计数正确。
func TestRunSkipsFailedDemos(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		return p
	}
	write("bad.dem")
	good := write("good.dem")
	out := filepath.Join(dir, "out", "samples.jsonl") // 输出目录不存在，Run 负责创建

	buildErr := errors.New("模拟解析失败")
	build := func(demo string, _ Options) ([]Sample, error) {
		if demo != good {
			return nil, buildErr
		}
		return []Sample{
			{RoundNumber: 1, CauseTags: []string{}, ManAdvantageSpans: []ManAdvantageSpan{}},
			{RoundNumber: 2, CauseTags: []string{}, ManAdvantageSpans: []ManAdvantageSpan{}},
		}, nil
	}

	res, err := run(dir, out, Options{}, build)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.TotalFiles != 2 || res.Succeeded != 1 || res.Samples != 2 {
		t.Errorf("汇总 = total %d / ok %d / samples %d，want 2 / 1 / 2",
			res.TotalFiles, res.Succeeded, res.Samples)
	}
	if len(res.Failed) != 1 {
		t.Fatalf("Failed 数 = %d，want 1", len(res.Failed))
	}
	if res.Failed[0].File != filepath.Join(dir, "bad.dem") {
		t.Errorf("失败文件 = %s，want bad.dem", res.Failed[0].File)
	}
	if !errors.Is(res.Failed[0].Err, buildErr) {
		t.Errorf("失败原因 = %v，want %v", res.Failed[0].Err, buildErr)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("读取输出: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("输出行数 = %d，want 2", len(lines))
	}
	for i, line := range lines {
		var s Sample
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			t.Fatalf("第 %d 行不是合法 JSON: %v", i+1, err)
		}
		if s.RoundNumber != i+1 {
			t.Errorf("第 %d 行 round_number = %d，want %d", i+1, s.RoundNumber, i+1)
		}
	}
}
