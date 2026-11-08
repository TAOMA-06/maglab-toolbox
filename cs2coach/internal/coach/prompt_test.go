package coach

import (
	"strings"
	"testing"
	"unicode/utf8"

	"cs2-demo-review/internal/model"
)

// TestReportPrompt 表驱动断言报告提示词包含契约要求的全部要素。
func TestReportPrompt(t *testing.T) {
	a := testAnalysis()
	system, user := ReportPrompt(a)

	tests := []struct {
		name string
		in   string
		want string
	}{
		// system：人设 + 架构锁 + 固定结构
		{"system 人设", system, "CS2 赛后复盘教练"},
		{"system 禁止发明", system, "禁止发明"},
		{"system 引用格式", system, "回合R [tick T]"},
		{"system 无证据直接删", system, "直接删"},
		{"system 结论段", system, "一句话结论"},
		{"system Top3 段", system, "丢分回合 Top3"},
		{"system 三段式", system, "发生了什么"},
		{"system 三段式-为什么", system, "为什么错"},
		{"system 三段式-怎么改", system, "下把怎么改"},
		{"system 好回合段", system, "值得保留的好回合"},
		{"system 追问段", system, "追问建议"},
		{"system demo_gototick", system, "demo_gototick"},

		// user：焦点画像
		{"user 焦点名", user, "张三"},
		{"user SteamID", user, "76561198000000001"},
		{"user 地图", user, "de_mirage"},

		// user：Story / 丢分原因
		{"user 最终比分", user, "T 13 - CT 9"},
		{"user 丢分原因频次", user, "首杀白给"},
		{"user 丢分原因标签", user, "trade_fail"},
		{"user 关键节点", user, "连丢"},

		// user：回合切片与引用（Suspects 顺序）
		{"user 切片1回合", user, "切片 1：回合 2"},
		{"user 切片2回合", user, "切片 2：回合 3"},
		{"user tick 引用", user, "[tick 5100]"},
		{"user 经济证据 tick", user, "[tick 5300]"},
		{"user tick 区间", user, "5001 ~ 9000"},
		{"user 回放入口", user, "demo_gototick 5001"},
		{"user 买型经济事实", user, "force"},
		{"user 归因 detail", user, "焦点玩家首死"},

		// user：游戏知识（经济规则数字）
		{"user 经济-胜奖励", user, "3250"},
		{"user 经济-下包胜/拆包胜", user, "3500"},
		{"user 经济-败方补偿", user, "1400"},
		{"user 经济-连败封顶", user, "3400"},
		{"user 经济-下包加钱", user, "$800"}, // 数据源 economy.md 为 +$800 写法
		{"user 经济-上限", user, "16000"},
		// user：trade/残局常识
		{"user trade 常识", user, "补枪"},
		{"user 残局常识", user, "残局"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.in, tt.want) {
				t.Errorf("提示词缺少 %q", tt.want)
			}
		})
	}
}

// TestReportPromptSuspectOrder 断言切片顺序 = Suspects 顺序（Top3 报告顺序）。
func TestReportPromptSuspectOrder(t *testing.T) {
	_, user := ReportPrompt(testAnalysis())
	i1 := strings.Index(user, "切片 1：回合 2")
	i2 := strings.Index(user, "切片 2：回合 3")
	if i1 < 0 || i2 < 0 {
		t.Fatalf("未找到切片标记：i1=%d i2=%d", i1, i2)
	}
	if i1 >= i2 {
		t.Errorf("切片顺序错误：回合 2 应排在回合 3 之前（%d >= %d）", i1, i2)
	}
}

// TestReportPromptPure 纯函数性：同一输入两次输出必须一致（无隐藏状态）。
func TestReportPromptPure(t *testing.T) {
	a := testAnalysis()
	s1, u1 := ReportPrompt(a)
	s2, u2 := ReportPrompt(a)
	if s1 != s2 || u1 != u2 {
		t.Error("ReportPrompt 非纯函数：两次输出不一致")
	}
}

