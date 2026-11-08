// Package parse 是解析层：用 demoinfocs-golang 把 .dem 解析为 model.Match 结构化事实。
// 设计锁（design.md）：本层只提事实，不做任何规则/归因；数字与结论由 tactics 层产生。
package parse

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	demoinfocs "github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/common"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/events"

	"cs2-demo-review/internal/model"
)

// fallbackTickRate 是 demo header 损坏、TickRate() 不可用时的兜底值（CS2 标准服务器 128 tick）。
const fallbackTickRate = 128

// heDamageWindowSec 是 HE 爆炸后归因 PlayerHurt 伤害的时间窗口（契约规定 2 秒）。
const heDamageWindowSec = 2.0

// molotovMatchWindowSec 是 InfernoStart 回溯匹配最近一次燃烧瓶投掷的最长窗口。
const molotovMatchWindowSec = 15.0

// Options 控制解析行为。
type Options struct {
	// FocusSteamID 是焦点玩家 SteamID64 十进制字符串；空则自动：POV 玩家 > 报错列出名单。
	FocusSteamID string
	// FocusName 按名字匹配（忽略大小写，精确匹配）；与 FocusSteamID 二选一。
	FocusName string
}

// Parse 解析 .dem 文件，返回完整事实 Match。
func Parse(demoPath string, opts Options) (*model.Match, error) {
	f, err := os.Open(demoPath)
	if err != nil {
		return nil, fmt.Errorf("打开 demo 文件失败: %w", err)
	}
	defer f.Close()

	p := demoinfocs.NewParser(f)
	acc := newAccumulator(demoPath, opts)
	acc.p = p
	acc.register(p)

	if err := p.ParseToEnd(); err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("解析 demo 失败: %w", err)
	}

	// 收尾读取 demo 级信息（解析过程中已缓存 tickRate，这里以最终值为准）。
	if r := p.TickRate(); r > 0 {
		acc.tickRate = r
	}
	acc.duration = p.CurrentTime().Seconds()
	_ = p.Close()

	return acc.finish()
}

// ProbeResult 是 demo 预检结果：玩家名单 + POV 录制者（GUI 选人用，不要求焦点）。
type ProbeResult struct {
	Players       []model.PlayerInfo `json:"players"`
	POVPlayer     string             `json:"povPlayer,omitempty"`
	POVPlayerName string             `json:"povPlayerName,omitempty"`
}

// Probe 只解析玩家名单与 POV 信息，不要求焦点、不做归因。
// 用途：GUI 在正式解析前展示玩家下拉框（非 POV demo 必须先选人）。
func Probe(demoPath string) (*ProbeResult, error) {
	f, err := os.Open(demoPath)
	if err != nil {
		return nil, fmt.Errorf("打开 demo 文件失败: %w", err)
	}
	defer f.Close()

	p := demoinfocs.NewParser(f)
	acc := newAccumulator(demoPath, Options{})
	acc.p = p
	acc.register(p)
	if err := p.ParseToEnd(); err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("解析 demo 失败: %w", err)
	}
	_ = p.Close()

	refs := acc.playerRefs()
	if acc.pov != nil && !containsRef(refs, *acc.pov) {
		refs = append(refs, *acc.pov)
	}
	out := &ProbeResult{}
	for _, r := range refs {
		out.Players = append(out.Players, model.PlayerInfo{SteamID64: r.steamID, Name: r.name, IsBot: r.isBot})
	}
	if acc.pov != nil {
		out.POVPlayer = acc.pov.idString()
		out.POVPlayerName = acc.pov.name
	}
	return out, nil
}

// ParseToFile 解析并写 JSON（缩进 2 空格）。
func ParseToFile(demoPath, outPath string, opts Options) error {
	m, err := Parse(demoPath, opts)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 Match 失败: %w", err)
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return fmt.Errorf("写入 JSON 失败: %w", err)
	}
	return nil
}

// accumulator 是解析期的唯一状态容器。事件回调在 ParseToEnd 中同步调用，无需加锁。
type accumulator struct {
	p          demoinfocs.Parser
	opts       Options
	sourceFile string
	mapName    string
	tickRate   float64
	duration   float64

	// 比分自己累计：RoundEnd 时 TeamState.Score() 尚未 +1，需要自己给胜方 +1。
	tScore, ctScore int
	roundSeq        int
	rounds          []model.Round

	half     halfTracker
	cur      *roundBuilder
	lastTick int
	lastSec  float64

	players map[*common.Player]model.PlayerInfo
	pov     *playerRef

	// 道具跟踪：投掷（GrenadeProjectileThrow）→ 效果事件按实体 ID / UniqueID 关联。
	utils       map[int64]*utilBuilder // 投掷 UniqueID -> 投掷事实
	utilsByEnt  map[int]int64          // GrenadeEntityID -> UniqueID（实体 ID 会复用，销毁时清除）
	heBlasts    []*utilBuilder         // 已爆炸、伤害窗口未关闭的 HE
	molotovWait map[*common.Player][]*utilBuilder

	// M2 事实采样（每回合重置，见 m2.go）。
	frameSamples map[*common.Player]*frameRing   // 存活玩家最近若干 tick 位置（速度差分）
	recentShots  map[*common.Player][]shotSample // 1s 内近期开枪（接敌快照回溯）
	openDuels    map[string]*duelBuilder         // pairKey -> 进行中的接敌
}

