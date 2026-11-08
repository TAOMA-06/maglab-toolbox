package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLabBaselineLine(t *testing.T) {
	legal := []byte(`{
  "kind": "maglab-counterstrafe-baseline",
  "version": 1,
  "recordedAt": "2026-11-07",
  "shotSpeedMedian": 42,
  "stopBandMs": 95
}`)
	b, err := ParseLabBaseline(legal)
	if err != nil {
		t.Fatalf("ParseLabBaseline() error = %v", err)
	}
	want := "实验室基准 · 2026-11-07 · 中位开枪速度 42 u/s · 停进精度带 95 ms"
	if got := b.Line(); got != want {
		t.Errorf("Line() = %q, want %q", got, want)
	}
	if got := LabLine(nil); got != "还没有实验室基准" {
		t.Errorf("LabLine(nil) = %q", got)
	}

	md, err := MarkdownWithBaseline(sampleAnalysis(), "", b)
	if err != nil {
		t.Fatalf("MarkdownWithBaseline() error = %v", err)
	}
	if !strings.Contains(md, want) {
		t.Errorf("Markdown 合法基准缺少 %q", want)
	}
	if !strings.Contains(md, "中位开枪速度") || !strings.Contains(md, "停进精度带") {
		t.Error("Markdown 合法基准应含中位开枪速度和停进精度带")
	}

	htmlOut, err := HTMLWithBaseline(sampleAnalysis(), "", b)
	if err != nil {
		t.Fatalf("HTMLWithBaseline() error = %v", err)
	}
	if !strings.Contains(htmlOut, want) {
		t.Errorf("HTML 合法基准缺少 %q", want)
	}

	// 对枪标题正下方：基准行应在标题之后、缺失文案/汇总之前。
	for name, got := range map[string]string{"Markdown": md, "HTML": htmlOut} {
		iTitle := strings.Index(got, "对枪分析")
		iLab := strings.Index(got, want)
		if iTitle < 0 || iLab < 0 || iLab < iTitle {
			t.Errorf("%s 基准行应在对枪标题下方，title@%d lab@%d", name, iTitle, iLab)
		}
	}
}

func TestLabBaselineMissing(t *testing.T) {
	for _, fn := range []struct {
		name string
		f    func(*LabBaseline) (string, error)
	}{
		{"Markdown-nil", func(b *LabBaseline) (string, error) { return MarkdownWithBaseline(sampleAnalysis(), "", b) }},
	} {
		got, err := fn.f(nil)
		if err != nil {
			t.Fatalf("%s error = %v", fn.name, err)
		}
		if !strings.Contains(got, "还没有实验室基准") {
			t.Errorf("%s 缺少「还没有实验室基准」", fn.name)
		}
	}
	gotHTML, err := HTMLWithBaseline(sampleAnalysis(), "", nil)
	if err != nil {
		t.Fatalf("HTMLWithBaseline(nil) error = %v", err)
	}
	if !strings.Contains(gotHTML, "还没有实验室基准") {
		t.Error("HTML nil 基准缺少「还没有实验室基准」")
	}

	// 旧入口不传基准时：只多出空文案，其余段落不变。
	mdOld, err := Markdown(sampleAnalysis(), "")
	if err != nil {
		t.Fatalf("Markdown() error = %v", err)
	}
	if !strings.Contains(mdOld, "还没有实验室基准") {
		t.Error("Markdown() 不传基准时应出现「还没有实验室基准」")
	}
}

func TestLabBaselineIllegal(t *testing.T) {
	bads := []string{
		`{"kind":"wrong","version":1,"recordedAt":"2026-11-07","shotSpeedMedian":42,"stopBandMs":95}`,
		`{"kind":"maglab-counterstrafe-baseline","version":2,"recordedAt":"2026-11-07","shotSpeedMedian":42,"stopBandMs":95}`,
		`{"kind":"maglab-counterstrafe-baseline","version":1,"recordedAt":"","shotSpeedMedian":42,"stopBandMs":95}`,
		`{"kind":"maglab-counterstrafe-baseline","version":1,"recordedAt":"2026-11-07","shotSpeedMedian":"42","stopBandMs":95}`,
		`not json`,
	}
	for i, s := range bads {
		if _, err := ParseLabBaseline([]byte(s)); err == nil {
			t.Errorf("非法基准 #%d 应返回错误", i)
		}
	}

	// 磁盘文件非法时的端到端：Load 失败，上层按 nil 渲染。
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(p, []byte(bads[0]), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLabBaselineFile(p); err == nil {
		t.Error("LoadLabBaselineFile(非法) 应返回错误")
	}
	if _, err := LoadLabBaselineFile(filepath.Join(dir, "no-such.json")); err == nil {
		t.Error("LoadLabBaselineFile(不存在) 应返回错误")
	}
}
