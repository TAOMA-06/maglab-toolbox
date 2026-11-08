package tactics

import (
	"math"
	"strings"
	"testing"

	"cs2-demo-review/internal/model"
)

// ---------- 测试辅助（手工构造 model，不依赖真实 demo） ----------

func m2Ctx(r *model.Round) *roundCtx {
	return &roundCtx{round: r, focusID: testFocus, window: DefaultTradeWindowSec, rate: 64, anchors: map[string][]model.Vec3{}}
}

func m2Snap(id, name string, side model.Side, x, y, yaw float64) model.PlayerSnapshot {
	return model.PlayerSnapshot{SteamID64: id, Name: name, Side: side, Pos: model.Vec3{X: x, Y: y}, Yaw: yaw}
}

// m2DeathRound 造一个「焦点被敌1 击杀」的回合（可选择携带 OtherAlive/Utility）。
func m2DeathRound(n, deathTick int, pos model.Vec3, victimYaw float64, others []model.PlayerSnapshot, util []model.UtilityEvent) *model.Round {
	return &model.Round{
		Number:    n,
		StartTick: n * 10000,
		EndTick:   n*10000 + 6000,
		Winner:    model.SideCT,
		EndReason: "elimination",
		Half:      "H1",
		Kills: []model.KillEvent{{
			Tick: deathTick, KillerID: sid(e1U64), KillerName: "敌1", KillerSide: model.SideCT,
			VictimID: testFocus, VictimName: "焦点", VictimSide: model.SideT,
			Weapon: "AK-47", VictimPos: pos, VictimYaw: victimYaw, OtherAlive: others,
		}},
		Utility: util,
	}
}

func m2Positioning(t *testing.T, r *model.Round) *model.PositionDeathAnalysis {
	t.Helper()
	pa := buildPositioning(m2Ctx(r), sortedKills(r.Kills))
	if pa == nil {
		t.Fatalf("回合 %d 焦点死亡应有站位分析", r.Number)
	}
	return pa
}

// ---------- 角度 / 距离 / 分位 ----------

func TestAngleHelpers(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{-90, 270}, {0, 0}, {360, 0}, {720, 0}, {359.9, 359.9}, {-0.5, 359.5},
	}
	for _, c := range cases {
		if got := normalizeYaw(c.in); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("normalizeYaw(%v) = %v want %v", c.in, got, c.want)
		}
	}
	delta := []struct{ a, b, want float64 }{
		{350, 10, 20}, {10, 350, 20}, {0, 180, 180}, {0, 181, 179}, {90, 90, 0}, {0, 359, 1},
	}
	for _, c := range delta {
		if got := yawDelta(c.a, c.b); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("yawDelta(%v,%v) = %v want %v", c.a, c.b, got, c.want)
		}
	}
	bearings := []struct {
		to   model.Vec3
		want float64
	}{
		{model.Vec3{X: 1}, 0},    // 正东
		{model.Vec3{Y: 1}, 90},   // 正北
		{model.Vec3{X: -1}, 180}, // 正西
		{model.Vec3{Y: -1}, 270}, // 正南
	}
	for _, b := range bearings {
		if got := bearingDeg(model.Vec3{}, b.to); math.Abs(got-b.want) > 1e-9 {
			t.Errorf("bearingDeg(0 -> %v) = %v want %v", b.to, got, b.want)
		}
	}
	if got := horizDist(model.Vec3{}, model.Vec3{X: 3, Y: 4, Z: 100}); math.Abs(got-5) > 1e-9 {
		t.Errorf("horizDist 应忽略 Z，got %v want 5", got)
	}
}

func TestSectorNames(t *testing.T) {
	// 焦点朝东（yaw 0）。北在你左手边，南在你右手边。
	cases := []struct {
		enemy model.Vec3
		want  string
	}{
		{model.Vec3{X: 1000}, "正前"},
		{model.Vec3{X: 1000, Y: -1000}, "右前"},
		{model.Vec3{Y: -1000}, "正右"},
		{model.Vec3{X: -1000, Y: -1000}, "右后"},
		{model.Vec3{X: -1000}, "正后"},
		{model.Vec3{X: -1000, Y: 1000}, "左后"},
		{model.Vec3{Y: 1000}, "正左"},
		{model.Vec3{X: 1000, Y: 1000}, "左前"},
	}
	for _, c := range cases {
		if got := sectorOf(0, bearingDeg(model.Vec3{}, c.enemy)); got != c.want {
			t.Errorf("enemy %v sector = %s want %s", c.enemy, got, c.want)
		}
	}
}

func TestPercentilesNearestRank(t *testing.T) {
	got := percentiles([]float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100})
	want := model.Percentiles{Min: 10, P50: 50, P75: 80, P90: 90, P95: 100, Max: 100}
	if got != want {
		t.Errorf("percentiles = %+v want %+v", got, want)
	}
	// 单样本：全部等于该值。
	single := percentiles([]float64{7})
	if single != (model.Percentiles{Min: 7, P50: 7, P75: 7, P90: 7, P95: 7, Max: 7}) {
		t.Errorf("单样本分位 = %+v", single)
	}
	// 空样本：全 0（不编造）。
	if empty := percentiles(nil); empty != (model.Percentiles{}) {
		t.Errorf("空样本应为全 0，got %+v", empty)
	}
	// 最近秩：4 个样本的 P50 取第 2 个（ceil(0.5*4)=2）。
	if p := percentiles([]float64{1, 2, 3, 4}); p.P50 != 2 || p.P90 != 4 {
		t.Errorf("4 样本最近秩错: %+v", p)
	}
}

