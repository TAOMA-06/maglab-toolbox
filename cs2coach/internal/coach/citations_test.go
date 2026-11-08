package coach

import (
	"strings"
	"testing"

	"cs2-demo-review/internal/model"
)

// TestValidateCitations 表驱动覆盖：合法引用保留、非法回合号/越界 tick 删除、
// 无引用建议句删除、demo_gototick 形态识别。
func TestValidateCitations(t *testing.T) {
	a := testAnalysis() // tick_rate 64 → 放宽 ±128；回合 2 区间 [5001, 9000]

	tests := []struct {
		name string
		in   string
		// wantOut 是净化后的完整输出（对比用）
		wantOut string
		// wantErrs 是期望的错误数（-1 表示不检查）
		wantErrs int
		// wantWhyContains 每条期望错误的原因子串（可空）
		wantWhyContains []string
	}{
		{
			name:     "合法回合+tick 引用保留",
			in:       "回合 2 [tick 5100] 首杀白给，被 M4A1-S 爆头。",
			wantOut:  "回合 2 [tick 5100] 首杀白给，被 M4A1-S 爆头。",
			wantErrs: 0,
		},
		{
			name:     "合法 tick: 形态保留",
			in:       "回合 2 [tick: 5200] 补枪失败。",
			wantOut:  "回合 2 [tick: 5200] 补枪失败。",
			wantErrs: 0,
		},
		{
			name:            "错误回合号整行删除",
			in:              "回合 9 [tick 5100] 开局白给。",
			wantOut:         "【无证据，已删】",
			wantErrs:        1,
			wantWhyContains: []string{"回合 9 不存在"},
		},
		{
			name:            "tick 越界整行删除",
			in:              "回合 2 [tick 20000] 开局对枪。",
			wantOut:         "【无证据，已删】",
			wantErrs:        1,
			wantWhyContains: []string{"tick 20000 不在回合 2"},
		},
		{
			name:            "tick 配对错回合也删除",
			in:              "回合 2 [tick 1100] 首杀。",
			wantOut:         "【无证据，已删】",
			wantErrs:        1,
			wantWhyContains: []string{"tick 1100 不在回合 2"},
		},
		{
			name:     "放宽 ±2*tick_rate 的下界保留",
			in:       "回合 2 [tick 4873] 尚在放宽区间。",
			wantOut:  "回合 2 [tick 4873] 尚在放宽区间。",
			wantErrs: 0,
		},
		{
			name:            "放宽区间外删除",
			in:              "回合 2 [tick 4872] 超出放宽区间。",
			wantOut:         "【无证据，已删】",
			wantErrs:        1,
			wantWhyContains: []string{"已放宽"},
		},
		{
			name:     "demo_gototick 形态识别且合法保留",
			in:       "demo_gototick 5050 回放回合 2 开局。",
			wantOut:  "demo_gototick 5050 回放回合 2 开局。",
			wantErrs: 0,
		},
		{
			name:            "demo_gototick 越界删除",
			in:              "demo_gototick 99999 看这一波。",
			wantOut:         "【无证据，已删】",
			wantErrs:        1,
			wantWhyContains: []string{"tick 99999"},
		},
		{
			name:     "第N回合 反序写法合法保留",
			in:       "第 3 回合的残局处理有问题。",
			wantOut:  "第 3 回合的残局处理有问题。",
			wantErrs: 0,
		},
		{
			name:            "第N回合 反序非法删除",
			in:              "第 9 回合的残局处理有问题。",
			wantOut:         "【无证据，已删】",
			wantErrs:        1,
			wantWhyContains: []string{"回合 9 不存在"},
		},
		{
			name:            "无引用建议句删除",
			in:              "应该多练补枪。",
			wantOut:         "【无证据，已删】",
			wantErrs:        1,
			wantWhyContains: []string{"无引用的建议句"},
		},
		{
			name:            "裸「要」建议句无引用删除",
			in:              "下把要多练补枪。",
			wantOut:         "【无证据，已删】",
			wantErrs:        1,
			wantWhyContains: []string{"无引用的建议句"},
		},
		{
			name:     "「重要」构词事实句不误删",
			in:       "张三是队内重要的火力点。",
			wantOut:  "张三是队内重要的火力点。",
			wantErrs: 0,
		},
		{
			name:     "「要点」构词句不误删",
			in:       "残局处理的要点是控图。",
			wantOut:  "残局处理的要点是控图。",
			wantErrs: 0,
		},
		{
			name:     "裸「要」带 demo_gototick 保留",
			in:       "下把要多补枪再进点 demo_gototick 5100。",
			wantOut:  "下把要多补枪再进点 demo_gototick 5100。",
			wantErrs: 0,
		},
		{
			name:     "带回合引用的建议保留",
			in:       "回合 2 应该先补枪再进点。",
			wantOut:  "回合 2 应该先补枪再进点。",
			wantErrs: 0,
		},
		{
			name:     "带 demo_gototick 的建议保留",
			in:       "必须先架住这个点 demo_gototick 5100。",
			wantOut:  "必须先架住这个点 demo_gototick 5100。",
			wantErrs: 0,
		},
		{
			name:            "同行内无引用建议句只删该句",
			in:              "回合 2 [tick 5100] 首杀白给。应该多练枪。",
			wantOut:         "回合 2 [tick 5100] 首杀白给。",
			wantErrs:        1,
			wantWhyContains: []string{"无引用的建议句"},
		},
		{
			name:     "结构标题行豁免建议句检查",
			in:       "## 追问建议",
			wantOut:  "## 追问建议",
			wantErrs: 0,
		},
		{
			name:            "列表行删除保留列表前缀",
			in:              "- 需要多打补枪",
			wantOut:         "- 【无证据，已删】",
			wantErrs:        1,
			wantWhyContains: []string{"无引用的建议句"},
		},
		{
			name:     "纯事实无引用行保留",
			in:       "比分 13 - 9。",
			wantOut:  "比分 13 - 9。",
			wantErrs: 0,
		},
		{
			name:     "空 Analysis 下无引用行保留",
			in:       "一句话结论：输在补枪。",
			wantOut:  "一句话结论：输在补枪。",
			wantErrs: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, errs := ValidateCitations(tt.in, a)
			if got != tt.wantOut {
				t.Errorf("输出 = %q，想要 %q", got, tt.wantOut)
			}
			if tt.wantErrs >= 0 && len(errs) != tt.wantErrs {
				t.Fatalf("错误数 = %d，想要 %d：%+v", len(errs), tt.wantErrs, errs)
			}
			for _, want := range tt.wantWhyContains {
				found := false
				for _, e := range errs {
					if strings.Contains(e.Why, want) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("错误原因缺少 %q：%+v", want, errs)
				}
			}
			for _, e := range errs {
				if e.Line < 1 {
					t.Errorf("CitationError.Line 非法：%+v", e)
				}
				if e.Text == "" {
					t.Errorf("CitationError.Text 为空：%+v", e)
				}
			}
		})
	}
}

