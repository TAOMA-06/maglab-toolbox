package parse

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/common"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/events"

	"cs2-demo-review/internal/model"
)

// 半场赛制常量：CS2 常规赛 mr12（每半场 12 回合），加时赛每半场 3 回合（3+3）。
const (
	regulationHalfRounds = 12
	overtimeHalfRounds   = 3
)

// roundEndReasonString 把 demoinfocs 的 RoundEndReason 映射为契约约定的事实字符串。
// 映射表以 CONTRACTS.md 为准：TargetBombed→bomb_exploded、BombDefused→bomb_defused、
// CTWin/TerroristsWin→elimination、TargetSaved→time、Draw→draw、
// TerroristsSurrender/CTSurrender→surrender，其余→other。
func roundEndReasonString(r events.RoundEndReason) string {
	switch r {
	case events.RoundEndReasonTargetBombed:
		return "bomb_exploded"
	case events.RoundEndReasonBombDefused:
		return "bomb_defused"
	case events.RoundEndReasonCTWin, events.RoundEndReasonTerroristsWin:
		return "elimination"
	case events.RoundEndReasonTargetSaved:
		return "time"
	case events.RoundEndReasonDraw:
		return "draw"
	case events.RoundEndReasonTerroristsSurrender, events.RoundEndReasonCTSurrender:
		return "surrender"
	default:
		return "other"
	}
}

// playerSide 把玩家阵营映射为 model.Side；未分配阵营返回 SideNone。
func playerSide(pl *common.Player) model.Side {
	if pl == nil {
		return model.SideNone
	}
	if pl.Team == common.TeamUnassigned && pl.TeamState != nil {
		// 控制器 Team 字段可能未更新，退回 TeamState 记录的队伍。
		return sideFromTeam(pl.TeamState.Team())
	}
	return sideFromTeam(pl.Team)
}

// sideFromTeam 把 common.Team 映射为 model.Side；非 T/CT 返回 SideNone。
func sideFromTeam(t common.Team) model.Side {
	switch t {
	case common.TeamTerrorists:
		return model.SideT
	case common.TeamCounterTerrorists:
		return model.SideCT
	default:
		return model.SideNone
	}
}

// winnerSide 把 RoundEnd 的胜方映射为 model.Side；平局/未知返回 SideDraw。
func winnerSide(t common.Team) model.Side {
	if s := sideFromTeam(t); s != model.SideNone {
		return s
	}
	return model.SideDraw
}

// grenadeTypeString 把装备类型映射为 UtilityEvent.Type 的事实字符串。
// 网络上不区分 molotov 与 incendiary 时由调用方决定，这里只做一一映射；
// 无法识别的类型记 "other"（可随后续效果事件回填修正）。
func grenadeTypeString(t common.EquipmentType) string {
	switch t {
	case common.EqFlash:
		return "flash"
	case common.EqSmoke:
		return "smoke"
	case common.EqHE:
		return "he"
	case common.EqMolotov:
		return "molotov"
	case common.EqIncendiary:
		return "incendiary"
	case common.EqDecoy:
		return "decoy"
	default:
		return "other"
	}
}

// isGrenadeType 判断装备是否为投掷物（闪光/烟/HE/燃烧瓶/燃烧弹/诱饵）。
func isGrenadeType(t common.EquipmentType) bool {
	switch t {
	case common.EqFlash, common.EqSmoke, common.EqHE, common.EqMolotov, common.EqIncendiary, common.EqDecoy:
		return true
	default:
		return false
	}
}

// countGrenades 统计装备列表中仍持有的投掷物数量
// （flash/smoke/he/molotov/incendiary/decoy，用于 UtilityAtDeath）。
func countGrenades(weapons []*common.Equipment) int {
	n := 0
	for _, w := range weapons {
		if w == nil {
			continue
		}
		if isGrenadeType(w.Type) {
			n++
		}
	}
	return n
}

// weaponNames 把装备列表转为名字列表（排序保证输出确定性；不合并同名，两颗闪即两条）。
func weaponNames(weapons []*common.Equipment) []string {
	names := make([]string, 0, len(weapons))
	for _, w := range weapons {
		if w == nil {
			continue
		}
		names = append(names, w.String())
	}
	sort.Strings(names)
	return names
}

// weaponClassName 把装备类别映射为武器类别的事实字符串。
func weaponClassName(c common.EquipmentClass) string {
	switch c {
	case common.EqClassPistols:
		return "pistol"
	case common.EqClassSMG:
		return "smg"
	case common.EqClassHeavy:
		return "heavy"
	case common.EqClassRifle:
		return "rifle"
	case common.EqClassEquipment:
		return "equipment"
	case common.EqClassGrenade:
		return "grenade"
	default:
		return "unknown"
	}
}

// bombsiteString 把炸弹点映射为 "A"/"B"；未知返回空串。
func bombsiteString(s events.Bombsite) string {
	switch s {
	case events.BombsiteA:
		return "A"
	case events.BombsiteB:
		return "B"
	default:
		return ""
	}
}

// idString 把 SteamID64 编为事件里使用的十进制 ID 字符串。
func idString(steamID uint64) string {
	return strconv.FormatUint(steamID, 10)
}

// mapNameFromPath 从 demo 文件路径提取地图名占位值。
// v5.2.0 的 Parser 接口未导出 header.MapName（只存在于 CDemoFileHeader 内部），
// 按契约降级：取 demo 文件名（不含扩展名）作为 Meta.Map，绝不编造地图名。
func mapNameFromPath(demoPath string) string {
	base := demoPath
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	return base
}

