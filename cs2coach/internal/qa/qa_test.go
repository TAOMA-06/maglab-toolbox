package qa

import (
	"strings"
	"testing"

	"cs2-demo-review/internal/model"
)

// testAnalysis 构造 3 回合最小 Analysis（与 coach fixtures 对齐的形状）。
func testAnalysis() *model.Analysis {
	ev := func(tick int, kind, text string) model.Evidence {
		return model.Evidence{Tick: tick, RoundTime: float64(tick) / 64, Kind: kind, Text: text}
	}
	mk := func(n int, won bool, wincost float64, clutch bool, opening bool) model.RoundAnalysis {
		ra := model.RoundAnalysis{
			Number: n, Winner: model.SideCT, FocusWon: won, Half: "H1",
			ScoreAfter: model.Score{T: 1, CT: 1}, EndReason: "elimination",
			Buys:     map[model.Side]string{model.SideT: "force", model.SideCT: "full"},
			FocusBuy: "force", WinCost: wincost, TickRange: [2]int{n * 1000, n*1000 + 900},
			UtilBeforeContact: map[model.Side]int{model.SideT: 1, model.SideCT: 4},
			Evidence: []model.Evidence{
				ev(n*1000+100, "kill", "击杀事实"),
				ev(n*1000+200, "economy", "经济事实"),
				ev(n*1000+300, "utility", "道具事实"),
			},
		}
		if opening {
			ra.Opening = &model.OpeningInfo{Tick: n*1000 + 100, KillerName: "敌1", VictimName: "焦点", TradedWithinWindow: false, FocusRole: "victim"}
			ra.Trades = []model.TradeEvent{{Tick: n*1000 + 500, VictimName: "焦点", Success: false, DelaySec: 8}}
		}
		if clutch {
			ra.Clutch = &model.ClutchInfo{StartTick: n*1000 + 800, EndTick: n*1000 + 900, Situation: "1v2", Result: "lost", FocusInClutch: true}
			ra.Evidence = append(ra.Evidence, ev(n*1000+800, "alive", "进入 1v2 残局"))
		}
		return ra
	}
	return &model.Analysis{
		Meta: model.MatchMeta{Map: "de_mirage", TickRate: 64, TotalRounds: 3},
		Rounds: []model.RoundAnalysis{
			mk(1, true, 0, false, false),
			mk(2, false, 0.6, false, true),
			mk(3, false, 0.35, true, false),
		},
	}
}

func TestExtractRounds(t *testing.T) {
	valid := map[int]bool{1: true, 2: true, 3: true}
	cases := []struct {
		q    string
		want []int
	}{
		{"第 2 回合我为什么先死？", []int{2}},
		{"第2回合和第3回合对比", []int{2, 3}},
		{"round 2 为什么丢", []int{2}},
		{"R3 的残局怎么打", []int{3}},
		{"残局问题", nil},
		{"第 99 回合", nil}, // 超出范围丢弃
	}
	for _, c := range cases {
		got := extractRounds(c.q, valid)
		if len(got) != len(c.want) {
			t.Errorf("%q: got %v want %v", c.q, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q: got %v want %v", c.q, got, c.want)
				break
			}
		}
	}
}

func TestRetrieveExplicitRound(t *testing.T) {
	s := Retrieve(testAnalysis(), "第 2 回合我为什么先死？")
	if len(s.Rounds) != 1 || s.Rounds[0] != 2 {
		t.Errorf("应命中回合 2: %+v", s.Rounds)
	}
	if len(s.Analyses) != 1 || s.Analyses[0].Number != 2 {
		t.Error("Analyses 应为回合 2 全量")
	}
}

func TestWordBoundaryMatch(t *testing.T) {
	// ASCII 词紧贴中文也应命中；孤立 "he" 不应误命中其他单词。
	tags, _ := matchKeywords("1v2残局怎么打")
	if len(tags) == 0 || tags[0] != "clutch" {
		t.Errorf("1v2残局 应命中 clutch，got %v", tags)
	}
	tags, _ = matchKeywords("trade失败了")
	found := false
	for _, tg := range tags {
		if tg == "trade" {
			found = true
		}
	}
	if !found {
		t.Errorf("trade失败 应命中 trade，got %v", tags)
	}
	tags, _ = matchKeywords("the hero played well")
	for _, tg := range tags {
		if tg == "utility" {
			t.Errorf("the hero 不应命中 utility(he)，got %v", tags)
		}
	}
}

