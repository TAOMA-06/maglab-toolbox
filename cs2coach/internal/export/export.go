// Package export 把 demo 的三层管线产物（model.Match 事实 + model.Analysis 规则素材）
// 扁平化成「一回合一行」的训练样本（JSONL），供自学习框架消费。
//
// 设计约束：
//   - 只导出事实与 tactics 层已算出的数字/标签，本包不做任何推断或补全；
//   - 字段固定（缺事实时输出零值），下游训练脚本可按列对齐；
//   - 焦点玩家沿用 parse 契约：Options.Parse 显式指定 > POV 录制者 > 报错列出全部玩家。
package export

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cs2-demo-review/internal/model"
	"cs2-demo-review/internal/parse"
	"cs2-demo-review/internal/tactics"
)

// Options 控制导出行为。
type Options struct {
	// Parse 控制单场解析：焦点玩家（FocusName / FocusSteamID）与 POV 兜底规则。
	// 零值 = 沿用 parse 契约：显式指定 > POV 录制者 > 报错列出全部玩家。
	Parse parse.Options
	// TradeWindowSec 补枪窗口（秒），透传 tactics.Options；0 = tactics 默认 5 秒。
	TradeWindowSec float64
	// Recursive 目录模式下递归子目录。
	Recursive bool
}

// ManAdvantageSpan 是焦点方人数优势区间（tactics 层事实，原样导出）。
type ManAdvantageSpan struct {
	StartTick int    `json:"start_tick"`
	EndTick   int    `json:"end_tick"`
	AdvSide   string `json:"adv_side"`  // 哪边有人数优势（T / CT）
	AdvDelta  int    `json:"adv_delta"` // 优势人数
	Threw     bool   `json:"threw"`     // 该区间后最终是否丢分
}

// Sample 是一个回合的训练样本（JSONL 的一行）。
//
// 字段名即 JSON 键名，与导出契约一一对应；无法从事实得出时输出零值/空串，
// 不编造。utility 的四个计数是本回合双方合计的投掷数（decoy 不计，
// molotov_count 含燃烧弹 incendiary），he_damage_total 为 HE 造成的总伤害。
type Sample struct {
	// meta
	SourceFile  string  `json:"source_file"`
	Map         string  `json:"map"`
	TickRate    float64 `json:"tick_rate"`
	FocusPlayer string  `json:"focus_player"` // 焦点玩家 SteamID64 十进制字符串

	// context
	RoundNumber int         `json:"round_number"`
	Half        string      `json:"half"`         // H1 / H2 / OT1 ...
	ScoreBefore model.Score `json:"score_before"` // 本回合开始前比分（上一回合 ScoreAfter；首回合 0:0）

	// economy（回合开始现金 / 冻结结束装备值 / 本回合花费）
	TMoney           int    `json:"t_money"`
	CTMoney          int    `json:"ct_money"`
	TEquipFreezeEnd  int    `json:"t_equip_freeze_end"`
	CTEquipFreezeEnd int    `json:"ct_equip_freeze_end"`
	TMoneySpent      int    `json:"t_money_spent"`
	CTMoneySpent     int    `json:"ct_money_spent"`
	FocusBuy         string `json:"focus_buy"` // 焦点方买型：eco / force / half / full
	EnemyBuy         string `json:"enemy_buy"` // 敌方买型

	// opening（开局对枪）
	HasFirstKill       bool    `json:"has_first_kill"`
	FirstKillerSide    string  `json:"first_killer_side"`     // T / CT，世界击杀为空
	FirstVictimSide    string  `json:"first_victim_side"`     // T / CT
	FirstKillRoundTime float64 `json:"first_kill_round_time"` // 秒，自回合开始（含冻结）
	TradedWithinWindow bool    `json:"traded_within_window"`  // 首杀凶手是否在补枪窗口内被补

	// utility（道具事实）
	FocusUtilBeforeContact int `json:"focus_util_before_contact"` // 交火前焦点方投掷数
	EnemyUtilBeforeContact int `json:"enemy_util_before_contact"` // 交火前敌方投掷数
	FlashCount             int `json:"flash_count"`
	SmokeCount             int `json:"smoke_count"`
	HECount                int `json:"he_count"`
	MolotovCount           int `json:"molotov_count"` // 含燃烧弹
	HEDamageTotal          int `json:"he_damage_total"`

	// alive（存活态势）
	StartTAlive       int                `json:"start_t_alive"`
	StartCTAlive      int                `json:"start_ct_alive"`
	ClutchEntered     bool               `json:"clutch_entered"`      // 本回合是否出现残局（双方存活都 ≤2）
	ClutchSituation   string             `json:"clutch_situation"`    // 如 1v2（焦点方视角）；无残局为空
	ManAdvantageSpans []ManAdvantageSpan `json:"man_advantage_spans"` // 焦点方人数优势区间

	// bomb
	Planted        bool    `json:"planted"`
	PlantSite      string  `json:"plant_site"` // A / B；未下包为空
	Defused        bool    `json:"defused"`
	Exploded       bool    `json:"exploded"`
	PlantRoundTime float64 `json:"plant_round_time"` // 秒，自回合开始；未下包为 0

	// outcome labels（tactics 层产物，原样透传）
	WinnerSide string   `json:"winner_side"` // T / CT / draw
	FocusWon   bool     `json:"focus_won"`
	EndReason  string   `json:"end_reason"`
	WinCost    float64  `json:"win_cost"`
	CauseTags  []string `json:"cause_tags"`
}