// ---------- 站位：暴露枪线 ----------

func TestExposureConeAndDistanceBoundary(t *testing.T) {
	cases := []struct {
		name   string
		enemy  model.PlayerSnapshot
		want   int
		sector string
	}{
		{"45° 边界内算暴露", m2Snap(sid(e1U64), "敌1", model.SideCT, 1000, 1000, 270), 1, "左前"},
		{"45.5° 锥外不算", m2Snap(sid(e1U64), "敌1", model.SideCT, 1000, 1000, 270.5), 0, ""},
		{"2500 units 边界算暴露", m2Snap(sid(e1U64), "敌1", model.SideCT, 2500, 0, 180), 1, "正前"},
		{"2501 units 超距不算", m2Snap(sid(e1U64), "敌1", model.SideCT, 2501, 0, 180), 0, ""},
		{"敌人背对不算", m2Snap(sid(e1U64), "敌1", model.SideCT, 1000, 0, 0), 0, ""},
		{"右前方扇区", m2Snap(sid(e1U64), "敌1", model.SideCT, 1000, -1000, 135), 1, "右前"},
	}
	for _, tc := range cases {
		r := m2DeathRound(1, 10050, model.Vec3{}, 0, []model.PlayerSnapshot{tc.enemy}, nil)
		pa := m2Positioning(t, r)
		if pa.ExposedLines != tc.want {
			t.Errorf("%s: ExposedLines = %d want %d (%+v)", tc.name, pa.ExposedLines, tc.want, pa.ExposedBy)
			continue
		}
		if tc.want == 1 && pa.ExposedBy[0].Sector != tc.sector {
			t.Errorf("%s: sector = %s want %s", tc.name, pa.ExposedBy[0].Sector, tc.sector)
		}
	}
}

func TestExposureIgnoresSameSide(t *testing.T) {
	mate := m2Snap(sid(mateU64), "队友A", model.SideT, 1000, 0, 180) // 队友同样「朝向对准你」也不算枪线
	r := m2DeathRound(1, 10050, model.Vec3{}, 0, []model.PlayerSnapshot{mate}, nil)
	pa := m2Positioning(t, r)
	if pa.ExposedLines != 0 {
		t.Errorf("同阵营不算暴露枪线，got %+v", pa.ExposedBy)
	}
}

// ---------- 站位：道具掩护 ----------

func TestCoverRules(t *testing.T) {
	const deathTick = 10050
	util := func(typ string, side model.Side, x float64, start, end int) model.UtilityEvent {
		return model.UtilityEvent{Type: typ, ThrowerSide: side, LandPos: model.Vec3{X: x}, EffectStart: start, EffectEnd: end}
	}
	cases := []struct {
		name      string
		utility   []model.UtilityEvent
		wantCover bool
		wantType  string
		wantTick  int
	}{
		{"己方烟仍生效", []model.UtilityEvent{util("smoke", model.SideT, 50, deathTick-640, 0)}, true, "smoke", deathTick - 640},
		{"己方火仍生效", []model.UtilityEvent{util("molotov", model.SideT, 0, deathTick-100, deathTick+100)}, true, "molotov", deathTick - 100},
		{"燃烧弹掩护", []model.UtilityEvent{util("incendiary", model.SideT, 0, deathTick-50, 0)}, true, "incendiary", deathTick - 50},
		{"敌方烟不算掩护", []model.UtilityEvent{util("smoke", model.SideCT, 0, deathTick-100, 0)}, false, "", 0},
		{"已消散的烟不算", []model.UtilityEvent{util("smoke", model.SideT, 0, deathTick-300, deathTick-1)}, false, "", 0},
		{"落点超半径不算", []model.UtilityEvent{util("smoke", model.SideT, CoverRadius+1, deathTick-300, 0)}, false, "", 0},
		{"落点恰在半径边界算", []model.UtilityEvent{util("smoke", model.SideT, CoverRadius, deathTick-300, 0)}, true, "smoke", deathTick - 300},
		{"闪光不算掩护", []model.UtilityEvent{util("flash", model.SideT, 0, deathTick-10, 0)}, false, "", 0},
		{"死亡后才生效不算", []model.UtilityEvent{util("smoke", model.SideT, 0, deathTick+10, 0)}, false, "", 0},
		{"缺生效 tick 不算", []model.UtilityEvent{util("smoke", model.SideT, 0, 0, 0)}, false, "", 0},
	}
	for _, tc := range cases {
		r := m2DeathRound(1, deathTick, model.Vec3{}, 0, nil, tc.utility)
		pa := m2Positioning(t, r)
		if pa.CoverUtility != tc.wantCover {
			t.Errorf("%s: CoverUtility = %v want %v", tc.name, pa.CoverUtility, tc.wantCover)
			continue
		}
		if tc.wantCover && (pa.CoverType != tc.wantType || pa.CoverTick != tc.wantTick) {
			t.Errorf("%s: cover = %s@%d want %s@%d", tc.name, pa.CoverType, pa.CoverTick, tc.wantType, tc.wantTick)
		}
	}
}

