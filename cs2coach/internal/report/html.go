package report

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"strings"
)

// htmlTpl 是自包含单文件 HTML 模板：深色主题、内联 CSS、无外部资源。
// 动态文本一律经 html/template 自动转义（或经 nl2br 先 EscapeString 再换行）。
const htmlTpl = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
  :root {
    --bg: #06090e;
    --card: #0c121a;
    --line: rgba(255, 255, 255, 0.075);
    --text: #e9eff7;
    --muted: #8b98aa;
    --accent: #d7ff3f;
    --font: -apple-system, BlinkMacSystemFont, "PingFang SC", "Hiragino Sans GB", "Segoe UI", "Microsoft YaHei", sans-serif;
    --mono: ui-monospace, "SFMono-Regular", Menlo, Consolas, monospace;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0;
    padding: 24px 16px 48px;
    background: var(--bg);
    color: var(--text);
    font-family: var(--font);
    font-size: 14px;
    line-height: 22px;
  }
  main { max-width: 1000px; margin: 0 auto; }
  h1 { font-size: 20px; font-weight: 700; margin: 0 0 10px; color: var(--text); letter-spacing: 0.01em; }
  h2 { font-size: 16px; font-weight: 700; margin: 28px 0 12px; color: var(--text); }
  h3 { font-size: 16px; font-weight: 600; margin: 16px 0 10px; color: var(--text); }
  h3.h-yours { color: var(--accent); }
  h3.h-split { color: var(--muted); }
  .badges { display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 18px; }
  .badge {
    display: inline-flex; align-items: center; gap: 6px;
    padding: 3px 12px; border-radius: 999px;
    border: 1px solid var(--line); background: var(--card);
    color: var(--muted); font-size: 12px;
  }
  .badge .dot { width: 8px; height: 8px; border-radius: 50%; background: var(--accent); }
  .badge.score { color: var(--accent); border-color: rgba(215, 255, 63, 0.35); }
  .badge.focus { color: var(--text); }
  .badge.focus .dot { background: var(--muted); }
  .card {
    background: var(--card); border: 1px solid var(--line);
    border-radius: 12px; padding: 16px 18px; margin: 12px 0;
  }
  .note {
    border-left: 3px solid var(--accent); padding: 10px 14px;
    background: var(--card); border-radius: 0 10px 10px 0;
    color: var(--muted); margin: 12px 0;
  }
  .lead { font-size: 14px; color: var(--text); }
  .facts { color: var(--muted); font-size: 14px; margin-top: 8px; }
  .round-head { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; margin-bottom: 12px; }
  .round-no { color: var(--accent); font-weight: 700; }
  .tick-badge {
    font-family: var(--mono); font-size: 12px; color: var(--accent);
    border: 1px solid var(--line); border-radius: 8px; padding: 2px 8px;
    background: var(--bg); user-select: all;
  }
  .seg { border-radius: 10px; padding: 10px 12px; margin: 8px 0; background: var(--card); }
  .seg-why { border-left: 3px solid var(--muted); }
  .seg-title { font-size: 13px; letter-spacing: 0.04em; margin-bottom: 6px; color: var(--muted); }
  .ev {
    border-left: 3px solid var(--line); padding: 4px 10px; margin: 6px 0;
    background: var(--bg); border-radius: 0 8px 8px 0;
    color: var(--text); font-size: 14px;
  }
  .ev .t {
    display: inline-block; font-family: var(--mono); font-size: 12px;
    color: var(--accent); border: 1px solid var(--line);
    border-radius: 6px; padding: 0 6px; margin-right: 6px; user-select: all;
  }
  .goto {
    font-family: var(--mono); font-size: 12px; color: var(--accent);
    margin: 6px 0 2px; user-select: all;
  }
  .tag {
    display: inline-block; font-size: 12px; padding: 1px 10px; border-radius: 999px;
    border: 1px solid var(--line); color: var(--muted);
    background: var(--card); margin-right: 6px;
  }
  .good .round-no { color: var(--accent); }
  .good .tick-badge { color: var(--accent); }
  table { width: 100%; border-collapse: collapse; margin: 10px 0; font-size: 14px; }
  th, td { text-align: left; padding: 8px 10px; border-bottom: 1px solid var(--line); }
  th { color: var(--muted); font-weight: 600; font-size: 13px; letter-spacing: 0.05em; }
  td.count { color: var(--accent); font-weight: 700; }
  code, .cli {
    font-family: var(--mono); background: var(--card);
    border: 1px solid var(--line); border-radius: 8px; padding: 2px 8px;
    color: var(--accent); font-size: 13px; user-select: all;
  }
  .cli { display: inline-block; padding: 8px 12px; margin: 6px 0; }
  ul { padding-left: 1.2em; margin: 6px 0; }
  li { margin: 4px 0; }
  footer {
    margin-top: 36px; padding-top: 14px; border-top: 1px solid var(--line);
    color: var(--muted); font-size: 13px;
  }
  @media (max-width: 640px) { body { padding: 16px 10px 32px; } }