// Failure 是单个 demo 的失败记录；其余 demo 继续处理。
type Failure struct {
	File string
	Err  error
}

// Error 实现 error，便于日志输出。
func (f Failure) Error() string {
	return f.File + ": " + f.Err.Error()
}

// Result 是一次批量导出的汇总。
type Result struct {
	TotalFiles int       // 发现的 demo 数
	Succeeded  int       // 成功导出的 demo 数
	Failed     []Failure // 解析/归因失败的 demo（已跳过）
	Samples    int       // 写出的回合样本行数
	OutputPath string
}

// BuildSamples 解析单个 demo 并生成该场全部回合样本。
func BuildSamples(demoPath string, opts Options) ([]Sample, error) {
	m, err := parse.Parse(demoPath, opts.Parse)
	if err != nil {
		return nil, err
	}
	a, err := tactics.Analyze(m, tactics.Options{TradeWindowSec: opts.TradeWindowSec})
	if err != nil {
		return nil, err
	}
	return SamplesFromMatch(m, a)
}

// SamplesFromMatch 把已解析的 Match + Analysis 转成回合样本（纯函数，单测入口）。
// Match.Rounds 需按回合号升序（parse 层保证）；Analysis 必须覆盖所有回合号。
func SamplesFromMatch(m *model.Match, a *model.Analysis) ([]Sample, error) {
	if m == nil {
		return nil, fmt.Errorf("export: Match 为空")
	}
	if a == nil {
		return nil, fmt.Errorf("export: Analysis 为空")
	}
	focusID := strings.TrimSpace(m.Meta.FocusPlayer)
	if focusID == "" {
		return nil, fmt.Errorf("export: 未确定焦点玩家（Match.Meta.FocusPlayer 为空）")
	}
	byNumber := make(map[int]*model.RoundAnalysis, len(a.Rounds))
	for i := range a.Rounds {
		byNumber[a.Rounds[i].Number] = &a.Rounds[i]
	}

	samples := make([]Sample, 0, len(m.Rounds))
	prevScore := model.Score{}
	for i := range m.Rounds {
		r := &m.Rounds[i]
		ra, ok := byNumber[r.Number]
		if !ok {
			return nil, fmt.Errorf("export: 回合 %d 缺少 Analysis 数据（analysis 与 match 不匹配）", r.Number)
		}
		samples = append(samples, newSample(m, r, ra, prevScore, focusID))
		prevScore = r.ScoreAfter
	}
	return samples, nil
}

