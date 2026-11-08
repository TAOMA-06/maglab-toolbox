package model

// Analysis 是 tactics 层的输出：全部「教练素材」。
// 数字与证据永远在这一层产生；coach 层只许解读这里的事实。
type Analysis struct {
	Meta   MatchMeta       `json:"meta"`
	Map    string          `json:"map"`
	Focus  FocusSummary    `json:"focus"`
	Story  StorySummary    `json:"story"`
	Rounds []RoundAnalysis `json:"rounds"`
	// LostRoundCauses 是丢分原因频次排名（回答「最痛的环节」）。
	LostRoundCauses []CauseStat  `json:"lost_round_causes"`
	Suspects        []Suspect    `json:"suspects"`    // 可疑回合候选（喂给 LLM 的切片索引）
	GoodRounds      []GoodRound  `json:"good_rounds"` // 值得保留的好回合
	Clutches        []ClutchInfo `json:"clutches"`
	// SiteAnchors 是本局观测到的包点锚地（下包/拆包坐标），用于点位标签。
	SiteAnchors map[string][]Vec3 `json:"site_anchors"`

	// M2：站位 / 对枪两个教练模块的汇总（全部由规则层计算，带阈值与证据 tick）。
	Positioning *PositioningReport `json:"positioning,omitempty"`
	Dueling     *DuelingReport     `json:"dueling,omitempty"`
}

// PositioningReport 是「站位分析」全场汇总。逐次死亡明细在 RoundAnalysis.Positioning。
type PositioningReport struct {
	Deaths         int     `json:"deaths"`          // 焦点玩家死亡次数（参与评估的）
	HighRiskCount  int     `json:"high_risk_count"` // 命中高危规则的死亡次数
	HighRiskRate   float64 `json:"high_risk_rate"`
	ExposedDeaths  int     `json:"exposed_deaths"`  // 暴露枪线 >=2 的死亡次数（视锥近似）
	NoCoverDeaths  int     `json:"no_cover_deaths"` // 死亡前 10s 内无己方烟/火覆盖
	IsolatedDeaths int     `json:"isolated_deaths"` // 与最近队友距离 > 阈值的死亡次数
	// HotZones 是按死亡次数降序的前 3 个死亡热区。
	HotZones   []HotZone             `json:"hot_zones"`
	Thresholds PositioningThresholds `json:"thresholds"`
}

// PositioningThresholds 记录本次计算使用的具名阈值（报告可解释、可调）。
type PositioningThresholds struct {
	ExposureMaxDistance   float64 `json:"exposure_max_distance"`    // units
	ExposureConeHalfAngle float64 `json:"exposure_cone_half_angle"` // 度
	IsolatedTeammateDist  float64 `json:"isolated_teammate_dist"`   // units
	CoverRadius           float64 `json:"cover_radius"`             // units
	CoverWindowSec        float64 `json:"cover_window_sec"`         // 秒
	HotZoneRadius         float64 `json:"hot_zone_radius"`          // units
}

// PositionDeathAnalysis 是焦点玩家一次死亡时的站位指标（规则层计算）。
type PositionDeathAnalysis struct {
	Round     int     `json:"round"`
	Tick      int     `json:"tick"`
	RoundTime float64 `json:"round_time"`
	Pos       Vec3    `json:"pos"`
	PosLabel  string  `json:"pos_label"`

	// ExposedLines 是「暴露枪线（视锥近似）」数量：存活敌人 + 距离阈值内 + 敌人朝向对准你。
	ExposedLines int            `json:"exposed_lines"`
	ExposedBy    []ExposedEnemy `json:"exposed_by,omitempty"`

	NearestTeammateDist float64 `json:"nearest_teammate_dist"`
	NearestTeammateName string  `json:"nearest_teammate_name,omitempty"`
	Isolated            bool    `json:"isolated"`
	// LastAlive 表示死亡瞬间已无存活队友（残局）。它与「队友还活着但太远」分开：
	// 后者才是站位孤立，前者只是 1vX，不作为高危站位的独立依据。
	LastAlive bool `json:"last_alive,omitempty"`

	// CoverEvaluated 表示本回合是否有道具事实可供判断掩护；
	// false 时 CoverUtility 的 false 不代表「无掩护」，不得据此判高危。
	CoverEvaluated bool   `json:"cover_evaluated"`
	CoverUtility   bool   `json:"cover_utility"` // 死亡前窗口内己方烟/火覆盖死亡位置
	CoverType      string `json:"cover_type,omitempty"`
	CoverTick      int    `json:"cover_tick,omitempty"`

	HighRisk    bool     `json:"high_risk"`
	RiskReasons []string `json:"risk_reasons,omitempty"` // 中文量化理由（含数字）
}

