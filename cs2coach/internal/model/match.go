// Package model 定义三层管线之间的数据契约：
//
//	parse 层 (.dem)  -> Match   结构化事实 JSON
//	tactics 层       -> Analysis 教练素材（归因、可疑回合、证据切片）
//	coach/report/qa  -> 消费 Analysis，产出报告与问答
//
// 设计锁：解析层只写事实；规则/归因只出现在 tactics 层；LLM 只解读、不发明。
package model

// Vec3 是游戏世界坐标（单位：game units）。
type Vec3 struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// Side 是阵营。
type Side string

const (
	SideT    Side = "T"
	SideCT   Side = "CT"
	SideDraw Side = "draw"
	SideNone Side = ""
)

// Score 是 T/CT 计分板比分。
type Score struct {
	T  int `json:"t"`
	CT int `json:"ct"`
}

// Match 是 parse 层的输出：一场 demo 的全部事实。
type Match struct {
	Meta    MatchMeta    `json:"meta"`
	Players []PlayerInfo `json:"players"`
	Rounds  []Round      `json:"rounds"`
}

// MatchMeta 是 demo 级元信息。
type MatchMeta struct {
	SourceFile          string  `json:"source_file"`
	Map                 string  `json:"map"`
	TickRate            float64 `json:"tick_rate"`
	DemoDurationSeconds float64 `json:"demo_duration_seconds"`
	TotalRounds         int     `json:"total_rounds"`
	FinalScore          Score   `json:"final_score"` // [T, CT] 全场结束
	// FocusPlayer 是复盘焦点玩家的 SteamID64（十进制字符串）。空 = 未指定。
	FocusPlayer     string `json:"focus_player"`
	FocusPlayerName string `json:"focus_player_name"`
	// POVPlayer：若为 POV demo（玩家自己的录像），记录该玩家 SteamID64。
	POVPlayer     string `json:"pov_player,omitempty"`
	POVPlayerName string `json:"pov_player_name,omitempty"`
	Parser        string `json:"parser"`
	ParsedAt      string `json:"parsed_at"`
}

// PlayerInfo 是参赛选手的身份信息。
type PlayerInfo struct {
	SteamID64 uint64 `json:"steamid64"`
	Name      string `json:"name"`
	IsBot     bool   `json:"is_bot"`
}

// Round 是单回合事实（1-based 回合号，不含热身）。
type Round struct {
	Number     int     `json:"number"`
	StartTick  int     `json:"start_tick"` // ingame tick（demo_gototick 用它）
	EndTick    int     `json:"end_tick"`
	StartSec   float64 `json:"start_sec"` // 相对 demo 开头的秒数
	EndSec     float64 `json:"end_sec"`
	Winner     Side    `json:"winner"`     // "T" / "CT" / "draw"
	EndReason  string  `json:"end_reason"` // elimination / bomb_planted / bomb_defused / time / surrender / other
	ScoreAfter Score   `json:"score_after"`
	Half       string  `json:"half"` // H1 / H2 / OT1 ...

	Economy EconomySnapshot `json:"economy"`
	Players []RoundPlayer   `json:"players"`
	Kills   []KillEvent     `json:"kills"`
	Utility []UtilityEvent  `json:"utility"`
	Bomb    []BombEvent     `json:"bomb"`
	Alive   []AliveCount    `json:"alive"` // 每次击杀后的存活人数序列（事实）

	// Shots / Duels 是 M2 新增事实（parse 层只采样，不做判断）。
	Shots []ShotFact `json:"shots,omitempty"` // 每次开枪的视角/速度/位置快照
	Duels []DuelFact `json:"duels,omitempty"` // 两个敌人的接敌事实（首次交火快照 + 结果）
}

// EconomySnapshot 是回合开局经济快照（事实：钱与装备值）。
type EconomySnapshot struct {
	T  SideEconomy `json:"t"`
	CT SideEconomy `json:"ct"`
}

// SideEconomy 是单边经济。
type SideEconomy struct {
	Money                int             `json:"money"` // 回合开始全员现金合计
	EquipValueRoundStart int             `json:"equip_value_round_start"`
	EquipValueFreezeEnd  int             `json:"equip_value_freeze_end"` // 冻结结束时装备总值（实际用出的家当）
	MoneySpent           int             `json:"money_spent"`
	Players              []PlayerEconomy `json:"players"`
}