// 同一位置有多条掩护时：死亡前 10s 内刚落下的优先作为证据。
func TestCoverPrefersRecentUtility(t *testing.T) {
	const deathTick = 10050
	r := m2DeathRound(1, deathTick, model.Vec3{}, 0, nil, []model.UtilityEvent{
		{Type: "smoke", ThrowerSide: model.SideT, EffectStart: deathTick - 1200, EffectEnd: 0},
		{Type: "molotov", ThrowerSide: model.SideT, EffectStart: deathTick - 32, EffectEnd: deathTick + 100},
	})
	pa := m2Positioning(t, r)
	if !pa.CoverUtility || pa.CoverType != "molotov" || pa.CoverTick != deathTick-32 {
		t.Errorf("应优先记录窗口内刚落下的火，got %s@%d", pa.CoverType, pa.CoverTick)
	}
}

// ---------- 站位：队友间距 ----------

func TestIsolatedBoundary(t *testing.T) {
	cases := []struct {
		name       string
		others     []model.PlayerSnapshot
		wantIso    bool
		wantLast   bool
		wantDist   float64
		wantReason string
	}{
		{"800 边界不孤立", []model.PlayerSnapshot{m2Snap(sid(mateU64), "队友A", model.SideT, IsolatedTeammateDist, 0, 0)}, false, false, IsolatedTeammateDist, ""},
		{"801 孤立", []model.PlayerSnapshot{m2Snap(sid(mateU64), "队友A", model.SideT, IsolatedTeammateDist+1, 0, 0)}, true, false, IsolatedTeammateDist + 1, "最近队友 队友A 距离 801 units（> 800）"},
		{"无快照不推断", nil, false, false, 0, ""},
		{"只剩敌人算最后存活，不算孤立", []model.PlayerSnapshot{m2Snap(sid(e1U64), "敌1", model.SideCT, 100, 0, 180)}, false, true, 0, "死亡瞬间无存活队友（残局，不计孤立站位）"},
	}
	for _, tc := range cases {
		r := m2DeathRound(1, 10050, model.Vec3{}, 0, tc.others, nil)
		pa := m2Positioning(t, r)
		if pa.Isolated != tc.wantIso {
			t.Errorf("%s: Isolated = %v want %v", tc.name, pa.Isolated, tc.wantIso)
		}
		if pa.LastAlive != tc.wantLast {
			t.Errorf("%s: LastAlive = %v want %v", tc.name, pa.LastAlive, tc.wantLast)
		}
		if math.Abs(pa.NearestTeammateDist-tc.wantDist) > 1e-9 {
			t.Errorf("%s: NearestTeammateDist = %v want %v", tc.name, pa.NearestTeammateDist, tc.wantDist)
		}
		if tc.wantReason != "" {
			if !containsLine(pa.RiskReasons, tc.wantReason) {
				t.Errorf("%s: 缺少理由 %q，实际 %v", tc.name, tc.wantReason, pa.RiskReasons)
			}
		}
	}
}

// ---------- 站位：高危规则 ----------

func TestHighRiskRule(t *testing.T) {
	enemyEast := m2Snap(sid(e1U64), "敌1", model.SideCT, 1000, 0, 180)
	enemyNorth := m2Snap(sid(e2U64), "敌2", model.SideCT, 0, 1000, 270)
	mateClose := m2Snap(sid(mateU64), "队友A", model.SideT, 100, 0, 0)
	mateFar := m2Snap(sid(mateU64), "队友A", model.SideT, 1200, 0, 0)
	smoke := []model.UtilityEvent{{Type: "smoke", ThrowerSide: model.SideT, EffectStart: 9000, EffectEnd: 0}}
	flashOnly := []model.UtilityEvent{{Type: "flash", ThrowerSide: model.SideT, EffectStart: 9000, EffectEnd: 0}}

	cases := []struct {
		name       string
		others     []model.PlayerSnapshot
		util       []model.UtilityEvent
		wantHigh   bool
		wantReason string // 必须出现的理由片段
	}{
		{
			"暴露 2 条枪线 → 高危",
			[]model.PlayerSnapshot{enemyEast, enemyNorth, mateClose}, nil,
			true, "暴露枪线 2 条（视锥近似）",
		},
		{
			"无掩护且孤立 → 高危（第二分支）",
			[]model.PlayerSnapshot{enemyEast, mateFar}, flashOnly,
			true, "无己方烟/火覆盖该位置",
		},
		{
			"无道具数据 + 孤立 → 不判高危（掩护不可评估）",
			[]model.PlayerSnapshot{enemyEast, mateFar}, nil,
			false, "掩护不可评估",
		},
		{
			"队友全死（最后存活）不计孤立站位",
			[]model.PlayerSnapshot{enemyEast}, nil,
			false, "死亡瞬间无存活队友（残局，不计孤立站位）",
		},
		{
			"1 条枪线 + 队友近 + 有掩护 → 不高危",
			[]model.PlayerSnapshot{enemyEast, mateClose}, smoke,
			false, "暴露枪线 1 条（视锥近似）",
		},
		{
			"孤立但有掩护 → 不高危",
			[]model.PlayerSnapshot{enemyEast, mateFar}, smoke,
			false, "最近队友 队友A 距离 1200 units（> 800）",
		},
		{
			"有道具数据但无烟火 → 记无掩护理由",
			[]model.PlayerSnapshot{enemyEast, mateClose}, flashOnly,
			false, "无己方烟/火覆盖该位置",
		},
	}
	for _, tc := range cases {
		r := m2DeathRound(1, 10050, model.Vec3{}, 0, tc.others, tc.util)
		pa := m2Positioning(t, r)
		if pa.HighRisk != tc.wantHigh {
			t.Errorf("%s: HighRisk = %v want %v（理由 %v）", tc.name, pa.HighRisk, tc.wantHigh, pa.RiskReasons)
		}
		if !containsLine(pa.RiskReasons, tc.wantReason) {
			t.Errorf("%s: 缺少理由片段 %q，实际 %v", tc.name, tc.wantReason, pa.RiskReasons)
		}
	}
}