// newSample 从单回合事实 + 规则素材组装一条样本。
func newSample(m *model.Match, r *model.Round, ra *model.RoundAnalysis, scoreBefore model.Score, focusID string) Sample {
	focusSide := roundFocusSide(r, focusID)
	enemySide := otherSide(focusSide)

	half := r.Half
	if half == "" {
		half = ra.Half
	}
	s := Sample{
		SourceFile:        m.Meta.SourceFile,
		Map:               m.Meta.Map,
		TickRate:          m.Meta.TickRate,
		FocusPlayer:       focusID,
		RoundNumber:       r.Number,
		Half:              half,
		ScoreBefore:       scoreBefore,
		TMoney:            r.Economy.T.Money,
		CTMoney:           r.Economy.CT.Money,
		TEquipFreezeEnd:   r.Economy.T.EquipValueFreezeEnd,
		CTEquipFreezeEnd:  r.Economy.CT.EquipValueFreezeEnd,
		TMoneySpent:       r.Economy.T.MoneySpent,
		CTMoneySpent:      r.Economy.CT.MoneySpent,
		FocusBuy:          ra.FocusBuy,
		EnemyBuy:          ra.Buys[enemySide],
		ManAdvantageSpans: make([]ManAdvantageSpan, 0, len(ra.ManAdv)),
		CauseTags:         append([]string{}, ra.Causes...),
	}
	// 兜底：Analysis.FocusBuy 为空但 Buys 里能查到焦点边（手工构造的 Analysis）。
	if s.FocusBuy == "" {
		s.FocusBuy = ra.Buys[focusSide]
	}

	// opening：tactics 层取本回合第一次击杀并标注是否被补。
	if op := ra.Opening; op != nil {
		s.HasFirstKill = true
		s.FirstKillerSide = string(op.KillerSide)
		s.FirstVictimSide = string(op.VictimSide)
		s.FirstKillRoundTime = op.RoundTime
		s.TradedWithinWindow = op.TradedWithinWindow
	}

	// utility：本回合双方投掷合计（事实）。
	for _, ev := range r.Utility {
		switch ev.Type {
		case "flash":
			s.FlashCount++
		case "smoke":
			s.SmokeCount++
		case "he":
			s.HECount++
			s.HEDamageTotal += ev.HEDamage
		case "molotov", "incendiary":
			s.MolotovCount++
		}
	}
	s.FocusUtilBeforeContact = ra.UtilBeforeContact[focusSide]
	s.EnemyUtilBeforeContact = ra.UtilBeforeContact[enemySide]

	// alive：回合开始存活数（Alive[0]，缺失时按 RoundPlayer 阵营计数）。
	s.StartTAlive, s.StartCTAlive = startAlive(r)
	if cl := ra.Clutch; cl != nil {
		s.ClutchEntered = true
		s.ClutchSituation = cl.Situation
	}
	for _, sp := range ra.ManAdv {
		s.ManAdvantageSpans = append(s.ManAdvantageSpans, ManAdvantageSpan{
			StartTick: sp.StartTick,
			EndTick:   sp.EndTick,
			AdvSide:   string(sp.AdvSide),
			AdvDelta:  sp.AdvDelta,
			Threw:     sp.Threw,
		})
	}

	// bomb：只认 parse 记录的事件；planted 取第一次（正常每回合至多一次）。
	for _, b := range r.Bomb {
		switch b.Type {
		case "planted":
			if !s.Planted {
				s.Planted = true
				s.PlantSite = b.Site
				s.PlantRoundTime = b.RoundTime
			}
		case "defused":
			s.Defused = true
		case "exploded":
			s.Exploded = true
		}
	}

	// outcome labels
	s.WinnerSide = string(r.Winner)
	s.FocusWon = ra.FocusWon
	s.EndReason = r.EndReason
	s.WinCost = ra.WinCost
	return s
}

