// Package report 是渲染层：把 tactics 层的 Analysis（事实）
// 与 coach 层的 interpret（LLM 解读，可为空）渲染成一页教练报告。
//
// 设计锁（design.md）：事实一律来自 Analysis，本层不产生数字、不发明事件；
// interpret 只做提取与拼装，拿不到就退回规则层事实（纯事实版）。
package report

import "cs2-demo-review/internal/model"

// Markdown 渲染一页教练报告（facts 来自 Analysis，interpret 来自教练文本，可为空）。
// interpret 为空时输出纯事实版（含「未配置 LLM」注记）。
func Markdown(a *model.Analysis, interpret string) (string, error) {
	return MarkdownWithBaseline(a, interpret, nil)
}

// MarkdownWithBaseline 在 Markdown 基础上多带一条可选的实验室基准。
// baseline 为 nil（没传参数、文件打不开、内容不合法）时，对枪节显示「还没有实验室基准」。
func MarkdownWithBaseline(a *model.Analysis, interpret string, baseline *LabBaseline) (string, error) {
	p, err := buildPageWithBaseline(a, interpret, baseline)
	if err != nil {
		return "", err
	}
	return renderMarkdown(p), nil
}

// HTML 渲染自包含本地 HTML（内联 CSS，深色主题，无外部依赖）。
// 内容与 Markdown 一致；所有动态文本经 html/template 转义。
func HTML(a *model.Analysis, interpret string) (string, error) {
	return HTMLWithBaseline(a, interpret, nil)
}

// HTMLWithBaseline 在 HTML 基础上多带一条可选的实验室基准。
func HTMLWithBaseline(a *model.Analysis, interpret string, baseline *LabBaseline) (string, error) {
	p, err := buildPageWithBaseline(a, interpret, baseline)
	if err != nil {
		return "", err
	}
	return renderHTML(p)
}
