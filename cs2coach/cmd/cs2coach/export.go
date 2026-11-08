package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"cs2-demo-review/internal/export"
)

// cmdExport 把 demo（单个文件或整个目录）导出成「一回合一行」的训练样本 JSONL。
// 单个文件解析失败不会中断批量任务：跳过并继续，最后打印成功/失败汇总；
// 只要有文件失败，退出码为 3（与 parse 失败一致），全部成功为 0。
func cmdExport(args []string) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	args = reorderArgs(args, map[string]bool{"recursive": true})
	out := fs.String("o", "samples.jsonl", "输出 JSONL 路径")
	player := fs.String("player", "", "焦点玩家（名字或 SteamID64）；省略则自动选 POV 玩家")
	recursive := fs.Bool("recursive", false, "目录模式下递归子目录")
	if code := parseFlags(fs, args); code >= 0 {
		return code
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "用法：cs2coach export <demo.dem|目录> [-o samples.jsonl] [--player 名字|SteamID64] [--recursive]")
		return 2
	}
	opts := export.Options{Recursive: *recursive}
	if s := strings.TrimSpace(*player); s != "" {
		if isSteamID(s) {
			opts.Parse.FocusSteamID = s
		} else {
			opts.Parse.FocusName = s
		}
	}

	res, err := export.Run(fs.Arg(0), *out, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "导出失败：%v\n", err)
		return 3
	}
	for _, f := range res.Failed {
		fmt.Fprintf(os.Stderr, "  跳过 %s：%v\n", f.File, f.Err)
	}
	fmt.Printf("导出完成：成功 %d/%d 个 demo，%d 条回合样本 → %s\n",
		res.Succeeded, res.TotalFiles, res.Samples, res.OutputPath)
	if len(res.Failed) > 0 {
		fmt.Fprintf(os.Stderr, "失败 %d 个（已跳过，成功部分已写入）\n", len(res.Failed))
		return 3
	}
	return 0
}
