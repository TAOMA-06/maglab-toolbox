package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cs2-demo-review/internal/model"
)

func TestMetricsPercentiles(t *testing.T) {
	got := metricsPercentiles([]float64{40, 10, 30, 20})
	if got.Min != 10 || got.P50 != 20 || got.P75 != 30 || got.P90 != 40 || got.Max != 40 {
		t.Errorf("percentiles = %+v", got)
	}
	if empty := metricsPercentiles(nil); empty != (model.Percentiles{}) {
		t.Errorf("空样本应为零值，got %+v", empty)
	}
}

func TestLoadMetricRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "analysis.json")
	a := model.Analysis{
		Rounds: []model.RoundAnalysis{{Number: 1, Duels: []model.DuelAnalysis{
			{Round: 1, Tick: 100, PreAimMeasured: true, PreAimDeltaDeg: 20, FocusSpeedValid: true, FocusSpeed: 120},
			{Round: 1, Tick: 200, PreAimMeasured: false, FocusSpeedValid: false},
		}}},
		Dueling: &model.DuelingReport{Total: 2, Measured: 1},
	}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	rows, code := loadMetricRows([]string{path})
	if code != 0 || len(rows) != 1 {
		t.Fatalf("loadMetricRows code=%d rows=%d", code, len(rows))
	}
	if rows[0].name != filepath.Base(dir) {
		t.Errorf("name = %q, want 目录名 %q", rows[0].name, filepath.Base(dir))
	}
	if len(rows[0].preAimDeg) != 1 || len(rows[0].speeds) != 1 {
		t.Errorf("样本抽取错：preaim=%v speeds=%v", rows[0].preAimDeg, rows[0].speeds)
	}
}

func TestLoadMetricRowsRejectsNonAnalysis(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "match.json")
	// match.json 有 rounds/number，但没有 dueling/duels，应被跳过而不是打印空白行。
	if err := os.WriteFile(path, []byte(`{"rounds":[{"number":1}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code := loadMetricRows([]string{path}); code != 3 {
		t.Errorf("非 analysis.json 应返回 3，got %d", code)
	}
}
