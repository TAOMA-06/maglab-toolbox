package parse

import (
	"math"
	"sort"

	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/common"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/events"

	"cs2-demo-review/internal/model"
)

// M2 parse 层事实常量（见 docs/M2-DESIGN.md 1.4）。
const (
	// duelSnapshotWindowSec：接敌快照取「首次 PlayerHurt 前 1s 内最近一次 WeaponFire」。
	duelSnapshotWindowSec = 1.0
	// duelReengageWindowSec：同一对玩家最后一次交火活动后 3s 无新活动即结束本次接敌。
	duelReengageWindowSec = 3.0
	// maxPlausibleSpeedUPS：位置差分超过该速度视为瞬移/换生，速度不可信（不钳制伪造）。
	maxPlausibleSpeedUPS = 2000.0
	// duelAimConeDeg：只有朝向对手（±该角度）的开火才计入本次接敌的开火数/首枪快照。
	// 避免一个接敌长时间开着时，把对别的目标的扫射也算进来（会虚增命中率分母）。
	duelAimConeDeg = 30.0
	// duelMaxAimDistance：超过该距离的开火不视为本次接敌（对枪通常发生在可视距离内）。
	duelMaxAimDistance = 3000.0
)

// frameSample 是某一 tick 的存活玩家位置采样（速度差分的基准）。
type frameSample struct {
	pos  model.Vec3
	tick int
	sec  float64
}

// frameRingSize / speedLookbackTicks 控制速度差分口径：
// 保留最近 4 个不同 tick 的位置；取「当前 tick - 2」的样本做差分（64 tick 下约 31ms），
// 既避开 POV 同 tick 多帧导致的 dt=0，又比单 tick 差分更抗位置量化噪声。
const (
	frameRingSize      = 4
	speedLookbackTicks = 2
)

// frameRing 是一名玩家最近若干 tick 的位置环（newest 在末尾，同 tick 只保留最后一帧）。
type frameRing struct {
	samples [frameRingSize]frameSample
	n       int
}

// push 写入一个采样：同 tick 覆盖，新 tick 追加，满了丢最旧。
func (r *frameRing) push(s frameSample) {
	if r == nil {
		return
	}
	if r.n > 0 && r.samples[r.n-1].tick == s.tick {
		r.samples[r.n-1] = s
		return
	}
	if r.n == frameRingSize {
		copy(r.samples[0:], r.samples[1:])
		r.n--
	}
	r.samples[r.n] = s
	r.n++
}

// before 返回严格早于当前 tick 的差分基准：
// 优先「tick - lag」及更早的最新样本；没有则退回任意更早样本。
func (r *frameRing) before(tick, lag int) *frameSample {
	if r == nil || r.n == 0 {
		return nil
	}
	want := tick - lag
	if want >= tick {
		want = tick - 1
	}
	for i := r.n - 1; i >= 0; i-- {
		if r.samples[i].tick <= want {
			return &r.samples[i]
		}
	}
	for i := r.n - 1; i >= 0; i-- {
		if r.samples[i].tick < tick {
			return &r.samples[i]
		}
	}
	return nil
}

// shotSample 是近期一次开枪的轻量快照，用于接敌瞬间回溯（只保留 snapshot 窗口内的样本）。
type shotSample struct {
	tick       int
	sec        float64
	pos        model.Vec3
	yaw        float64
	pitch      float64
	speed      float64
	speedValid bool
}

// duelBuilder 是进行中的接敌事实 + 活动时间。
type duelBuilder struct {
	fact             model.DuelFact
	aID              string
	bID              string
	aPlayer          *common.Player
	bPlayer          *common.Player
	lastActivityTick int
	lastActivitySec  float64
}

// resetM2RoundState 在非热身回合开始时重置 M2 采样状态（内存只保留本回合数据）。
func (a *accumulator) resetM2RoundState() {
	a.frameSamples = make(map[*common.Player]*frameRing)
	a.recentShots = make(map[*common.Player][]shotSample)
	a.openDuels = make(map[string]*duelBuilder)
}