// ExposedEnemy 是一条暴露枪线（视锥近似）的敌人方位事实。
type ExposedEnemy struct {
	SteamID64   string  `json:"steamid64"`
	Name        string  `json:"name"`
	Side        Side    `json:"side"`
	Distance    float64 `json:"distance"`
	YawDeltaDeg float64 `json:"yaw_delta_deg"` // 敌人朝向 vs「敌人→你」方位夹角
	Sector      string  `json:"sector"`        // 相对你的方位扇区（正前/左前/…）
}

// HotZone 是死亡位置聚类（半径阈值内的死亡归为一区）。
type HotZone struct {
	Pos      Vec3   `json:"pos"` // 聚类中心（成员坐标均值）
	PosLabel string `json:"pos_label"`
	Deaths   int    `json:"deaths"`
	Rounds   []int  `json:"rounds"`
	Ticks    []int  `json:"ticks"`
}

// DuelingReport 是「对枪分析」全场汇总。逐次对枪明细在 RoundAnalysis.Duels。
type DuelingReport struct {
	Total            int                `json:"total"`              // 焦点参与的对枪数
	Measured         int                `json:"measured"`           // 有接敌快照、可评估预瞄的对枪数
	Fired            int                `json:"fired"`              // 焦点实际开火、可评估急停的对枪数
	KillOnly         int                `json:"kill_only"`          // 仅有击杀快照（缺枪械伤害事件的降级）
	PreAimFailures   int                `json:"pre_aim_failures"`   // 预瞄角度差超阈值的对枪数
	MovingShots      int                `json:"moving_shots"`       // 开火瞬间速度超阈值的对枪数
	FirstShotWins    int                `json:"first_shot_wins"`    // 焦点先开火且拿到击杀
	FirstHitterFocus int                `json:"first_hitter_focus"` // 焦点先命中（造成第一次伤害）的对枪数
	FocusKills       int                `json:"focus_kills"`
	FocusDeaths      int                `json:"focus_deaths"`
	Distribution     MetricDistribution `json:"distribution"`
	Thresholds       DuelThresholds     `json:"thresholds"`
}

// DuelThresholds 是对枪指标的具名阈值（M2b 由实测分布校准，集中可调）。
type DuelThresholds struct {
	PreAimBadDeg       float64 `json:"pre_aim_bad_deg"`
	MovingFireSpeedUPS float64 `json:"moving_fire_speed_ups"`
}

// MetricDistribution 是阈值校准用的指标分布（M2b 产物）。
type MetricDistribution struct {
	PreAimSamples    int         `json:"pre_aim_samples"`
	PreAimDeltaDeg   Percentiles `json:"pre_aim_delta_deg"`
	ShotSpeedSamples int         `json:"shot_speed_samples"`
	ShotSpeedUPS     Percentiles `json:"shot_speed_ups"`
}

// Percentiles 是分位统计（0 表示该样本不存在）。
type Percentiles struct {
	Min float64 `json:"min"`
	P50 float64 `json:"p50"`
	P75 float64 `json:"p75"`
	P90 float64 `json:"p90"`
	P95 float64 `json:"p95"`
	Max float64 `json:"max"`
}