// ---------- 站位：热区聚类 ----------

func TestHotZoneClusteringDeterministic(t *testing.T) {
	mk := func(n, tick int, x, y float64) model.RoundAnalysis {
		return model.RoundAnalysis{
			Number:      n,
			Positioning: &model.PositionDeathAnalysis{Round: n, Tick: tick, Pos: model.Vec3{X: x, Y: y}},
		}
	}
	rounds := []model.RoundAnalysis{
		mk(1, 100, 0, 0),
		mk(2, 200, 300, 0),
		mk(3, 300, 1000, 0),
		mk(4, 400, 400, 0), // 距首簇中心 150 -> 250，落入首簇（贪心首个命中）
	}
	rep := buildPositioningReport(rounds, nil)
	if rep == nil {
		t.Fatal("有焦点死亡应有汇总")
	}
	if len(rep.HotZones) != 2 {
		t.Fatalf("应聚成 2 个热区，got %+v", rep.HotZones)
	}
	z := rep.HotZones[0]
	if z.Deaths != 3 {
		t.Errorf("首热区死亡数 = %d want 3", z.Deaths)
	}
	wantRounds := []int{1, 2, 4}
	if !equalInts(z.Rounds, wantRounds) {
		t.Errorf("首热区回合 = %v want %v", z.Rounds, wantRounds)
	}
	wantTicks := []int{100, 200, 400}
	if !equalInts(z.Ticks, wantTicks) {
		t.Errorf("首热区 tick = %v want %v", z.Ticks, wantTicks)
	}
	if math.Abs(z.Pos.X-233.3) > 0.05 || math.Abs(z.Pos.Y) > 1e-9 {
		t.Errorf("首热区中心 = %+v want x≈233.3", z.Pos)
	}
	if rep.HotZones[1].Deaths != 1 || math.Abs(rep.HotZones[1].Pos.X-1000) > 1e-9 {
		t.Errorf("次热区 = %+v want 1000,1 次", rep.HotZones[1])
	}
	// 同输入两次结果完全一致（无 map 迭代影响顺序）。
	again := buildPositioningReport(rounds, nil)
	if len(again.HotZones) != len(rep.HotZones) || again.HotZones[0].Pos != rep.HotZones[0].Pos {
		t.Errorf("聚类结果不确定: %+v vs %+v", rep.HotZones, again.HotZones)
	}
}

func TestHotZoneTopThree(t *testing.T) {
	mk := func(n, tick int, x float64) model.RoundAnalysis {
		return model.RoundAnalysis{Number: n, Positioning: &model.PositionDeathAnalysis{Round: n, Tick: tick, Pos: model.Vec3{X: x}}}
	}
	// 4 个互不相邻的簇，各 1 次死亡 -> 按首次 tick 取前 3。
	rounds := []model.RoundAnalysis{mk(1, 100, 0), mk(2, 200, 2000), mk(3, 300, 4000), mk(4, 400, 6000)}
	rep := buildPositioningReport(rounds, nil)
	if len(rep.HotZones) != MaxHotZones {
		t.Fatalf("热区应截断到 %d 个，got %d", MaxHotZones, len(rep.HotZones))
	}
	if !equalInts(rep.HotZones[0].Rounds, []int{1}) || !equalInts(rep.HotZones[2].Rounds, []int{3}) {
		t.Errorf("并列按首次 tick 升序错: %+v", rep.HotZones)
	}
}

// ---------- 对枪 ----------

func TestDuelGeometryAndVerdict(t *testing.T) {
	r := &model.Round{Number: 7, StartTick: 88000, EndTick: 94000}
	c := m2Ctx(r)
	d := model.DuelFact{
		StartTick: 88213,
		RoundTime: 3.3,
		A: model.DuelPlayerFact{
			SteamID64: testFocus, Name: "焦点", Side: model.SideT,
			Pos: model.Vec3{}, Yaw: 0, Speed: 182, SpeedValid: true,
			FirstShotTick: 88213, Shots: 3, Hits: 1, Damage: 80,
		},
		B: model.DuelPlayerFact{
			SteamID64: sid(e1U64), Name: "Q", Side: model.SideCT,
			Pos: model.Vec3{Y: 1000}, Yaw: 180,
			FirstShotTick: 88218, Shots: 4, Hits: 1, Damage: 100,
		},
		KillTick: 88260, KillerID: sid(e1U64), VictimID: testFocus, Weapon: "AK-47",
	}
	duels := buildDuels(c, []model.DuelFact{d})
	if len(duels) != 1 {
		t.Fatalf("焦点参与的对枪应有 1 条，got %d", len(duels))
	}
	da := duels[0]
	// 焦点朝东（yaw 0），对手在正北 -> 方位 90°。
	if da.PreAimDeltaDeg != 90 {
		t.Errorf("PreAimDeltaDeg = %v want 90", da.PreAimDeltaDeg)
	}
	if !da.PreAimFailed {
		t.Error("90° > 15° 应判预瞄失败")
	}
	if !da.HasBothShots {
		t.Error("双方都有首发 tick，HasBothShots 应为 true")
	}
	if da.FirstShotDiffSec != -0.08 { // (88213-88218)/64 = -0.078125 -> 正数才是焦点更晚
		t.Errorf("FirstShotDiffSec = %v want -0.08（负数 = 焦点更早）", da.FirstShotDiffSec)
	}
	if da.Outcome != "focus_death" {
		t.Errorf("Outcome = %s want focus_death", da.Outcome)
	}
	if da.OpponentName != "Q" || da.OpponentSide != model.SideCT {
		t.Errorf("对手信息错: %+v", da)
	}
	if da.Tick != 88213 || da.Round != 7 {
		t.Errorf("tick/round = %d/%d want 88213/7", da.Tick, da.Round)
	}
	for _, want := range []string{
		"第7回合", "[tick 88213]", "对 Q 对枪", "预瞄偏差 90.0°（阈值 15°）",
		"开火速度 182 u/s（阈值 100）", "焦点早开火 0.08s", "命中 1/3", "结果被击杀。",
	} {
		if !strings.Contains(da.Verdict, want) {
			t.Errorf("Verdict 缺少 %q: %s", want, da.Verdict)
		}
	}
}