// inActiveRound 表示当前处于非热身、未到 RoundEnd 的回合（M2 事实采样前置条件）。
func (a *accumulator) inActiveRound() bool {
	return a.inRound() && !a.p.GameState().IsWarmupPeriod()
}

// onFrameDone 每帧记录存活玩家的位置/时间，供 WeaponFire 做水平速度差分。
// 只在本回合进行中采样，且只保留存活玩家（死亡即从 map 删除，避免无界增长）。
// 同一 ingame tick 可能有多帧（POV）：同 tick 覆盖，不同 tick 追加进环。
func (a *accumulator) onFrameDone(events.FrameDone) {
	if !a.inActiveRound() {
		return
	}
	gs := a.p.GameState()
	tick := gs.IngameTick()
	sec := a.p.CurrentTime().Seconds()
	if a.frameSamples == nil {
		a.frameSamples = make(map[*common.Player]*frameRing)
	}
	for _, pl := range gs.Participants().Playing() {
		if pl == nil {
			continue
		}
		if !pl.IsAlive() {
			delete(a.frameSamples, pl)
			continue
		}
		ring := a.frameSamples[pl]
		if ring == nil {
			ring = &frameRing{}
			a.frameSamples[pl] = ring
		}
		ring.push(frameSample{pos: playerPos(pl), tick: tick, sec: sec})
	}
}

// lastFrameBefore 返回严格早于当前 tick 的差分基准（优先 2 tick 前，避开同 tick 多帧）。
func (a *accumulator) lastFrameBefore(pl *common.Player, tick int) *frameSample {
	return a.frameSamples[pl].before(tick, speedLookbackTicks)
}

// onWeaponFire 记录一次开枪事实（Shots），并推进进行中接敌的开枪计数。
// 只记录枪械（手枪/SMG/重武器/步枪）开火；刀挥、投掷物出手也会触发 WeaponFire，
// 但它们不是「开枪」，计入速度/预瞄分布会污染阈值校准。
func (a *accumulator) onWeaponFire(e events.WeaponFire) {
	if !a.inActiveRound() || e.Shooter == nil {
		return
	}
	if !isFirearmWeapon(e.Weapon) {
		return
	}
	shooter := e.Shooter
	tick := a.tick()
	sec := a.sec()
	pos := playerPos(shooter)
	yaw := normalizeYaw(float64(shooter.ViewDirectionX()))
	pitch := float64(shooter.ViewDirectionY())

	var last *frameSample
	last = a.lastFrameBefore(shooter, tick)
	speed, speedValid := shotSpeed(pos, sec, last)

	weapon := ""
	if e.Weapon != nil {
		weapon = e.Weapon.String()
	}

	a.cur.round.Shots = append(a.cur.round.Shots, model.ShotFact{
		Tick:        tick,
		RoundTime:   a.roundTime(tick),
		ShooterID:   idString(shooter.SteamID64),
		ShooterName: shooter.Name,
		ShooterSide: playerSide(shooter),
		Weapon:      weapon,
		Pos:         pos,
		Yaw:         yaw,
		Pitch:       pitch,
		Speed:       speed,
		SpeedValid:  speedValid,
	})
	a.upsertPlayer(shooter)

	// 近期开枪样本：只保留 snapshot 窗口内的样本。
	if a.recentShots == nil {
		a.recentShots = make(map[*common.Player][]shotSample)
	}
	shots := append(a.recentShots[shooter], shotSample{
		tick:       tick,
		sec:        sec,
		pos:        pos,
		yaw:        yaw,
		pitch:      pitch,
		speed:      speed,
		speedValid: speedValid,
	})
	a.recentShots[shooter] = pruneRecentShots(shots, sec, duelSnapshotWindowSec)

	// 该玩家若已在某个进行中的接敌里，本次开枪计入该侧 Shots；
	// 若是该侧第一次「对准对手」的开火，补记首枪快照（接敌可能由对手先手触发）。
	a.countShotInOpenDuels(shooter, pos, yaw, pitch, speed, speedValid, tick, sec)
}