func TestRetrieveDrawExcludedFromFallback(t *testing.T) {
	// 无关键词回退只取丢分回合：全胜 Analysis 应返回空（不编造相关性）。
	a := testAnalysis()
	for i := range a.Rounds {
		a.Rounds[i].FocusWon = true
		a.Rounds[i].Winner = "T"
		a.Rounds[i].Causes = nil
		a.Rounds[i].WinCost = 0
	}
	s := Retrieve(a, "这场打得怎么样？")
	if len(s.Rounds) != 0 {
		t.Errorf("全胜无关键词应返回空，got %v", s.Rounds)
	}
}

func TestRetrieveByKeyword(t *testing.T) {
	s := Retrieve(testAnalysis(), "残局怎么打？")
	if len(s.KeywordHits) == 0 || s.KeywordHits[0] != "clutch" {
		t.Errorf("应命中 clutch 标签: %v", s.KeywordHits)
	}
	found := false
	for _, n := range s.Rounds {
		if n == 3 {
			found = true
		}
	}
	if !found {
		t.Errorf("有残局的回合 3 应被检索到: %v", s.Rounds)
	}
	if len(s.Rounds) > MaxRounds {
		t.Errorf("无显式回合号时最多 %d 个: %v", MaxRounds, s.Rounds)
	}
}

func TestRetrieveEmptyQuestion(t *testing.T) {
	s := Retrieve(testAnalysis(), "   ")
	if len(s.Rounds) != 0 || len(s.Analyses) != 0 {
		t.Errorf("空问题不应编造相关回合: %v", s.Rounds)
	}
}

func TestRetrieveNoKeywordFallsBackToWinCost(t *testing.T) {
	s := Retrieve(testAnalysis(), "这场哪打得不好？")
	if len(s.Rounds) == 0 || s.Rounds[0] != 2 {
		t.Errorf("无关键词时应按 WinCost 取 R2 打头: %v", s.Rounds)
	}
}

func TestAnswerFacts(t *testing.T) {
	a := testAnalysis()
	s := Retrieve(a, "第 2 回合为什么丢？")
	out := AnswerFacts(a, s)
	for _, want := range []string{"回合 2", "焦点方丢分", "[tick", "买型"} {
		if !strings.Contains(out, want) {
			t.Errorf("回答缺 %q:\n%s", want, out)
		}
	}
	if out2 := AnswerFacts(nil, nil); !strings.Contains(out2, "没有可用的复盘数据") {
		t.Errorf("nil 应降级: %q", out2)
	}
	empty := &model.QASlice{Question: "第 1 回合？"}
	if out3 := AnswerFacts(a, empty); !strings.Contains(out3, "给定数据里没有") {
		t.Errorf("空切片应答「给定数据里没有」: %q", out3)
	}
}

// TestExtractRoundsChineseNumerals 中文数字回合号（小模型/玩家都常这么写）。
func TestExtractRoundsChineseNumerals(t *testing.T) {
	valid := map[int]bool{1: true, 2: true, 3: true}
	cases := []struct {
		q    string
		want []int
	}{
		{"第二回合我为什么先死？", []int{2}},
		{"第三局怎么样", []int{3}},
		{"第十二回合", nil}, // 超出本场范围，丢弃
		{"第二回合和第三回合", []int{2, 3}},
		{"两回合连丢", []int{2}},
	}
	for _, c := range cases {
		got := extractRounds(c.q, valid)
		if len(got) != len(c.want) {
			t.Errorf("%q: got %v want %v", c.q, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q: got %v want %v", c.q, got, c.want)
				break
			}
		}
	}
}