func TestDuelFocusOnSideB(t *testing.T) {
	r := &model.Round{Number: 1, StartTick: 10000, EndTick: 16000}
	d := model.DuelFact{
		StartTick: 10100,
		A:         model.DuelPlayerFact{SteamID64: sid(e1U64), Name: "敌1", Side: model.SideCT, Pos: model.Vec3{X: 1000}, FirstShotTick: 10100},
		B:         model.DuelPlayerFact{SteamID64: testFocus, Name: "焦点", Side: model.SideT, Pos: model.Vec3{}, Yaw: 0, FirstShotTick: 10105, Shots: 2, Hits: 1},
		KillerID:  testFocus, VictimID: sid(e1U64),
	}
	duels := buildDuels(m2Ctx(r), []model.DuelFact{d})
	if len(duels) != 1 {
		t.Fatalf("焦点在 B 侧也应识别，got %d", len(duels))
	}
	da := duels[0]
	if da.OpponentID != sid(e1U64) || da.Outcome != "focus_kill" {
		t.Errorf("B 侧焦点对枪错: %+v", da)
	}
	if da.FirstShotDiffSec != 0.08 { // 10105-10100 -> 正数 = 焦点更晚
		t.Errorf("FirstShotDiffSec = %v want 0.08", da.FirstShotDiffSec)
	}
	if !strings.Contains(da.Verdict, "焦点晚开火 0.08s") {
		t.Errorf("Verdict 应写焦点晚开火: %s", da.Verdict)
	}
	if strings.Contains(da.Verdict, "对手未开火") {
		t.Errorf("对手已开火，不应写未开火: %s", da.Verdict)
	}
}

func TestDuelOutcomeMapping(t *testing.T) {
	cases := []struct{ name, killer, victim, want string }{
		{"焦点击杀", testFocus, sid(e1U64), "focus_kill"},
		{"焦点死亡", sid(e1U64), testFocus, "focus_death"},
		{"窗口内无击杀", "", "", "no_kill"},
		{"第三方击杀也记 no_kill", sid(e2U64), sid(e1U64), "no_kill"},
	}
	for _, tc := range cases {
		r := &model.Round{Number: 1, StartTick: 10000, EndTick: 16000}
		d := model.DuelFact{
			StartTick: 10100,
			A:         model.DuelPlayerFact{SteamID64: testFocus, Name: "焦点", Side: model.SideT, FirstShotTick: 10100},
			B:         model.DuelPlayerFact{SteamID64: sid(e1U64), Name: "敌1", Side: model.SideCT, Pos: model.Vec3{Y: 100}},
			KillerID:  tc.killer, VictimID: tc.victim,
		}
		duels := buildDuels(m2Ctx(r), []model.DuelFact{d})
		if len(duels) != 1 || duels[0].Outcome != tc.want {
			t.Errorf("%s: outcome = %v want %s", tc.name, duels, tc.want)
		}
	}
}

func TestDuelMissingFactsVerdict(t *testing.T) {
	r := &model.Round{Number: 3, StartTick: 30000, EndTick: 36000}
	// 焦点未开火、速度不可测、对手先开火。
	d := model.DuelFact{
		StartTick: 30100,
		A: model.DuelPlayerFact{
			SteamID64: testFocus, Name: "焦点", Side: model.SideT,
			Pos: model.Vec3{}, Yaw: 0, SpeedValid: false,
		},
		B: model.DuelPlayerFact{
			SteamID64: sid(e1U64), Name: "敌1", Side: model.SideCT,
			Pos: model.Vec3{X: 1000}, Yaw: 180, FirstShotTick: 30100, Shots: 2, Hits: 2,
		},
		KillerID: sid(e1U64), VictimID: testFocus,
	}
	da := buildDuels(m2Ctx(r), []model.DuelFact{d})[0]
	if da.HasBothShots || da.FirstShotDiffSec != 0 {
		t.Errorf("单方开火时 HasBothShots=false 且差值应为 0: %+v", da)
	}
	if da.PreAimMeasured || da.PreAimDeltaDeg != 0 || da.PreAimFailed || da.MovingShot {
		t.Errorf("焦点未开火时不应评预瞄/急停: %+v", da)
	}
	for _, want := range []string{"无接敌瞬间快照，不评估预瞄/急停", "对手命中 2/2", "结果被击杀"} {
		if !strings.Contains(da.Verdict, want) {
			t.Errorf("Verdict 缺少 %q: %s", want, da.Verdict)
		}
	}
}