// recordHurtDuel 把一次 PlayerHurt 写入接敌事实（命中/伤害/活动时间）。
func (a *accumulator) recordHurtDuel(e events.PlayerHurt) {
	if !a.inActiveRound() || e.Attacker == nil || e.Player == nil {
		return
	}
	attacker, victim := e.Attacker, e.Player
	if playerSide(attacker) == playerSide(victim) {
		return
	}
	if !isDuelDamageWeapon(e.Weapon) {
		return // 道具/世界伤害不算对枪：避免「先手命中但没开火」的假接敌
	}
	tick := a.tick()
	sec := a.sec()
	d := a.getOrOpenDuel(attacker, victim, tick, sec, "hurt")
	if d.fact.FirstHurtBy == "" {
		d.fact.FirstHurtBy = idString(attacker.SteamID64)
	}
	applyDuelHurt(d, idString(attacker.SteamID64), e.HealthDamage, tick, sec)
}

// isFirearmWeapon 判断一次 WeaponFire 是否来自枪械。
// 刀（EqClassEquipment）与投掷物（EqClassGrenade）也会触发 WeaponFire，必须排除。
func isFirearmWeapon(w *common.Equipment) bool {
	if w == nil {
		return false
	}
	switch w.Class() {
	case common.EqClassPistols, common.EqClassSMG, common.EqClassHeavy, common.EqClassRifle:
		return true
	default:
		return false
	}
}

// isDuelDamageWeapon 判断一次伤害是否来自枪械（对枪），而不是手雷/刀/世界伤害。
// 对枪事实只由「人对人武器伤害」驱动，否则扔雷也会伪造接敌与「先手命中」。
func isDuelDamageWeapon(w *common.Equipment) bool {
	if w == nil {
		return false
	}
	switch w.Type {
	case common.EqWorld, common.EqUnknown:
		return false
	}
	switch w.Class() {
	case common.EqClassGrenade, common.EqClassEquipment:
		return false
	}
	return true
}

// applyDuelHurt 把一次伤害记到攻击方一侧（命中数/伤害），并推进最后活动时间。
func applyDuelHurt(d *duelBuilder, attackerID string, damage, tick int, sec float64) {
	if d == nil {
		return
	}
	switch attackerID {
	case d.aID:
		d.fact.A.Hits++
		d.fact.A.Damage += damage
	case d.bID:
		d.fact.B.Hits++
		d.fact.B.Damage += damage
	}
	d.lastActivityTick = tick
	d.lastActivitySec = sec
}

// recordKillDuel 把一次击杀写入接敌事实；没有进行中的接敌时用当前快照新建一条。
func (a *accumulator) recordKillDuel(e events.Kill) {
	if !a.inActiveRound() || e.Killer == nil || e.Victim == nil {
		return
	}
	killer, victim := e.Killer, e.Victim
	if playerSide(killer) == playerSide(victim) {
		return
	}
	tick := a.tick()
	sec := a.sec()
	d := a.getOrOpenDuel(killer, victim, tick, sec, "kill")
	d.fact.KillTick = tick
	d.fact.KillerID = idString(killer.SteamID64)
	d.fact.VictimID = idString(victim.SteamID64)
	d.fact.Headshot = e.IsHeadshot
	if e.Weapon != nil {
		d.fact.Weapon = e.Weapon.String()
	}
	d.lastActivityTick = tick
	d.lastActivitySec = sec
	d.fact.EndTick = tick

	key := pairKey(d.aID, d.bID)
	a.appendDuel(d.fact)
	delete(a.openDuels, key)
}

