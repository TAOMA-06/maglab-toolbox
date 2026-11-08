package coach

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// openaiClient 用标准库 net/http 调 OpenAI 兼容 chat/completions 接口
// （DeepSeek/Kimi/gpt 等**非 Claude** 模型）。
// Claude 系列禁止走此通道——见 Complete 开头的守卫。
type openaiClient struct {
	baseURL    string // 不含尾斜杠；请求 {baseURL}/chat/completions
	apiKey     string
	model      string
	httpClient *http.Client
	retryDelay time.Duration
}

// openaiChatRequest 是 chat/completions 请求体。
type openaiChatRequest struct {
	Model    string          `json:"model"`
	Messages []openaiMessage `json:"messages"`
}

// openaiMessage 是一条 chat 消息。
type openaiMessage struct {
	Role    string `json:"role"` // system | user
	Content string `json:"content"`
}

// openaiChatResponse 是 chat/completions 响应体（只取所需字段）。
type openaiChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// newOpenAIClient 按配置构造 OpenAI 兼容客户端。
func newOpenAIClient(cfg envConfig) (*openaiClient, error) {
	return &openaiClient{
		baseURL:    cfg.baseURL,
		apiKey:     cfg.apiKey,
		model:      cfg.model,
		httpClient: &http.Client{Timeout: requestTimeout},
		retryDelay: defaultRetryDelay,
	}, nil
}

// Model 返回当前模型名。
func (c *openaiClient) Model() string { return c.model }

// Complete 发送 system+user 并取 choices[0].message.content；429/5xx 指数退避重试 2 次。
func (c *openaiClient) Complete(ctx context.Context, system, user string) (string, error) {
	// 架构守卫：Claude 模型必须走官方 SDK（provider=anthropic）。
	if strings.HasPrefix(strings.ToLower(c.model), "claude") {
		return "", fmt.Errorf("模型 %q 是 Claude 系列，禁止走 OpenAI 兼容通道：请设置 CS2COACH_LLM_PROVIDER=anthropic", c.model)
	}

	payload, err := json.Marshal(openaiChatRequest{
		Model: c.model,
		Messages: []openaiMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	})
	if err != nil {
		return "", fmt.Errorf("构造请求失败：%w", err)
	}
	endpoint := strings.TrimRight(c.baseURL, "/") + "/chat/completions"

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", fmt.Errorf("OpenAI 兼容调用被取消：%w", ctx.Err())
			case <-time.After(backoffDelay(c.retryDelay, attempt-1)):
			}
		}
		text, status, err := c.completeOnce(ctx, endpoint, payload)
		if err == nil {
			return text, nil
		}
		lastErr = err
		if status == 0 || !retryableStatus(status) {
			return "", err
		}
	}
	return "", fmt.Errorf("OpenAI 兼容调用重试 %d 次仍失败：%w", maxRetries, lastErr)
}

// completeOnce 发起一次 HTTP 请求；返回文本、HTTP 状态码（网络层错误为 0）与错误。
func (c *openaiClient) completeOnce(ctx context.Context, endpoint string, payload []byte) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", 0, fmt.Errorf("构造请求失败：%w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("OpenAI 兼容请求失败：%w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", resp.StatusCode, fmt.Errorf("读取响应失败：%w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, fmt.Errorf("OpenAI 兼容接口返回 HTTP %d：%s", resp.StatusCode, snippet(body))
	}

	var parsed openaiChatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", resp.StatusCode, fmt.Errorf("解析响应失败：%w", err)
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return "", resp.StatusCode, fmt.Errorf("OpenAI 兼容接口返回空回答")
	}
	return parsed.Choices[0].Message.Content, resp.StatusCode, nil
}

// snippet 截断响应体用于错误信息（避免刷屏）。
// 按 rune 边界截断，错误体常含中文，不能在字节中缝切开多字节 UTF-8。
func snippet(body []byte) string {
	const max = 200
	s := strings.TrimSpace(string(body))
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