// roundBuilder 是回合建造中的事实集合。
type roundBuilder struct {
	round     model.Round
	order     []*common.Player
	players   map[*common.Player]*model.RoundPlayer
	utilsList []*utilBuilder

	moneyAtStart map[*common.Player]int // 回合开始现金（Money() 在回合中会变，须在 RoundStart 采样）
	econDone     bool
	ended        bool // RoundEnd 已到：停止记账，等待 RoundEndOfficial 定 EndTick
	finalized    bool
	endTick      int
	endSec       float64
}

// utilBuilder 是一次道具投掷的建造中事实。
type utilBuilder struct {
	ev           model.UtilityEvent
	uniqueID     int64
	entityID     int
	thrower      *common.Player
	throwSec     float64 // 投掷时刻的 demo 相对秒数
	lastKnownPos model.Vec3
	landSet      bool

	heExploded   bool
	heExplodeSec float64
}

func newAccumulator(demoPath string, opts Options) *accumulator {
	return &accumulator{
		opts:       opts,
		sourceFile: demoPath,
		// v5.2.0 未导出 header.MapName，按契约用 demo 文件名（不含扩展名）占位，绝不编造地图名。
		mapName:     mapNameFromPath(demoPath),
		players:     make(map[*common.Player]model.PlayerInfo),
		utils:       make(map[int64]*utilBuilder),
		utilsByEnt:  make(map[int]int64),
		molotovWait: make(map[*common.Player][]*utilBuilder),
	}
}

// register 注册全部事件处理器（回调同步执行）。
func (a *accumulator) register(p demoinfocs.Parser) {
	p.RegisterEventHandler(func(e events.RoundStart) { a.onRoundStart(e) })
	p.RegisterEventHandler(func(e events.RoundFreezetimeEnd) { a.onRoundFreezetimeEnd(e) })
	p.RegisterEventHandler(func(e events.RoundEnd) { a.onRoundEnd(e) })
	p.RegisterEventHandler(func(e events.RoundEndOfficial) { a.onRoundEndOfficial(e) })
	p.RegisterEventHandler(func(e events.Kill) { a.onKill(e) })
	p.RegisterEventHandler(func(e events.PlayerHurt) { a.onPlayerHurt(e) })
	p.RegisterEventHandler(func(e events.WeaponFire) { a.onWeaponFire(e) })
	p.RegisterEventHandler(func(e events.FrameDone) { a.onFrameDone(e) })
	p.RegisterEventHandler(func(e events.GrenadeProjectileThrow) { a.onGrenadeThrow(e) })
	p.RegisterEventHandler(func(e events.GrenadeProjectileDestroy) { a.onGrenadeDestroy(e) })
	p.RegisterEventHandler(func(e events.HeExplode) { a.onHeExplode(e) })
	p.RegisterEventHandler(func(e events.FlashExplode) { a.onNadeEffectStart(e.GrenadeEvent) })
	p.RegisterEventHandler(func(e events.SmokeStart) { a.onNadeEffectStart(e.GrenadeEvent) })
	p.RegisterEventHandler(func(e events.SmokeExpired) { a.onNadeEffectEnd(e.GrenadeEvent) })
	p.RegisterEventHandler(func(e events.DecoyStart) { a.onNadeEffectStart(e.GrenadeEvent) })
	p.RegisterEventHandler(func(e events.DecoyExpired) { a.onNadeEffectEnd(e.GrenadeEvent) })
	p.RegisterEventHandler(func(e events.InfernoStart) { a.onInfernoStart(e) })
	p.RegisterEventHandler(func(e events.InfernoExpired) { a.onInfernoExpired(e) })
	p.RegisterEventHandler(func(e events.PlayerFlashed) { a.onPlayerFlashed(e) })
	p.RegisterEventHandler(func(e events.BombPlantBegin) {
		a.onBombEvent("plant_begin", e.Site, e.Player)
	})
	p.RegisterEventHandler(func(e events.BombPlanted) {
		a.onBombEvent("planted", e.Site, e.Player)
	})
	p.RegisterEventHandler(func(e events.BombDefuseStart) {
		// v5.2.0 的 BombDefuseStart 无 Site 字段：Site 记空串，坐标取拆包者当时位置。
		a.onBombEvent("defuse_begin", events.BomsiteUnknown, e.Player)
	})
	p.RegisterEventHandler(func(e events.BombDefused) {
		a.onBombEvent("defused", e.Site, e.Player)
	})
	p.RegisterEventHandler(func(e events.BombExplode) {
		a.onBombEvent("exploded", e.Site, e.Player)
	})
	p.RegisterEventHandler(func(e events.BombDropped) {
		a.onBombEvent("drop", events.BomsiteUnknown, e.Player)
	})
	p.RegisterEventHandler(func(e events.BombPickup) {
		a.onBombEvent("pickup", events.BomsiteUnknown, e.Player)
	})
	p.RegisterEventHandler(func(e events.POVRecordingPlayerDetected) { a.onPOV(e) })
	p.RegisterEventHandler(func(e events.TeamSideSwitch) { a.onTeamSideSwitch() })
}

