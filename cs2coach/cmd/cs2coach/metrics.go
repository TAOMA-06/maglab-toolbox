package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"cs2-demo-review/internal/model"
)

// metricRow 是 metrics 命令的一个输入文件及其对枪样本。
type metricRow struct {
	name      string
	dueling   *model.DuelingReport
	preAimDeg []float64
	speeds    []float64
}

// cmdMetrics 打印 M2b 指标分布（预瞄角度差 / 开火速度），用于阈值校准。
// 输入是 analysis.json（可多个）；逐文件用文件内分布，合计行从对枪明细重算快照口径。
func cmdMetrics(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "用法：cs2coach metrics <analysis.json> [more.json ...]")
		return 2
	}
	rows, code := loadMetricRows(args)
	if code != 0 {
		return code
	}

	fmt.Println("M2b 指标分布（预瞄角度差 / 开火速度）")
	fmt.Println()
	fmt.Println("| demo | 对枪 | 可评估 | 预瞄 P50 | P75 | P90 | 速度 P50 | P75 | P90 | 预瞄超阈值 | 移动中开枪 |")
	fmt.Println("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |")

	var allPreAim, allSpeeds []float64
	thPreAim, thSpeed := 0.0, 0.0
	for _, r := range rows {
		allPreAim = append(allPreAim, r.preAimDeg...)
		allSpeeds = append(allSpeeds, r.speeds...)
		duels, measured, fails, moving := 0, 0, 0, 0
		var dist model.MetricDistribution
		if r.dueling != nil {
			duels = r.dueling.Total
			measured = r.dueling.Measured
			fails = r.dueling.PreAimFailures
			moving = r.dueling.MovingShots
			dist = r.dueling.Distribution
			if thPreAim == 0 {
				thPreAim = r.dueling.Thresholds.PreAimBadDeg
			}
			if thSpeed == 0 {
				thSpeed = r.dueling.Thresholds.MovingFireSpeedUPS
			}
		}
		fmt.Printf("| %s | %d | %d | %s | %s | %s | %s | %s | %s | %d | %d |\n",
			r.name, duels, measured,
			fmtMetric(dist.PreAimDeltaDeg.P50, dist.PreAimSamples),
			fmtMetric(dist.PreAimDeltaDeg.P75, dist.PreAimSamples),
			fmtMetric(dist.PreAimDeltaDeg.P90, dist.PreAimSamples),
			fmtMetric(dist.ShotSpeedUPS.P50, dist.ShotSpeedSamples),
			fmtMetric(dist.ShotSpeedUPS.P75, dist.ShotSpeedSamples),
			fmtMetric(dist.ShotSpeedUPS.P90, dist.ShotSpeedSamples),
			fails, moving)
	}

	aggPreAim := metricsPercentiles(allPreAim)
	aggSpeed := metricsPercentiles(allSpeeds)
	fmt.Println()
	fmt.Printf("合计（对枪快照口径）：预瞄差 P50 %s / P75 %s / P90 %s（%d 样本）；开火速度 P50 %s / P75 %s / P90 %s（%d 样本）\n",
		fmtMetric(aggPreAim.P50, len(allPreAim)), fmtMetric(aggPreAim.P75, len(allPreAim)), fmtMetric(aggPreAim.P90, len(allPreAim)), len(allPreAim),
		fmtMetric(aggSpeed.P50, len(allSpeeds)), fmtMetric(aggSpeed.P75, len(allSpeeds)), fmtMetric(aggSpeed.P90, len(allSpeeds)), len(allSpeeds))
	if thPreAim > 0 || thSpeed > 0 {
		fmt.Printf("当前阈值：预瞄偏差 %.0f° / 开火速度 %.0f u/s；按各文件自带阈值统计：预瞄超阈值 %d 次，移动中开枪 %d 次\n",
			thPreAim, thSpeed, sumMetric(rows, func(d *model.DuelingReport) int { return d.PreAimFailures }),
			sumMetric(rows, func(d *model.DuelingReport) int { return d.MovingShots }))
	} else {
		fmt.Println("当前阈值：未提供（输入是旧 analysis.json 或缺少 dueling 字段）")
	}
	fmt.Println()
	fmt.Println("说明：逐文件行优先用该文件内全部开枪样本的速度分布；合计行只能用 analysis.json 里的对枪快照速度重算（样本更少）。阈值校准后只改 internal/tactics/m2.go 常量。")
	return 0
}

// loadMetricRows 读取全部 analysis.json 并抽取对枪样本。
func loadMetricRows(paths []string) ([]metricRow, int) {
	var rows []metricRow
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取失败：%v\n", err)
			return nil, 3
		}
		var a model.Analysis
		if err := json.Unmarshal(data, &a); err != nil {
			fmt.Fprintf(os.Stderr, "解析 JSON 失败（%s）：%v\n", path, err)
			return nil, 3
		}
		hasDuelFacts := false
		for _, ra := range a.Rounds {
			if len(ra.Duels) > 0 {
				hasDuelFacts = true
				break
			}
		}
		if a.Dueling == nil && !hasDuelFacts {
			fmt.Fprintf(os.Stderr, "跳过 %s：不是 M2 analysis.json（缺 dueling/duels 字段），请传 analyze 的产物\n", path)
			continue
		}
		r := metricRow{name: path, dueling: a.Dueling}
		if base := filepath.Base(path); base == "analysis.json" {
			r.name = filepath.Base(filepath.Dir(path))
		} else {
			r.name = base
		}
		for _, ra := range a.Rounds {
			for _, d := range ra.Duels {
				if d.PreAimMeasured {
					r.preAimDeg = append(r.preAimDeg, d.PreAimDeltaDeg)
				}
				if d.FocusSpeedValid {
					r.speeds = append(r.speeds, d.FocusSpeed)
				}
			}
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, "没有可用的 M2 analysis.json")
		return nil, 3
	}
	return rows, 0
}

func sumMetric(rows []metricRow, pick func(*model.DuelingReport) int) int {
	n := 0
	for _, r := range rows {
		if r.dueling != nil {
			n += pick(r.dueling)
		}
	}
	return n
}

// metricsPercentiles 最近秩分位（与 tactics 口径一致）。
func metricsPercentiles(values []float64) model.Percentiles {
	if len(values) == 0 {
		return model.Percentiles{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	rank := func(p float64) float64 {
		idx := int(p*float64(len(sorted)) + 0.999999)
		if idx < 1 {
			idx = 1
		}
		if idx > len(sorted) {
			idx = len(sorted)
		}
		return sorted[idx-1]
	}
	return model.Percentiles{
		Min: sorted[0],
		P50: rank(0.50),
		P75: rank(0.75),
		P90: rank(0.90),
		P95: rank(0.95),
		Max: sorted[len(sorted)-1],
	}
}

// fmtMetric 在样本数为 0 时显示「—」，避免把「没数据」显示成 0。
func fmtMetric(v float64, samples int) string {
	if samples == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f", v)
}