// killSnapshotFacts 在死亡瞬间采样受害者视角与其余存活玩家的位置/朝向。
func (a *accumulator) killSnapshotFacts(e events.Kill) (victimYaw, victimPitch float64, otherAlive []model.PlayerSnapshot) {
	if e.Victim != nil {
		victimYaw = normalizeYaw(float64(e.Victim.ViewDirectionX()))
		victimPitch = float64(e.Victim.ViewDirectionY())
	}
	gs := a.p.GameState()
	for _, pl := range gs.Participants().Playing() {
		if pl == nil || pl == e.Victim || !pl.IsAlive() {
			continue
		}
		otherAlive = append(otherAlive, model.PlayerSnapshot{
			SteamID64: idString(pl.SteamID64),
			Name:      pl.Name,
			Side:      playerSide(pl),
			Pos:       playerPos(pl),
			Yaw:       normalizeYaw(float64(pl.ViewDirectionX())),
			Pitch:     float64(pl.ViewDirectionY()),
			HP:        pl.Health(),
		})
	}
	// 输出确定：按 SteamID64 字符串升序（同一 tick 内与事件顺序无关）。
	sort.Slice(otherAlive, func(i, j int) bool { return otherAlive[i].SteamID64 < otherAlive[j].SteamID64 })
	return victimYaw, victimPitch, otherAlive
}

// getOrOpenDuel 取该 pair 进行中的接敌；超过重接窗口则先结束旧的再开新的。
// source 只在新建时写入（hurt/kill），已有接敌保留原始证据来源。
func (a *accumulator) getOrOpenDuel(p1, p2 *common.Player, tick int, sec float64, source string) *duelBuilder {
	if a.openDuels == nil {
		a.openDuels = make(map[string]*duelBuilder)
	}
	id1 := idString(p1.SteamID64)
	id2 := idString(p2.SteamID64)
	key := pairKey(id1, id2)
	if d, ok := a.openDuels[key]; ok {
		if sec-d.lastActivitySec <= duelReengageWindowSec {
			return d
		}
		a.closeDuel(key)
	}
	d := a.newDuel(p1, p2, tick, sec, source)
	a.openDuels[key] = d
	return d
}

// newDuel 用两侧 1s 内最近一次开枪（无则当前值）构建接敌快照。
func (a *accumulator) newDuel(p1, p2 *common.Player, tick int, sec float64, source string) *duelBuilder {
	a1 := a.snapshotDuelPlayer(p1, sec, playerPos(p2))
	a2 := a.snapshotDuelPlayer(p2, sec, playerPos(p1))
	a1, a2 = orderDuelSides(a1, a2)
	pA, pB := p1, p2
	if a1.SteamID64 == idString(p2.SteamID64) {
		pA, pB = p2, p1
	}
	start := duelStartTick(a1, a2, tick)
	fact := model.DuelFact{
		StartTick: start,
		RoundTime: a.roundTime(start),
		Contact:   source,
		A:         a1,
		B:         a2,
	}
	return &duelBuilder{
		fact:             fact,
		aID:              a1.SteamID64,
		bID:              a2.SteamID64,
		aPlayer:          pA,
		bPlayer:          pB,
		lastActivityTick: tick,
		lastActivitySec:  sec,
	}
}

// snapshotDuelPlayer 构造接敌一侧快照：优先最近一次开枪，否则当前 Position/视角。
// oppPos 是对手在该时刻的位置，写入 TargetPos 供预瞄角度差使用（只算对准对手的开火）。
func (a *accumulator) snapshotDuelPlayer(pl *common.Player, nowSec float64, oppPos model.Vec3) model.DuelPlayerFact {
	f := model.DuelPlayerFact{
		SteamID64: idString(pl.SteamID64),
		Name:      pl.Name,
		Side:      playerSide(pl),
	}
	shots := a.recentShots[pl]
	if s, ok := lastShotWithin(shots, nowSec, duelSnapshotWindowSec); ok {
		f.Pos = s.pos
		f.Yaw = s.yaw
		f.Pitch = s.pitch
		f.Speed = s.speed
		f.SpeedValid = s.speedValid
		f.FirstShotTick = s.tick
		f.TargetPos = oppPos
		f.Shots = countAimedRecentShots(shots, nowSec, duelSnapshotWindowSec, oppPos)
		return f
	}
	f.Pos = playerPos(pl)
	f.Yaw = normalizeYaw(float64(pl.ViewDirectionX()))
	f.Pitch = float64(pl.ViewDirectionY())
	f.TargetPos = oppPos
	return f
}

