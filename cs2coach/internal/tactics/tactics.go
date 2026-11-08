// Package tactics 是规则层：从 parse 层的 Match 事实计算全部教练素材。
//
// 设计锁：纯规则，禁止 import 任何 LLM/网络包；数字和证据永远由这一层产生。
package tactics

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"cs2-demo-review/internal/labels"
	"cs2-demo-review/internal/model"
)

// Options 控制战术分析行为。
type Options struct {
	TradeWindowSec float64 // 补枪窗口（秒）；<=0 则用默认值
}

// 数值阈值集中定义（见 docs/CONTRACTS.md 规则细则）。
const (
	// DefaultTradeWindowSec 是默认补枪窗口：A 死后 5s 内队友补掉凶手才算成功补枪。
	DefaultTradeWindowSec = 5.0
	// SplitCloseSec 是「分开」再死算紧接着的上限（秒）= 补枪窗口的 2 倍。
	SplitCloseSec = DefaultTradeWindowSec * 2
	// BuyFullAvg 是 full 判定线：人均冻结结束装备值 >= 4750。
	BuyFullAvg = 4750.0
	// BuyForceMinAvg 是 force 下限：人均 [2500, 4750) 且花了钱才算 force/half。
	BuyForceMinAvg = 2500.0
	// BuyHalfAvg 是 half 判定线：人均 >= 3800 的 force 记 half（半起）。
	BuyHalfAvg = 3800.0
	// ManAdvMinDelta 是人数优势最小差值（焦点方存活 - 敌方 >= 1）。
	ManAdvMinDelta = 1
	// ManAdvMinSec 是人数优势区间最短持续（秒）。
	ManAdvMinSec = 5.0
	// SiteAnchorRadius 是包点锚地半径（units）：死亡点落在此半径内则标「X 点附近」。
	SiteAnchorRadius = 800.0
	// DefaultTickRate 是 tick_rate 缺失/非法时的兜底。
	DefaultTickRate = 64.0
	// MaxSuspects 是可疑回合上限。
	MaxSuspects = 5
	// MaxGoodRounds 是好回合上限。
	MaxGoodRounds = 3
)

// Analyze 从 Match 计算全部教练素材。焦点玩家取 m.Meta.FocusPlayer。
func Analyze(m *model.Match, opts Options) (*model.Analysis, error) {
	if m == nil {
		return nil, fmt.Errorf("tactics: Match 为空")
	}
	window := opts.TradeWindowSec
	if window <= 0 {
		window = DefaultTradeWindowSec
	}
	rate := m.Meta.TickRate
	if rate <= 0 {
		rate = DefaultTickRate
	}
	focusID := strings.TrimSpace(m.Meta.FocusPlayer)
	if focusID != "" && !focusInMatch(m, focusID) {
		return nil, fmt.Errorf("tactics: 焦点玩家 %s 在本场 demo 中未出现（检查 --player 是否写对）", focusID)
	}

	a := &model.Analysis{
		Meta:        m.Meta,
		Map:         m.Meta.Map,
		SiteAnchors: map[string][]model.Vec3{},
	}
	// 包点锚地：全场下包/拆包坐标（先收集，供 PosLabel 用）。
	a.SiteAnchors = collectSiteAnchors(m)

	// 逐回合分析。
	a.Rounds = make([]model.RoundAnalysis, 0, len(m.Rounds))
	for i := range m.Rounds {
		ra := analyzeRound(&m.Rounds[i], focusID, window, rate, a.SiteAnchors)
		a.Rounds = append(a.Rounds, ra)
	}

	// M2：站位 / 对枪两个教练模块的全场汇总（逐次明细已在 RoundAnalysis 里）。
	// 无焦点死亡 / 无焦点对枪时保持 nil（报告层据此写「未采到数据」，不编造）。
	a.Positioning = buildPositioningReport(a.Rounds, a.SiteAnchors)
	a.Dueling = buildDuelingReport(m, a.Rounds, focusID)

	// 焦点画像与全场叙事。
	a.Focus = buildFocus(m, a.Rounds, focusID, window, rate)
	// 赛后判决在焦点画像算完后回填（本回合边优先，Focus.Sides 兜底）。
	fillRoundCalls(a, m, focusID)
	a.Story = buildStory(m, a.Rounds, focusID)
	a.LostRoundCauses = buildLostCauses(a.Rounds)
	a.Clutches = collectClutches(a.Rounds)
	buildSuspects(a)
	a.GoodRounds = buildGoodRounds(m, a.Rounds, focusID, rate)
	return a, nil
}

// focusInMatch 检查焦点 ID 是否出现在本场（玩家名单/回合/击杀任一处）。
func focusInMatch(m *model.Match, focusID string) bool {
	for _, p := range m.Players {
		if fmt.Sprint(p.SteamID64) == focusID {
			return true
		}
	}
	for i := range m.Rounds {
		r := &m.Rounds[i]
		for _, p := range r.Players {
			if fmt.Sprint(p.SteamID64) == focusID {
				return true
			}
		}
		for _, k := range r.Kills {
			if k.KillerID == focusID || k.VictimID == focusID {
				return true
			}
		}
	}
	return false
}

