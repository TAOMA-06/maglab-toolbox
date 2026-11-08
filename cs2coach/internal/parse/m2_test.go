package parse

import (
	"math"
	"os"
	"testing"

	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/common"

	"cs2-demo-review/internal/model"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}

func TestNormalizeYaw(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want float64
	}{
		{"零", 0, 0},
		{"常规", 45, 45},
		{"接近 360", 359.9, 359.9},
		{"360 归零", 360, 0},
		{"720 归零", 720, 0},
		{"负数", -10, 350},
		{"负 360 归零", -360, 0},
		{"超过一圈", 361, 1},
		{"大正角", 1080 + 90, 90},
		{"大负角", -450, 270},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeYaw(tt.in); !almostEqual(got, tt.want) {
				t.Errorf("normalizeYaw(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestShotSpeed(t *testing.T) {
	base := frameSample{pos: model.Vec3{X: 0, Y: 0, Z: 64}, tick: 1000, sec: 10}
	tests := []struct {
		name      string
		nowPos    model.Vec3
		nowSec    float64
		last      *frameSample
		wantSpeed float64
		wantValid bool
	}{
		{
			name:   "无上一采样",
			nowPos: model.Vec3{X: 10},
			nowSec: 10.1,
			last:   nil,
		},
		{
			name:   "dt 为 0",
			nowPos: model.Vec3{X: 10},
			nowSec: 10,
			last:   &base,
		},
		{
			name:   "dt 为负",
			nowPos: model.Vec3{X: 10},
			nowSec: 9.9,
			last:   &base,
		},
		{
			name:      "静止（0 速度仍可信）",
			nowPos:    model.Vec3{X: 0, Y: 0, Z: 64},
			nowSec:    10.1,
			last:      &base,
			wantSpeed: 0,
			wantValid: true,
		},
		{
			name:      "正常移动 100 units/0.1s = 1000 u/s",
			nowPos:    model.Vec3{X: 100, Y: 0, Z: 64},
			nowSec:    10.1,
			last:      &base,
			wantSpeed: 1000,
			wantValid: true,
		},
		{
			name:      "只取水平距离（Z 变化不计）",
			nowPos:    model.Vec3{X: 0, Y: 0, Z: 200},
			nowSec:    10.1,
			last:      &base,
			wantSpeed: 0,
			wantValid: true,
		},
		{
			name:      "斜向移动用欧氏距离",
			nowPos:    model.Vec3{X: 6, Y: 8, Z: 64},
			nowSec:    10.1,
			last:      &base,
			wantSpeed: 100,
			wantValid: true,
		},
		{
			name:      "上限边界 2000 可信",
			nowPos:    model.Vec3{X: 250},
			nowSec:    10.125,
			last:      &base,
			wantSpeed: 2000,
			wantValid: true,
		},
		{
			name:   "瞬移超过 2000 u/s 不可信",
			nowPos: model.Vec3{X: 300},
			nowSec: 10.1,
			last:   &base,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			speed, valid := shotSpeed(tt.nowPos, tt.nowSec, tt.last)
			if valid != tt.wantValid {
				t.Fatalf("shotSpeed() valid = %v, want %v", valid, tt.wantValid)
			}
			if !almostEqual(speed, tt.wantSpeed) {
				t.Errorf("shotSpeed() = %v, want %v", speed, tt.wantSpeed)
			}
			if !valid && speed != 0 {
				t.Errorf("不可信时 speed 必须为 0，得到 %v", speed)
			}
		})
	}
}

func TestPairKey(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want string
	}{
		{"升序输入", "100", "200", "100|200"},
		{"降序输入顺序无关", "200", "100", "100|200"},
		{"相同 id", "100", "100", "100|100"},
		{"字符串比较非数值", "9", "10", "10|9"},
		{"真实 steamid", "76561198000000002", "76561198000000001", "76561198000000001|76561198000000002"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pairKey(tt.a, tt.b); got != tt.want {
				t.Errorf("pairKey(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
			}
			if got := pairKey(tt.b, tt.a); got != tt.want {
				t.Errorf("pairKey 不满足顺序无关：%q vs %q", got, tt.want)
			}
		})
	}
}