// ---------- 基础工具 ----------

// tick 返回当前 ingame tick（契约：所有 tick 一律用 gs.IngameTick()）。
func (a *accumulator) tick() int {
	t := a.p.GameState().IngameTick()
	a.lastTick = t
	return t
}

// sec 返回当前 demo 相对秒数。
func (a *accumulator) sec() float64 {
	s := a.p.CurrentTime().Seconds()
	a.lastSec = s
	return s
}

// rate 返回 tick rate（header 损坏时兜底 128）。
func (a *accumulator) rate() float64 {
	if a.tickRate > 0 {
		return a.tickRate
	}
	r := a.p.TickRate()
	if r <= 0 {
		r = fallbackTickRate
	}
	a.tickRate = r
	return r
}

// roundTime 把 ingame tick 换算为回合内秒数（自回合开始，含冻结）。
func (a *accumulator) roundTime(tick int) float64 {
	if a.cur == nil {
		return 0
	}
	return float64(tick-a.cur.round.StartTick) / a.rate()
}

// inRound 表示当前有进行中的非热身回合且未到 RoundEnd。
func (a *accumulator) inRound() bool {
	return a.cur != nil && !a.cur.ended
}

// upsertPlayer 登记/更新选手身份（名字以最后见到的为准）。
func (a *accumulator) upsertPlayer(pl *common.Player) {
	if pl == nil {
		return
	}
	a.players[pl] = model.PlayerInfo{SteamID64: pl.SteamID64, Name: pl.Name, IsBot: pl.IsBot}
}

// ---------- 回合生命周期 ----------

func (a *accumulator) onRoundStart(events.RoundStart) {
	// 契约：热身回合全部过滤（RoundStart 时点判断）。
	if a.p.GameState().IsWarmupPeriod() {
		return
	}
	// 上一回合若缺 RoundEndOfficial，此刻按 RoundEnd 记录的 tick 兜底收尾。
	if a.cur != nil {
		a.finalizeRound()
	}

	a.roundSeq++
	rb := &roundBuilder{
		moneyAtStart: make(map[*common.Player]int),
		players:      make(map[*common.Player]*model.RoundPlayer),
	}
	tick := a.tick()
	sec := a.sec()
	rb.round = model.Round{
		Number:    a.roundSeq,
		StartTick: tick,
		StartSec:  sec,
		Half:      a.half.beginRound(),
	}

	// 回合开始采样：全员现金（SideEconomy.Money 的事实来源）+ 每人一条 RoundPlayer。
	for _, pl := range a.p.GameState().Participants().Playing() {
		if pl == nil {
			continue
		}
		rb.moneyAtStart[pl] = pl.Money()
		rb.roundPlayer(pl)
		a.upsertPlayer(pl)
	}

	// Alive 序列第一条：回合开始的双方实际存活数。
	t, ct := a.aliveCounts()
	rb.round.Alive = append(rb.round.Alive, model.AliveCount{Tick: tick, TAlive: t, CTAlive: ct})

	a.cur = rb
	a.resetM2RoundState()
}

// roundPlayer 取或建该回合的 RoundPlayer 条目。
func (rb *roundBuilder) roundPlayer(pl *common.Player) *model.RoundPlayer {
	if rp, ok := rb.players[pl]; ok {
		return rp
	}
	rp := &model.RoundPlayer{
		SteamID64: pl.SteamID64,
		Name:      pl.Name,
		Side:      playerSide(pl),
	}
	rb.players[pl] = rp
	rb.order = append(rb.order, pl)
	return rp
}

func (a *accumulator) onRoundFreezetimeEnd(events.RoundFreezetimeEnd) {
	if a.cur == nil || a.cur.ended {
		return
	}
	a.sampleEconomy()
}

