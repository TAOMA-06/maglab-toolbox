package main

import (
	"flag"
	"io"
	"reflect"
	"testing"
)

func newTestFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func TestReorderArgs(t *testing.T) {
	bools := map[string]bool{"llm": true, "prompt-only": true}
	cases := []struct {
		name  string
		args  []string
		bools map[string]bool
		want  []string
	}{
		{"flag 在后提到前", []string{"a.dem", "-o", "m.json"}, nil,
			[]string{"-o", "m.json", "a.dem"}},
		{"bool flag 在后", []string{"a.json", "--llm"}, bools,
			[]string{"--llm", "a.json"}},
		{"混合", []string{"a.dem", "--out-dir", "out", "--player", "张三", "--llm"}, bools,
			[]string{"--out-dir", "out", "--player", "张三", "--llm", "a.dem"}},
		{"等号形式", []string{"a.json", "--out-dir=out"}, bools,
			[]string{"--out-dir=out", "a.json"}},
		{"已在前不动", []string{"-o", "m.json", "a.dem"}, nil,
			[]string{"-o", "m.json", "a.dem"}},
		{"纯 positional 不动", []string{"a.json", "问题"}, nil,
			[]string{"a.json", "问题"}},
		{"空", nil, nil, nil},
	}
	for _, c := range cases {
		if got := reorderArgs(c.args, c.bools); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestParseFlagsHelp(t *testing.T) {
	// -h 应返回 0（用法已由 flag 包打印），而不是 2。
	for _, args := range [][]string{{"-h"}, {"--help"}} {
		fs := newTestFlagSet()
		if code := parseFlags(fs, args); code != 0 {
			t.Errorf("%v: got %d want 0", args, code)
		}
	}
	fs := newTestFlagSet()
	if code := parseFlags(fs, []string{"--nope"}); code != 2 {
		t.Errorf("未知 flag 应返回 2，got %d", code)
	}
}