// DuelAnalysis 是一次焦点玩家参与的对枪（规则层计算：预瞄差、急停、先手、命中）。
type DuelAnalysis struct {
	Round        int     `json:"round"`
	Tick         int     `json:"tick"` // 接敌（首次开火）tick，报告证据锚点
	RoundTime    float64 `json:"round_time"`
	OpponentID   string  `json:"opponent_id"`
	OpponentName string  `json:"opponent_name"`
	OpponentSide Side    `json:"opponent_side"`
	// Contact 是接敌来源：hurt（首次武器伤害，接敌瞬间）/ kill（缺枪械伤害事件的降级）。
	Contact string `json:"contact"`

	FocusYaw        float64 `json:"focus_yaw"`
	FocusPitch      float64 `json:"focus_pitch"`
	FocusSpeed      float64 `json:"focus_speed"` // 接敌首次开火瞬间水平速度 u/s
	FocusSpeedValid bool    `json:"focus_speed_valid"`
	// PreAimDeltaDeg 是接敌瞬间焦点准星朝向与「焦点→对手」方位角的夹角（度）。
	// PreAimMeasured=false 表示没有接敌快照（GOTV 仅击杀且焦点未开火），
	// 预瞄/急停都不可评估，也不参与分布与阈值命中统计。
	PreAimMeasured bool    `json:"pre_aim_measured"`
	PreAimDeltaDeg float64 `json:"pre_aim_delta_deg"`

	FocusFirstShotTick int     `json:"focus_first_shot_tick"` // 0 = 焦点未开火
	OppFirstShotTick   int     `json:"opp_first_shot_tick"`
	HasBothShots       bool    `json:"has_both_shots"`
	FirstShotDiffSec   float64 `json:"first_shot_diff_sec"` // 正数=焦点更晚开枪

	FocusShots  int `json:"focus_shots"`
	OppShots    int `json:"opp_shots"`
	FocusHits   int `json:"focus_hits"`
	OppHits     int `json:"opp_hits"`
	FocusDamage int `json:"focus_damage"`
	OppDamage   int `json:"opp_damage"`

	Outcome string `json:"outcome"` // focus_kill / focus_death / no_kill
	// FirstHitter 是本次接敌谁先命中：focus / opponent / none（无伤害事实，例如 GOTV 仅有击杀）。
	FirstHitter  string `json:"first_hitter"`
	PreAimFailed bool   `json:"pre_aim_failed"`
	MovingShot   bool   `json:"moving_shot"`
	Verdict      string `json:"verdict"` // 中文事实句（数字 + 可回看 tick）
}

// FocusSummary 是焦点玩家画像。
type FocusSummary struct {
	SteamID64 string           `json:"steamid64"`
	Name      string           `json:"name"`
	Sides     []FocusSideNote  `json:"sides"` // 每半场所在方
	Stats     PlayerMatchStats `json:"stats"`
	// SideStats 按 T/CT 分列的战绩。
	SideStats map[string]PlayerMatchStats `json:"side_stats"`
}

// FocusSideNote 记录某半场焦点玩家所在阵营。
type FocusSideNote struct {
	Half string `json:"half"`
	Side Side   `json:"side"`
}

// PlayerMatchStats 是规则层算出的玩家汇总数据。
type PlayerMatchStats struct {
	RoundsPlayed    int     `json:"rounds_played"`
	Kills           int     `json:"kills"`
	Deaths          int     `json:"deaths"`
	Assists         int     `json:"assists"`
	KDR             float64 `json:"kdr"`
	Damage          int     `json:"damage"`
	ADR             float64 `json:"adr"`
	FirstKills      int     `json:"first_kills"`
	FirstDeaths     int     `json:"first_deaths"`
	TradedDeaths    int     `json:"traded_deaths"`   // 死亡后 5s 内被队友补枪
	UntradedDeaths  int     `json:"untraded_deaths"` // 死亡后 5s 内无人补
	TradeKills      int     `json:"trade_kills"`     // 队友死后 5s 内补掉凶手
	ClutchWins      int     `json:"clutch_wins"`
	ClutchLosses    int     `json:"clutch_losses"`
	MultikillRounds int     `json:"multikill_rounds"`
	UtilThrown      int     `json:"util_thrown"`
	EnemiesFlashed  int     `json:"enemies_flashed"` // 致盲敌方人次（>0.5s 计）
}

// StorySummary 是全场叙事摘要（规则层事实）。
type StorySummary struct {
	FinalScore      Score `json:"final_score"`
	FocusTeamScore  int   `json:"focus_team_score"`
	OpponentScore   int   `json:"opponent_score"`
	FirstHalfScore  Score `json:"first_half_score"`
	SecondHalfScore Score `json:"second_half_score"`
	FocusWonRounds  []int `json:"focus_won_rounds"`
	FocusLostRounds []int `json:"focus_lost_rounds"`
	// WonCauses / LostCauses 按回合类型统计（full/force/eco 下的胜负）。
	WonByBuy  map[string]int `json:"won_by_buy"`  // buy type -> 赢的回合数
	LostByBuy map[string]int `json:"lost_by_buy"` // buy type -> 输的回合数
	// Comeback / Collapse 等由规则判定的比分节点
	KeySwings []string `json:"key_swings"` // 一句话事实（如「连丢 4 回合」）
}

// 丢分归因标签（与 design.md 的五类对齐）。
const (
	CauseFirstBloodGift = "first_blood_gift" // 首杀白给
	CauseTradeFail      = "trade_fail"       // trade 失败
	CauseUtilityGap     = "utility_gap"      // 道具缺口
	CauseEcoForce       = "eco_force"        // 经济局强起
	CauseClutchThrow    = "clutch_throw"     // 残局决策
	// 附加标签（非五类，用于可疑回合标注）
	CauseManAdvLost = "man_advantage_lost" // 有人数优势却丢分
)