// TestCnNumeral 中文数字解析（1..99）。
func TestCnNumeral(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"一", 1, true}, {"十", 10, true}, {"十二", 12, true},
		{"二十", 20, true}, {"二十四", 24, true}, {"十九", 19, true},
		{"", 0, false}, {"回合", 0, false},
	}
	for _, c := range cases {
		got, ok := cnNumeral(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("cnNumeral(%q) = %d,%v want %d,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// TestRetrieveByTick tick 是最精确的定位方式：问题里给 tick，反查所属回合。
func TestRetrieveByTick(t *testing.T) {
	a := testAnalysis() // 回合 2 区间 2000~2900
	s := Retrieve(a, "tick 2500 那波补枪怎么回事？")
	if len(s.Rounds) != 1 || s.Rounds[0] != 2 {
		t.Fatalf("tick 2500 应定位回合 2，got %v", s.Rounds)
	}
	s = Retrieve(a, "demo_gototick 3800 这个残局")
	if len(s.Rounds) != 1 || s.Rounds[0] != 3 {
		t.Fatalf("tick 3800 应定位回合 3，got %v", s.Rounds)
	}
}

// TestRetrieveKeywordChineseCoverage 口语关键词必须能命中对应标签。
func TestRetrieveKeywordChineseCoverage(t *testing.T) {
	cases := []struct {
		q       string
		wantTag string
	}{
		{"开局就被打死", "opening"},
		{"第一个死的是谁", "opening"},
		{"一换一怎么还亏", "trade"},
		{"补掉凶手了吗", "trade"},
		{"起狙了没", "economy"},
		{"强起值不值", "economy"},
		{"下包之后怎么处理", "bomb"},
		{"多打少还输", "man_adv"},
		{"3v5 怎么打", "clutch"},
		{"只剩我一个怎么办", "clutch"},
	}
	for _, c := range cases {
		tags, _ := matchKeywords(c.q)
		found := false
		for _, tag := range tags {
			if tag == c.wantTag {
				found = true
			}
		}
		if !found {
			t.Errorf("%q 应命中 %q，got %v", c.q, c.wantTag, tags)
		}
	}
}

// TestKeywordExceptWords 「火力/火车」不该被当成道具话题。
func TestKeywordExceptWords(t *testing.T) {
	for _, q := range []string{"火力不够", "火车这张图怎么打"} {
		tags, _ := matchKeywords(q)
		for _, tag := range tags {
			if tag == "utility" {
				t.Errorf("%q 误命中 utility：%v", q, tags)
			}
		}
	}
}

// TestMatchCountTag 人数写法归类：多的在前 = 人数优势，少的在前 = 残局。
func TestMatchCountTag(t *testing.T) {
	cases := []struct {
		q   string
		tag string
		ok  bool
	}{
		{"5打3", "man_adv", true},
		{"3v5", "clutch", true},
		{"2v2", "", false},
	}
	for _, c := range cases {
		tag, _, ok := matchCountTag(c.q)
		if tag != c.tag || ok != c.ok {
			t.Errorf("matchCountTag(%q) = %q,%v want %q,%v", c.q, tag, ok, c.tag, c.ok)
		}
	}
}

// TestRetrieveNoRelevanceReturnsEmpty 关键词命中不到任何回合时返回空切片，
// 不能拿 WinCost 最高的回合凑数（否则模型会硬编不相关的故事）。
func TestRetrieveNoRelevanceReturnsEmpty(t *testing.T) {
	s := Retrieve(testAnalysis(), "多打少还输") // fixture 里没有 ManAdv 数据
	if len(s.Rounds) != 0 {
		t.Errorf("无相关回合时应返回空，got %v", s.Rounds)
	}
	out := AnswerFacts(testAnalysis(), s)
	if !strings.Contains(out, "给定数据里没有") {
		t.Errorf("空切片应答「给定数据里没有」：%q", out)
	}
}

// TestAnswerFactsTickMarkers 降级回答必须带 tick 锚点，且给出可复制的引用写法。
func TestAnswerFactsTickMarkers(t *testing.T) {
	a := testAnalysis()
	out := AnswerFacts(a, Retrieve(a, "第2回合为什么丢？"))
	for _, want := range []string{
		"## 回合 2 [tick 2000]",
		"- 引用写法：回合 2 [tick 2000]",
		"可用引用（tick 均在对应回合区间内）",
		"- 回合 2：2100、2200、2300、2500",
		"- 开局对枪：[tick 2100]",
		"- 补枪：[tick 2500]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("回答缺少 %q：\n%s", want, out)
		}
	}
}