// PlayerEconomy 是单人回合经济。
type PlayerEconomy struct {
	SteamID64  uint64   `json:"steamid64"`
	Name       string   `json:"name"`
	Money      int      `json:"money"`       // 回合开始现金
	EquipValue int      `json:"equip_value"` // 冻结结束装备值
	HasHelmet  bool     `json:"has_helmet"`
	HasDefuse  bool     `json:"has_defuse"`
	Weapons    []string `json:"weapons"` // 冻结结束时的武器名列表
}

// KillEvent 是一次击杀事实。
type KillEvent struct {
	Tick          int     `json:"tick"`
	RoundTime     float64 `json:"round_time"` // 秒，自回合开始（含冻结）
	KillerID      string  `json:"killer_id"`  // SteamID64 字符串；世界/未知为 ""
	KillerName    string  `json:"killer_name"`
	KillerSide    Side    `json:"killer_side"`
	VictimID      string  `json:"victim_id"`
	VictimName    string  `json:"victim_name"`
	VictimSide    Side    `json:"victim_side"`
	AssisterID    string  `json:"assister_id,omitempty"`
	AssisterName  string  `json:"assister_name,omitempty"`
	Weapon        string  `json:"weapon"`
	WeaponClass   string  `json:"weapon_class"`
	IsHeadshot    bool    `json:"is_headshot"`
	ThroughSmoke  bool    `json:"through_smoke"`
	NoScope       bool    `json:"noscope"`
	AttackerBlind bool    `json:"attacker_blind"`
	Wallbang      bool    `json:"wallbang"`
	Distance      float64 `json:"distance"`
	VictimPos     Vec3    `json:"victim_pos"`
	KillerPos     Vec3    `json:"killer_pos"`

	// M2 站位事实：死亡瞬间双方视角 + 其余存活玩家的位置/朝向快照。
	// 仅记录事实，暴露枪线等判断由 tactics 层计算。
	VictimYaw   float64          `json:"victim_yaw,omitempty"`
	VictimPitch float64          `json:"victim_pitch,omitempty"`
	OtherAlive  []PlayerSnapshot `json:"other_alive,omitempty"`
}

// PlayerSnapshot 是某一 tick 时刻一名存活玩家的位置与朝向事实。
// Yaw 为水平视角（0..360 度），Pitch 为俯仰视角（-90..90 度）。
type PlayerSnapshot struct {
	SteamID64 string  `json:"steamid64"`
	Name      string  `json:"name"`
	Side      Side    `json:"side"`
	Pos       Vec3    `json:"pos"`
	Yaw       float64 `json:"yaw"`
	Pitch     float64 `json:"pitch"`
	HP        int     `json:"hp,omitempty"`
}

// ShotFact 是一次开枪事实：位置、视角、开火瞬间水平速度。
// Speed 由相邻采样帧的位置差分得到；SpeedValid=false 表示差分不可用（首次采样/瞬移）。
type ShotFact struct {
	Tick        int     `json:"tick"`
	RoundTime   float64 `json:"round_time"`
	ShooterID   string  `json:"shooter_id"`
	ShooterName string  `json:"shooter_name"`
	ShooterSide Side    `json:"shooter_side"`
	Weapon      string  `json:"weapon"`
	Pos         Vec3    `json:"pos"`
	Yaw         float64 `json:"yaw"`
	Pitch       float64 `json:"pitch"`
	Speed       float64 `json:"speed"` // 水平速度，units/second
	SpeedValid  bool    `json:"speed_valid"`
}

// DuelPlayerFact 是对枪一侧的接敌快照与命中事实。
type DuelPlayerFact struct {
	SteamID64     string  `json:"steamid64"`
	Name          string  `json:"name"`
	Side          Side    `json:"side"`
	Pos           Vec3    `json:"pos"`
	Yaw           float64 `json:"yaw"`
	Pitch         float64 `json:"pitch"`
	Speed         float64 `json:"speed"`
	SpeedValid    bool    `json:"speed_valid"`
	FirstShotTick int     `json:"first_shot_tick"` // 0 = 接敌窗口内没有开火
	// TargetPos 是该侧首次开火瞬间对手的位置（预瞄角度差的几何依据；
	// 对手后续移动不改变它）。未开火时为零值。
	TargetPos Vec3 `json:"target_pos,omitempty"`
	Shots     int  `json:"shots"`
	Hits      int  `json:"hits"`
	Damage    int  `json:"damage"`
}