// CauseStat 是某丢分原因的频次统计。
type CauseStat struct {
	Tag      string  `json:"tag"`
	Label    string  `json:"label"` // 中文名
	Count    int     `json:"count"`
	Rounds   []int   `json:"rounds"`
	Fraction float64 `json:"fraction"` // 占丢分回合比例
}

// RoundAnalysis 是单回合的教练素材注解。
type RoundAnalysis struct {
	Number     int    `json:"number"`
	Winner     Side   `json:"winner"`
	FocusWon   bool   `json:"focus_won"`
	ScoreAfter Score  `json:"score_after"`
	EndReason  string `json:"end_reason"`
	Half       string `json:"half"`

	// Buys 是双方买型：eco / force / half / full。
	Buys     map[Side]string `json:"buys"`
	FocusBuy string          `json:"focus_buy"`

	Opening *OpeningInfo `json:"opening,omitempty"`
	Trades  []TradeEvent `json:"trades,omitempty"`
	Clutch  *ClutchInfo  `json:"clutch,omitempty"`

	// ManAdv 是焦点方出现人数优势的区间。
	ManAdv []ManAdvSpan `json:"man_adv,omitempty"`

	// UtilBeforeContact 双方首次交火前（或前 30s 内）投入的道具数。
	UtilBeforeContact map[Side]int `json:"util_before_contact"`

	// FocusDeath 是焦点玩家本回合死亡信息（若死亡）。
	FocusDeath *FocusDeathInfo `json:"focus_death,omitempty"`

	// M2：本回合焦点玩家的站位指标与对枪明细（规则层计算）。
	Positioning *PositionDeathAnalysis `json:"positioning,omitempty"`
	Duels       []DuelAnalysis         `json:"duels,omitempty"`

	// Causes 是焦点方丢分时的归因标签（赢的回合为空）。
	Causes      []string          `json:"causes,omitempty"`
	CauseDetail map[string]string `json:"cause_detail,omitempty"` // 标签 -> 一句话证据

	// Call 是丢分回合的赛后判决（谁先倒 → 下次照旧还是该改）。赢/平局为空。
	Call *RoundCall `json:"call,omitempty"`

	// WinCost 是「这分不该丢」的程度（0-1，越高越可惜）。用于 Top3 排序。
	WinCost float64 `json:"win_cost"`

	Suspect     bool     `json:"suspect"`
	SuspectTags []string `json:"suspect_tags,omitempty"`

	TickRange [2]int     `json:"tick_range"` // 供 LLM 切片的 tick 区间 [start, end]
	Evidence  []Evidence `json:"evidence"`   // 本回合硬证据（带 tick）
}

// 赛后判决种类（RoundCall.Kind）。
const (
	CallYours    = "yours"    // 焦点是我方第一个死者
	CallTeammate = "teammate" // 队友先死，焦点没再死
	CallSplit    = "split"    // 队友先死，焦点后来又死
	CallUnknown  = "unknown"  // 我方没人死 / 对不上边
)

// RoundCall 是单回合丢分判决（报告第一屏的一行）。
type RoundCall struct {
	Kind string `json:"kind"` // yours / teammate / split / unknown

	// 我方第一个死者那一枪（yours / teammate / split）。
	VictimName string `json:"victim_name,omitempty"`
	KillerName string `json:"killer_name,omitempty"`
	Weapon     string `json:"weapon,omitempty"`
	Tick       int    `json:"tick,omitempty"`

	// 焦点后来那一枪（仅 split）。
	FocusKillerName string `json:"focus_killer_name,omitempty"`
	FocusWeapon     string `json:"focus_weapon,omitempty"`
	FocusTick       int    `json:"focus_tick,omitempty"`
}

// OpeningInfo 是开局对枪事实+规则标注。
type OpeningInfo struct {
	Tick       int     `json:"tick"`
	RoundTime  float64 `json:"round_time"`
	KillerID   string  `json:"killer_id"`
	KillerName string  `json:"killer_name"`
	KillerSide Side    `json:"killer_side"`
	VictimID   string  `json:"victim_id"`
	VictimName string  `json:"victim_name"`
	VictimSide Side    `json:"victim_side"`
	Weapon     string  `json:"weapon"`
	// TradedWithinWindow：凶手是否在 TRADE_WINDOW 秒内被补（规则：5s）。
	TradedWithinWindow bool   `json:"traded_within_window"`
	TradeTick          int    `json:"trade_tick,omitempty"`
	TradeByID          string `json:"trade_by_id,omitempty"`
	TradeByName        string `json:"trade_by_name,omitempty"`
	FocusRole          string `json:"focus_role"` // killer / victim / assist / none
}