// SaveJSON 把 Analysis 落盘（缩进 2 空格）。
func SaveJSON(a *model.Analysis, path string) error {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadJSON 从文件读回 Analysis。
func LoadJSON(path string) (*model.Analysis, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var a model.Analysis
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// ---------- 回合分析 ----------

// roundCtx 是单回合分析的上下文（避免长参数列表）。
type roundCtx struct {
	round   *model.Round
	focusID string
	window  float64
	rate    float64
	anchors map[string][]model.Vec3
}

func analyzeRound(r *model.Round, focusID string, window, rate float64, anchors map[string][]model.Vec3) model.RoundAnalysis {
	c := &roundCtx{round: r, focusID: focusID, window: window, rate: rate, anchors: anchors}
	ra := model.RoundAnalysis{
		Number:     r.Number,
		Winner:     r.Winner,
		ScoreAfter: r.ScoreAfter,
		EndReason:  r.EndReason,
		Half:       r.Half,
		TickRange:  [2]int{r.StartTick, r.EndTick},
		Buys: map[model.Side]string{
			model.SideT:  classifyBuy(avgEquip(r.Economy.T), r.Economy.T.MoneySpent),
			model.SideCT: classifyBuy(avgEquip(r.Economy.CT), r.Economy.CT.MoneySpent),
		},
		UtilBeforeContact: map[model.Side]int{},
		Evidence:          []model.Evidence{},
	}
	focusSide := focusSideInRound(r, focusID)
	ra.FocusWon = r.Winner != model.SideDraw && r.Winner == focusSide && focusSide != model.SideNone && focusSide != ""
	ra.FocusBuy = string(focusSideBuy(ra.Buys, focusSide))

	kills := sortedKills(r.Kills)
	ra.Opening = buildOpening(c, kills, focusSide)
	ra.Trades = buildTrades(c, kills, focusSide)
	ra.FocusDeath = buildFocusDeath(c, kills, ra.Opening, ra.Trades, focusSide)
	// M2 明细：焦点死亡的站位指标 + 焦点参与的对枪（只写规则结果，不改既有字段）。
	ra.Positioning = buildPositioning(c, kills)
	ra.Duels = buildDuels(c, r.Duels)
	ra.UtilBeforeContact = buildUtilBeforeContact(c, kills)
	ra.Clutch = buildClutch(c, focusSide)
	ra.ManAdv = buildManAdv(c, focusSide, ra.FocusWon)
	if ra.FocusWon || r.Winner == model.SideDraw {
		// 胜/平局不打归因标签，WinCost 归零。
	} else {
		var fails int
		ra.Causes, ra.CauseDetail, fails = buildCauses(c, ra, kills, focusSide)
		ra.WinCost = winCost(ra, fails)
	}
	ra.Evidence = buildEvidence(c, ra, kills, focusSide)
	return ra
}

// classifyBuy 按人均冻结结束装备值与花费判定买型。
// avg >= 4750 → full；2500 <= avg < 4750 且花了钱 → force（avg >= 3800 则 half）；avg < 2500 → eco。
// 花了钱指本回合 MoneySpent > 0；只攒钱没花则按 eco 处理。
func classifyBuy(avg float64, moneySpent int) string {
	switch {
	case avg >= BuyFullAvg:
		return labels.BuyFull
	case avg >= BuyForceMinAvg && moneySpent > 0:
		if avg >= BuyHalfAvg {
			return labels.BuyHalf
		}
		return labels.BuyForce
	default:
		return labels.BuyEco
	}
}

// avgEquip 计算单边人均冻结结束装备值（人数为 0 时按 0 处理）。
func avgEquip(s model.SideEconomy) float64 {
	n := len(s.Players)
	if n == 0 {
		return 0
	}
	return float64(s.EquipValueFreezeEnd) / float64(n)
}

// focusSideInRound 取焦点玩家本回合所在边；找不到返回 SideNone。
func focusSideInRound(r *model.Round, focusID string) model.Side {
	if focusID == "" {
		return model.SideNone
	}
	for _, p := range r.Players {
		if fmt.Sprint(p.SteamID64) == focusID {
			return p.Side
		}
	}
	// 兜底：按击杀事件里的阵营推断（玩家列表缺失时）。
	for _, k := range r.Kills {
		if k.KillerID == focusID && k.KillerSide != "" {
			return k.KillerSide
		}
		if k.VictimID == focusID && k.VictimSide != "" {
			return k.VictimSide
		}
	}
	return model.SideNone
}

// focusSideBuy 取焦点所在边的买型。
func focusSideBuy(buys map[model.Side]string, side model.Side) model.Side {
	if b, ok := buys[side]; ok {
		return model.Side(b)
	}
	return ""
}

// oppSide 取敌方边。
func oppSide(s model.Side) model.Side {
	switch s {
	case model.SideT:
		return model.SideCT
	case model.SideCT:
		return model.SideT
	default:
		return model.SideNone
	}
}

// sortedKills 按 tick 升序返回击杀副本（parse 层一般已有序，这里保证）。
func sortedKills(ks []model.KillEvent) []model.KillEvent {
	out := append([]model.KillEvent(nil), ks...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Tick < out[j].Tick })
	return out
}

// fillRoundCalls 按击杀顺序给丢分回合填 Call。
// 焦点边优先用本回合实际边（与 FocusWon 同一套）；对不上时再退回 Focus.Sides；
// 半场多数边与本回合不一致时（换边那一分）仍按本回合边，避免把敌方死者当成「我方先倒」。
func fillRoundCalls(a *model.Analysis, m *model.Match, focusID string) {
	sideByHalf := map[string]model.Side{}
	for _, note := range a.Focus.Sides {
		sideByHalf[note.Half] = note.Side
	}
	byNumber := map[int]*model.Round{}
	for i := range m.Rounds {
		byNumber[m.Rounds[i].Number] = &m.Rounds[i]
	}
	for i := range a.Rounds {
		ra := &a.Rounds[i]
		if ra.FocusWon || ra.Winner == model.SideDraw {
			ra.Call = nil
			continue
		}
		r := byNumber[ra.Number]
		if r == nil {
			ra.Call = &model.RoundCall{Kind: model.CallUnknown}
			continue
		}
		focusSide := focusSideInRound(r, focusID)
		if focusSide == "" || focusSide == model.SideNone {
			var ok bool
			focusSide, ok = sideByHalf[ra.Half]
			if !ok || focusSide == "" || focusSide == model.SideNone {
				ra.Call = &model.RoundCall{Kind: model.CallUnknown}
				continue
			}
		}
		ra.Call = judgeRoundCall(sortedKills(r.Kills), focusSide, focusID)
		fillSplitGap(ra.Call, a.Meta.TickRate)
	}
}

// fillSplitGap 给 split 填再死间隔。judgeRoundCall 只判四类，不猜 tick rate。
// 比较用原始秒数；TickRate<=0 或 FocusTick<Tick 时 GapKnown=false，不编秒数。
func fillSplitGap(call *model.RoundCall, tickRate float64) {
	if call == nil || call.Kind != model.CallSplit {
		return
	}
	if tickRate <= 0 || call.FocusTick < call.Tick {
		call.GapKnown = false
		call.GapClose = false
		call.GapSec = 0
		return
	}
	sec := float64(call.FocusTick-call.Tick) / tickRate
	call.GapSec = sec
	call.GapKnown = true
	call.GapClose = sec <= SplitCloseSec
}

// judgeRoundCall 按我方谁先死判定 yours / teammate / split / unknown。
func judgeRoundCall(kills []model.KillEvent, focusSide model.Side, focusID string) *model.RoundCall {
	var firstOurs *model.KillEvent
	var focusDeath *model.KillEvent
	for i := range kills {
		k := &kills[i]
		if k.VictimSide != focusSide {
			continue
		}
		if firstOurs == nil {
			firstOurs = k
		}
		if focusID != "" && k.VictimID == focusID && focusDeath == nil {
			focusDeath = k
		}
	}
	if firstOurs == nil {
		return &model.RoundCall{Kind: model.CallUnknown}
	}
	call := &model.RoundCall{
		VictimName: callName(firstOurs.VictimName),
		KillerName: callName(firstOurs.KillerName),
		Weapon:     callWeapon(firstOurs.Weapon),
		Tick:       firstOurs.Tick,
	}
	if focusID != "" && firstOurs.VictimID == focusID {
		call.Kind = model.CallYours
		return call
	}
	if focusDeath == nil {
		call.Kind = model.CallTeammate
		return call
	}
	call.Kind = model.CallSplit
	call.FocusKillerName = callName(focusDeath.KillerName)
	call.FocusWeapon = callWeapon(focusDeath.Weapon)
	call.FocusTick = focusDeath.Tick
	return call
}

func callName(s string) string {
	if strings.TrimSpace(s) == "" {
		return "未知"
	}
	return s
}

func callWeapon(s string) string {
	if strings.TrimSpace(s) == "" {
		return "未知武器"
	}
	return s
}

// CallCounts 是丢分回合判决计数（N 含 unknown；M 只计纯队友）。
type CallCounts struct {
	Lost     int // N
	Teammate int // M
	Split    int // S
	Yours    int // Y
	Unknown  int
}

// CountCalls 统计丢分回合的判决种类。
func CountCalls(rounds []model.RoundAnalysis) CallCounts {
	var c CallCounts
	for _, ra := range rounds {
		if ra.FocusWon || ra.Winner == model.SideDraw {
			continue
		}
		c.Lost++
		if ra.Call == nil {
			c.Unknown++
			continue
		}
		switch ra.Call.Kind {
		case model.CallTeammate:
			c.Teammate++
		case model.CallSplit:
			c.Split++
		case model.CallYours:
			c.Yours++
		default:
			c.Unknown++
		}
	}
	return c
}

// MainCauseNotYou 纯队友局数 × 2 > 丢分局数才成立（刚好一半不算）。
func MainCauseNotYou(c CallCounts) bool {
	return c.Teammate*2 > c.Lost
}

// MatchVerdictLead 整场开场判决句（报告第一屏；不含回合行）。
func MatchVerdictLead(focusScore, oppScore int, rounds []model.RoundAnalysis) string {
	c := CountCalls(rounds)
	if c.Lost == 0 {
		return "这场没有要你改的。"
	}
	switch {
	case focusScore < oppScore:
		if MainCauseNotYou(c) {
			if c.Yours+c.Split == 0 {
				return "这场没有要你改的。"
			}
			return fmt.Sprintf("这场输了，主因不在你。丢的 %d 分里，有 %d 分是队友先倒、你没有再死。要你改的另列在下面。", c.Lost, c.Teammate)
		}
		return fmt.Sprintf("这场输了，不能说主因不在你。丢的 %d 分里，纯队友的有 %d 分，队友先倒你后来又死的有 %d 分，你先死的有 %d 分。",
			c.Lost, c.Teammate, c.Split, c.Yours)
	case focusScore > oppScore:
		return fmt.Sprintf("这局赢了。丢的 %d 分里，纯队友的有 %d 分，队友先倒你后来又死的有 %d 分，你先死的有 %d 分。",
			c.Lost, c.Teammate, c.Split, c.Yours)
	default:
		return fmt.Sprintf("丢的 %d 分里，纯队友的有 %d 分，队友先倒你后来又死的有 %d 分，你先死的有 %d 分。",
			c.Lost, c.Teammate, c.Split, c.Yours)
	}
}

// roundTime 把 tick 换算成回合内秒数（tick - startTick）/ rate，钳制 >= 0。
func roundTime(tick, startTick int, rate float64) float64 {
	if rate <= 0 {
		rate = DefaultTickRate
	}
	v := float64(tick-startTick) / rate
	if v < 0 {
		return 0
	}
	return v
}

// ---------- 开局 / 补枪 ----------

// buildOpening 取本回合第一次击杀为开局对枪，并标注凶手是否在窗口内被补。
func buildOpening(c *roundCtx, kills []model.KillEvent, focusSide model.Side) *model.OpeningInfo {
	if len(kills) == 0 {
		return nil
	}
	k := kills[0]
	op := &model.OpeningInfo{
		Tick:       k.Tick,
		RoundTime:  roundTime(k.Tick, c.round.StartTick, c.rate),
		KillerID:   k.KillerID,
		KillerName: k.KillerName,
		KillerSide: k.KillerSide,
		VictimID:   k.VictimID,
		VictimName: k.VictimName,
		VictimSide: k.VictimSide,
		Weapon:     k.Weapon,
		FocusRole:  focusRole(k, c.focusID),
	}
	if av := findAvenger(kills, 0, c.window, c.rate); av != nil && av.delaySec <= c.window {
		op.TradedWithinWindow = true
		op.TradeTick = av.tick
		op.TradeByID = av.id
		op.TradeByName = av.name
	}
	return op
}

// focusRole 判定焦点玩家在某次击杀中的角色。
func focusRole(k model.KillEvent, focusID string) string {
	switch {
	case focusID == "":
		return "none"
	case k.KillerID == focusID:
		return "killer"
	case k.VictimID == focusID:
		return "victim"
	case k.AssisterID == focusID:
		return "assist"
	default:
		return "none"
	}
}

// avenger 是补枪结果：凶手被补的 tick 与补枪者。
type avenger struct {
	tick     int
	id       string
	name     string
	delaySec float64
}

// findAvenger 在 kills[i] 之后找第一个「受害者队友补掉凶手」的击杀。
// 要求后续击杀的 Victim == 原凶手，且后续 KillerSide == 原受害者边。
// 返回最早的一次（无论是否在窗口内），调用方用 delaySec 判断窗口。
func findAvenger(kills []model.KillEvent, i int, window, rate float64) *avenger {
	k := kills[i]
	if k.KillerID == "" {
		return nil // 世界击杀没有可补的凶手
	}
	for j := i + 1; j < len(kills); j++ {
		n := kills[j]
		if n.VictimID == k.KillerID && n.KillerSide == k.VictimSide && n.KillerID != "" {
			return &avenger{
				tick:     n.Tick,
				id:       n.KillerID,
				name:     n.KillerName,
				delaySec: float64(n.Tick-k.Tick) / rate,
			}
		}
	}
	return nil
}

// buildTrades 为每次击杀生成补枪事件（凶手为空的世界击杀跳过）。
// 成功：凶手在窗口内被补，Tick 取补枪 tick；失败：凶手之后再死则 Tick 取该 tick，
// 凶手整回合没死则 Tick 取原击杀 tick、补枪者记「无人」。
func buildTrades(c *roundCtx, kills []model.KillEvent, focusSide model.Side) []model.TradeEvent {
	var out []model.TradeEvent
	for i, k := range kills {
		if k.KillerID == "" {
			continue
		}
		te := model.TradeEvent{
			Tick:        k.Tick,
			VictimID:    k.VictimID,
			VictimName:  k.VictimName,
			VictimSide:  k.VictimSide,
			KilledID:    k.KillerID,
			KilledName:  k.KillerName,
			AvengerSide: k.VictimSide,
		}
		if av := findAvenger(kills, i, c.window, c.rate); av != nil {
			te.Tick = av.tick
			te.AvengerID = av.id
			te.AvengerName = av.name
			te.DelaySec = av.delaySec
			te.Success = av.delaySec <= c.window
		} else {
			te.AvengerName = "无人"
			endTick := k.Tick
			if c.round != nil {
				endTick = c.round.EndTick
			}
			te.DelaySec = roundTime(endTick, k.Tick, c.rate)
			te.Success = false
		}
		out = append(out, te)
	}
	return out
}

// tradeResultOf 查某次击杀（按凶手+受害者+tick 定位）对应的补枪结果。
func tradeResultOf(trades []model.TradeEvent, killerID, victimID string, tick int) (traded bool, delay float64) {
	for _, t := range trades {
		if t.KilledID == killerID && t.VictimID == victimID {
			// 同一对凶手/受害者可能出现多次（少见），取 tick 最接近的一条。
			if t.Tick >= tick {
				return t.Success, t.DelaySec
			}
		}
	}
	return false, 0
}

// ---------- 焦点死亡 ----------

// buildFocusDeath 若焦点玩家本回合阵亡，汇总其死亡细节。
func buildFocusDeath(c *roundCtx, kills []model.KillEvent, op *model.OpeningInfo, trades []model.TradeEvent, focusSide model.Side) *model.FocusDeathInfo {
	if c.focusID == "" || focusSide == model.SideNone {
		return nil
	}
	for _, k := range kills {
		if k.VictimID != c.focusID {
			continue
		}
		traded, delay := tradeResultOf(trades, k.KillerID, k.VictimID, k.Tick)
		fd := &model.FocusDeathInfo{
			Tick:          k.Tick,
			RoundTime:     roundTime(k.Tick, c.round.StartTick, c.rate),
			KillerName:    k.KillerName,
			KillerSide:    k.KillerSide,
			Weapon:        k.Weapon,
			Headshot:      k.IsHeadshot,
			ThroughSmoke:  k.ThroughSmoke,
			Traded:        traded,
			TradeDelaySec: delay,
			Pos:           k.VictimPos,
			UtilityHeld:   utilityAtDeath(c.round, c.focusID),
			IsFirstDeath:  op != nil && op.VictimID == c.focusID,
		}
		fd.PosLabel = posLabel(k.VictimPos, c.anchors)
		return fd
	}
	return nil
}

// utilityAtDeath 取焦点玩家死亡时身上剩余道具数（RoundPlayer.UtilityAtDeath 事实）。
func utilityAtDeath(r *model.Round, focusID string) int {
	for _, p := range r.Players {
		if fmt.Sprint(p.SteamID64) == focusID {
			return p.UtilityAtDeath
		}
	}
	return 0
}

// collectSiteAnchors 收集全场下包/拆包坐标锚地（key 为包点，无点位信息则 key 为 unknown）。
// 全零坐标（事实缺失）不收录，避免 posLabel 误判。
func collectSiteAnchors(m *model.Match) map[string][]model.Vec3 {
	out := map[string][]model.Vec3{}
	for _, r := range m.Rounds {
		for _, b := range r.Bomb {
			if b.Type != "planted" && b.Type != "defused" {
				continue
			}
			if b.Pos == (model.Vec3{}) {
				continue
			}
			key := strings.TrimSpace(b.Site)
			if key == "" {
				key = "unknown"
			}
			out[key] = append(out[key], b.Pos)
		}
	}
	return out
}

// posLabel 把坐标映射为点位标签：锚地 800 units 内 →「A 点附近」；否则「坐标(x,y)」。
// 禁止编造点位名：没有锚地时一律用坐标兜底；死亡坐标全零（事实缺失）时标「未知点位」。
func posLabel(p model.Vec3, anchors map[string][]model.Vec3) string {
	if p == (model.Vec3{}) {
		return "未知点位"
	}
	bestKey, bestDist := "", math.MaxFloat64
	for key, pts := range anchors {
		for _, q := range pts {
			if d := dist3(p, q); d < bestDist {
				bestDist, bestKey = d, key
			}
		}
	}
	if bestKey != "" && bestDist <= SiteAnchorRadius {
		if bestKey == "unknown" {
			return "包点附近"
		}
		return bestKey + " 点附近"
	}
	return fmt.Sprintf("坐标(%.0f,%.0f)", p.X, p.Y)
}

func dist3(a, b model.Vec3) float64 {
	dx, dy, dz := a.X-b.X, a.Y-b.Y, a.Z-b.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// ---------- 道具 / 残局 / 人数优势 ----------

// buildUtilBeforeContact 统计双方首次交火前投入的道具数；无击杀则统计前 30s。
func buildUtilBeforeContact(c *roundCtx, kills []model.KillEvent) map[model.Side]int {
	out := map[model.Side]int{model.SideT: 0, model.SideCT: 0}
	cutoff := c.round.StartTick + int(30*c.rate)
	if len(kills) > 0 {
		cutoff = kills[0].Tick
	}
	for _, u := range c.round.Utility {
		if u.Tick > cutoff {
			continue
		}
		if u.ThrowerSide == model.SideT || u.ThrowerSide == model.SideCT {
			out[u.ThrowerSide]++
		}
	}
	return out
}

// buildClutch 从 Alive 序列找双方都 ≤2 的最早时刻判残局。
func buildClutch(c *roundCtx, focusSide model.Side) *model.ClutchInfo {
	alive := c.round.Alive
	if len(alive) == 0 {
		return nil
	}
	idx := -1
	for i, ac := range alive {
		if ac.TAlive <= 2 && ac.CTAlive <= 2 && ac.TAlive+ac.CTAlive > 0 {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	start := alive[idx]
	var fCount, eCount int
	if focusSide == model.SideT {
		fCount, eCount = start.TAlive, start.CTAlive
	} else if focusSide == model.SideCT {
		fCount, eCount = start.CTAlive, start.TAlive
	} else {
		fCount, eCount = start.TAlive, start.CTAlive
	}
	cl := &model.ClutchInfo{
		StartTick:    start.Tick,
		EndTick:      c.round.EndTick,
		Situation:    fmt.Sprintf("%dv%d", fCount, eCount),
		FocusSide:    focusSide,
		WinnerSide:   c.round.Winner,
		Advantageous: fCount > eCount, // 占优只按人数判：Match 事实无残局开始时 HP，
		// 契约第二条件（人数相等+焦点 HP≥50+敌方最低≤30）暂无事实来源，未实现，见 CONTRACTS 注记
		FocusInClutch: focusAliveAt(c.round, c.focusID, start.Tick),
	}
	switch {
	case c.round.Winner == model.SideDraw:
		cl.Result = "drawn"
	case focusSide != model.SideNone && c.round.Winner == focusSide:
		cl.Result = "won"
	default:
		cl.Result = "lost"
	}
	return cl
}

// focusAliveAt 判断焦点玩家在某 tick 时是否仍存活（无死亡记录或死于该 tick 之后）。
func focusAliveAt(r *model.Round, focusID string, tick int) bool {
	if focusID == "" {
		return false
	}
	for _, p := range r.Players {
		if fmt.Sprint(p.SteamID64) == focusID {
			if p.DeathTick == 0 {
				return true
			}
			return p.DeathTick > tick
		}
	}
	// 玩家列表缺失时退回击杀事件判断。
	for _, k := range r.Kills {
		if k.VictimID == focusID && k.Tick <= tick {
			return false
		}
	}
	return true
}

// buildManAdv 扫描 Alive 序列：焦点方存活 − 敌方 ≥1 且持续 ≥5s 的区间。
// Threw = 该优势段后最终丢分（本回合焦点方输则全段标 true）。
func buildManAdv(c *roundCtx, focusSide model.Side, focusWon bool) []model.ManAdvSpan {
	if focusSide != model.SideT && focusSide != model.SideCT {
		return nil
	}
	var out []model.ManAdvSpan
	var cur *model.ManAdvSpan
	flush := func(endTick int) {
		if cur == nil {
			return
		}
		cur.EndTick = endTick
		if float64(cur.EndTick-cur.StartTick)/c.rate >= ManAdvMinSec {
			cur.Threw = !focusWon
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, ac := range c.round.Alive {
		var f, e int
		if focusSide == model.SideT {
			f, e = ac.TAlive, ac.CTAlive
		} else {
			f, e = ac.CTAlive, ac.TAlive
		}
		if d := f - e; d >= ManAdvMinDelta {
			if cur == nil {
				cur = &model.ManAdvSpan{StartTick: ac.Tick, EndTick: ac.Tick, AdvSide: focusSide, AdvDelta: d}
			} else if d > cur.AdvDelta {
				cur.AdvDelta = d
				cur.EndTick = ac.Tick
			} else {
				cur.EndTick = ac.Tick
			}
		} else {
			flush(ac.Tick)
		}
	}
	if cur != nil {
		flush(c.round.EndTick)
	}
	return out
}

// aliveBefore 取某 tick 之前最近一条 Alive 记录（用于判断接火时人数态势）。
func aliveBefore(alive []model.AliveCount, tick int) *model.AliveCount {
	var best *model.AliveCount
	for i := range alive {
		if alive[i].Tick < tick {
			best = &alive[i]
		} else {
			break
		}
	}
	if best == nil && len(alive) > 0 {
		best = &alive[0]
	}
	return best
}

// ---------- 归因 / WinCost ----------

// buildCauses 对焦点方丢分回合打归因标签（可多标），每标附一句话证据。
// 返回标签、证据与 trade_fail 的失败次数（WinCost 口径与此处一致：排除人数劣势溃败）。
func buildCauses(c *roundCtx, ra model.RoundAnalysis, kills []model.KillEvent, focusSide model.Side) ([]string, map[string]string, int) {
	var causes []string
	detail := map[string]string{}
	add := func(tag, d string) {
		causes = append(causes, tag)
		detail[tag] = d
	}
	enemy := oppSide(focusSide)
	if focusSide == model.SideNone || focusSide == "" {
		return nil, detail, 0
	}
	win := fmt.Sprintf("%.0f 秒", c.window)

	// 1. first_blood_gift：对方拿下首杀且未在窗口内被补。
	if ra.Opening != nil && ra.Opening.KillerSide == enemy && !ra.Opening.TradedWithinWindow {
		if ra.Opening.VictimID == c.focusID {
			add(model.CauseFirstBloodGift,
				fmt.Sprintf("焦点玩家首死（被%s用%s击杀，tick %d）且未被补", ra.Opening.KillerName, labels.Or(ra.Opening.Weapon, "未知武器"), ra.Opening.Tick))
		} else {
			add(model.CauseFirstBloodGift,
				fmt.Sprintf("对方%s用%s拿下首杀（tick %d），%s %s内未补掉", ra.Opening.KillerName, labels.Or(ra.Opening.Weapon, "未知武器"), ra.Opening.Tick, labels.Or(ra.Opening.VictimName, ""), win))
		}
	}

	// 2. trade_fail：焦点方死亡后窗口内没补掉凶手，且死亡时人数不劣势。
	// 注意：人数查死亡 tick（不是补枪 tick），排除 eco 大溃败。
	failCount := 0
	focusKills := 0
	for _, k := range kills {
		if k.KillerSide == focusSide && k.KillerID != "" {
			focusKills++
		}
		if k.VictimSide != focusSide || k.KillerID == "" {
			continue
		}
		if traded, _ := tradeResultOf(ra.Trades, k.KillerID, k.VictimID, k.Tick); traded {
			continue
		}
		if ac := aliveBefore(c.round.Alive, k.Tick); ac != nil {
			var f, e int
			if focusSide == model.SideT {
				f, e = ac.TAlive, ac.CTAlive
			} else {
				f, e = ac.CTAlive, ac.TAlive
			}
			if f < e {
				continue // 人数已劣势的溃败不计（排除 eco 大溃败）
			}
		}
		failCount++
	}
	if failCount > 0 {
		add(model.CauseTradeFail, fmt.Sprintf("焦点方 %d 次死亡后 %s内没补掉凶手", failCount, win))
	}

	// 3. utility_gap：交火前道具不足一半，或焦点玩家带着 ≥2 道具死。
	fUtil := ra.UtilBeforeContact[focusSide]
	eUtil := ra.UtilBeforeContact[enemy]
	if float64(fUtil) < float64(eUtil)*0.5 {
		add(model.CauseUtilityGap,
			fmt.Sprintf("交火前道具 %d : %d，焦点方不足对方一半", fUtil, eUtil))
	} else if ra.FocusDeath != nil && ra.FocusDeath.UtilityHeld >= 2 {
		add(model.CauseUtilityGap,
			fmt.Sprintf("焦点玩家带着 %d 件道具死亡（tick %d），道具全浪费", ra.FocusDeath.UtilityHeld, ra.FocusDeath.Tick))
	}

	// 4. eco_force：焦点方 force/half 撞对方 full；或 eco 强行接团被横扫
	// （eco 撞 full 且本回合焦点方击杀 ≤1，即没换到人头）。
	enemyBuy := ra.Buys[enemy]
	if (ra.FocusBuy == labels.BuyForce || ra.FocusBuy == labels.BuyHalf) && enemyBuy == labels.BuyFull {
		add(model.CauseEcoForce,
			fmt.Sprintf("焦点方 %s 撞对方 full（买型 %s vs %s）", ra.FocusBuy, ra.FocusBuy, enemyBuy))
	} else if ra.FocusBuy == labels.BuyEco && enemyBuy == labels.BuyFull && focusKills <= 1 {
		add(model.CauseEcoForce,
			fmt.Sprintf("焦点方 eco 撞对方 full，本回合仅 %d 次击杀被横扫", focusKills))
	}

	// 5. clutch_throw：占优残局输掉。
	if ra.Clutch != nil && ra.Clutch.Advantageous {
		add(model.CauseClutchThrow,
			fmt.Sprintf("占优残局（%s，tick %d）被翻盘", ra.Clutch.Situation, ra.Clutch.StartTick))
	}

	// 6. man_advantage_lost（附加标）：有人数优势区间且最终丢分。
	maxDelta := 0
	for _, sp := range ra.ManAdv {
		if sp.Threw && sp.AdvDelta > maxDelta {
			maxDelta = sp.AdvDelta
		}
	}
	if maxDelta > 0 {
		add(model.CauseManAdvLost,
			fmt.Sprintf("曾有 +%d 人数优势（tick %d ~ %d）最终被翻", maxDelta, ra.ManAdv[0].StartTick, ra.ManAdv[len(ra.ManAdv)-1].EndTick))
	}
	return causes, detail, failCount
}

// WinCost 档位常量（与 CONTRACTS 公式一一对应，改公式只改这里）。
const (
	winCostClutch     = 1.0  // 有利残局丢
	winCostManAdvBase = 0.5  // 人数优势被翻：基础
	winCostManAdvPer  = 0.1  // 人数优势被翻：每点优势差
	winCostManAdvCap  = 0.9  // 人数优势被翻：封顶
	winCostFirstBlood = 0.6  // 首杀白给未补
	winCostTradeBase  = 0.4  // trade 失败：基础
	winCostTradePer   = 0.05 // trade 失败：每次
	winCostTradeCap   = 0.8  // trade 失败：封顶
	winCostUtility    = 0.35 // 道具缺口
	winCostEco        = 0.15 // eco/force 撞墙
)

// winCost 计算「这分不该丢」的程度（0-1，多标签取 max）。
// tradeFails 与 buildCauses 的 failCount 同口径（排除人数劣势溃败），由调用方传入。
func winCost(ra model.RoundAnalysis, tradeFails int) float64 {
	cost := 0.0
	has := func(tag string) bool {
		for _, t := range ra.Causes {
			if t == tag {
				return true
			}
		}
		return false
	}
	if has(model.CauseClutchThrow) {
		cost = math.Max(cost, winCostClutch)
	}
	if has(model.CauseManAdvLost) {
		maxDelta := 0
		for _, sp := range ra.ManAdv {
			if sp.Threw && sp.AdvDelta > maxDelta {
				maxDelta = sp.AdvDelta
			}
		}
		cost = math.Max(cost, math.Min(winCostManAdvBase+winCostManAdvPer*float64(maxDelta), winCostManAdvCap))
	}
	if has(model.CauseFirstBloodGift) {
		cost = math.Max(cost, winCostFirstBlood)
	}
	if has(model.CauseTradeFail) {
		n := tradeFails
		if n < 1 {
			n = 1
		}
		cost = math.Max(cost, math.Min(winCostTradeBase+winCostTradePer*float64(n), winCostTradeCap))
	}
	if has(model.CauseUtilityGap) {
		cost = math.Max(cost, winCostUtility)
	}
	if has(model.CauseEcoForce) {
		cost = math.Max(cost, winCostEco)
	}
	return math.Round(cost*100) / 100
}

// ---------- 证据 ----------

// buildEvidence 为每回合挂至少 3 条证据：首杀、焦点死亡/关键击杀、经济/道具一句。
func buildEvidence(c *roundCtx, ra model.RoundAnalysis, kills []model.KillEvent, focusSide model.Side) []model.Evidence {
	evs := []model.Evidence{}
	rt := func(tick int) float64 { return roundTime(tick, c.round.StartTick, c.rate) }

	// 1. 首杀。
	if op := ra.Opening; op != nil {
		tradeNote := "凶手未被补"
		if op.TradedWithinWindow {
			tradeNote = fmt.Sprintf("凶手 %.1fs 内被 %s 补掉", (float64(op.TradeTick-op.Tick))/c.rate, labels.Or(op.TradeByName, "队友"))
		}
		evs = append(evs, model.Evidence{
			Tick: op.Tick, RoundTime: op.RoundTime, Kind: "kill",
			Text: fmt.Sprintf("首杀：%s（%s）用 %s 击杀 %s（%s）；%s",
				labels.Or(op.KillerName, "世界"), string(op.KillerSide), labels.Or(op.Weapon, "未知武器"),
				labels.Or(op.VictimName, "未知"), string(op.VictimSide), tradeNote),
		})
	}
	// 2. 焦点玩家死亡或其关键击杀。
	if fd := ra.FocusDeath; fd != nil {
		killer := labels.Or(fd.KillerName, "世界")
		evs = append(evs, model.Evidence{
			Tick: fd.Tick, RoundTime: fd.RoundTime, Kind: "kill",
			Text: fmt.Sprintf("焦点玩家被 %s（%s）用 %s 击杀于 %s；爆头=%v，被补=%v，死时剩余道具 %d",
				killer, string(fd.KillerSide), labels.Or(fd.Weapon, "未知武器"), fd.PosLabel, fd.Headshot, fd.Traded, fd.UtilityHeld),
		})
	} else {
		// 焦点玩家存活：取其本回合最后一杀（若有）。
		for i := len(kills) - 1; i >= 0; i-- {
			if kills[i].KillerID == c.focusID {
				k := kills[i]
				evs = append(evs, model.Evidence{
					Tick: k.Tick, RoundTime: rt(k.Tick), Kind: "kill",
					Text: fmt.Sprintf("焦点玩家用 %s 击杀 %s（%s）", labels.Or(k.Weapon, "未知武器"), labels.Or(k.VictimName, "未知"), string(k.VictimSide)),
				})
				break
			}
		}
	}
	// 3. 经济一句。
	evs = append(evs, model.Evidence{
		Tick: c.round.StartTick, RoundTime: 0, Kind: "economy",
		Text: fmt.Sprintf("买型 T-%s（均枪 %.0f）vs CT-%s（均枪 %.0f）；焦点方 %s",
			ra.Buys[model.SideT], avgEquip(c.round.Economy.T),
			ra.Buys[model.SideCT], avgEquip(c.round.Economy.CT), labels.Or(ra.FocusBuy, "未知")),
	})
	// 4. 道具一句（恒挂：双方交火前道具数；HE 子句仅在有 HE 伤害/投掷时出现）。
	// 焦点边未知时不分焦点/敌方，只报 T/CT。
	tUtil := ra.UtilBeforeContact[model.SideT]
	ctUtil := ra.UtilBeforeContact[model.SideCT]
	heDmg, heThrown := 0, 0
	for _, u := range c.round.Utility {
		heDmg += u.HEDamage
		if u.Type == "he" {
			heThrown++
		}
	}
	utilText := ""
	if focusSide == model.SideT || focusSide == model.SideCT {
		fUtil := ra.UtilBeforeContact[focusSide]
		eUtil := ra.UtilBeforeContact[oppSide(focusSide)]
		utilText = fmt.Sprintf("交火前道具 焦点方 %d : 敌方 %d", fUtil, eUtil)
	} else {
		utilText = fmt.Sprintf("交火前道具 T %d : CT %d", tUtil, ctUtil)
	}
	if heDmg > 0 || heThrown > 0 {
		utilText += fmt.Sprintf("；本回合 HE 共造成 %d 伤害", heDmg)
	}
	evs = append(evs, model.Evidence{
		Tick: c.round.StartTick, RoundTime: 0, Kind: "utility", Text: utilText,
	})
	// 5. 炸弹事件（只收录改变回合走向的；drop/pickup 噪音大，跳过）。
	for _, b := range c.round.Bomb {
		switch b.Type {
		case "plant_begin", "planted", "defuse_begin", "defused", "exploded":
		default:
			continue
		}
		site := labels.Or(b.Site, "")
		if site != "" {
			site = site + " 点"
		} else {
			site = "未知点位"
		}
		evs = append(evs, model.Evidence{
			Tick: b.Tick, RoundTime: b.RoundTime, Kind: "bomb",
			Text: fmt.Sprintf("炸弹：%s %s（%s）", b.Type, site, labels.Or(b.PlayerName, "")),
		})
	}
	// 6. 残局 / 人数优势。
	if cl := ra.Clutch; cl != nil {
		evs = append(evs, model.Evidence{
			Tick: cl.StartTick, RoundTime: rt(cl.StartTick), Kind: "alive",
			Text: fmt.Sprintf("进入 %s 残局（tick %d ~ %d），结果 %s", cl.Situation, cl.StartTick, cl.EndTick, cl.Result),
		})
	}
	for _, sp := range ra.ManAdv {
		evs = append(evs, model.Evidence{
			Tick: sp.StartTick, RoundTime: rt(sp.StartTick), Kind: "alive",
			Text: fmt.Sprintf("人数优势 +%d（tick %d ~ %d）%s", sp.AdvDelta, sp.StartTick, sp.EndTick, threwText(sp.Threw)),
		})
	}
	// 7. 回合结果兜底（保证 ≥3 条）。
	evs = append(evs, model.Evidence{
		Tick: c.round.EndTick, RoundTime: rt(c.round.EndTick), Kind: "buy",
		Text: fmt.Sprintf("回合结束：%s 胜（%s），比分 T %d : CT %d",
			string(c.round.Winner), labels.Or(c.round.EndReason, "未知"), c.round.ScoreAfter.T, c.round.ScoreAfter.CT),
	})
	return evs
}

func threwText(threw bool) string {
	if threw {
		return "最终被翻盘"
	}
	return "保持到回合结束"
}

// ---------- 全场汇总 ----------

// buildFocus 汇总焦点玩家画像（出场/击杀/补枪/残局/道具等）。
func buildFocus(m *model.Match, rounds []model.RoundAnalysis, focusID string, window, rate float64) model.FocusSummary {
	fs := model.FocusSummary{
		SteamID64: focusID,
		SideStats: map[string]model.PlayerMatchStats{},
	}
	if focusID == "" {
		return fs
	}
	for _, p := range m.Players {
		if fmt.Sprint(p.SteamID64) == focusID {
			fs.Name = p.Name
		}
	}
	// 每半场所在边：取该半场出现最多的边。
	halves := []string{}
	seenHalf := map[string]bool{}
	for i := range m.Rounds {
		if h := m.Rounds[i].Half; !seenHalf[h] {
			seenHalf[h] = true
			halves = append(halves, h)
		}
	}
	for _, h := range halves {
		counts := map[model.Side]int{}
		for i := range m.Rounds {
			if m.Rounds[i].Half != h {
				continue
			}
			if s := focusSideInRound(&m.Rounds[i], focusID); s == model.SideT || s == model.SideCT {
				counts[s]++
			}
		}
		best, bestN := model.SideNone, 0
		for s, n := range counts {
			if n > bestN {
				best, bestN = s, n
			}
		}
		if best != model.SideNone {
			fs.Sides = append(fs.Sides, model.FocusSideNote{Half: h, Side: best})
		}
	}

	var all, perSideT, perSideCT model.PlayerMatchStats
	roundSide := map[int]model.Side{} // 回合号 -> 焦点边
	for i := range m.Rounds {
		r := &m.Rounds[i]
		side := focusSideInRound(r, focusID)
		roundSide[r.Number] = side
		var rp *model.RoundPlayer
		for j := range r.Players {
			if fmt.Sprint(r.Players[j].SteamID64) == focusID {
				rp = &r.Players[j]
				break
			}
		}
		if rp == nil {
			continue
		}
		st := &all
		var sst *model.PlayerMatchStats
		switch side {
		case model.SideT:
			sst = &perSideT
		case model.SideCT:
			sst = &perSideCT
		}
		acc := func(s *model.PlayerMatchStats) {
			s.RoundsPlayed++
			s.Kills += rp.Kills
			s.Deaths += rp.Deaths
			s.Assists += rp.Assists
			s.Damage += rp.DamageDealt
			if rp.FirstKill {
				s.FirstKills++
			}
			if rp.FirstDeath {
				s.FirstDeaths++
			}
			if rp.Kills >= 2 {
				s.MultikillRounds++
			}
		}
		acc(st)
		if sst != nil {
			acc(sst)
		}
		// 道具：本回合投掷数与致盲敌方人次。
		for _, u := range r.Utility {
			if u.ThrowerID == focusID {
				st.UtilThrown++
				if sst != nil {
					sst.UtilThrown++
				}
				for _, f := range u.Flashes {
					if f.Duration > 0.5 {
						st.EnemiesFlashed++
						if sst != nil {
							sst.EnemiesFlashed++
						}
					}
				}
			}
		}
	}
	// 补枪与残局（从 RoundAnalysis 取规则结果）。
	for _, ra := range rounds {
		if focusSideOf(ra, m, focusID) == model.SideNone {
			continue
		}
		if fd := ra.FocusDeath; fd != nil {
			if fd.Traded {
				all.TradedDeaths++
				sideAcc(&perSideT, &perSideCT, roundSide[ra.Number]).TradedDeaths++
			} else {
				all.UntradedDeaths++
				sideAcc(&perSideT, &perSideCT, roundSide[ra.Number]).UntradedDeaths++
			}
		}
		for _, t := range ra.Trades {
			if t.AvengerID == focusID && t.Success {
				all.TradeKills++
				sideAcc(&perSideT, &perSideCT, roundSide[ra.Number]).TradeKills++
			}
		}
		if ra.Clutch != nil && ra.Clutch.FocusInClutch {
			if ra.Clutch.Result == "won" {
				all.ClutchWins++
				sideAcc(&perSideT, &perSideCT, roundSide[ra.Number]).ClutchWins++
			} else if ra.Clutch.Result == "lost" {
				all.ClutchLosses++
				sideAcc(&perSideT, &perSideCT, roundSide[ra.Number]).ClutchLosses++
			}
		}
	}
	finalize := func(s *model.PlayerMatchStats) {
		if s.Deaths > 0 {
			s.KDR = float64(s.Kills) / float64(s.Deaths)
		} else {
			s.KDR = float64(s.Kills)
		}
		if s.RoundsPlayed > 0 {
			s.ADR = float64(s.Damage) / float64(s.RoundsPlayed)
		}
		s.KDR = math.Round(s.KDR*100) / 100
		s.ADR = math.Round(s.ADR*10) / 10
	}
	finalize(&all)
	finalize(&perSideT)
	finalize(&perSideCT)
	fs.Stats = all
	if perSideT.RoundsPlayed > 0 {
		fs.SideStats["T"] = perSideT
	}
	if perSideCT.RoundsPlayed > 0 {
		fs.SideStats["CT"] = perSideCT
	}
	return fs
}

// focusSideOf 从原始 Match 取某分析回合的焦点边。
func focusSideOf(ra model.RoundAnalysis, m *model.Match, focusID string) model.Side {
	for i := range m.Rounds {
		if m.Rounds[i].Number == ra.Number {
			return focusSideInRound(&m.Rounds[i], focusID)
		}
	}
	return model.SideNone
}

// sideAcc 按边取分边统计指针。
func sideAcc(t, ct *model.PlayerMatchStats, side model.Side) *model.PlayerMatchStats {
	if side == model.SideT {
		return t
	}
	return ct
}

// buildStory 组装全场叙事：比分、上下半场、胜负回合、买型战绩、连丢/连胜节点。
func buildStory(m *model.Match, rounds []model.RoundAnalysis, focusID string) model.StorySummary {
	st := model.StorySummary{
		WonByBuy:  map[string]int{},
		LostByBuy: map[string]int{},
	}
	if len(m.Rounds) > 0 {
		last := m.Rounds[len(m.Rounds)-1]
		st.FinalScore = last.ScoreAfter
	}
	// 上半场 =  distinct Half 序列的第一个（通常 H1）最后一回合比分；
	// 下半场 = 第二个 Half（通常 H2）相对上半场的增量；加时（OT…）不计入上下半场，
	// 只体现在 FinalScore 与 KeySwings 里。
	halves := distinctHalves(m.Rounds)
	if len(halves) >= 1 {
		st.FirstHalfScore = lastScoreOfHalf(m.Rounds, halves[0])
	}
	if len(halves) >= 2 {
		second := lastScoreOfHalf(m.Rounds, halves[1])
		st.SecondHalfScore = model.Score{
			T:  second.T - st.FirstHalfScore.T,
			CT: second.CT - st.FirstHalfScore.CT,
		}
	}
	focusTotal, oppTotal := 0, 0
	for _, ra := range rounds {
		switch {
		case ra.FocusWon:
			st.FocusWonRounds = append(st.FocusWonRounds, ra.Number)
			focusTotal++
			if ra.FocusBuy != "" {
				st.WonByBuy[ra.FocusBuy]++
			}
		case ra.Winner == model.SideDraw:
			// 平局不计胜负。
		default:
			st.FocusLostRounds = append(st.FocusLostRounds, ra.Number)
			oppTotal++
			if ra.FocusBuy != "" {
				st.LostByBuy[ra.FocusBuy]++
			}
		}
	}
	st.FocusTeamScore = focusTotal
	st.OpponentScore = oppTotal
	st.KeySwings = findStreaks(rounds)
	return st
}

// distinctHalves 按出现顺序返回全场 Half 序列（如 H1/H2/OT1…）。
func distinctHalves(rounds []model.Round) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range rounds {
		if !seen[r.Half] {
			seen[r.Half] = true
			out = append(out, r.Half)
		}
	}
	return out
}

// lastScoreOfHalf 取某 Half 最后一回合的比分。
func lastScoreOfHalf(rounds []model.Round, half string) model.Score {
	var s model.Score
	for _, r := range rounds {
		if r.Half == half {
			s = r.ScoreAfter
		}
	}
	return s
}

// findStreaks 找焦点方连续胜/丢 ≥3 的节点（如「第 11-14 回合连丢 4 分」）。
func findStreaks(rounds []model.RoundAnalysis) []string {
	var out []string
	type streak struct {
		won   bool
		start int
		end   int
		n     int
	}
	var cur *streak
	flush := func() {
		if cur != nil && cur.n >= 3 {
			if cur.won {
				out = append(out, fmt.Sprintf("第 %d-%d 回合连胜 %d 场", cur.start, cur.end, cur.n))
			} else {
				out = append(out, fmt.Sprintf("第 %d-%d 回合连丢 %d 分", cur.start, cur.end, cur.n))
			}
		}
		cur = nil
	}
	for _, ra := range rounds {
		if ra.Winner == model.SideDraw {
			flush() // 平局打断连胜/连丢
			continue
		}
		if cur == nil {
			cur = &streak{won: ra.FocusWon, start: ra.Number, end: ra.Number, n: 1}
		} else if cur.won == ra.FocusWon {
			cur.end, cur.n = ra.Number, cur.n+1
		} else {
			flush()
			cur = &streak{won: ra.FocusWon, start: ra.Number, end: ra.Number, n: 1}
		}
	}
	flush()
	return out
}

// buildLostCauses 统计丢分原因频次（按 count 降序；count 为 0 的标签不收录）。
func buildLostCauses(rounds []model.RoundAnalysis) []model.CauseStat {
	lost := 0
	counts := map[string]int{}
	roundLists := map[string][]int{}
	for _, ra := range rounds {
		if ra.FocusWon || ra.Winner == model.SideDraw {
			continue
		}
		lost++
		seen := map[string]bool{}
		for _, tag := range ra.Causes {
			if seen[tag] {
				continue
			}
			seen[tag] = true
			counts[tag]++
			roundLists[tag] = append(roundLists[tag], ra.Number)
		}
	}
	if lost == 0 {
		return nil
	}
	var out []model.CauseStat
	for tag, n := range counts {
		out = append(out, model.CauseStat{
			Tag: tag, Label: labels.CauseLabel(tag), Count: n,
			Rounds: roundLists[tag], Fraction: float64(n) / float64(lost),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Tag < out[j].Tag
	})
	return out
}

// collectClutches 汇总全场残局。
func collectClutches(rounds []model.RoundAnalysis) []model.ClutchInfo {
	var out []model.ClutchInfo
	for _, ra := range rounds {
		if ra.Clutch != nil {
			out = append(out, *ra.Clutch)
		}
	}
	return out
}

// MaxPatternSuspects 是首死模式补入的上限（总 Suspects 上限 = MaxSuspects + MaxPatternSuspects，
// 防止 prompt 切片无限膨胀）。
const MaxPatternSuspects = 3

// buildSuspects 生成可疑回合：丢分回合按 WinCost 降序取前 5（契约只要求前 5，不做 WinCost 截断）；
// 若焦点玩家全场首死 ≥3，再补入未入选的首死丢分回合（最多补 MaxPatternSuspects 个）。顺序即 Top3 报告顺序。
func buildSuspects(a *model.Analysis) {
	var lost []model.RoundAnalysis
	for _, ra := range a.Rounds {
		if ra.FocusWon || ra.Winner == model.SideDraw {
			continue
		}
		lost = append(lost, ra)
	}
	// 首死总数按全场画像口径（含胜/平局回合）。
	totalFirstDeaths := 0
	for _, ra := range a.Rounds {
		if ra.FocusDeath != nil && ra.FocusDeath.IsFirstDeath {
			totalFirstDeaths++
		}
	}
	sort.SliceStable(lost, func(i, j int) bool {
		if lost[i].WinCost != lost[j].WinCost {
			return lost[i].WinCost > lost[j].WinCost
		}
		return lost[i].Number < lost[j].Number
	})
	picked := map[int]bool{}
	var suspects []model.Suspect
	for _, ra := range lost {
		if len(suspects) >= MaxSuspects {
			break
		}
		suspects = append(suspects, toSuspect(ra))
		picked[ra.Number] = true
	}
	if totalFirstDeaths >= 3 {
		added := 0
		for _, ra := range lost {
			if added >= MaxPatternSuspects {
				break
			}
			if picked[ra.Number] {
				continue
			}
			if ra.FocusDeath != nil && ra.FocusDeath.IsFirstDeath {
				suspects = append(suspects, toSuspect(ra))
				picked[ra.Number] = true
				added++
			}
		}
	}
	a.Suspects = suspects
	// 回写每回合的 Suspect 标记。
	mark := map[int][]string{}
	for _, s := range suspects {
		mark[s.Round] = s.Tags
	}
	for i := range a.Rounds {
		if tags, ok := mark[a.Rounds[i].Number]; ok {
			a.Rounds[i].Suspect = true
			a.Rounds[i].SuspectTags = tags
		}
	}
}

// toSuspect 把回合转成可疑候选。
func toSuspect(ra model.RoundAnalysis) model.Suspect {
	reason := ""
	if len(ra.Causes) > 0 {
		parts := []string{}
		for _, tag := range ra.Causes {
			label := labels.CauseLabel(tag)
			if d := ra.CauseDetail[tag]; d != "" {
				parts = append(parts, label+"："+d)
			} else {
				parts = append(parts, label)
			}
		}
		reason = strings.Join(parts, "；")
	} else {
		reason = fmt.Sprintf("丢分回合（WinCost %.2f）", ra.WinCost)
	}
	return model.Suspect{
		Round: ra.Number, TickRange: ra.TickRange,
		Tags:   append([]string(nil), ra.Causes...),
		Reason: reason, ScoreHint: ra.WinCost,
	}
}

// buildGoodRounds 取焦点方胜回合中焦点玩家贡献最高的（kills*1 + clutch_win*3 + first_kill*1），最多 3 个。
func buildGoodRounds(m *model.Match, rounds []model.RoundAnalysis, focusID string, rate float64) []model.GoodRound {
	type scored struct {
		ra    model.RoundAnalysis
		score int
	}
	var cands []scored
	for _, ra := range rounds {
		if !ra.FocusWon {
			continue
		}
		score := 0
		for i := range m.Rounds {
			if m.Rounds[i].Number != ra.Number {
				continue
			}
			for _, rp := range m.Rounds[i].Players {
				if fmt.Sprint(rp.SteamID64) == focusID {
					score += rp.Kills
					if rp.FirstKill {
						score++ // 首杀 +1
					}
				}
			}
		}
		if ra.Clutch != nil && ra.Clutch.FocusInClutch && ra.Clutch.Result == "won" {
			score += 3
		}
		cands = append(cands, scored{ra: ra, score: score})
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return cands[i].ra.Number < cands[j].ra.Number
	})
	if len(cands) > MaxGoodRounds {
		cands = cands[:MaxGoodRounds]
	}
	var out []model.GoodRound
	for _, c := range cands {
		hl, tick := goodHighlight(m, c.ra, focusID)
		reason := fmt.Sprintf("焦点方获胜回合中个人贡献最高（评分 %d）", c.score)
		out = append(out, model.GoodRound{Round: c.ra.Number, Reason: reason, Highlight: hl, Tick: tick})
	}
	return out
}

// goodHighlight 用事实写高光：多杀/首杀/残局 + tick。
func goodHighlight(m *model.Match, ra model.RoundAnalysis, focusID string) (string, int) {
	kills, firstKill, lastTick := 0, false, ra.TickRange[0]
	for i := range m.Rounds {
		if m.Rounds[i].Number != ra.Number {
			continue
		}
		for _, k := range m.Rounds[i].Kills {
			if k.KillerID == focusID {
				kills++
				lastTick = k.Tick
			}
		}
		for _, rp := range m.Rounds[i].Players {
			if fmt.Sprint(rp.SteamID64) == focusID && rp.FirstKill {
				firstKill = true
			}
		}
	}
	parts := []string{}
	if kills > 0 {
		parts = append(parts, fmt.Sprintf("%d 杀", kills))
	}
	if firstKill {
		parts = append(parts, "含首杀")
	}
	if ra.Clutch != nil && ra.Clutch.FocusInClutch && ra.Clutch.Result == "won" {
		parts = append(parts, ra.Clutch.Situation+" 残局获胜")
		lastTick = ra.Clutch.StartTick
	}
	if len(parts) == 0 {
		parts = append(parts, "团队获胜（个人无击杀）")
	}
	return strings.Join(parts, "、"), lastTick
}
