package coach

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// anthropicMaxTokens 是非流式生成的安全上限（契约固定 16000）。
const anthropicMaxTokens = 16000

// anthropicClient 用官方 SDK 调 Claude 系列模型。
// 禁止用 net/http 手搓 Anthropic 接口——认证、重试、错误语义都以 SDK 为准。
type anthropicClient struct {
	inner      anthropic.Client
	model      string
	retryDelay time.Duration
}

// newAnthropicClient 按配置构造 Anthropic 客户端。
// 鉴权：CS2COACH_LLM_API_KEY 显式覆盖（option.WithAPIKey）；
// 否则由 SDK 自行解析 ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN / ant profile。
func newAnthropicClient(cfg envConfig) (*anthropicClient, error) {
	opts := []option.RequestOption{
		option.WithRequestTimeout(requestTimeout),
		// 重试统一由 Complete 外层循环负责（契约：429/5xx 共重试 2 次指数退避）。
		// 关掉 SDK 内建重试（默认还会再重试 2 次），避免叠成每轮 3 次请求。
		option.WithMaxRetries(0),
	}
	if cfg.explicitKey {
		opts = append(opts, option.WithAPIKey(cfg.apiKey))
	}
	if cfg.baseURLOverridden {
		opts = append(opts, option.WithBaseURL(cfg.baseURL))
	}
	return &anthropicClient{
		inner:      anthropic.NewClient(opts...),
		model:      cfg.model,
		retryDelay: defaultRetryDelay,
	}, nil
}

// Model 返回当前模型名。
func (c *anthropicClient) Model() string { return c.model }

// Complete 调用 Messages API 生成文本；429/5xx 按指数退避重试 2 次。
// Thinking 参数留空（Opus 5.5 默认 adaptive）。
func (c *anthropicClient) Complete(ctx context.Context, system, user string) (string, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", fmt.Errorf("Anthropic 调用被取消：%w", ctx.Err())
			case <-time.After(backoffDelay(c.retryDelay, attempt-1)):
			}
		}
		text, err := c.completeOnce(ctx, system, user)
		if err == nil {
			return text, nil
		}
		lastErr = err
		var apiErr *anthropic.Error
		if !errors.As(err, &apiErr) || !retryableStatus(apiErr.StatusCode) {
			return "", err
		}
	}
	return "", fmt.Errorf("Anthropic 调用重试 %d 次仍失败：%w", maxRetries, lastErr)
}

// completeOnce 发起一次 Messages.New 调用并解析响应。
func (c *anthropicClient) completeOnce(ctx context.Context, system, user string) (string, error) {
	resp, err := c.inner.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: anthropicMaxTokens,
		System:    []anthropic.TextBlockParam{{Text: system}},
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(user))},
	})
	if err != nil {
		return "", fmt.Errorf("Anthropic 调用失败：%w", err)
	}
	return messageText(resp)
}

// messageText 从 Anthropic 响应提取纯文本。
// refusal（流式分类器拦截）不按普通文本处理：返回带类别的中文错误。
func messageText(resp *anthropic.Message) (string, error) {
	if resp == nil {
		return "", fmt.Errorf("Anthropic 返回空响应体")
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return "", fmt.Errorf("Anthropic 拒绝生成（类别：%s）：报告未产出，请检查输入数据或调整提示词", resp.StopDetails.Category)
	}
	var b strings.Builder
	for _, block := range resp.Content {
		if block.Type != "text" {
			continue // thinking 等块不进入解读文本
		}
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok && tb.Text != "" {
			b.WriteString(tb.Text)
			continue
		}
		// 兜底：联合体未解析（如手工构造）时直接读 Text 字段。
		b.WriteString(block.Text)
	}
	text := b.String()
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("Anthropic 返回空文本（stop_reason=%s）", resp.StopReason)
	}
	return text, nil
}