// sampleEconomy 在冻结结束时点采样经济（契约细则 3）。
func (a *accumulator) sampleEconomy() {
	rb := a.cur
	if rb == nil || rb.econDone {
		return
	}
	gs := a.p.GameState()
	var tPlayers, ctPlayers []model.PlayerEconomy

	for _, pl := range gs.Participants().Playing() {
		if pl == nil {
			continue
		}
		money, ok := rb.moneyAtStart[pl]
		if !ok {
			// 回合中途连入的玩家没有 RoundStart 采样，取此刻现金并如实记录。
			money = pl.Money()
		}
		pe := model.PlayerEconomy{
			SteamID64:  pl.SteamID64,
			Name:       pl.Name,
			Money:      money,
			EquipValue: pl.EquipmentValueFreezeTimeEnd(),
			HasHelmet:  pl.HasHelmet(),
			HasDefuse:  pl.HasDefuseKit(),
			Weapons:    weaponNames(pl.Weapons()),
		}
		switch playerSide(pl) {
		case model.SideT:
			tPlayers = append(tPlayers, pe)
		case model.SideCT:
			ctPlayers = append(ctPlayers, pe)
		}
		a.upsertPlayer(pl)
	}

	tState := gs.TeamTerrorists()
	ctState := gs.TeamCounterTerrorists()
	if tState != nil {
		rb.round.Economy.T = aggregateSideEconomy(tPlayers,
			tState.RoundStartEquipmentValue(), tState.FreezeTimeEndEquipmentValue(), tState.MoneySpentThisRound())
	} else {
		rb.round.Economy.T = aggregateSideEconomy(tPlayers, 0, 0, 0)
	}
	if ctState != nil {
		rb.round.Economy.CT = aggregateSideEconomy(ctPlayers,
			ctState.RoundStartEquipmentValue(), ctState.FreezeTimeEndEquipmentValue(), ctState.MoneySpentThisRound())
	} else {
		rb.round.Economy.CT = aggregateSideEconomy(ctPlayers, 0, 0, 0)
	}
	rb.econDone = true
}

func (a *accumulator) onRoundEnd(e events.RoundEnd) {
	if a.cur == nil || a.cur.ended {
		return
	}
	rb := a.cur
	rb.ended = true
	rb.endTick = a.tick()
	rb.endSec = a.sec()
	rb.round.EndReason = roundEndReasonString(e.Reason)
	rb.round.Winner = winnerSide(e.Winner)
	// 若缺 RoundFreezetimeEnd，此刻兜底采样经济（数据为该时刻真实观察值）。
	if !rb.econDone {
		a.sampleEconomy()
	}
}

func (a *accumulator) onRoundEndOfficial(events.RoundEndOfficial) {
	if a.cur == nil || a.cur.finalized {
		return
	}
	// 契约：EndTick 用 RoundEndOfficial 的 tick（RoundEnd 缺失时上面的兜底值仍在）。
	if a.cur.ended {
		a.cur.endTick = a.tick()
		a.cur.endSec = a.sec()
	}
	a.finalizeRound()
}

// finalizeRound 定案当前回合：写 EndTick/EndSec、累计比分、收尾玩家条目。
func (a *accumulator) finalizeRound() {
	rb := a.cur
	if rb == nil || rb.finalized {
		return
	}
	rb.finalized = true

	endTick, endSec := rb.endTick, rb.endSec
	if endTick == 0 {
		// 既无 RoundEndOfficial 也无 RoundEnd：用最后观察到的 tick 收尾。
		endTick = a.lastTick
		endSec = a.lastSec
	}
	rb.round.EndTick = endTick
	rb.round.EndSec = endSec

	// 比分自己累计（RoundEnd 时 TeamState.Score() 未 +1）。
	switch rb.round.Winner {
	case model.SideT:
		a.tScore++
	case model.SideCT:
		a.ctScore++
	}
	rb.round.ScoreAfter = model.Score{T: a.tScore, CT: a.ctScore}

	// RoundPlayer 收尾 + 回合选手列表定序。
	for _, pl := range rb.order {
		rp := rb.players[pl]
		rp.AliveAtEnd = rp.DeathTick == 0
		rb.round.Players = append(rb.round.Players, *rp)
	}

	// 道具事实定序；未收到效果/销毁事件的投掷用最后轨迹点补落点。
	for _, u := range rb.utilsList {
		if !u.landSet {
			u.ev.LandPos = u.lastKnownPos
			u.landSet = true
		}
		rb.round.Utility = append(rb.round.Utility, u.ev)
	}

	// M2：结束全部进行中的接敌并按 StartTick 升序写入 Round.Duels。
	a.flushDuels(rb)

	a.rounds = append(a.rounds, rb.round)
	a.cur = nil
}

// ---------- 击杀 / 伤害 ----------

