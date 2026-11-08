package knowledge

import (
	"encoding/json"
	"strings"
	"testing"

	"cs2-demo-review/internal/model"
)

// knownMaps 是 data/maps.json 应收录的官方竞技地图。
var knownMaps = []string{
	"de_mirage", "de_inferno", "de_dust2", "de_nuke",
	"de_anubis", "de_ancient", "de_overpass", "de_train", "de_vertigo",
}

func TestEconomyRules(t *testing.T) {
	text, err := EconomyRules()
	if err != nil {
		t.Fatalf("EconomyRules() error = %v", err)
	}
	if strings.TrimSpace(text) == "" {
		t.Fatal("EconomyRules() 返回空文本")
	}

	// 表驱动：经济规则必须出现的公开通行数字（与 CONTRACTS.md 枚举一一对应）。
	for _, tc := range []struct {
		name string
		want string
	}{
		{"胜方奖金", "$3250"},
		{"败方补偿封顶", "$3400"},
		{"金钱上限", "$16000"},
		{"下包胜（C4 爆炸）", "C4 爆炸"},
		{"拆包胜", "拆包获胜"},
		{"下包胜/拆包胜金额", "$3500"},
		{"下包全员奖励", "+$800"}, // 锚定下包奖励（半场 $800 重置不参与误匹配）
		{"败方基础补偿", "$1400"},
		{"败方连败递增", "$500"}, // 带 $ 前缀，避免子串误命中 3500/3400
		{"半场换边", "换边"},
		{"MR12 赛制", "MR12"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(text, tc.want) {
				t.Errorf("EconomyRules() 缺少 %q", tc.want)
			}
		})
	}

	// 下包胜与拆包胜是两条独立规则，$3500 至少出现两次。
	if n := strings.Count(text, "$3500"); n < 2 {
		t.Errorf("$3500 出现 %d 次，下包胜/拆包胜应各有一条", n)
	}
}

func TestMapBrief(t *testing.T) {
	t.Run("已知地图", func(t *testing.T) {
		for _, m := range knownMaps {
			if brief := MapBrief(m); strings.TrimSpace(brief) == "" {
				t.Errorf("MapBrief(%q) = 空串，已知地图应有简报", m)
			}
		}
	})

	for _, tc := range []struct {
		name    string
		mapName string
	}{
		{"空串", ""},
		{"未知地图", "de_unknown"},
		{"非地图名", "hello"},
		{"空白", "   "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := MapBrief(tc.mapName); got != "" {
				t.Errorf("MapBrief(%q) = %q，未知地图应返回空串", tc.mapName, got)
			}
		})
	}

	// 地图名规范化：大小写不敏感、可省略 de_ 前缀、去首尾空白。
	for _, tc := range []struct {
		name    string
		mapName string
		wantKey string // 期望命中的规范地图名
	}{
		{"省略 de_ 前缀", "Mirage", "de_mirage"},
		{"全大写带前缀", "DE_INFERNO", "de_inferno"},
		{"首尾空白", " de_mirage ", "de_mirage"},
		{"混合大小写省前缀", "Dust2", "de_dust2"},
	} {
		t.Run("规范化_"+tc.name, func(t *testing.T) {
			got, want := MapBrief(tc.mapName), MapBrief(tc.wantKey)
			if got == "" || got != want {
				t.Errorf("MapBrief(%q) = %q，应与 MapBrief(%q) 一致且非空", tc.mapName, got, tc.wantKey)
			}
		})
	}
}

func TestNormalizeMapKey(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"de_", "de_"},
		{"Mirage", "de_mirage"},
		{"DE_MIRAGE", "de_mirage"},
		{"de_dust2", "de_dust2"},
		{" dust2 ", "de_dust2"},
		{"Inferno", "de_inferno"},
	} {
		if got := normalizeMapKey(tc.in); got != tc.want {
			t.Errorf("normalizeMapKey(%q) = %q，want %q", tc.in, got, tc.want)
		}
	}
}

func TestCalloutTable(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mapName   string
		wantEmpty bool // 期望返回空表（当前点位刻意留空，全部为空表）
	}{
		{"已知地图", "de_mirage", true},
		{"已知地图大写", "DE_DUST2", true},
		{"已知地图省前缀", "dust2", true},
		{"已知地图带空白", " de_nuke ", true},
		{"未知地图", "de_unknown", true},
		{"空串", "", true},
		{"仅 de_ 前缀", "de_", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := CalloutTable(tc.mapName)
			if got == nil {
				t.Fatal("CalloutTable() 返回 nil，必须返回非 nil map")
			}
			if tc.wantEmpty && len(got) != 0 {
				t.Errorf("CalloutTable(%q) 长度 = %d，当前数据应为空表", tc.mapName, len(got))
			}
		})
	}

	// 返回值是副本：修改不影响后续调用。
	tab := CalloutTable("de_mirage")
	tab["x"] = model.Vec3{}
	if len(CalloutTable("de_mirage")) != 0 {
		t.Error("CalloutTable() 返回的 map 被外部修改后不应影响内部表")
	}
}

func TestMapsJSONValid(t *testing.T) {
	if !json.Valid(mapsJSON) {
		t.Fatal("内嵌 data/maps.json 不是合法 JSON")
	}
	var table map[string]mapEntry
	if err := json.Unmarshal(mapsJSON, &table); err != nil {
		t.Fatalf("data/maps.json 解析失败: %v", err)
	}
	for _, m := range knownMaps {
		if _, ok := table[m]; !ok {
			t.Errorf("data/maps.json 缺少地图 %q", m)
		}
	}
	// 表键必须已是规范形式（小写、de_ 前缀）：否则 MapBrief/CalloutTable
	// 规范化查表会静默失配，已知地图会伪装成「未知」。
	for k, e := range table {
		if normalizeMapKey(k) != k {
			t.Errorf("data/maps.json 键 %q 未规范化（查表会失配），应为 %q", k, normalizeMapKey(k))
		}
		if strings.TrimSpace(e.Brief) == "" {
			t.Errorf("data/maps.json 地图 %q 的 brief 为空（已知地图禁止空简报）", k)
		}
	}
}

// TestEconomyBrief 压缩版经济常识：必须覆盖关键数字，且明显短于完整版。
// 小模型提示词只用这一版——完整 price list 是噪声，会稀释「只能引用数据」的主指令。
func TestEconomyBrief(t *testing.T) {
	brief, err := EconomyBrief()
	if err != nil {
		t.Fatalf("EconomyBrief() error = %v", err)
	}
	for _, want := range []string{"$3250", "$3500", "$1400", "$3400", "$800", "16000", "4750", "eco", "force"} {
		if !strings.Contains(brief, want) {
			t.Errorf("EconomyBrief() 缺少 %q", want)
		}
	}
	full, err := EconomyRules()
	if err != nil {
		t.Fatalf("EconomyRules() error = %v", err)
	}
	if len(brief) >= len(full)/2 {
		t.Errorf("压缩版不够短：brief=%d full=%d", len(brief), len(full))
	}
}

// TestEconomyRulesBuyTypes 完整版也要写清买型口径（与 tactics 层一致）。
func TestEconomyRulesBuyTypes(t *testing.T) {
	full, err := EconomyRules()
	if err != nil {
		t.Fatalf("EconomyRules() error = %v", err)
	}
	for _, want := range []string{"$4750", "$2500", "$3800", "full", "force", "eco", "half"} {
		if !strings.Contains(full, want) {
			t.Errorf("EconomyRules() 缺少买型口径 %q", want)
		}
	}
}