// countShotInOpenDuels 给该 shooter 参与的所有进行中接敌累加「对准对手」的开火数。
// 超过重接窗口的陈旧接敌不计（下一次伤害会结束旧接敌并开新的）。
// 若该侧尚未记录首枪（接敌由对手先手触发），本次开火补记首枪快照与 TargetPos。
func (a *accumulator) countShotInOpenDuels(shooter *common.Player, pos model.Vec3, yaw, pitch, speed float64, speedValid bool, tick int, nowSec float64) {
	id := idString(shooter.SteamID64)
	// 同一枪只归属「最对准」的那一条接敌，避免一枪同时给多个接敌刷 Shots。
	var best *duelBuilder
	var bestOpp model.Vec3
	bestDelta := duelAimConeDeg + 1
	for _, d := range a.openDuels {
		if nowSec-d.lastActivitySec > duelReengageWindowSec {
			continue
		}
		var other *common.Player
		switch id {
		case d.aID:
			other = d.bPlayer
		case d.bID:
			other = d.aPlayer
		default:
			continue
		}
		if other == nil || !other.IsAlive() {
			continue
		}
		oppPos := playerPos(other)
		if horizontalDist(pos, oppPos) > duelMaxAimDistance {
			continue
		}
		delta := yawDeltaDeg(yaw, bearingTo(pos, oppPos))
		if delta > duelAimConeDeg || delta >= bestDelta {
			continue
		}
		best, bestOpp, bestDelta = d, oppPos, delta
	}
	if best != nil {
		applyShotToDuel(best, id, pos, yaw, pitch, speed, speedValid, tick, nowSec, bestOpp)
	}
}

// applyShotToDuel 把一次开火应用到单条接敌（纯函数，便于单测）：
// 只对准对手的开火计入；该侧第一次开火补记预瞄快照与 TargetPos。
func applyShotToDuel(d *duelBuilder, shooterID string, pos model.Vec3, yaw, pitch, speed float64, speedValid bool, tick int, nowSec float64, oppPos model.Vec3) {
	if d == nil || nowSec-d.lastActivitySec > duelReengageWindowSec {
		return
	}
	var side *model.DuelPlayerFact
	switch shooterID {
	case d.aID:
		side = &d.fact.A
	case d.bID:
		side = &d.fact.B
	default:
		return
	}
	if !shotAimedAt(pos, yaw, oppPos) {
		return
	}
	side.Shots++
	if side.FirstShotTick == 0 {
		side.Pos = pos
		side.Yaw = yaw
		side.Pitch = pitch
		side.Speed = speed
		side.SpeedValid = speedValid
		side.FirstShotTick = tick
		side.TargetPos = oppPos
	}
	// 契约：A/B 按先开火者在前。接敌可能由「双方都没开火」的状态创建，
	// 之后某侧先开火时必须重排，否则 A/B 顺序会停在 SteamID 排序。
	reorderDuelSides(d)
}