// TestValidateCitationsMultiLine 多行报告：行号对应原文，未涉及行原样保留。
func TestValidateCitationsMultiLine(t *testing.T) {
	a := testAnalysis()
	report := strings.Join([]string{
		"**一句话结论**：13 - 9 输在补枪。",
		"回合 9 [tick 5100] 有人白给。",
		"回合 2 [tick 5100] 首杀白给。",
	}, "\n")

	got, errs := ValidateCitations(report, a)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("行数 = %d，想要 3：%q", len(lines), got)
	}
	if lines[0] != "**一句话结论**：13 - 9 输在补枪。" {
		t.Errorf("第 1 行被误改：%q", lines[0])
	}
	if lines[1] != "【无证据，已删】" {
		t.Errorf("第 2 行未删除：%q", lines[1])
	}
	if lines[2] != "回合 2 [tick 5100] 首杀白给。" {
		t.Errorf("第 3 行被误改：%q", lines[2])
	}
	if len(errs) != 1 || errs[0].Line != 2 {
		t.Fatalf("错误记录 = %+v，想要 1 条且 Line=2", errs)
	}
}

// TestValidateCitationsNilAnalysis 无 Analysis 时只删引用行，不 panic。
func TestValidateCitationsNilAnalysis(t *testing.T) {
	got, errs := ValidateCitations("回合 1 [tick 100] 白给。", nil)
	if got != "【无证据，已删】" {
		t.Errorf("输出 = %q", got)
	}
	if len(errs) != 1 {
		t.Errorf("错误数 = %d，想要 1", len(errs))
	}
}