// DuelFact 是一次「接敌」事实：两名敌人在交火窗口内的首次开火快照 + 交火结果。
// A/B 按首次开火先后排序（同时开火按 SteamID 字符串排序），保证输出确定。
// 事件的「接敌瞬间」取首次开火前 1s 内最近一次开枪快照（无开枪则取首次伤害时刻）。
type DuelFact struct {
	StartTick int            `json:"start_tick"`
	EndTick   int            `json:"end_tick"`
	RoundTime float64        `json:"round_time"`
	Contact   string         `json:"contact"` // hurt = 由首次伤害触发（接敌瞬间）；kill = 仅有击杀事件（缺枪械伤害事件时的降级）
	A         DuelPlayerFact `json:"a"`
	B         DuelPlayerFact `json:"b"`

	KillTick int    `json:"kill_tick,omitempty"` // 0 = 窗口内无击杀
	KillerID string `json:"killer_id,omitempty"`
	VictimID string `json:"victim_id,omitempty"`
	// FirstHurtBy 是本次接敌中第一个对敌方造成伤害的玩家 SteamID64（先手命中事实）。
	FirstHurtBy string `json:"first_hurt_by,omitempty"`
	Headshot    bool   `json:"headshot,omitempty"`
	Weapon      string `json:"weapon,omitempty"`
}

// FlashEffect 是闪光弹对单个玩家的致盲效果事实。
type FlashEffect struct {
	PlayerID   string  `json:"player_id"`
	PlayerName string  `json:"player_name"`
	PlayerSide Side    `json:"player_side"`
	Duration   float64 `json:"duration"` // 秒
}

// UtilityEvent 是一次道具投掷事实。
type UtilityEvent struct {
	Tick        int           `json:"tick"`
	RoundTime   float64       `json:"round_time"`
	Type        string        `json:"type"` // flash / smoke / he / molotov / incendiary / decoy
	ThrowerID   string        `json:"thrower_id"`
	ThrowerName string        `json:"thrower_name"`
	ThrowerSide Side          `json:"thrower_side"`
	ThrowPos    Vec3          `json:"throw_pos"`
	LandPos     Vec3          `json:"land_pos"`
	EffectStart int           `json:"effect_start_tick,omitempty"`
	EffectEnd   int           `json:"effect_end_tick,omitempty"`
	Flashes     []FlashEffect `json:"flashes,omitempty"`   // 只记被闪的敌方（事实：致盲时长）
	HEDamage    int           `json:"he_damage,omitempty"` // 该 HE 造成的总伤害（事实，由 PlayerHurt 聚合）
}

// BombEvent 是炸弹相关事实。
type BombEvent struct {
	Tick       int     `json:"tick"`
	RoundTime  float64 `json:"round_time"`
	Type       string  `json:"type"`           // plant_begin / planted / defuse_begin / defused / exploded / drop / pickup
	Site       string  `json:"site,omitempty"` // A / B
	PlayerID   string  `json:"player_id,omitempty"`
	PlayerName string  `json:"player_name,omitempty"`
	PlayerSide Side    `json:"player_side,omitempty"`
	Pos        Vec3    `json:"pos"`
}

// RoundPlayer 是单人单回合事实。
type RoundPlayer struct {
	SteamID64   uint64  `json:"steamid64"`
	Name        string  `json:"name"`
	Side        Side    `json:"side"`
	AliveAtEnd  bool    `json:"alive_at_end"`
	DeathTick   int     `json:"death_tick,omitempty"`
	DeathTime   float64 `json:"death_time,omitempty"`
	DeathPos    *Vec3   `json:"death_pos,omitempty"`
	Kills       int     `json:"kills"`
	Deaths      int     `json:"deaths"`
	Assists     int     `json:"assists"`
	DamageDealt int     `json:"damage_dealt"` // 对敌方造成的伤害合计
	FirstKill   bool    `json:"first_kill"`
	FirstDeath  bool    `json:"first_death"`
	// UtilityAtDeath 记录死亡时身上仍持有的道具数（flash/smoke/he/molotov 合计，事实）。
	UtilityAtDeath int `json:"utility_at_death"`
}

// AliveCount 是某 tick 时刻双方存活人数。
type AliveCount struct {
	Tick    int `json:"tick"`
	TAlive  int `json:"t_alive"`
	CTAlive int `json:"ct_alive"`
}