// reorderDuelSides 按契约重排 A/B：先开火者在前；都未开火按 SteamID 升序；
// 同一 tick 开火按 SteamID 升序。同时交换玩家引用，供后续射击归属使用。
func reorderDuelSides(d *duelBuilder) {
	if d == nil {
		return
	}
	a, b := d.fact.A, d.fact.B
	swap := false
	switch {
	case a.FirstShotTick > 0 && b.FirstShotTick == 0:
		// A 已在前
	case b.FirstShotTick > 0 && a.FirstShotTick == 0:
		swap = true
	case a.FirstShotTick > 0 && b.FirstShotTick > 0 && b.FirstShotTick < a.FirstShotTick:
		swap = true
	case a.FirstShotTick > 0 && b.FirstShotTick > 0 && b.FirstShotTick == a.FirstShotTick && b.SteamID64 < a.SteamID64:
		swap = true
	case a.FirstShotTick == 0 && b.FirstShotTick == 0 && b.SteamID64 < a.SteamID64:
		swap = true
	}
	if !swap {
		return
	}
	d.fact.A, d.fact.B = d.fact.B, d.fact.A
	d.aID, d.bID = d.bID, d.aID
	d.aPlayer, d.bPlayer = d.bPlayer, d.aPlayer
}

// closeDuel 结束一条进行中的接敌（EndTick = 最后活动 tick），并落入当前回合。
func (a *accumulator) closeDuel(key string) {
	d, ok := a.openDuels[key]
	if !ok {
		return
	}
	d.fact.EndTick = d.lastActivityTick
	if d.fact.EndTick == 0 {
		d.fact.EndTick = d.fact.StartTick
	}
	a.appendDuel(d.fact)
	delete(a.openDuels, key)
}

// flushDuels 回合收尾：结束全部进行中的接敌，并按 StartTick 升序写入 Round.Duels。
func (a *accumulator) flushDuels(rb *roundBuilder) {
	if rb == nil {
		return
	}
	for key := range a.openDuels {
		a.closeDuel(key)
	}
	sort.SliceStable(rb.round.Duels, func(i, j int) bool {
		if rb.round.Duels[i].StartTick != rb.round.Duels[j].StartTick {
			return rb.round.Duels[i].StartTick < rb.round.Duels[j].StartTick
		}
		if rb.round.Duels[i].A.SteamID64 != rb.round.Duels[j].A.SteamID64 {
			return rb.round.Duels[i].A.SteamID64 < rb.round.Duels[j].A.SteamID64
		}
		return rb.round.Duels[i].B.SteamID64 < rb.round.Duels[j].B.SteamID64
	})
	a.openDuels = make(map[string]*duelBuilder)
}

// appendDuel 把一条已结束的接敌写入当前回合。
func (a *accumulator) appendDuel(fact model.DuelFact) {
	if a.cur == nil {
		return
	}
	a.cur.round.Duels = append(a.cur.round.Duels, fact)
}

// ---------- 纯函数（可单测） ----------

// normalizeYaw 把任意角度归一化到 [0, 360)。
func normalizeYaw(yaw float64) float64 {
	if math.IsNaN(yaw) || math.IsInf(yaw, 0) {
		return 0
	}
	v := math.Mod(yaw, 360)
	if v < 0 {
		v += 360
	}
	if v >= 360 {
		v = 0
	}
	return v
}

// horizontalDist 返回两点的水平（X/Y）距离。
func horizontalDist(a, b model.Vec3) float64 {
	dx := a.X - b.X
	dy := a.Y - b.Y
	return math.Hypot(dx, dy)
}

// shotSpeed 用相邻帧位置差分算水平速度（u/s）。
// 无上一采样、dt<=0、距离为负或速度超上限（瞬移）时返回 (0, false)，绝不钳制伪造。
func shotSpeed(nowPos model.Vec3, nowSec float64, last *frameSample) (float64, bool) {
	if last == nil {
		return 0, false
	}
	dt := nowSec - last.sec
	if dt <= 0 {
		return 0, false
	}
	dist := horizontalDist(nowPos, last.pos)
	if dist < 0 {
		return 0, false
	}
	speed := dist / dt
	if speed < 0 || speed > maxPlausibleSpeedUPS {
		return 0, false
	}
	return speed, true
}

// pairKey 生成与 A/B 顺序无关的配对键（SteamID64 字符串升序拼接）。
func pairKey(id1, id2 string) string {
	if id1 > id2 {
		id1, id2 = id2, id1
	}
	return id1 + "|" + id2
}

