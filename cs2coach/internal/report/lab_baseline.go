package report

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
)

// LabBaseline 是急停实验室导出的基准（maglab-baseline.json）。
// 只摆在「对枪分析」标题下方，不比较、不打分、不进 LLM。
type LabBaseline struct {
	Kind            string
	Version         int
	RecordedAt      string
	ShotSpeedMedian float64
	StopBandMs      float64
}

// ParseLabBaseline 解析并校验实验室基准文件内容。
// 合法指：kind 为 maglab-counterstrafe-baseline，version 为 1，
// recordedAt 非空，两个数字是数。
func ParseLabBaseline(data []byte) (*LabBaseline, error) {
	var tmp struct {
		Kind            *string  `json:"kind"`
		Version         *int     `json:"version"`
		RecordedAt      *string  `json:"recordedAt"`
		ShotSpeedMedian *float64 `json:"shotSpeedMedian"`
		StopBandMs      *float64 `json:"stopBandMs"`
	}
	if err := json.Unmarshal(data, &tmp); err != nil {
		return nil, fmt.Errorf("实验室基准解析失败：%v", err)
	}
	if tmp.Kind == nil || *tmp.Kind != "maglab-counterstrafe-baseline" {
		got := ""
		if tmp.Kind != nil {
			got = *tmp.Kind
		}
		return nil, fmt.Errorf("实验室基准无效：kind 应为 maglab-counterstrafe-baseline，实际为 %q", got)
	}
	if tmp.Version == nil || *tmp.Version != 1 {
		return nil, fmt.Errorf("实验室基准无效：version 应为 1")
	}
	if tmp.RecordedAt == nil || strings.TrimSpace(*tmp.RecordedAt) == "" {
		return nil, fmt.Errorf("实验室基准无效：recordedAt 为空")
	}
	if tmp.ShotSpeedMedian == nil || math.IsNaN(*tmp.ShotSpeedMedian) || math.IsInf(*tmp.ShotSpeedMedian, 0) {
		return nil, fmt.Errorf("实验室基准无效：shotSpeedMedian 不是数")
	}
	if tmp.StopBandMs == nil || math.IsNaN(*tmp.StopBandMs) || math.IsInf(*tmp.StopBandMs, 0) {
		return nil, fmt.Errorf("实验室基准无效：stopBandMs 不是数")
	}
	return &LabBaseline{
		Kind:            *tmp.Kind,
		Version:         *tmp.Version,
		RecordedAt:      strings.TrimSpace(*tmp.RecordedAt),
		ShotSpeedMedian: *tmp.ShotSpeedMedian,
		StopBandMs:      *tmp.StopBandMs,
	}, nil
}

// LoadLabBaselineFile 从磁盘读取实验室基准文件。
func LoadLabBaselineFile(path string) (*LabBaseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("实验室基准读取失败：%v", err)
	}
	return ParseLabBaseline(data)
}

// Line 返回报告里「对枪分析」标题正下方的那一句。
// nil 时返回「还没有实验室基准」。
func (b *LabBaseline) Line() string {
	if b == nil {
		return "还没有实验室基准"
	}
	return fmt.Sprintf("实验室基准 · %s · 中位开枪速度 %s u/s · 停进精度带 %s ms",
		b.RecordedAt, formatNum(b.ShotSpeedMedian), formatNum(b.StopBandMs))
}

// LabLine 返回 page 上的基准行（nil 安全）。
func LabLine(b *LabBaseline) string {
	if b == nil {
		return "还没有实验室基准"
	}
	return b.Line()
}