// TestDuelVerdictKillOnlyNoShot 覆盖 GOTV「只有击杀事件、开火事件缺失」：
// 不能写成「双方均未开火」，否则与「结果击杀对手」自相矛盾。
func TestDuelVerdictKillOnlyNoShot(t *testing.T) {
	r := &model.Round{Number: 17, StartTick: 120000, EndTick: 130000}
	d := model.DuelFact{
		StartTick: 127453,
		Contact:   "kill",
		A:         model.DuelPlayerFact{SteamID64: testFocus, Name: "焦点", Side: model.SideT},
		B:         model.DuelPlayerFact{SteamID64: sid(e1U64), Name: "敌1", Side: model.SideCT},
		KillTick:  127453,
		KillerID:  testFocus,
		VictimID:  sid(e1U64),
	}
	da := buildDuels(m2Ctx(r), []model.DuelFact{d})[0]
	if da.PreAimMeasured {
		t.Error("contact=kill 且无开火事件时不应声称有接敌快照")
	}
	if strings.Contains(da.Verdict, "双方均未开火") {
		t.Errorf("有击杀事实时不应写「双方均未开火」: %s", da.Verdict)
	}
	if !strings.Contains(da.Verdict, "开火事件未采到") || !strings.Contains(da.Verdict, "结果击杀对手") {
		t.Errorf("Verdict 应说明开火事件缺失并保留击杀事实: %s", da.Verdict)
	}
}

func TestBuildDuelsIgnoresOtherPlayers(t *testing.T) {
	r := &model.Round{Number: 1, StartTick: 10000, EndTick: 16000}
	d := model.DuelFact{
		StartTick: 10100,
		A:         model.DuelPlayerFact{SteamID64: sid(e1U64)},
		B:         model.DuelPlayerFact{SteamID64: sid(e2U64)},
	}
	if got := buildDuels(m2Ctx(r), []model.DuelFact{d}); got != nil {
		t.Errorf("焦点未参与的对枪不应输出，got %+v", got)
	}
}

// ---------- 对枪：汇总与分布 ----------

func TestDuelingReportAggregate(t *testing.T) {
	mk := func(outcome string, focusFirst, oppFirst int, speed float64, valid bool) model.DuelAnalysis {
		return model.DuelAnalysis{
			Outcome: outcome, FocusFirstShotTick: focusFirst, OppFirstShotTick: oppFirst,
			FocusSpeed: speed, FocusSpeedValid: valid,
			HasBothShots:   focusFirst > 0 && oppFirst > 0,
			PreAimMeasured: focusFirst > 0,
			PreAimDeltaDeg: 20, PreAimFailed: true, MovingShot: focusFirst > 0 && valid && speed > MovingFireSpeedUPS,
		}
	}
	rounds := []model.RoundAnalysis{{Number: 1, Duels: []model.DuelAnalysis{
		mk("focus_kill", 100, 0, 50, true),     // 对手未开火 -> 先手胜
		mk("focus_kill", 200, 100, 50, true),   // 焦点更晚 -> 不算先手胜
		mk("focus_kill", 100, 200, 50, true),   // 焦点更早 -> 先手胜
		mk("focus_death", 150, 100, 200, true), // 焦点开火且超速 -> 移动中开枪
		mk("no_kill", 0, 0, 0, false),          // 焦点未开火 -> 不参与预瞄/急停统计
	}}}
	rep := buildDuelingReport(nil, rounds, testFocus)
	if rep == nil {
		t.Fatal("有对枪应有汇总")
	}
	if rep.Total != 5 {
		t.Errorf("Total = %d want 5", rep.Total)
	}
	if rep.PreAimFailures != 4 {
		t.Errorf("PreAimFailures = %d want 4", rep.PreAimFailures)
	}
	if rep.MovingShots != 1 {
		t.Errorf("MovingShots = %d want 1", rep.MovingShots)
	}
	if rep.FirstShotWins != 2 {
		t.Errorf("FirstShotWins = %d want 2", rep.FirstShotWins)
	}
	if rep.FocusKills != 3 || rep.FocusDeaths != 1 {
		t.Errorf("FocusKills/Deaths = %d/%d want 3/1", rep.FocusKills, rep.FocusDeaths)
	}
	if rep.Thresholds.PreAimBadDeg != PreAimBadDeg || rep.Thresholds.MovingFireSpeedUPS != MovingFireSpeedUPS {
		t.Errorf("阈值未原样写入: %+v", rep.Thresholds)
	}
	// 无 ShotFact -> 退回对枪快照速度（只取 SpeedValid）：[50,50,50,200]
	if rep.Distribution.ShotSpeedSamples != 4 {
		t.Errorf("ShotSpeedSamples = %d want 4", rep.Distribution.ShotSpeedSamples)
	}
	if rep.Distribution.ShotSpeedUPS.Min != 50 || rep.Distribution.ShotSpeedUPS.Max != 200 {
		t.Errorf("退回快照速度分布错: %+v", rep.Distribution.ShotSpeedUPS)
	}
	if rep.Distribution.PreAimSamples != 4 {
		t.Errorf("PreAimSamples = %d want 4", rep.Distribution.PreAimSamples)
	}
}