// pruneRecentShots 丢弃窗口外（含未来时间）的样本，保持内存有界。
func pruneRecentShots(shots []shotSample, nowSec, window float64) []shotSample {
	kept := shots[:0]
	for _, s := range shots {
		if s.sec > nowSec {
			continue
		}
		if nowSec-s.sec > window {
			continue
		}
		kept = append(kept, s)
	}
	return kept
}

// lastShotWithin 返回窗口内最近一次开枪样本（窗口端点包含）。
func lastShotWithin(shots []shotSample, nowSec, window float64) (shotSample, bool) {
	var best shotSample
	found := false
	for _, s := range shots {
		if s.sec > nowSec || nowSec-s.sec > window {
			continue
		}
		if !found || s.sec >= best.sec {
			best = s
			found = true
		}
	}
	return best, found
}

// countRecentShots 统计窗口内的开枪数（接敌初始 Shots）。
func countRecentShots(shots []shotSample, nowSec, window float64) int {
	n := 0
	for _, s := range shots {
		if s.sec > nowSec || nowSec-s.sec > window {
			continue
		}
		n++
	}
	return n
}

// countAimedRecentShots 统计窗口内「朝向 target」的开火数（接敌初始 Shots）。
// 未对准对手的枪（例如同时在打另一个目标）不计入命中率分母。
func countAimedRecentShots(shots []shotSample, nowSec, window float64, target model.Vec3) int {
	n := 0
	for _, s := range shots {
		if s.sec > nowSec || nowSec-s.sec > window {
			continue
		}
		if shotAimedAt(s.pos, s.yaw, target) {
			n++
		}
	}
	return n
}

// shotAimedAt 判断一次开火是否朝向 target：距离不超过 duelMaxAimDistance，
// 且水平视角与「射击点→target」方位夹角不超过 duelAimConeDeg。
func shotAimedAt(pos model.Vec3, yaw float64, target model.Vec3) bool {
	if horizontalDist(pos, target) > duelMaxAimDistance {
		return false
	}
	return yawDeltaDeg(yaw, bearingTo(pos, target)) <= duelAimConeDeg
}

// bearingTo 返回从 from 指向 to 的水平方位角（0..360，CS 坐标系 atan2(dy,dx)）。
func bearingTo(from, to model.Vec3) float64 {
	return normalizeYaw(math.Atan2(to.Y-from.Y, to.X-from.X) * 180 / math.Pi)
}

// yawDeltaDeg 返回两个水平角的最短夹角（0..180 度）。
func yawDeltaDeg(a, b float64) float64 {
	d := math.Mod(math.Abs(normalizeYaw(a)-normalizeYaw(b)), 360)
	if d > 180 {
		d = 360 - d
	}
	return d
}

// orderDuelSides 按契约排序 A/B：先开火者在前（比较各自快照 tick）；
// 都未开火或同一 tick 时按 SteamID64 字符串升序。
func orderDuelSides(a, b model.DuelPlayerFact) (model.DuelPlayerFact, model.DuelPlayerFact) {
	aShot := a.FirstShotTick > 0
	bShot := b.FirstShotTick > 0
	switch {
	case aShot && !bShot:
		return a, b
	case bShot && !aShot:
		return b, a
	case aShot && bShot && a.FirstShotTick != b.FirstShotTick:
		if a.FirstShotTick < b.FirstShotTick {
			return a, b
		}
		return b, a
	default:
		if a.SteamID64 <= b.SteamID64 {
			return a, b
		}
		return b, a
	}
}

// duelStartTick 取两侧首个非零开枪 tick 的较小值；都无开枪则用当前事件 tick。
func duelStartTick(a, b model.DuelPlayerFact, fallback int) int {
	start := 0
	for _, t := range []int{a.FirstShotTick, b.FirstShotTick} {
		if t > 0 && (start == 0 || t < start) {
			start = t
		}
	}
	if start == 0 {
		return fallback
	}
	return start
}