func TestRecentShotsWindow(t *testing.T) {
	now := 20.0
	shots := []shotSample{
		{sec: 18.5, tick: 1}, // 窗口外
		{sec: 19.0, tick: 2}, // 端点（= now-1.0，包含）
		{sec: 19.5, tick: 3},
		{sec: 20.0, tick: 4}, // 当前时刻
		{sec: 20.5, tick: 5}, // 未来时间，丢弃
	}

	got := pruneRecentShots(append([]shotSample(nil), shots...), now, duelSnapshotWindowSec)
	if len(got) != 3 {
		t.Fatalf("pruneRecentShots() 长度 = %d, want 3（窗口内 19.0/19.5/20.0）", len(got))
	}
	for i, wantTick := range []int{2, 3, 4} {
		if got[i].tick != wantTick {
			t.Errorf("pruneRecentShots()[%d].tick = %d, want %d", i, got[i].tick, wantTick)
		}
	}

	last, ok := lastShotWithin(shots, now, duelSnapshotWindowSec)
	if !ok || last.tick != 4 {
		t.Errorf("lastShotWithin() = (%+v, %v), want tick 4", last, ok)
	}
	if n := countRecentShots(shots, now, duelSnapshotWindowSec); n != 3 {
		t.Errorf("countRecentShots() = %d, want 3", n)
	}

	// 窗口内没有任何样本。
	if _, ok := lastShotWithin(shots, 30, duelSnapshotWindowSec); ok {
		t.Error("窗口外不应返回样本")
	}
	if n := countRecentShots(shots, 30, duelSnapshotWindowSec); n != 0 {
		t.Errorf("窗口外 countRecentShots() = %d, want 0", n)
	}
}

func TestOrderDuelSides(t *testing.T) {
	low := model.DuelPlayerFact{SteamID64: "100", FirstShotTick: 0}
	high := model.DuelPlayerFact{SteamID64: "200", FirstShotTick: 0}
	early := model.DuelPlayerFact{SteamID64: "200", FirstShotTick: 100}
	late := model.DuelPlayerFact{SteamID64: "100", FirstShotTick: 200}
	sameTickLow := model.DuelPlayerFact{SteamID64: "100", FirstShotTick: 100}
	sameTickHigh := model.DuelPlayerFact{SteamID64: "200", FirstShotTick: 100}

	tests := []struct {
		name  string
		a, b  model.DuelPlayerFact
		wantA string
		wantB string
	}{
		{"先开火者在前", late, early, "200", "100"},
		{"后开火者输入顺序无关", early, late, "200", "100"},
		{"只有一方开火", low, early, "200", "100"},
		{"都未开火按 SteamID 升序", high, low, "100", "200"},
		{"同一 tick 按 SteamID 升序", sameTickHigh, sameTickLow, "100", "200"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotA, gotB := orderDuelSides(tt.a, tt.b)
			if gotA.SteamID64 != tt.wantA || gotB.SteamID64 != tt.wantB {
				t.Errorf("orderDuelSides() = (%s, %s), want (%s, %s)",
					gotA.SteamID64, gotB.SteamID64, tt.wantA, tt.wantB)
			}
		})
	}
}