func (a *accumulator) onKill(e events.Kill) {
	if !a.inRound() {
		return
	}
	rb := a.cur
	tick := a.tick()
	ke := model.KillEvent{
		Tick:          tick,
		RoundTime:     a.roundTime(tick),
		IsHeadshot:    e.IsHeadshot,
		ThroughSmoke:  e.ThroughSmoke,
		NoScope:       e.NoScope,
		AttackerBlind: e.AttackerBlind,
		Wallbang:      e.PenetratedObjects > 0,
		Distance:      float64(e.Distance),
	}
	// 世界击杀（Killer nil）/ 损坏 demo（Victim nil）：对应字段留空，绝不解引用 nil。
	if e.Killer != nil {
		ke.KillerID = idString(e.Killer.SteamID64)
		ke.KillerName = e.Killer.Name
		ke.KillerSide = playerSide(e.Killer)
		ke.KillerPos = playerPos(e.Killer)
		rb.roundPlayer(e.Killer).Kills++
		a.upsertPlayer(e.Killer)
	}
	if e.Victim != nil {
		ke.VictimID = idString(e.Victim.SteamID64)
		ke.VictimName = e.Victim.Name
		ke.VictimSide = playerSide(e.Victim)
		ke.VictimPos = playerPos(e.Victim)
		// M2 站位事实：受害者视角 + 其余存活玩家位置/朝向快照。
		ke.VictimYaw, ke.VictimPitch, ke.OtherAlive = a.killSnapshotFacts(e)
		a.upsertPlayer(e.Victim)
	}
	if e.Assister != nil {
		ke.AssisterID = idString(e.Assister.SteamID64)
		ke.AssisterName = e.Assister.Name
		rb.roundPlayer(e.Assister).Assists++
		a.upsertPlayer(e.Assister)
	}
	if e.Weapon != nil {
		ke.Weapon = e.Weapon.String()
		ke.WeaponClass = weaponClassName(e.Weapon.Class())
	}
	rb.round.Kills = append(rb.round.Kills, ke)

	isFirst := len(rb.round.Kills) == 1
	if e.Killer != nil && isFirst {
		rb.roundPlayer(e.Killer).FirstKill = true
	}
	if e.Victim != nil {
		rp := rb.roundPlayer(e.Victim)
		rp.Deaths++
		if isFirst {
			rp.FirstDeath = true
		}
		rp.DeathTick = tick
		rp.DeathTime = a.sec() // demo 相对秒数，与 StartSec/EndSec 同一坐标系
		pos := ke.VictimPos
		rp.DeathPos = &pos
		// 死亡时身上剩余投掷物（读死亡事件时点的 Weapons()）。
		rp.UtilityAtDeath = countGrenades(e.Victim.Weapons())
	}

	// Alive 序列：每次击杀后追加一条。
	a.appendAlive(e.Victim)

	// M2 接敌事实：窗口内击杀写入进行中的接敌（无则用当前快照新建）。
	a.recordKillDuel(e)
}

func (a *accumulator) onPlayerHurt(e events.PlayerHurt) {
	if !a.inRound() {
		return
	}
	if e.Attacker != nil && e.Player != nil {
		a.upsertPlayer(e.Attacker)
		a.upsertPlayer(e.Player)
		// DamageDealt 只计对敌方的伤害。
		if playerSide(e.Attacker) != playerSide(e.Player) {
			a.cur.roundPlayer(e.Attacker).DamageDealt += e.HealthDamage
		}
	}
	// HE 伤害归因：投掷者在 HE 爆炸后 2 秒内的 PlayerHurt 累加到该次投掷的 HEDamage。
	if e.Attacker != nil && isHEDamageWeapon(e.Weapon) {
		if u := a.findHeBlast(e.Attacker); u != nil {
			u.ev.HEDamage += e.HealthDamage
		}
	}
	// M2 接敌事实：敌对伤害记录命中/伤害/活动时间。
	a.recordHurtDuel(e)
}

// isHEDamageWeapon 判断伤害来源是否可能为 HE（世界伤害/未知武器也可能是爆炸溅射）。
func isHEDamageWeapon(w *common.Equipment) bool {
	if w == nil {
		return true
	}
	switch w.Type {
	case common.EqHE, common.EqUnknown, common.EqWorld:
		return true
	default:
		return false
	}
}

// findHeBlast 找该投掷者最近一次 2 秒窗口内的 HE 爆炸（伤害归因给最近的那次）。
func (a *accumulator) findHeBlast(attacker *common.Player) *utilBuilder {
	now := a.sec()
	var best *utilBuilder
	kept := a.heBlasts[:0]
	for _, u := range a.heBlasts {
		if now-u.heExplodeSec > heDamageWindowSec {
			continue // 窗口已过，丢弃
		}
		kept = append(kept, u)
		if u.thrower == attacker && u.heExploded && (best == nil || u.heExplodeSec >= best.heExplodeSec) {
			best = u
		}
	}
	a.heBlasts = kept
	return best
}

// ---------- 道具 ----------

func (a *accumulator) onGrenadeThrow(e events.GrenadeProjectileThrow) {
	if !a.inRound() || e.Projectile == nil {
		return
	}
	g := e.Projectile
	tick := a.tick()
	sec := a.sec()
	typ := common.EqUnknown
	if g.WeaponInstance != nil {
		typ = g.WeaponInstance.Type
	}
	thrower := g.Thrower
	if thrower == nil {
		thrower = g.Owner
	}

	u := &utilBuilder{
		thrower:      thrower,
		throwSec:     sec,
		lastKnownPos: projectilePos(g),
	}
	u.ev = model.UtilityEvent{
		Tick:      tick,
		RoundTime: a.roundTime(tick),
		Type:      grenadeTypeString(typ),
		ThrowPos:  projectilePosFirst(g),
	}
	if thrower != nil {
		u.ev.ThrowerID = idString(thrower.SteamID64)
		u.ev.ThrowerName = thrower.Name
		u.ev.ThrowerSide = playerSide(thrower)
		a.upsertPlayer(thrower)
	}
	u.uniqueID = g.UniqueID()
	if g.Entity != nil {
		u.entityID = g.Entity.ID()
		a.utilsByEnt[u.entityID] = u.uniqueID
	}
	a.utils[u.uniqueID] = u
	a.cur.utilsList = append(a.cur.utilsList, u)

	// 燃烧瓶/燃烧弹先入等待队列，等 InfernoStart 关联效果。
	if thrower != nil && (u.ev.Type == "molotov" || u.ev.Type == "incendiary") {
		a.molotovWait[thrower] = append(a.molotovWait[thrower], u)
	}
}