// TestFirstShotWinsRequiresFocusShot 无焦点开火事件的击杀（手雷/刀/事件缺失）不算先手胜。
func TestFirstShotWinsRequiresFocusShot(t *testing.T) {
	rounds := []model.RoundAnalysis{{Number: 1, Duels: []model.DuelAnalysis{
		{Outcome: "focus_kill", FocusFirstShotTick: 0, OppFirstShotTick: 0, FocusSpeedValid: false},
	}}}
	rep := buildDuelingReport(nil, rounds, testFocus)
	if rep == nil {
		t.Fatal("有对枪应有汇总")
	}
	if rep.FirstShotWins != 0 {
		t.Errorf("焦点没开火的击杀不应计入先手胜，got %d", rep.FirstShotWins)
	}
	if rep.FocusKills != 1 {
		t.Errorf("FocusKills = %d want 1", rep.FocusKills)
	}
}

func TestDuelingDistributionUsesShotsWhenEnough(t *testing.T) { // 12 条有效 ShotFact -> 用逐枪样本，而不是对枪快照。
	r := &model.Round{Number: 1}
	for i := 1; i <= 12; i++ {
		r.Shots = append(r.Shots, model.ShotFact{
			Tick: 10000 + i, ShooterID: testFocus, Speed: float64(i) * 10, SpeedValid: true,
		})
	}
	// 一条无效样本必须被排除。
	r.Shots = append(r.Shots, model.ShotFact{Tick: 10050, ShooterID: testFocus, Speed: 999, SpeedValid: false})
	// 别人的样本也必须被排除。
	r.Shots = append(r.Shots, model.ShotFact{Tick: 10051, ShooterID: sid(e1U64), Speed: 999, SpeedValid: true})

	m := &model.Match{Rounds: []model.Round{*r}}
	rounds := []model.RoundAnalysis{{Number: 1, Duels: []model.DuelAnalysis{{
		Outcome: "no_kill", FocusSpeed: 500, FocusSpeedValid: true,
	}}}}
	rep := buildDuelingReport(m, rounds, testFocus)
	if rep.Distribution.ShotSpeedSamples != 12 {
		t.Fatalf("ShotSpeedSamples = %d want 12（逐枪样本）", rep.Distribution.ShotSpeedSamples)
	}
	d := rep.Distribution.ShotSpeedUPS
	if d.Min != 10 || d.P50 != 60 || d.P75 != 90 || d.P90 != 110 || d.P95 != 120 || d.Max != 120 {
		t.Errorf("分位错: %+v", d)
	}
}

func TestDuelingReportNilWhenNoDuels(t *testing.T) {
	rounds := []model.RoundAnalysis{{Number: 1}}
	if rep := buildDuelingReport(nil, rounds, testFocus); rep != nil {
		t.Errorf("无对枪应返回 nil，got %+v", rep)
	}
}

// ---------- Analyze 接线 / 旧数据降级 ----------