</style>
</head>
<body>
<main>
  <header>
    <h1>{{.Title}}</h1>
    <div class="badges">
      <span class="badge"><span class="dot"></span>{{.Map}}</span>
      <span class="badge score">{{.Score}}</span>
      <span class="badge focus"><span class="dot"></span>{{.FocusName}}</span>
    </div>
  </header>

  <section class="card">
    <h2>判决</h2>
    <p class="lead">{{.Verdict.Lead}}</p>
    {{- if .Verdict.Hint}}<p class="facts">{{.Verdict.Hint}}</p>{{end}}
    {{- if and (not .Verdict.Yours) (not .Verdict.Split) (not .Verdict.Other)}}<p class="facts">本场没有丢分回合。</p>{{end}}
    {{- if .Verdict.Yours}}
    <h3 class="h-yours">要你改的</h3>
    {{- range .Verdict.Yours}}
    <div class="ev">{{nl2br .}}</div>
    {{- end}}
    {{- end}}
    {{- if .Verdict.Split}}
    <h3 class="h-split">分开的</h3>
    {{- range .Verdict.Split}}
    <div class="ev">{{nl2br .}}</div>
    {{- end}}
    {{- end}}
    {{- range .Verdict.Other}}
    <div class="ev">{{nl2br .}}</div>
    {{- end}}
    {{- if .FactsOnly}}<p class="note">{{.NoteText}}</p>{{end}}
  </section>

  <section>
    <h2>值得保留的好回合</h2>
    {{- if not .GoodRounds}}<div class="card">本场未识别出值得保留的好回合。</div>{{end}}
    {{- range .GoodRounds}}
    <div class="card good">
      <div class="round-head">
        <span class="round-no">回合 {{.Number}}</span>
        <span class="tick-badge">tick {{.Tick}}</span>
      </div>
      {{- if .Reason}}<div class="ev">{{nl2br .Reason}}</div>{{end}}
      {{- if .Highlight}}<div class="ev">高光：{{nl2br .Highlight}}</div>{{end}}
      <div class="goto">定位：[tick {{.Tick}}] · demo_gototick {{.Tick}}</div>
    </div>
    {{- end}}
  </section>

  <section>
    <h2>站位分析</h2>
    {{- if not .Positioning.HasData}}
    <div class="card">{{.Positioning.Missing}}</div>
    {{- else}}
    <div class="card">
      <p class="facts">{{.Positioning.Summary}}</p>
      <h3>死亡热区 Top3</h3>
      {{- if not .Positioning.HotZones}}<div class="ev">{{.NoHotZoneText}}</div>{{end}}
      {{- range .Positioning.HotZones}}
      <div class="ev">{{nl2br .Line}}</div>
      {{- end}}
      <h3>高危站位</h3>
      {{- if not .Positioning.HighRisk}}<div class="ev">{{.NoHighRiskText}}</div>{{end}}
      {{- range .Positioning.HighRisk}}
      <div class="ev">回合 {{.Round}} [tick {{.Tick}}] {{nl2br .Text}}</div>
      {{- end}}
    </div>
    {{- end}}
    {{- if and .Positioning.HasData .Positioning.Coach}}
    <div class="card seg-why">
      <div class="seg-title">教练解读</div>
      <div class="ev">{{nl2br .Positioning.Coach}}</div>
    </div>
    {{- end}}
  </section>

  <section>
    <h2>对枪分析</h2>
    <p class="facts">{{.LabLine}}</p>
    {{- if not .Dueling.HasData}}
    <div class="card">{{.Dueling.Missing}}</div>
    {{- else}}
    <div class="card">
      <p class="facts">{{.Dueling.Summary}}</p>
      <h3>对枪明细</h3>
      {{- if not .Dueling.Rows}}<div class="ev">{{.NoDuelRowsText}}</div>{{end}}
      {{- range .Dueling.Rows}}
      <div class="ev">回合 {{.Round}} [tick {{.Tick}}] {{nl2br .Text}}</div>
      {{- end}}
    </div>
    {{- end}}
    {{- if and .Dueling.HasData .Dueling.Coach}}
    <div class="card seg-why">
      <div class="seg-title">教练解读</div>
      <div class="ev">{{nl2br .Dueling.Coach}}</div>
    </div>
    {{- end}}
  </section>

  <section>
    <h2>追问入口</h2>
    <div class="card">
      <p>用 CLI 继续追问（检索该回合数据回答）：</p>
      <div class="cli">{{.CliExample}}</div>
      {{- if .Questions}}
      <p>示例问题：</p>
      <ul>
        {{- range .Questions}}<li>{{.}}</li>{{end}}
      </ul>
      {{- end}}
    </div>
  </section>

  <section>
    <h2>如何回看</h2>
    <div class="card">
      <p>{{.HowTo}}</p>
    </div>
  </section>

  <footer>
    <p>事实与证据由规则层（tactics）生成；LLM 只解读不发明——证据引用架构锁。工具：cs2coach。</p>
    <p>{{.Title}}</p>
  </footer>
</main>
</body>
</html>
`

// renderHTML 把页面数据渲染成自包含深色 HTML（与 Markdown 内容一致）。
func renderHTML(p *page) (string, error) {
	tpl, err := template.New("report").Funcs(template.FuncMap{
		// nl2br：先转义再把换行变成 <br>（转义后注入，安全）。
		"nl2br": func(s string) template.HTML {
			return template.HTML(strings.ReplaceAll(html.EscapeString(s), "\n", "<br>"))
		},
	}).Parse(htmlTpl)
	if err != nil {
		return "", fmt.Errorf("报告渲染失败：HTML 模板解析错误：%w", err)
	}
	data := struct {
		*page
		NoteText       string
		NoHotZoneText  string
		NoHighRiskText string
		NoDuelRowsText string
		LabLine        string
	}{
		page:           p,
		NoteText:       factsOnlyNote + "；配置 LLM 后可得教练解读。",
		NoHotZoneText:  noHotZoneText,
		NoHighRiskText: noHighRiskText,
		NoDuelRowsText: noDuelRowsText,
		LabLine:        p.LabLine(),
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("报告渲染失败：HTML 模板执行错误：%w", err)
	}
	return buf.String(), nil
}