func (a *accumulator) onGrenadeDestroy(e events.GrenadeProjectileDestroy) {
	if e.Projectile == nil {
		return
	}
	g := e.Projectile
	if g.Entity != nil {
		delete(a.utilsByEnt, g.Entity.ID())
	}
	u, ok := a.utils[g.UniqueID()]
	if !ok {
		return
	}
	// 落点：效果事件优先，没有就用 projectile 最后位置。
	u.lastKnownPos = projectilePos(g)
	if !u.landSet {
		u.ev.LandPos = u.lastKnownPos
		u.landSet = true
	}
}

// utilByEntity 按 GrenadeEntityID 找到对应投掷。
func (a *accumulator) utilByEntity(entityID int) *utilBuilder {
	uid, ok := a.utilsByEnt[entityID]
	if !ok {
		return nil
	}
	return a.utils[uid]
}

func (a *accumulator) onNadeEffectStart(ne events.GrenadeEvent) {
	if !a.inRound() {
		return
	}
	u := a.utilByEntity(ne.GrenadeEntityID)
	if u == nil {
		return
	}
	pos := grenadePos(ne)
	u.ev.EffectStart = a.tick()
	if !u.landSet {
		u.ev.LandPos = pos
		u.landSet = true
	}
	u.lastKnownPos = pos
	if u.ev.Type == "other" {
		u.ev.Type = grenadeTypeString(ne.GrenadeType)
	}
}

func (a *accumulator) onNadeEffectEnd(ne events.GrenadeEvent) {
	if !a.inRound() {
		return
	}
	u := a.utilByEntity(ne.GrenadeEntityID)
	if u == nil {
		return
	}
	pos := grenadePos(ne)
	u.ev.EffectEnd = a.tick()
	if !u.landSet {
		u.ev.LandPos = pos
		u.landSet = true
	}
	u.lastKnownPos = pos
}

func (a *accumulator) onHeExplode(e events.HeExplode) {
	if !a.inRound() {
		return
	}
	ne := e.GrenadeEvent
	u := a.utilByEntity(ne.GrenadeEntityID)
	if u == nil {
		return
	}
	pos := grenadePos(ne)
	u.ev.EffectStart = a.tick()
	if !u.landSet {
		u.ev.LandPos = pos
		u.landSet = true
	}
	u.lastKnownPos = pos
	if u.ev.Type == "other" {
		u.ev.Type = "he"
	}
	// 打开 2 秒伤害归因窗口。
	u.heExploded = true
	u.heExplodeSec = a.sec()
	a.heBlasts = append(a.heBlasts, u)
}

func (a *accumulator) onInfernoStart(e events.InfernoStart) {
	if !a.inRound() || e.Inferno == nil {
		return
	}
	tick := a.tick()
	pos := infernoPos(e.Inferno)
	thrower := e.Inferno.Thrower() // 可能为 nil
	u := a.takePendingMolotov(thrower)
	if u == nil {
		// 匹配不到投掷（事件缺失/POV demo）：以火焰实体为准新建事实。
		// 网络上不区分 molotov 与 incendiary（见 demoinfocs FireGrenadeStart 注释），这里记 incendiary。
		u = &utilBuilder{
			thrower:      thrower,
			throwSec:     a.sec(),
			lastKnownPos: pos,
		}
		u.ev = model.UtilityEvent{
			Tick:      tick,
			RoundTime: a.roundTime(tick),
			Type:      "incendiary",
			ThrowPos:  pos,
		}
		if thrower != nil {
			u.ev.ThrowerID = idString(thrower.SteamID64)
			u.ev.ThrowerName = thrower.Name
			u.ev.ThrowerSide = playerSide(thrower)
			a.upsertPlayer(thrower)
		}
		a.cur.utilsList = append(a.cur.utilsList, u)
	}
	u.ev.EffectStart = tick
	if !u.landSet {
		u.ev.LandPos = pos
		u.landSet = true
	}
	u.lastKnownPos = pos
}

func (a *accumulator) onInfernoExpired(e events.InfernoExpired) {
	if !a.inRound() || e.Inferno == nil {
		return
	}
	tick := a.tick()
	pos := infernoPos(e.Inferno)
	thrower := e.Inferno.Thrower()
	// 优先按投掷者匹配等待中的投掷；匹配不到就回溯效果已开始、未结束的最近一条火。
	u := a.matchBurningMolotov(thrower)
	if u == nil {
		return
	}
	u.ev.EffectEnd = tick
	if !u.landSet {
		u.ev.LandPos = pos
		u.landSet = true
	}
	u.lastKnownPos = pos
}

