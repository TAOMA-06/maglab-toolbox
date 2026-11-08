package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
)

// marshalIndent 输出 2 空格缩进的 JSON。
func marshalIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// unmarshalFile 读取并解析 JSON 文件。
func unmarshalFile(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// reorderArgs 把散落在 positional 参数之后的 flag 提到前面。
// stdlib flag 遇到第一个 positional 就停，导致 README 里
// `parse <demo> [--player ...]` 这类写法 flag 被静默忽略。
// bools 是不需要取值的布尔 flag 名集合（如 llm / i）。
func reorderArgs(args []string, bools map[string]bool) []string {
	var flags, pos []string
	for i := 0; i < len(args); {
		a := args[i]
		if len(a) > 1 && strings.HasPrefix(a, "-") {
			name := strings.TrimLeft(a, "-")
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name = name[:eq]
			}
			flags = append(flags, a)
			i++
			// 非布尔 flag 吃掉下一个 token 当值（含 --flag=value 的已在上面处理）。
			if !strings.Contains(a, "=") && !bools[name] && i < len(args) {
				flags = append(flags, args[i])
				i++
			}
			continue
		}
		pos = append(pos, a)
		i++
	}
	return append(flags, pos...)
}

// newLineScanner 返回逐行读取函数（io.EOF 时 ok=false）与取错函数。
// 调用方在 ok=false 后必须检查 scanErr()，区分正常 EOF 与读取失败。
func newLineScanner(r io.Reader) (next func() (string, bool), scanErr func() error) {
	sc := bufio.NewScanner(r)
	next = func() (string, bool) {
		if sc.Scan() {
			return sc.Text(), true
		}
		return "", false
	}
	scanErr = func() error { return sc.Err() }
	return next, scanErr
}