// TestReportPromptNilSafe nil / 空 Analysis 不 panic 且仍产出固定结构。
func TestReportPromptNilSafe(t *testing.T) {
	for _, a := range []*model.Analysis{nil, {}, {Meta: model.MatchMeta{Map: "de_dust2"}}} {
		system, user := ReportPrompt(a)
		for _, want := range []string{"一句话结论", "丢分回合 Top3", "值得保留的好回合", "追问建议"} {
			if !strings.Contains(system, want) {
				t.Errorf("system 缺少固定结构 %q", want)
			}
		}
		if !strings.Contains(user, "丢分原因频次") {
			t.Error("user 缺少丢分原因段")
		}
		if !strings.Contains(user, "游戏知识") {
			t.Error("user 缺少游戏知识段")
		}
	}
}

// TestQAPromptNilSafe nil Analysis / nil 切片不 panic，且明示数据缺失。
func TestQAPromptNilSafe(t *testing.T) {
	system, user := QAPrompt(nil, nil)
	if !strings.Contains(system, "复盘教练") {
		t.Error("system 缺少人设")
	}
	if !strings.Contains(user, "给定数据里没有") {
		t.Error("nil 切片应提示「给定数据里没有」")
	}
}

// TestWriteRoundSliceDraw 平局回合不误标「焦点方丢分」。
func TestWriteRoundSliceDraw(t *testing.T) {
	var b strings.Builder
	writeRoundSlice(&b, model.RoundAnalysis{
		Number: 7, Winner: model.SideDraw, FocusWon: false,
		EndReason: "draw", TickRange: [2]int{100, 200},
	}, 1, nil)
	out := b.String()
	if !strings.Contains(out, "平局") {
		t.Errorf("平局回合缺少平局标注：%q", out)
	}
	if strings.Contains(out, "焦点方丢分") {
		t.Errorf("平局回合被误标丢分：%q", out)
	}
	if !strings.Contains(out, "回合 7") {
		t.Errorf("缺回合号：%q", out)
	}
}

// TestQAPrompt 表驱动断言追问提示词包含问题、切片与引用规则。
func TestQAPrompt(t *testing.T) {
	a := testAnalysis()
	slice := testQASlice()
	system, user := QAPrompt(a, slice)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"system 人设", system, "复盘教练"},
		{"system 禁止发明", system, "禁止发明"},
		{"system 引用格式", system, "回合R [tick T]"},
		{"system 无数据话术", system, "给定数据里没有"},

		{"user 问题", user, "第 2 回合我为什么先死？"},
		{"user 焦点名", user, "张三"},
		{"user 切片回合", user, "回合 2"},
		{"user 切片 tick", user, "5100"},
		{"user 补充证据", user, "补充证据"},
		{"user 命中关键词", user, "首杀"},
		{"user 经济规则", user, "3250"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.in, tt.want) {
				t.Errorf("提示词缺少 %q", tt.want)
			}
		})
	}
}

// TestQAPromptEmptySlice 没有检索结果时明示数据缺失，禁止编造。
func TestQAPromptEmptySlice(t *testing.T) {
	_, user := QAPrompt(testAnalysis(), &model.QASlice{Question: "第 9 回合发生了什么？"})
	if !strings.Contains(user, "给定数据里没有") {
		t.Error("空切片应提示「给定数据里没有」")
	}
}

// 提示词预算：面向本地 17-20GB 小模型，上下文必须收在可控范围。
// 这两个数字是护栏——加字段时先想清楚它值不值这个 token。
const (
	maxReportSystemRunes = 800
	maxReportUserRunes   = 2500
	maxQAUserRunes       = 1500
)

// TestReportPromptBudget 报告提示词总长受控（小模型上下文越长越容易跑偏）。
func TestReportPromptBudget(t *testing.T) {
	system, user := ReportPrompt(testAnalysis())
	if n := utf8.RuneCountInString(system); n > maxReportSystemRunes {
		t.Errorf("system 长度 %d 超预算 %d", n, maxReportSystemRunes)
	}
	if n := utf8.RuneCountInString(user); n > maxReportUserRunes {
		t.Errorf("user 长度 %d 超预算 %d", n, maxReportUserRunes)
	}
}

// TestQAPromptBudget 追问提示词同样受控。
func TestQAPromptBudget(t *testing.T) {
	_, user := QAPrompt(testAnalysis(), testQASlice())
	if n := utf8.RuneCountInString(user); n > maxQAUserRunes {
		t.Errorf("qa user 长度 %d 超预算 %d", n, maxQAUserRunes)
	}
}