// takePendingMolotov 取该投掷者窗口内最近一次未消费的燃烧瓶投掷。
func (a *accumulator) takePendingMolotov(thrower *common.Player) *utilBuilder {
	if thrower == nil {
		return nil
	}
	q := a.molotovWait[thrower]
	now := a.sec()
	for len(q) > 0 && now-q[0].throwSec > molotovMatchWindowSec {
		// 过期项直接丢弃（窗口 15 秒，正常燃烧瓶燃起远早于此）。
		q = q[1:]
	}
	if len(q) == 0 {
		delete(a.molotovWait, thrower)
		return nil
	}
	u := q[0]
	a.molotovWait[thrower] = q[1:]
	return u
}

// matchBurningMolotov 匹配火焰效果所属投掷：先试等待队列，再回溯已开始未结束的火。
func (a *accumulator) matchBurningMolotov(thrower *common.Player) *utilBuilder {
	if u := a.takePendingMolotov(thrower); u != nil {
		return u
	}
	if a.cur == nil {
		return nil
	}
	var best *utilBuilder
	for _, u := range a.cur.utilsList {
		if u.ev.Type != "molotov" && u.ev.Type != "incendiary" {
			continue
		}
		if u.ev.EffectStart == 0 || u.ev.EffectEnd != 0 {
			continue
		}
		if thrower != nil && u.thrower != thrower {
			continue
		}
		if best == nil || u.ev.EffectStart > best.ev.EffectStart {
			best = u
		}
	}
	return best
}

func (a *accumulator) onPlayerFlashed(e events.PlayerFlashed) {
	if !a.inRound() || e.Player == nil || e.Projectile == nil {
		return
	}
	u, ok := a.utils[e.Projectile.UniqueID()]
	if !ok {
		return
	}
	a.upsertPlayer(e.Player)
	// 契约：只记被闪的敌方玩家。
	if u.thrower == nil || playerSide(e.Player) == playerSide(u.thrower) {
		return
	}
	u.ev.Flashes = append(u.ev.Flashes, model.FlashEffect{
		PlayerID:   idString(e.Player.SteamID64),
		PlayerName: e.Player.Name,
		PlayerSide: playerSide(e.Player),
		Duration:   e.FlashDuration().Seconds(),
	})
}

// ---------- 炸弹 / POV / 换边 ----------

func (a *accumulator) onBombEvent(typ string, site events.Bombsite, pl *common.Player) {
	if !a.inRound() {
		return
	}
	tick := a.tick()
	be := model.BombEvent{
		Tick:      tick,
		RoundTime: a.roundTime(tick),
		Type:      typ,
		Site:      bombsiteString(site),
		// v5.2.0 的 BombEvent 无 Position 字段：坐标取行动玩家当时位置。
		Pos: playerPos(pl),
	}
	if pl != nil {
		be.PlayerID = idString(pl.SteamID64)
		be.PlayerName = pl.Name
		be.PlayerSide = playerSide(pl)
		a.upsertPlayer(pl)
	}
	a.cur.round.Bomb = append(a.cur.round.Bomb, be)
}

func (a *accumulator) onPOV(e events.POVRecordingPlayerDetected) {
	// v5.2.0 该事件携带 PlayerInfo（XUID/Name），而非 *common.Player。
	if e.PlayerInfo.XUID == 0 && e.PlayerInfo.Name == "" {
		return
	}
	a.pov = &playerRef{steamID: e.PlayerInfo.XUID, name: e.PlayerInfo.Name}
}

func (a *accumulator) onTeamSideSwitch() {
	if a.cur == nil {
		return
	}
	a.cur.round.Half = a.half.noteSideSwitch()
}

// ---------- 计数 / 坐标 ----------

// aliveCounts 从 Participants().Playing() 重数双方存活人数（契约：Alive 序列按实际存活数）。
func (a *accumulator) aliveCounts() (t, ct int) {
	for _, pl := range a.p.GameState().Participants().Playing() {
		if pl == nil || !pl.IsAlive() {
			continue
		}
		switch playerSide(pl) {
		case model.SideT:
			t++
		case model.SideCT:
			ct++
		}
	}
	return t, ct
}

// appendAlive 每次击杀后追加一条存活人数（重数未反映死亡时按受害者阵营递减兜底）。
func (a *accumulator) appendAlive(victim *common.Player) {
	rb := a.cur
	if rb == nil || len(rb.round.Alive) == 0 {
		return
	}
	t, ct := a.aliveCounts()
	prev := rb.round.Alive[len(rb.round.Alive)-1]
	if t == prev.TAlive && ct == prev.CTAlive && victim != nil {
		switch playerSide(victim) {
		case model.SideT:
			if t > 0 {
				t--
			}
		case model.SideCT:
			if ct > 0 {
				ct--
			}
		}
	}
	rb.round.Alive = append(rb.round.Alive, model.AliveCount{Tick: a.tick(), TAlive: t, CTAlive: ct})
}