func TestAnalyzeM2Wiring(t *testing.T) {
	// 焦点被击杀：2 条暴露枪线 + 队友很远 + 无掩护 -> 高危；同时有一条对枪。
	r := m2DeathRound(1, 10050, model.Vec3{}, 0, []model.PlayerSnapshot{
		m2Snap(sid(e1U64), "敌1", model.SideCT, 1000, 0, 180),
		m2Snap(sid(e2U64), "敌2", model.SideCT, 0, 1000, 270),
		m2Snap(sid(mateU64), "队友A", model.SideT, 1200, 0, 0),
	}, nil)
	r.Duels = []model.DuelFact{{
		StartTick: 10040,
		A:         model.DuelPlayerFact{SteamID64: testFocus, Name: "焦点", Side: model.SideT, Pos: model.Vec3{}, Yaw: 0, Speed: 20, SpeedValid: true, FirstShotTick: 10040, Shots: 2, Hits: 1},
		B:         model.DuelPlayerFact{SteamID64: sid(e1U64), Name: "敌1", Side: model.SideCT, Pos: model.Vec3{X: 1000}, Yaw: 180, FirstShotTick: 10045, Shots: 2, Hits: 2},
		KillTick:  10050, KillerID: sid(e1U64), VictimID: testFocus,
	}}
	r.Players = []model.RoundPlayer{
		{SteamID64: focusU64, Name: "焦点", Side: model.SideT, Deaths: 1, DeathTick: 10050},
		{SteamID64: mateU64, Name: "队友A", Side: model.SideT},
		{SteamID64: e1U64, Name: "敌1", Side: model.SideCT},
	}
	r.Alive = []model.AliveCount{{Tick: 10000, TAlive: 5, CTAlive: 5}, {Tick: 10050, TAlive: 4, CTAlive: 5}}

	m := &model.Match{
		Meta:    model.MatchMeta{Map: "de_mirage", TickRate: 64, TotalRounds: 1, FocusPlayer: testFocus, FocusPlayerName: "焦点"},
		Players: []model.PlayerInfo{{SteamID64: focusU64, Name: "焦点"}},
		Rounds:  []model.Round{*r},
	}
	a, err := Analyze(m, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if a.Rounds[0].Positioning == nil {
		t.Fatal("焦点死亡回合应有 RoundAnalysis.Positioning")
	}
	if a.Positioning == nil {
		t.Fatal("应有全场站位汇总")
	}
	p := a.Positioning
	if p.Deaths != 1 || p.HighRiskCount != 1 || p.ExposedDeaths != 1 || p.NoCoverDeaths != 0 || p.IsolatedDeaths != 1 {
		t.Errorf("站位汇总错（无道具数据时 NoCover 不计数）: %+v", p)
	}
	if p.HighRiskRate != 1 {
		t.Errorf("HighRiskRate = %v want 1", p.HighRiskRate)
	}
	if len(p.HotZones) != 1 || p.HotZones[0].Deaths != 1 {
		t.Errorf("热区错: %+v", p.HotZones)
	}
	if p.Thresholds.ExposureConeHalfAngle != ExposureConeHalfAngle || p.Thresholds.HotZoneRadius != HotZoneRadius {
		t.Errorf("站位阈值未写入: %+v", p.Thresholds)
	}
	if len(a.Rounds[0].Duels) != 1 {
		t.Fatalf("回合对枪明细 = %d want 1", len(a.Rounds[0].Duels))
	}
	if a.Dueling == nil || a.Dueling.Total != 1 || a.Dueling.FocusDeaths != 1 || a.Dueling.FocusKills != 0 {
		t.Errorf("对枪汇总错: %+v", a.Dueling)
	}
	if a.Dueling.FocusKills != 0 || a.Dueling.FirstShotWins != 0 {
		t.Errorf("焦点未拿到击杀，不应计先手胜: %+v", a.Dueling)
	}
}

func TestM2OldMatchDegradesToZero(t *testing.T) {
	// 旧 match.json：KillEvent 没有 OtherAlive/VictimYaw，回合没有 Utility/Shots/Duels。
	m := &model.Match{
		Meta:    model.MatchMeta{TickRate: 64, FocusPlayer: testFocus, FocusPlayerName: "焦点"},
		Players: []model.PlayerInfo{{SteamID64: focusU64, Name: "焦点"}},
		Rounds: []model.Round{{
			Number: 1, StartTick: 10000, EndTick: 16000, Winner: model.SideCT, EndReason: "elimination", Half: "H1",
			Players: []model.RoundPlayer{{SteamID64: focusU64, Name: "焦点", Side: model.SideT, Deaths: 1, DeathTick: 10100}},
			Kills: []model.KillEvent{{
				Tick: 10100, KillerID: sid(e1U64), KillerName: "敌1", KillerSide: model.SideCT,
				VictimID: testFocus, VictimName: "焦点", VictimSide: model.SideT, Weapon: "AK-47",
				VictimPos: model.Vec3{X: 500, Y: 500},
			}},
			Alive: []model.AliveCount{{Tick: 10000, TAlive: 5, CTAlive: 5}, {Tick: 10100, TAlive: 4, CTAlive: 5}},
		}},
	}
	a, err := Analyze(m, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ra := a.Rounds[0]
	if ra.Positioning == nil {
		t.Fatal("降级路径仍应输出站位条目（零值），而不是消失")
	}
	if ra.Positioning.ExposedLines != 0 || ra.Positioning.Isolated || ra.Positioning.CoverUtility || ra.Positioning.HighRisk {
		t.Errorf("旧数据应降级为 0/false: %+v", ra.Positioning)
	}
	if len(ra.Positioning.RiskReasons) != 0 {
		t.Errorf("旧数据缺事实时不应生成理由，got %v", ra.Positioning.RiskReasons)
	}
	if ra.Duels != nil {
		t.Errorf("旧数据无对枪，got %+v", ra.Duels)
	}
	if a.Dueling != nil {
		t.Errorf("无对枪应保持 nil，got %+v", a.Dueling)
	}
	p := a.Positioning
	if p == nil {
		t.Fatal("有焦点死亡应有汇总")
	}
	if p.Deaths != 1 || p.HighRiskCount != 0 || p.ExposedDeaths != 0 || p.NoCoverDeaths != 0 || p.IsolatedDeaths != 0 {
		t.Errorf("旧数据汇总应全 0（NoCover 不编造）: %+v", p)
	}
	if len(p.HotZones) != 1 || p.HotZones[0].Deaths != 1 {
		t.Errorf("旧数据仍应保留死亡坐标热区: %+v", p.HotZones)
	}
}

func TestM2NoFocusDeathKeepsNil(t *testing.T) {
	m := &model.Match{
		Meta:    model.MatchMeta{TickRate: 64, FocusPlayer: testFocus, FocusPlayerName: "焦点"},
		Players: []model.PlayerInfo{{SteamID64: focusU64, Name: "焦点"}},
		Rounds: []model.Round{{
			Number: 1, StartTick: 10000, EndTick: 16000, Winner: model.SideT, EndReason: "elimination", Half: "H1",
			Players: []model.RoundPlayer{{SteamID64: focusU64, Name: "焦点", Side: model.SideT, Kills: 1, AliveAtEnd: true}},
			Kills: []model.KillEvent{{
				Tick: 10100, KillerID: testFocus, KillerName: "焦点", KillerSide: model.SideT,
				VictimID: sid(e1U64), VictimName: "敌1", VictimSide: model.SideCT, Weapon: "AK-47",
			}},
			Alive: []model.AliveCount{{Tick: 10000, TAlive: 5, CTAlive: 5}, {Tick: 10100, TAlive: 5, CTAlive: 4}},
		}},
	}
	a, err := Analyze(m, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if a.Positioning != nil {
		t.Errorf("焦点未死亡时 Positioning 应为 nil，got %+v", a.Positioning)
	}
	if a.Dueling != nil {
		t.Errorf("无对枪时 Dueling 应为 nil，got %+v", a.Dueling)
	}
}

// ---------- 小工具 ----------

func containsLine(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