// TradeEvent 是一次补枪尝试（规则层计算，窗口 5s）。
type TradeEvent struct {
	Tick        int     `json:"tick"`
	VictimID    string  `json:"victim_id"` // 先死的队友
	VictimName  string  `json:"victim_name"`
	VictimSide  Side    `json:"victim_side"`
	KilledID    string  `json:"killed_id"` // 凶手
	KilledName  string  `json:"killed_name"`
	AvengerID   string  `json:"avenger_id"` // 补枪者
	AvengerName string  `json:"avenger_name"`
	AvengerSide Side    `json:"avenger_side"`
	Success     bool    `json:"success"` // 5s 内是否补掉
	DelaySec    float64 `json:"delay_sec"`
}

// ManAdvSpan 是人数优势区间。
type ManAdvSpan struct {
	StartTick int  `json:"start_tick"`
	EndTick   int  `json:"end_tick"`
	AdvSide   Side `json:"adv_side"`  // 哪边有人数优势
	AdvDelta  int  `json:"adv_delta"` // 优势人数（如 5v3 -> 2）
	Threw     bool `json:"threw"`     // 最终是否被翻盘
}

// ClutchInfo 是残局情况（规则层判定）。
type ClutchInfo struct {
	StartTick     int    `json:"start_tick"`
	EndTick       int    `json:"end_tick"`
	Situation     string `json:"situation"` // "1v2" / "2v1" / "1v1" ...
	FocusInClutch bool   `json:"focus_in_clutch"`
	FocusSide     Side   `json:"focus_side,omitempty"`
	Result        string `json:"result"` // won / lost / drawn
	WinnerSide    Side   `json:"winner_side"`
	// Advantageous：残局开始时焦点方是否占优（人数或全甲全枪 vs 残枪）
	Advantageous bool `json:"advantageous"`
	HPFocus      int  `json:"hp_focus,omitempty"` // 残局开始时焦点玩家 HP（若在残局）
	HPOppMin     int  `json:"hp_opp_min,omitempty"`
}

// FocusDeathInfo 是焦点玩家死亡细节（事实+规则标注）。
type FocusDeathInfo struct {
	Tick          int     `json:"tick"`
	RoundTime     float64 `json:"round_time"`
	KillerName    string  `json:"killer_name"`
	KillerSide    Side    `json:"killer_side"`
	Weapon        string  `json:"weapon"`
	Headshot      bool    `json:"headshot"`
	ThroughSmoke  bool    `json:"through_smoke"`
	Traded        bool    `json:"traded"` // 死后 5s 内被队友补掉凶手
	TradeDelaySec float64 `json:"trade_delay_sec,omitempty"`
	PosLabel      string  `json:"pos_label"` // 点位标签（锚地推断 / 坐标兜底）
	Pos           Vec3    `json:"pos"`
	UtilityHeld   int     `json:"utility_held"` // 死时身上剩余道具
	IsFirstDeath  bool    `json:"is_first_death"`
}

// Suspect 是可疑回合候选（LLM 输入切片的索引与理由）。
type Suspect struct {
	Round     int      `json:"round"`
	TickRange [2]int   `json:"tick_range"`
	Tags      []string `json:"tags"`
	Reason    string   `json:"reason"` // 一句话：为什么可疑
	ScoreHint float64  `json:"score_hint"`
}

// GoodRound 是值得保留的好回合。
type GoodRound struct {
	Round  int    `json:"round"`
	Reason string `json:"reason"`
	// Highlight 是该回合的高光事实（多杀/残局/关键道具）。
	Highlight string `json:"highlight"`
	Tick      int    `json:"tick"` // 高光时刻 tick
}

// Evidence 是一条带 tick 的硬证据（报告引用的锚点）。
type Evidence struct {
	Tick      int     `json:"tick"`
	RoundTime float64 `json:"round_time"`
	Kind      string  `json:"kind"` // kill / utility / bomb / economy / alive / buy
	Text      string  `json:"text"` // 一句话事实描述（含人名/武器/数字）
}

// QASlice 是问答层的检索结果。
type QASlice struct {
	Question    string          `json:"question"`
	Rounds      []int           `json:"rounds"`
	Analyses    []RoundAnalysis `json:"analyses"`
	ExtraFacts  []Evidence      `json:"extra_facts"`
	KeywordHits []string        `json:"keyword_hits"`
}