// TestReportPromptSmallModelStructure 小模型需要显式结构：
// 编号硬规则 + 可照抄模板 + 填法说明，缺一不可。
func TestReportPromptSmallModelStructure(t *testing.T) {
	system, _ := ReportPrompt(testAnalysis())
	for _, want := range []string{
		"【硬规则】", "【输出模板】", "【填法】",
		"照抄", "不要增减小节", "不知道", "数据里没有",
	} {
		if !strings.Contains(system, want) && want != "不知道" {
			t.Errorf("system 缺少小模型结构指令 %q", want)
		}
	}
	// R/T 必须是可替换的占位符，不能直接给死数字（否则模型会照抄假 tick）。
	if !strings.Contains(system, "<比分>") {
		t.Error("system 模板缺少 <比分> 占位符")
	}
}

// TestReportPromptCitationChecklist user 末尾必须给「可用引用」清单：
// 把 R/T 收窄成有限候选，是小模型最有效的防幻觉手段。
func TestReportPromptCitationChecklist(t *testing.T) {
	_, user := ReportPrompt(testAnalysis())
	for _, want := range []string{
		"# 可用引用",
		"回合 2：tick 5100、5200、5300",
		"回合 3：tick 9100、12000",
		"好回合 1：tick 4800",
		"Top3 按上面顺序写 2 个回合",
		"tick 只许抄清单里的数字",
	} {
		if !strings.Contains(user, want) {
			t.Errorf("user 缺少可用引用要素 %q", want)
		}
	}
	// 清单必须在最后一段（模型对末尾指令最敏感）。
	tail := user[len(user)-200:]
	if !strings.Contains(tail, "tick 只许抄清单里的数字") {
		t.Error("可用引用规则不在 user 末尾")
	}
}

// TestReportPromptDropsInternalScores 内部打分不进提示词：
// WinCost 对解读没有帮助，却经常被小模型当成「本场事实」复述。
func TestReportPromptDropsInternalScores(t *testing.T) {
	_, user := ReportPrompt(testAnalysis())
	for _, banned := range []string{"WinCost", "ScoreHint"} {
		if strings.Contains(user, banned) {
			t.Errorf("提示词泄漏内部字段 %q", banned)
		}
	}
}

// TestReportPromptEvidenceDedup 已被结构化事实覆盖的证据不再重复列出。
func TestReportPromptEvidenceDedup(t *testing.T) {
	_, user := ReportPrompt(testAnalysis())
	// 回合 2 的 kill 证据（tick 5100）已由「开局对枪 / 焦点死亡」覆盖。
	if strings.Contains(user, "王五用 M4A1-S 爆头击杀 张三（首杀）") {
		t.Error("重复证据未去重：开局击杀事实出现两遍")
	}
	if !strings.Contains(user, "[tick 5300]") {
		t.Error("未被覆盖的经济证据不应被丢掉")
	}
}

// TestReportPromptZeroValueFieldsOmitted 零值字段不写进提示词（0 会被模型当事实）。
func TestReportPromptZeroValueFieldsOmitted(t *testing.T) {
	a := testAnalysis()
	a.Focus.Stats = model.PlayerMatchStats{RoundsPlayed: 2}
	_, user := ReportPrompt(a)
	if strings.Contains(user, "ADR 0.0") || strings.Contains(user, "KDR 0.00") {
		t.Errorf("零值字段被写进提示词：\n%s", user)
	}
}

// TestQAPromptCitationHint 追问提示词末尾给可引用 tick。
func TestQAPromptCitationHint(t *testing.T) {
	_, user := QAPrompt(testAnalysis(), testQASlice())
	if !strings.Contains(user, "# 可引用 tick") {
		t.Error("缺少可引用 tick 段")
	}
	if !strings.Contains(user, "回合 2：5100、5200、5300") {
		t.Errorf("可引用 tick 清单不正确：\n%s", user)
	}
}

// TestQAPromptExtraFactsDedup 与切片完全重复的 ExtraFacts 不再重复注入。
func TestQAPromptExtraFactsDedup(t *testing.T) {
	a := testAnalysis()
	slice := testQASlice()
	slice.ExtraFacts = []model.Evidence{a.Rounds[1].Evidence[0]} // 与切片证据同 tick 同文
	_, user := QAPrompt(a, slice)
	if strings.Contains(user, "补充证据") {
		t.Error("重复 ExtraFacts 应整段省略")
	}
}
