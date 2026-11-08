// Package labels 收敛全仓散落的中文标签、买型常量与小文本工具。
// tactics / report / qa 三处各自拷贝的映射曾多次漏改，统一到此处单源。
package labels

import (
	"fmt"
	"strings"
)

// 买型常量（tactics.classifyBuy 产出；Story.WonByBuy/LostByBuy 的键）。
const (
	BuyFull  = "full"  // 全枪全弹
	BuyForce = "force" // 强起
	BuyHalf  = "half"  // 半起
	BuyEco   = "eco"   // 纯 eco
)

// CauseLabels 是归因标签 -> 中文名（tactics 与 report 共用单源）。
var CauseLabels = map[string]string{
	"first_blood_gift":   "首杀白给",
	"trade_fail":         "补枪失败",
	"utility_gap":        "道具缺口",
	"eco_force":          "经济局强起",
	"clutch_throw":       "残局决策",
	"man_advantage_lost": "人数优势被翻",
}

// CauseActions 是无 LLM 解读时按归因标签给出的可执行小动作（每回合只给 1 条）。
var CauseActions = map[string]string{
	"first_blood_gift":   "开局对枪前先架默认位，对枪后 3 秒内贴墙架补枪位，避免首杀白给",
	"trade_fail":         "队友接火后 3 秒内贴墙架补枪位，把补枪压进 5 秒窗口内",
	"utility_gap":        "交火前保证手里留 1 烟 1 闪，不带 2 件以上道具进点白给",
	"eco_force":          "强起局集中打一波道具配合，不散开单挑",
	"clutch_throw":       "人数占优残局先合流卡要道，不单人找人",
	"man_advantage_lost": "人数优势时先卡点等对方犯错，不前压送回去",
}

// CauseLabel 取归因标签中文名（无则回退原文，不发明）。
func CauseLabel(tag string) string {
	if v, ok := CauseLabels[tag]; ok {
		return v
	}
	return tag
}

// CauseAction 取归因标签对应的小动作（无则返回空串）。
func CauseAction(tag string) string {
	return CauseActions[tag]
}

// Or 空串兜底。
func Or(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// JoinInts 把整数列表渲染成 [1, 2, 3]。
func JoinInts(nums []int) string {
	if len(nums) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(nums))
	for _, n := range nums {
		parts = append(parts, fmt.Sprint(n))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
