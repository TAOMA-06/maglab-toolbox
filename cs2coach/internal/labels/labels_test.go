package labels

import (
	"strings"
	"testing"
)

func TestCauseLabelSingleSource(t *testing.T) {
	// 六类归因标签必须全有中文名；未知标签原样返回（不发明）。
	for _, tag := range []string{
		"first_blood_gift", "trade_fail", "utility_gap",
		"eco_force", "clutch_throw", "man_advantage_lost",
	} {
		if got := CauseLabel(tag); got == tag || got == "" {
			t.Errorf("CauseLabel(%q) = %q, 缺中文名", tag, got)
		}
		if act := CauseAction(tag); strings.TrimSpace(act) == "" {
			t.Errorf("CauseAction(%q) 为空", tag)
		}
	}
	if got := CauseLabel("unknown_tag"); got != "unknown_tag" {
		t.Errorf("未知标签应原样返回，got %q", got)
	}
}

func TestOrAndJoinInts(t *testing.T) {
	if got := Or("", "兜底"); got != "兜底" {
		t.Errorf("Or 空串应兜底，got %q", got)
	}
	if got := Or("x", "兜底"); got != "x" {
		t.Errorf("Or 非空应原样，got %q", got)
	}
	if got := JoinInts([]int{2, 3}); got != "[2, 3]" {
		t.Errorf("JoinInts = %q", got)
	}
	if got := JoinInts(nil); got != "[]" {
		t.Errorf("JoinInts(nil) = %q", got)
	}
}