// TestExtractRefs 覆盖三种引用形态的抽取与 demo_gototick/tick 不混淆。
func TestExtractRefs(t *testing.T) {
	tests := []struct {
		name string
		line string
		want []citeRef
	}{
		{"回合N", "回合 3 开局", []citeRef{{kind: "round", value: 3}}},
		{"第N回合", "第 12 回合残局", []citeRef{{kind: "round", value: 12}}},
		{"tick", "见 [tick 5100]", []citeRef{{kind: "tick", value: 5100}}},
		{"tick冒号", "tick: 5200 补枪", []citeRef{{kind: "tick", value: 5200}}},
		{
			"demo_gototick 不与 tick 混淆",
			"demo_gototick 5050",
			[]citeRef{{kind: "tick", value: 5050}},
		},
		{
			"混合形态按位置排序",
			"回合 2 [tick 5100] 与 demo_gototick 5200",
			[]citeRef{
				{kind: "round", value: 2},
				{kind: "tick", value: 5100},
				{kind: "tick", value: 5200},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractRefs(tt.line)
			if len(got) != len(tt.want) {
				t.Fatalf("refs = %+v，想要 %+v", got, tt.want)
			}
			for i := range tt.want {
				if got[i].kind != tt.want[i].kind || got[i].value != tt.want[i].value {
					t.Errorf("refs[%d] = %+v，想要 %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestValidateCitationsEmptyRounds 回合列表为空（0 回合）时任何回合引用都非法。
func TestValidateCitationsEmptyRounds(t *testing.T) {
	a := &model.Analysis{}
	got, errs := ValidateCitations("回合 1 开局白给。", a)
	if got != "【无证据，已删】" {
		t.Errorf("输出 = %q", got)
	}
	if len(errs) != 1 {
		t.Fatalf("错误数 = %d，想要 1：%+v", len(errs), errs)
	}
}

// TestValidateCitationsUnorderedRounds 切片乱序时按 Number 字段配对 TickRange，
// 不能按位次配错回合区间。
func TestValidateCitationsUnorderedRounds(t *testing.T) {
	a := testAnalysis()
	a.Rounds[0], a.Rounds[1] = a.Rounds[1], a.Rounds[0] // 回合 2 排在回合 1 前
	got, errs := ValidateCitations("回合 1 [tick 1100] 首杀。", a)
	if len(errs) != 0 {
		t.Fatalf("合法引用被误删：%q %+v", got, errs)
	}
	if got != "回合 1 [tick 1100] 首杀。" {
		t.Errorf("输出 = %q", got)
	}
	// 乱序下配错区间时会被误删的对照：tick 属于回合 2，引用回合 2 仍应保留。
	got, errs = ValidateCitations("回合 2 [tick 5100] 首杀白给。", a)
	if len(errs) != 0 {
		t.Fatalf("合法引用被误删：%q %+v", got, errs)
	}
}

// TestValidateCitationsAdvisoryWithRoundWord 裸「回合」语境的建议句按契约保留。
func TestValidateCitationsAdvisoryWithRoundWord(t *testing.T) {
	got, errs := ValidateCitations("这个回合应该多练补枪。", testAnalysis())
	if got != "这个回合应该多练补枪。" {
		t.Errorf("输出 = %q", got)
	}
	if len(errs) != 0 {
		t.Errorf("错误数 = %d，想要 0：%+v", len(errs), errs)
	}
}

// TestWidenedTickRange 覆盖 tick_rate 缺失时的保守放宽。
func TestWidenedTickRange(t *testing.T) {
	r := &model.RoundAnalysis{Number: 1, TickRange: [2]int{100, 200}}

	lo, hi, widen := widenedTickRange(r, testAnalysis()) // 64 tick → 128
	if lo != -28 || hi != 328 || widen != 128 {
		t.Errorf("64 tick: lo=%d hi=%d widen=%d", lo, hi, widen)
	}

	lo, hi, widen = widenedTickRange(r, &model.Analysis{}) // 缺 tick_rate → 按 64
	if lo != -28 || hi != 328 || widen != 128 {
		t.Errorf("缺 tick_rate: lo=%d hi=%d widen=%d", lo, hi, widen)
	}
}

// TestValidateCitationsFormatVariants 覆盖小模型常见的引用写法变体：
// 全角数字/冒号、千分位逗号、第N局、R3/[R3]、tick #T、demo_goto_tick、回合区间。
// 判据只有一条：认得出就必须校验——合法的留、非法的删。
func TestValidateCitationsFormatVariants(t *testing.T) {
	a := testAnalysis() // 回合 1..3；回合 2 区间 [5001, 9000]，放宽 ±128

	tests := []struct {
		name            string
		in              string
		wantOut         string
		wantErrs        int
		wantWhyContains []string
	}{
		{
			name:     "千分位逗号 tick 合法",
			in:       "回合 2 [tick 5,100] 补枪失败。",
			wantOut:  "回合 2 [tick 5,100] 补枪失败。",
			wantErrs: 0,
		},
		{
			name:     "全角数字合法",
			in:       "回合 ２ [tick ５１００] 首杀白给。",
			wantOut:  "回合 ２ [tick ５１００] 首杀白给。",
			wantErrs: 0,
		},
		{
			name:     "全角冒号 tick 合法",
			in:       "tick：５１００ 这波对枪输了。",
			wantOut:  "tick：５１００ 这波对枪输了。",
			wantErrs: 0,
		},
		{
			name:            "全角数字越界仍要删除",
			in:              "tick：２００００ 这波对枪输了。",
			wantOut:         "【无证据，已删】",
			wantErrs:        1,
			wantWhyContains: []string{"tick 20000"},
		},
		{
			name:     "R2 形态合法",
			in:       "R2 这波开局白给。",
			wantOut:  "R2 这波开局白给。",
			wantErrs: 0,
		},
		{
			name:     "[R3] 方括号形态合法",
			in:       "[R3] 这波残局输了。",
			wantOut:  "[R3] 这波残局输了。",
			wantErrs: 0,
		},
		{
			name:            "R 形态非法回合同样删除",
			in:              "R9 这波开局白给。",
			wantOut:         "【无证据，已删】",
			wantErrs:        1,
			wantWhyContains: []string{"回合 9 不存在"},
		},
		{
			name:     "R8 Revolver 是武器名不是回合引用",
			in:       "R8 Revolver 这枪没打中。",
			wantOut:  "R8 Revolver 这枪没打中。",
			wantErrs: 0,
		},
		{
			name:     "第N局合法且不按建议句删",
			in:       "第 3 局应该先架点。",
			wantOut:  "第 3 局应该先架点。",
			wantErrs: 0,
		},
		{
			name:            "第N局越界删除",
			in:              "第 9 局应该先架点。",
			wantOut:         "【无证据，已删】",
			wantErrs:        1,
			wantWhyContains: []string{"回合 9 不存在"},
		},
		{
			name:     "回合区间两端都合法",
			in:       "回合 2-3 连丢两分。",
			wantOut:  "回合 2-3 连丢两分。",
			wantErrs: 0,
		},
		{
			name:            "回合区间末端越界删除",
			in:              "回合 2-9 连丢两分。",
			wantOut:         "【无证据，已删】",
			wantErrs:        1,
			wantWhyContains: []string{"回合 9 不存在"},
		},
		{
			name:     "tick #T 形态合法",
			in:       "回合 3 [tick #9100] 道具缺口。",
			wantOut:  "回合 3 [tick #9100] 道具缺口。",
			wantErrs: 0,
		},
		{
			name:            "tick #T 越界删除",
			in:              "回合 3 [tick #99999] 道具缺口。",
			wantOut:         "【无证据，已删】",
			wantErrs:        1,
			wantWhyContains: []string{"tick 99999 不在回合 3"},
		},
		{
			name:     "demo_goto_tick 下划线变体合法",
			in:       "demo_goto_tick 5100 回放这一波。",
			wantOut:  "demo_goto_tick 5100 回放这一波。",
			wantErrs: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, errs := ValidateCitations(tt.in, a)
			if got != tt.wantOut {
				t.Errorf("输出 = %q，想要 %q", got, tt.wantOut)
			}
			if len(errs) != tt.wantErrs {
				t.Fatalf("错误数 = %d，想要 %d：%+v", len(errs), tt.wantErrs, errs)
			}
			for _, want := range tt.wantWhyContains {
				found := false
				for _, e := range errs {
					if strings.Contains(e.Why, want) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("错误原因缺少 %q：%+v", want, errs)
				}
			}
		})
	}
}

// TestValidateCitationsStructuralSections 模板段落必须带引用：
// 「为什么错」这类解释性幻觉没有建议措辞，通用规则抓不到，只能按小节契约抓。
func TestValidateCitationsStructuralSections(t *testing.T) {
	a := testAnalysis()
	tests := []struct {
		name            string
		in              string
		wantOut         string
		wantErrs        int
		wantWhyContains string
	}{
		{
			name:            "为什么错缺引用整行删除",
			in:              "- 为什么错：他站位太靠前。",
			wantOut:         "- 【无证据，已删】",
			wantErrs:        1,
			wantWhyContains: "模板段落缺少引用",
		},
		{
			name:            "发生了什么缺引用整行删除",
			in:              "- 发生了什么：开局被打掉。",
			wantOut:         "- 【无证据，已删】",
			wantErrs:        1,
			wantWhyContains: "模板段落缺少引用",
		},
		{
			name:            "下把怎么改只有回合号不够",
			in:              "- 下把怎么改：先补枪再进点 回合 2。",
			wantOut:         "- 【无证据，已删】",
			wantErrs:        1,
			wantWhyContains: "没有 tick 引用",
		},
		{
			name:     "下把怎么改带 demo_gototick 保留",
			in:       "- 下把怎么改：先补枪再进点 demo_gototick 5100。",
			wantOut:  "- 下把怎么改：先补枪再进点 demo_gototick 5100。",
			wantErrs: 0,
		},
		{
			name:     "发生了什么带引用保留",
			in:       "- 发生了什么：回合 2 [tick 5100] 首杀白给。",
			wantOut:  "- 发生了什么：回合 2 [tick 5100] 首杀白给。",
			wantErrs: 0,
		},
		{
			name:     "标题行豁免结构检查",
			in:       "### 回合 2（force）",
			wantOut:  "### 回合 2（force）",
			wantErrs: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, errs := ValidateCitations(tt.in, a)
			if got != tt.wantOut {
				t.Errorf("输出 = %q，想要 %q", got, tt.wantOut)
			}
			if len(errs) != tt.wantErrs {
				t.Fatalf("错误数 = %d，想要 %d：%+v", len(errs), tt.wantErrs, errs)
			}
			if tt.wantWhyContains != "" {
				found := false
				for _, e := range errs {
					if strings.Contains(e.Why, tt.wantWhyContains) {
						found = true
					}
				}
				if !found {
					t.Errorf("错误原因缺少 %q：%+v", tt.wantWhyContains, errs)
				}
			}
		})
	}
}

// TestValidateCitationsExtendedAdvisoryWords 扩充后的建议措辞同样必须带引用。
func TestValidateCitationsExtendedAdvisoryWords(t *testing.T) {
	a := testAnalysis()
	for _, in := range []string{
		"推荐多练补枪。",
		"下次记得先架点。",
		"务必先架点。",
		"最好别单走。",
	} {
		got, errs := ValidateCitations(in, a)
		if got != placeholder {
			t.Errorf("%q 应被删除，got %q", in, got)
		}
		if len(errs) != 1 || !strings.Contains(errs[0].Why, "无引用的建议句") {
			t.Errorf("%q 错误记录 = %+v", in, errs)
		}
	}
}

// TestValidateCitationsStripCodeFences 小模型常把整篇包进 ```markdown：
// 围栏要剥掉，否则报告会原样渲染出代码块。
func TestValidateCitationsStripCodeFences(t *testing.T) {
	in := "```markdown\n回合 2 [tick 5100] 首杀白给。\n## 值得保留的好回合\n- 回合 1 [tick 4800] 残局拆包胜。\n```"
	got, errs := ValidateCitations(in, testAnalysis())
	want := "回合 2 [tick 5100] 首杀白给。\n## 值得保留的好回合\n- 回合 1 [tick 4800] 残局拆包胜。"
	if got != want {
		t.Errorf("输出 = %q，想要 %q", got, want)
	}
	if len(errs) != 0 {
		t.Errorf("错误数 = %d，想要 0：%+v", len(errs), errs)
	}
}

// TestNormalizeCitationText 归一化：全角数字/标点、千分位逗号，其余文本不动。
func TestNormalizeCitationText(t *testing.T) {
	tests := []struct{ in, want string }{
		{"tick：５１００", "tick:5100"},
		{"tick 5,100", "tick 5100"},
		{"（回合 2）", "(回合 2)"},
		{"回合 2～3", "回合 2-3"},
		{"普通中文，不删逗号", "普通中文,不删逗号"},
		{"纯 ASCII tick 5100", "纯 ASCII tick 5100"},
	}
	for _, tt := range tests {
		if got := normalizeCitationText(tt.in); got != tt.want {
			t.Errorf("normalizeCitationText(%q) = %q，想要 %q", tt.in, got, tt.want)
		}
	}
}
