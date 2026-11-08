// Package knowledge 提供写死的游戏知识：CS2 经济规则文本、地图默认站位简报、
// 点位锚表。全部数据以 go:embed 内嵌，纯数据、零运行时依赖，供 coach 层拼提示词。
//
// 设计锁：只收录公开通行、确有把握的规则与默认；没有把握的一律留空，禁止编造。
package knowledge

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"cs2-demo-review/internal/model"
)

//go:embed data/economy.md
var economyMD string

//go:embed data/economy_brief.md
var economyBriefMD string

//go:embed data/maps.json
var mapsJSON []byte

// mapEntry 是 data/maps.json 中单张地图的条目。
type mapEntry struct {
	// Brief 是地图默认站位简述（中文，2-4 行通用默认）。
	Brief string `json:"brief"`
	// Callouts 是点位锚名 -> 世界坐标；当前刻意留空（不发明坐标）。
	Callouts map[string]model.Vec3 `json:"callouts"`
}

// loadMapTable 解析内嵌 data/maps.json（sync.OnceValues 惰性加载）。
// 解析失败返回错误，调用方按空表处理（未知地图语义），不再 panic 中断全仓。
var loadMapTable = sync.OnceValues(func() (map[string]mapEntry, error) {
	var t map[string]mapEntry
	if err := json.Unmarshal(mapsJSON, &t); err != nil {
		return nil, fmt.Errorf("knowledge: 内嵌 data/maps.json 解析失败: %w", err)
	}
	return t, nil
})

// EconomyRules 返回 CS2 经济规则说明文本（中文），供教练提示词引用。
// 内容来自内嵌 data/economy.md；嵌入文件为空时返回错误。
func EconomyRules() (string, error) {
	if strings.TrimSpace(economyMD) == "" {
		return "", fmt.Errorf("knowledge: 内嵌 data/economy.md 为空")
	}
	return economyMD, nil
}

// EconomyBrief 返回压缩版经济常识（中文，5 行以内），供提示词使用。
//
// 为什么单独一份：本地 17-20GB 小模型的上下文越长越容易跑偏，
// 完整 price list 对「解释这回合为什么该 eco」几乎没有帮助，
// 只保留胜负奖励、连败补偿、买型阈值三条主线即可。
// 内容来自内嵌 data/economy_brief.md；为空时返回错误，调用方回退到完整版。
func EconomyBrief() (string, error) {
	if strings.TrimSpace(economyBriefMD) == "" {
		return "", fmt.Errorf("knowledge: 内嵌 data/economy_brief.md 为空")
	}
	return economyBriefMD, nil
}

// MapBrief 返回地图默认站位简述（中文）。
// 未知地图（含空串）返回空字符串——没有把握的站位一律不写，禁止编造。
// 地图名大小写不敏感，可省略 de_ 前缀（如 "Mirage" 等价于 "de_mirage"）。
func MapBrief(mapName string) string {
	e, ok := lookupMap(mapName)
	if !ok {
		return ""
	}
	return e.Brief
}

// CalloutTable 返回地图点位锚表（点位名 -> 世界坐标）。
// 地图不存在或点位为空时返回空表（非 nil）。当前 data/maps.json 的
// callouts 全部刻意留空：不发明坐标。返回的是副本，调用方可安全修改。
func CalloutTable(mapName string) map[string]model.Vec3 {
	out := make(map[string]model.Vec3)
	e, ok := lookupMap(mapName)
	if !ok {
		return out
	}
	for name, pos := range e.Callouts {
		out[name] = pos
	}
	return out
}

// normalizeMapKey 规范化地图名：去空白、转小写、缺 de_ 前缀时补上前缀。
func normalizeMapKey(mapName string) string {
	k := strings.ToLower(strings.TrimSpace(mapName))
	if k == "" {
		return ""
	}
	if !strings.HasPrefix(k, "de_") {
		k = "de_" + k
	}
	return k
}

// lookupMap 按规范化地图名查表（内嵌数据损坏时按未知地图处理）。
func lookupMap(mapName string) (mapEntry, bool) {
	k := normalizeMapKey(mapName)
	if k == "" {
		return mapEntry{}, false
	}
	t, err := loadMapTable()
	if err != nil {
		return mapEntry{}, false
	}
	e, ok := t[k]
	return e, ok
}
