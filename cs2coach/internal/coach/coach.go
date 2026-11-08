// Package coach 是教练层：用 LLM 解读 tactics 层产出的 Analysis，产出教练报告与追问回答。
//
// 设计锁（架构锁）：LLM 只解读、不发明。提示词强制每条结论带「回合R [tick T]」引用，
// ValidateCitations 再把无证据论断剔除或标注。本包不做任何规则计算——
// 数字与证据全部来自 model.Analysis，提示词与校验均为纯函数（无隐藏状态）。
package coach

import (
	"context"
	"errors"
)

// ErrNotConfigured 表示没有任何可用的 LLM 密钥来源（环境变量未配置）。
var ErrNotConfigured = errors.New("未配置 LLM 密钥：请设置 CS2COACH_LLM_API_KEY（或 ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN / OPENAI_API_KEY）")

// Client 是最小 LLM 接口：两路径（Anthropic / OpenAI 兼容）共用，
// 测试可注入 fake 实现。
type Client interface {
	// Complete 发送 system+user 提示词，返回模型纯文本输出。
	Complete(ctx context.Context, system, user string) (string, error)
	// Model 返回当前使用的模型名（用于日志与报告标注）。
	Model() string
}

// CitationError 记录 ValidateCitations 删除/标注的一处无证据内容。
type CitationError struct {
	// Line 是原文行号（1 起）。
	Line int `json:"line"`
	// Text 是被删除的原文（整行或被删句子）。
	Text string `json:"text"`
	// Why 是中文原因（引用非法 / 无引用的建议句）。
	Why string `json:"why"`
}