// playerPos 取玩家坐标；Position() 对 nil 玩家返回零向量。
func playerPos(pl *common.Player) model.Vec3 {
	if pl == nil {
		return model.Vec3{}
	}
	v := pl.Position()
	return model.Vec3{X: float64(v.X), Y: float64(v.Y), Z: float64(v.Z)}
}

// projectilePos 取投掷物最后已知坐标（轨迹优先，避免 Entity 为空时解引用）。
func projectilePos(g *common.GrenadeProjectile) model.Vec3 {
	if g == nil {
		return model.Vec3{}
	}
	if len(g.Trajectory) > 0 {
		v := g.Trajectory[len(g.Trajectory)-1].Position
		return model.Vec3{X: float64(v.X), Y: float64(v.Y), Z: float64(v.Z)}
	}
	if g.Entity != nil {
		v := g.Position()
		return model.Vec3{X: float64(v.X), Y: float64(v.Y), Z: float64(v.Z)}
	}
	return model.Vec3{}
}

// projectilePosFirst 取投掷物起点坐标（轨迹首点，即出手位置）。
func projectilePosFirst(g *common.GrenadeProjectile) model.Vec3 {
	if g == nil {
		return model.Vec3{}
	}
	if len(g.Trajectory) > 0 {
		v := g.Trajectory[0].Position
		return model.Vec3{X: float64(v.X), Y: float64(v.Y), Z: float64(v.Z)}
	}
	return projectilePos(g)
}

// grenadePos 取效果事件坐标。
func grenadePos(ne events.GrenadeEvent) model.Vec3 {
	v := ne.Position
	return model.Vec3{X: float64(v.X), Y: float64(v.Y), Z: float64(v.Z)}
}

// infernoPos 取火焰实体坐标。
func infernoPos(inf *common.Inferno) model.Vec3 {
	if inf == nil || inf.Entity == nil {
		return model.Vec3{}
	}
	v := inf.Entity.Position()
	return model.Vec3{X: float64(v.X), Y: float64(v.Y), Z: float64(v.Z)}
}

// ---------- 收尾 ----------

// finish 定案并组装 model.Match。
func (a *accumulator) finish() (*model.Match, error) {
	if a.cur != nil {
		if !a.cur.ended {
			// demo 在回合中途结束：用最后观察到的 tick 收尾。
			a.cur.endTick = a.lastTick
			a.cur.endSec = a.lastSec
		}
		a.finalizeRound()
	}

	refs := a.playerRefs()
	if a.pov != nil && !containsRef(refs, *a.pov) {
		refs = append(refs, *a.pov)
	}

	focus, err := resolveFocus(a.opts, refs, a.pov)
	if err != nil {
		return nil, err
	}

	players := make([]model.PlayerInfo, 0, len(refs))
	for _, r := range refs {
		players = append(players, model.PlayerInfo{SteamID64: r.steamID, Name: r.name, IsBot: r.isBot})
	}

	m := &model.Match{
		Meta: model.MatchMeta{
			SourceFile:          a.sourceFile,
			Map:                 a.mapName,
			TickRate:            a.rate(),
			DemoDurationSeconds: a.duration,
			TotalRounds:         len(a.rounds),
			FinalScore:          model.Score{T: a.tScore, CT: a.ctScore},
			FocusPlayer:         focus.idString(),
			FocusPlayerName:     focus.name,
			Parser:              "demoinfocs-golang v5.2.0",
			ParsedAt:            time.Now().Format(time.RFC3339),
		},
		Players: players,
		Rounds:  a.rounds,
	}
	if a.pov != nil {
		m.Meta.POVPlayer = a.pov.idString()
		m.Meta.POVPlayerName = a.pov.name
	}
	return m, nil
}

// containsRef 判断名单里是否已有同一玩家（SteamID>0 按 SteamID 比，否则按名字）。
func containsRef(refs []playerRef, r playerRef) bool {
	for _, x := range refs {
		if r.steamID != 0 && x.steamID == r.steamID {
			return true
		}
		if r.steamID == 0 && x.steamID == 0 && x.name == r.name {
			return true
		}
	}
	return false
}

// playerRefs 汇总解析过程中见过的全部玩家（稳定排序，供焦点解析与报错名单使用）。
func (a *accumulator) playerRefs() []playerRef {
	byID := make(map[uint64]playerRef)
	var zeroIDRefs []playerRef
	for _, info := range a.players {
		r := playerRef{steamID: info.SteamID64, name: info.Name, isBot: info.IsBot}
		if info.SteamID64 == 0 {
			zeroIDRefs = append(zeroIDRefs, r)
			continue
		}
		// 同一 SteamID 多次出现（重连）时取名字较小者，保证输出确定。
		if old, ok := byID[info.SteamID64]; !ok || r.name < old.name {
			byID[info.SteamID64] = r
		}
	}
	refs := make([]playerRef, 0, len(byID)+len(zeroIDRefs))
	for _, r := range byID {
		refs = append(refs, r)
	}
	refs = append(refs, zeroIDRefs...)
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].name != refs[j].name {
			return refs[i].name < refs[j].name
		}
		return refs[i].steamID < refs[j].steamID
	})
	return refs
}