// roundFocusSide 取焦点玩家本回合所在边；规则与 tactics 一致：
// 优先 RoundPlayer.Side，缺失时按击杀事件的阵营兜底，仍未知返回 SideNone。
func roundFocusSide(r *model.Round, focusID string) model.Side {
	if focusID == "" {
		return model.SideNone
	}
	for _, p := range r.Players {
		if fmt.Sprint(p.SteamID64) == focusID {
			return p.Side
		}
	}
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

// otherSide 返回对方阵营。
func otherSide(s model.Side) model.Side {
	switch s {
	case model.SideT:
		return model.SideCT
	case model.SideCT:
		return model.SideT
	default:
		return model.SideNone
	}
}

// startAlive 取回合开始双方存活数：Alive 序列首条为回合开始快照；
// 没有存活序列时按 RoundPlayer 阵营计数（事实兜底，不假设 5v5）。
func startAlive(r *model.Round) (int, int) {
	if len(r.Alive) > 0 {
		return r.Alive[0].TAlive, r.Alive[0].CTAlive
	}
	var t, ct int
	for _, p := range r.Players {
		switch p.Side {
		case model.SideT:
			t++
		case model.SideCT:
			ct++
		}
	}
	return t, ct
}

// CollectDemos 把输入路径展开成 demo 文件列表：
// 单个文件原样返回；目录只收 *.dem（大小写不敏感），opts 递归时进入子目录。
func CollectDemos(path string, recursive bool) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	var demos []string
	if recursive {
		err = filepath.WalkDir(path, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			if isDemoFile(p) {
				demos = append(demos, p)
			}
			return nil
		})
	} else {
		var entries []os.DirEntry
		entries, err = os.ReadDir(path)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				p := filepath.Join(path, e.Name())
				if isDemoFile(p) {
					demos = append(demos, p)
				}
			}
		}
	}
	if err != nil {
		return nil, err
	}
	sort.Strings(demos) // 输出顺序稳定，便于 diff/复现
	return demos, nil
}

// isDemoFile 判断扩展名是否为 .dem（大小写不敏感）。
func isDemoFile(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".dem")
}

// Run 批量导出：path 可以是单个 demo 或目录；逐个解析，失败的跳过并记录，
// 成功的回合样本按 JSONL 追加写入 outPath。
func Run(path, outPath string, opts Options) (*Result, error) {
	return run(path, outPath, opts, BuildSamples)
}

// run 是 Run 的实现，build 可在单测中替换（不依赖真实 demo）。
func run(path, outPath string, opts Options, build func(string, Options) ([]Sample, error)) (*Result, error) {
	demos, err := CollectDemos(path, opts.Recursive)
	if err != nil {
		return nil, err
	}
	if len(demos) == 0 {
		return nil, fmt.Errorf("export: %s 下没有找到 .dem 文件", path)
	}
	if dir := filepath.Dir(outPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.Create(outPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	bw := bufio.NewWriterSize(f, 64*1024)
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false) // 保留中文/特殊字符原样，方便人工抽查
	res := &Result{TotalFiles: len(demos), OutputPath: outPath}

	for _, demo := range demos {
		samples, err := build(demo, opts)
		if err != nil {
			res.Failed = append(res.Failed, Failure{File: demo, Err: err})
			continue
		}
		for _, s := range samples {
			if err := enc.Encode(s); err != nil {
				return nil, fmt.Errorf("export: 写入 %s 失败: %w", outPath, err)
			}
		}
		res.Succeeded++
		res.Samples += len(samples)
	}
	if err := bw.Flush(); err != nil {
		return nil, fmt.Errorf("export: 写入 %s 失败: %w", outPath, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("export: 关闭 %s 失败: %w", outPath, err)
	}
	return res, nil
}