func TestDuelStartTick(t *testing.T) {
	tests := []struct {
		name     string
		a, b     int
		fallback int
		want     int
	}{
		{"两侧都有取较小", 200, 100, 50, 100},
		{"只有一侧有", 0, 300, 50, 300},
		{"都没有用当前 tick", 0, 0, 50, 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := model.DuelPlayerFact{FirstShotTick: tt.a}
			b := model.DuelPlayerFact{FirstShotTick: tt.b}
			if got := duelStartTick(a, b, tt.fallback); got != tt.want {
				t.Errorf("duelStartTick() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSnapshotDuelPlayer(t *testing.T) {
	now := 30.0
	p := &common.Player{SteamID64: 76561198000000001, Name: "Alice", Team: common.TeamTerrorists}
	shots := []shotSample{
		{sec: 28.0, tick: 1, yaw: 1}, // 窗口外
		{sec: 29.4, tick: 100, pos: model.Vec3{X: 10}, yaw: 350, pitch: -5, speed: 120, speedValid: true},
		{sec: 29.8, tick: 200, pos: model.Vec3{X: 20}, yaw: 10, pitch: 5, speed: 80, speedValid: true},
	}

	a := &accumulator{recentShots: map[*common.Player][]shotSample{p: shots}}
	got := a.snapshotDuelPlayer(p, now, model.Vec3{X: 100})

	if got.SteamID64 != "76561198000000001" || got.Name != "Alice" || got.Side != model.SideT {
		t.Errorf("身份字段异常：%+v", got)
	}
	if got.FirstShotTick != 200 {
		t.Errorf("FirstShotTick = %d, want 200（窗口内最近一次开枪）", got.FirstShotTick)
	}
	if got.Shots != 2 {
		t.Errorf("Shots = %d, want 2（两枪都朝向对手）", got.Shots)
	}
	if got.Pos.X != 20 || got.Yaw != 10 || got.Pitch != 5 || got.Speed != 80 || !got.SpeedValid {
		t.Errorf("快照未取最近一次开枪：%+v", got)
	}
	if got.TargetPos.X != 100 {
		t.Errorf("TargetPos = %+v, want 对手位置 X=100", got.TargetPos)
	}

	// 无开枪：用当前 Position/视角（未绑定 pawn 时为 0），FirstShotTick=0、速度不可信。
	empty := (&accumulator{recentShots: map[*common.Player][]shotSample{}}).snapshotDuelPlayer(p, now, model.Vec3{})
	if empty.FirstShotTick != 0 || empty.Shots != 0 || empty.Speed != 0 || empty.SpeedValid {
		t.Errorf("无开枪快照异常：%+v", empty)
	}
}

func TestShotAimedAt(t *testing.T) {
	from := model.Vec3{X: 0, Y: 0}
	target := model.Vec3{X: 500, Y: 0} // 正东，方位 0°
	tests := []struct {
		name string
		pos  model.Vec3
		yaw  float64
		tgt  model.Vec3
		want bool
	}{
		{"正对", from, 0, target, true},
		{"边界内 30°", from, 30, target, true},
		{"边界外 31°", from, 31, target, false},
		{"背对", from, 180, target, false},
		{"超距离", from, 0, model.Vec3{X: 4000}, false},
		{"侧向目标", from, 90, model.Vec3{X: 0, Y: 500}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shotAimedAt(tt.pos, tt.yaw, tt.tgt); got != tt.want {
				t.Errorf("shotAimedAt(%+v, %v, %+v) = %v, want %v", tt.pos, tt.yaw, tt.tgt, got, tt.want)
			}
		})
	}
}

func TestGetOrOpenDuelReengage(t *testing.T) {
	now := 100.0
	p1 := &common.Player{SteamID64: 1, Name: "A", Team: common.TeamTerrorists}
	p2 := &common.Player{SteamID64: 2, Name: "B", Team: common.TeamCounterTerrorists}

	acc := newAccumulator("x.dem", Options{})
	rb := &roundBuilder{
		round:        model.Round{Number: 1, StartTick: 1000},
		players:      map[*common.Player]*model.RoundPlayer{},
		moneyAtStart: map[*common.Player]int{},
	}
	acc.cur = rb
	acc.tickRate = 128
	acc.resetM2RoundState()

	d1 := acc.getOrOpenDuel(p1, p2, 1000, now, "hurt")
	if d1.fact.StartTick != 1000 {
		t.Errorf("首次接敌 StartTick = %d, want 1000", d1.fact.StartTick)
	}
	// 窗口内重复伤害：同一条接敌。
	if got := acc.getOrOpenDuel(p1, p2, 1100, now+1.0, "hurt"); got != d1 {
		t.Error("3s 内重复接触不应新建接敌")
	} else {
		// 模拟伤害处理器更新最后活动时间。
		d1.lastActivityTick = 1100
		d1.lastActivitySec = now + 1.0
	}

	// 超过 3s（相对最后活动 101s）：结束旧接敌并新建。
	d2 := acc.getOrOpenDuel(p1, p2, 1500, now+4.5, "hurt")
	if d2 == d1 {
		t.Fatal("超过重接窗口应新建接敌")
	}
	if d2.fact.StartTick != 1500 {
		t.Errorf("新接敌 StartTick = %d, want 1500", d2.fact.StartTick)
	}
	if len(rb.round.Duels) != 1 {
		t.Fatalf("旧接敌应已落入回合，得到 %d 条", len(rb.round.Duels))
	}
	if got := rb.round.Duels[0].EndTick; got != 1100 {
		t.Errorf("旧接敌 EndTick = %d, want 1100（最后活动 tick）", got)
	}

	// 收尾：新接敌 EndTick 缺失时用最后活动 tick = 创建时 tick。
	acc.flushDuels(rb)
	if len(rb.round.Duels) != 2 {
		t.Fatalf("flushDuels 后应有 2 条接敌，得到 %d", len(rb.round.Duels))
	}
	if rb.round.Duels[1].EndTick != 1500 {
		t.Errorf("新接敌 EndTick = %d, want 1500", rb.round.Duels[1].EndTick)
	}
	if len(acc.openDuels) != 0 {
		t.Errorf("flush 后仍有 %d 条进行中接敌", len(acc.openDuels))
	}
}

func TestDuelHurtAndShotCounting(t *testing.T) {
	d := &duelBuilder{
		fact: model.DuelFact{
			A: model.DuelPlayerFact{SteamID64: "1"},
			B: model.DuelPlayerFact{SteamID64: "2"},
		},
		aID: "1",
		bID: "2",
	}
	applyDuelHurt(d, "1", 27, 1000, 10)
	applyDuelHurt(d, "1", 13, 1010, 10.5)
	applyDuelHurt(d, "2", 40, 1020, 11)
	if d.fact.A.Hits != 2 || d.fact.A.Damage != 40 {
		t.Errorf("A 命中/伤害 = %d/%d, want 2/40", d.fact.A.Hits, d.fact.A.Damage)
	}
	if d.fact.B.Hits != 1 || d.fact.B.Damage != 40 {
		t.Errorf("B 命中/伤害 = %d/%d, want 1/40", d.fact.B.Hits, d.fact.B.Damage)
	}
	if d.lastActivityTick != 1020 || !almostEqual(d.lastActivitySec, 11) {
		t.Errorf("最后活动 = (%d, %v), want (1020, 11)", d.lastActivityTick, d.lastActivitySec)
	}

	opp := model.Vec3{X: 500} // 对手在正东
	// 对准对手的开火计入；第一枪补记首枪快照。
	applyShotToDuel(d, "1", model.Vec3{}, 0, 0, 120, true, 1030, 11.2, opp)
	applyShotToDuel(d, "1", model.Vec3{}, 0, 0, 90, true, 1040, 11.5, opp)
	applyShotToDuel(d, "2", model.Vec3{}, 0, 0, 40, true, 1045, 11.5, opp)
	applyShotToDuel(d, "999", model.Vec3{}, 0, 0, 0, false, 1050, 11.5, opp)
	// 没对准对手的枪不计入命中率分母。
	applyShotToDuel(d, "1", model.Vec3{}, 180, 0, 0, false, 1055, 11.6, opp)
	// 超过重接窗口的开枪不计入陈旧接敌。
	applyShotToDuel(d, "1", model.Vec3{}, 0, 0, 0, false, 1060, 15, opp)
	if d.fact.A.Shots != 2 || d.fact.B.Shots != 1 {
		t.Errorf("Shots = %d/%d, want 2/1", d.fact.A.Shots, d.fact.B.Shots)
	}
	if d.fact.A.FirstShotTick != 1030 || d.fact.A.Speed != 120 || !d.fact.A.SpeedValid {
		t.Errorf("A 首枪快照 = tick %d speed %v valid %v，want 1030/120/true",
			d.fact.A.FirstShotTick, d.fact.A.Speed, d.fact.A.SpeedValid)
	}
	if d.fact.A.TargetPos.X != 500 {
		t.Errorf("A TargetPos = %+v, want X=500", d.fact.A.TargetPos)
	}
	if d.fact.B.FirstShotTick != 1045 {
		t.Errorf("B 首枪 tick = %d, want 1045", d.fact.B.FirstShotTick)
	}
}

// TestParseS2DemoM2Facts 用真实 GOTV demo 做结构/范围校验（不断言具体数量）。
func TestParseS2DemoM2Facts(t *testing.T) {
	const demo = "../../testdata/demos/s2/s2.dem"
	if _, err := os.Stat(demo); err != nil {
		t.Skipf("测试 demo 不存在：%v", err)
	}
	m, err := Parse(demo, Options{FocusName: "Appl3Thr33"})
	if err != nil {
		t.Fatalf("Parse(%s): %v", demo, err)
	}
	if len(m.Rounds) == 0 {
		t.Fatal("解析结果没有回合")
	}

	var shots, validShots, duels int
	var killsWithSnapshot int
	for _, r := range m.Rounds {
		shots += len(r.Shots)
		for _, s := range r.Shots {
			if s.SpeedValid {
				validShots++
			}
			if s.Yaw < 0 || s.Yaw >= 360 {
				t.Errorf("第 %d 回合 ShotFact yaw 越界：%v", r.Number, s.Yaw)
			}
			if s.Tick < r.StartTick || s.Tick > r.EndTick {
				t.Errorf("第 %d 回合 ShotFact tick %d 不在 [%d,%d]", r.Number, s.Tick, r.StartTick, r.EndTick)
			}
		}
		for _, k := range r.Kills {
			if k.VictimID != "" && (k.VictimYaw < 0 || k.VictimYaw >= 360) {
				t.Errorf("第 %d 回合 KillEvent 受害者 yaw 越界：%v", r.Number, k.VictimYaw)
			}
			if len(k.OtherAlive) > 0 {
				killsWithSnapshot++
			}
			for _, o := range k.OtherAlive {
				if o.SteamID64 == "" || o.SteamID64 == k.VictimID {
					t.Errorf("第 %d 回合 OtherAlive 玩家 ID 异常：%q（victim=%q）", r.Number, o.SteamID64, k.VictimID)
				}
				if o.Side != model.SideT && o.Side != model.SideCT {
					t.Errorf("第 %d 回合 OtherAlive 阵营异常：%q", r.Number, o.Side)
				}
			}
		}
		duels += len(r.Duels)
		for _, d := range r.Duels {
			if d.StartTick <= 0 || d.EndTick < d.StartTick {
				t.Errorf("第 %d 回合 DuelFact tick 区间异常：start=%d end=%d", r.Number, d.StartTick, d.EndTick)
			}
			if d.StartTick < r.StartTick || d.EndTick > r.EndTick {
				t.Errorf("第 %d 回合 DuelFact [%d,%d] 超出回合 [%d,%d]",
					r.Number, d.StartTick, d.EndTick, r.StartTick, r.EndTick)
			}
			if d.A.SteamID64 == "" || d.B.SteamID64 == "" || d.A.SteamID64 == d.B.SteamID64 {
				t.Errorf("第 %d 回合 DuelFact 双方 ID 异常：A=%q B=%q", r.Number, d.A.SteamID64, d.B.SteamID64)
			}
			if d.A.Side == d.B.Side {
				t.Errorf("第 %d 回合 DuelFact 双方同阵营：A=%s B=%s", r.Number, d.A.Side, d.B.Side)
			}
			if d.KillTick != 0 && (d.KillTick < d.StartTick || d.KillTick > d.EndTick) {
				t.Errorf("第 %d 回合 DuelFact KillTick %d 不在 [%d,%d]", r.Number, d.KillTick, d.StartTick, d.EndTick)
			}
		}
	}
	t.Logf("s2.dem：回合=%d shots=%d speed_valid=%d duels=%d kills_with_snapshot=%d",
		len(m.Rounds), shots, validShots, duels, killsWithSnapshot)
	if shots == 0 {
		t.Error("应至少采集到一条 ShotFact")
	}
	if validShots == 0 {
		t.Error("应至少有一条 SpeedValid 的 ShotFact")
	}
	if duels == 0 {
		t.Error("应至少采集到一条 DuelFact")
	}
	if killsWithSnapshot == 0 {
		t.Error("应至少有一次击杀带 OtherAlive 站位快照")
	}
}

func TestFrameRingBefore(t *testing.T) {
	var r frameRing
	for _, s := range []frameSample{{tick: 97}, {tick: 98}, {tick: 99}, {tick: 100}} {
		r.push(s)
	}
	tests := []struct {
		name string
		tick int
		lag  int
		want int
	}{
		{"2 tick 回看", 101, 2, 99},
		{"同 tick 多帧：退回 2 tick 前", 100, 2, 98},
		{"没有足够旧样本时退回任意更早", 99, 2, 97},
		{"没有早于当前 tick 的样本时不取", 97, 0, 0},
		{"无样本用环内最旧", 200, 2, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.before(tt.tick, tt.lag)
			if tt.want == 0 {
				if got != nil {
					t.Errorf("before(%d,%d) = %+v, want nil", tt.tick, tt.lag, got)
				}
				return
			}
			if got == nil || got.tick != tt.want {
				t.Errorf("before(%d,%d) = %+v, want tick %d", tt.tick, tt.lag, got, tt.want)
			}
		})
	}
	var empty frameRing
	if got := empty.before(100, 2); got != nil {
		t.Errorf("空环应返回 nil，got %+v", got)
	}
}

func TestIsFirearmWeapon(t *testing.T) {
	cases := []struct {
		name string
		w    *common.Equipment
		want bool
	}{
		{"AK-47", &common.Equipment{Type: common.EqAK47}, true},
		{"AWP", &common.Equipment{Type: common.EqAWP}, true},
		{"USP", &common.Equipment{Type: common.EqUSP}, true},
		{"烟雾弹不算开枪", &common.Equipment{Type: common.EqSmoke}, false},
		{"闪光弹不算开枪", &common.Equipment{Type: common.EqFlash}, false},
		{"刀不算开枪", &common.Equipment{Type: common.EqKnife}, false},
		{"C4 不算开枪", &common.Equipment{Type: common.EqBomb}, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFirearmWeapon(tc.w); got != tc.want {
				t.Errorf("isFirearmWeapon(%v) = %v, want %v", tc.w, got, tc.want)
			}
		})
	}
}

func TestReorderDuelSidesAfterFirstShot(t *testing.T) {
	// 创建时双方都没开火：按 SteamID 排 A=2、B=1。
	d := &duelBuilder{
		fact: model.DuelFact{
			A: model.DuelPlayerFact{SteamID64: "2"},
			B: model.DuelPlayerFact{SteamID64: "1"},
		},
		aID: "2", bID: "1", lastActivitySec: 10,
	}
	// B（id=1）先开火：契约要求先开火者在前，必须交换。
	applyShotToDuel(d, "1", model.Vec3{}, 0, 0, 0, true, 100, 11, model.Vec3{X: 500})
	if d.aID != "1" || d.fact.A.SteamID64 != "1" || d.fact.B.SteamID64 != "2" {
		t.Errorf("先开火者应在 A：aID=%s A=%s B=%s", d.aID, d.fact.A.SteamID64, d.fact.B.SteamID64)
	}
	if d.fact.A.FirstShotTick != 100 {
		t.Errorf("A.FirstShotTick = %d want 100", d.fact.A.FirstShotTick)
	}

	// 双方都有开火 tick：更早者在前。
	d2 := &duelBuilder{
		fact: model.DuelFact{
			A: model.DuelPlayerFact{SteamID64: "1", FirstShotTick: 200},
			B: model.DuelPlayerFact{SteamID64: "2", FirstShotTick: 100},
		},
		aID: "1", bID: "2",
	}
	reorderDuelSides(d2)
	if d2.aID != "2" || d2.fact.A.FirstShotTick != 100 {
		t.Errorf("更早开火者应在 A：aID=%s tick=%d", d2.aID, d2.fact.A.FirstShotTick)
	}
}