// playerRef 是焦点解析用的轻量玩家身份。
type playerRef struct {
	steamID uint64
	name    string
	isBot   bool
}

// idString 返回事件里使用的玩家 ID 字符串。
func (r playerRef) idString() string {
	return idString(r.steamID)
}

// playerListString 把玩家名单格式化为 "name (steamid64)" 列表，供报错信息使用。
func playerListString(players []playerRef) string {
	parts := make([]string, 0, len(players))
	for _, pl := range players {
		parts = append(parts, fmt.Sprintf("%s (%s)", pl.name, pl.idString()))
	}
	return strings.Join(parts, "、")
}

// resolveFocus 按契约优先级解析焦点玩家：
// FocusSteamID > FocusName（忽略大小写精确匹配，同名歧义报错）> POV 录制者 > 报错列出全部玩家。
func resolveFocus(opts Options, players []playerRef, pov *playerRef) (playerRef, error) {
	if opts.FocusSteamID != "" {
		id, err := strconv.ParseUint(strings.TrimSpace(opts.FocusSteamID), 10, 64)
		if err != nil {
			return playerRef{}, fmt.Errorf("焦点 SteamID64 不是合法的十进制字符串: %q", opts.FocusSteamID)
		}
		for _, pl := range players {
			if pl.steamID == id {
				return pl, nil
			}
		}
		return playerRef{}, fmt.Errorf("未找到 SteamID64 为 %s 的玩家；可用玩家名单：%s", opts.FocusSteamID, playerListString(players))
	}

	if opts.FocusName != "" {
		want := strings.ToLower(strings.TrimSpace(opts.FocusName))
		var hits []playerRef
		for _, pl := range players {
			if strings.ToLower(pl.name) == want {
				hits = append(hits, pl)
			}
		}
		switch len(hits) {
		case 1:
			return hits[0], nil
		case 0:
			return playerRef{}, fmt.Errorf("未找到名为 %q 的玩家（忽略大小写精确匹配）；可用玩家名单：%s", opts.FocusName, playerListString(players))
		default:
			return playerRef{}, fmt.Errorf("名为 %q 的玩家有 %d 个同名匹配，无法确定焦点玩家；请改用 SteamID64 指定：%s",
				opts.FocusName, len(hits), playerListString(hits))
		}
	}

	if pov != nil {
		return *pov, nil
	}
	return playerRef{}, fmt.Errorf("未指定焦点玩家且未检测到 POV 录制者；可用玩家名单：%s（用 --player name|steamid64 指定）", playerListString(players))
}

// aggregateSideEconomy 汇总单边经济（纯函数）：
// Money = 回合开始全员现金合计，其余为队伍级快照值。
func aggregateSideEconomy(players []model.PlayerEconomy, equipRoundStart, equipFreezeEnd, moneySpent int) model.SideEconomy {
	se := model.SideEconomy{
		EquipValueRoundStart: equipRoundStart,
		EquipValueFreezeEnd:  equipFreezeEnd,
		MoneySpent:           moneySpent,
		Players:              players,
	}
	for _, p := range players {
		se.Money += p.Money
	}
	return se
}

// halfTracker 推算回合所属半场标签（H1 / H2 / OT1 / OT2 ...）。
//
// 规则（与 CS2 mr12 赛制对齐）：
//   - 常规赛每半场 12 回合：H1 = 回合 1-12，H2 = 13-24；
//   - 加时赛每半场 3 回合（3+3）：第 25 回合起每半场依次记 OT1、OT2……（第 n 个加时半场）；
//   - TeamSideSwitch 事件在换边回合的 RoundStart 之后触发。以回合数边界推进为主，
//     事件作兜底：若按回合数本回合还不该换半场却收到了换边事件（非标准赛制 demo），
//     立即推进半场并改写当前回合的标签。
type halfTracker struct {
	idx               int  // 0=H1、1=H2、2=OT1、3=OT2……
	roundsInHalf      int  // 当前半场已开始的回合数
	advancedThisRound bool // 本回合是否已按回合数边界推进过半场
}

// noteSideSwitch 在收到 TeamSideSwitch 时校正半场边界，返回当前回合应使用的半场标签。
func (h *halfTracker) noteSideSwitch() string {
	if h.advancedThisRound {
		// 按回合数边界已在本回合推进过，事件与边界一致，无需再动。
		return h.label()
	}
	h.idx++
	h.roundsInHalf = 1
	h.advancedThisRound = true
	return h.label()
}

// beginRound 在非热身回合开始时推进半场状态，返回该回合的半场标签。
func (h *halfTracker) beginRound() string {
	h.advancedThisRound = false
	h.roundsInHalf++
	if h.roundsInHalf > h.halfLimit() {
		h.idx++
		h.roundsInHalf = 1
		h.advancedThisRound = true
	}
	return h.label()
}

// halfLimit 返回当前半场的回合数上限。
func (h *halfTracker) halfLimit() int {
	if h.idx >= 2 {
		return overtimeHalfRounds
	}
	return regulationHalfRounds
}

// label 返回当前半场标签。
func (h *halfTracker) label() string {
	switch {
	case h.idx == 0:
		return "H1"
	case h.idx == 1:
		return "H2"
	default:
		return fmt.Sprintf("OT%d", h.idx-1)
	}
}
