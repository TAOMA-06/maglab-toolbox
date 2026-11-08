package coach

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Ollama 路径常量。
const (
	// ollamaChatPath 是首选端点：/api/chat 原生支持 system/user 消息，与 Client 接口语义一致。
	ollamaChatPath = "/api/chat"
	// ollamaGeneratePath 是兼容端点：老版本 Ollama 没有 /api/chat 时退回到这里（system 单列）。
	ollamaGeneratePath = "/api/generate"
	// ollamaTagsPath 列出本机已安装模型（自动选模型、报错时给可用清单）。
	ollamaTagsPath = "/api/tags"

	// ollamaProbeTimeout 限制模型探测耗时：探测只为选模型，失败不该拖住主流程。
	ollamaProbeTimeout = 3 * time.Second
	// ollamaNumCtx 显式放大上下文窗口。Ollama 缺省上下文只有 4k，而真实一局的教练提示词
	// 约 8k rune（≈6k token 量级）；不放大就会从开头静默截断——system 里
	// 「只解读不发明、必须带回合引用」的锁被吃掉，输出必然过不了证据校验。
	ollamaNumCtx = 16384
	// ollamaTemperature 低温：报告要贴证据、要可复现，不要创作。
	ollamaTemperature = 0.2
	// ollamaMaxBody 是响应体读取上限（本地服务，4MB 足够）。
	ollamaMaxBody = 4 << 20

	// envOllamaThink 控制是否保留思考模型的推理过程（1/true/yes/on 开启，缺省关闭）。
	// 缺省关闭的理由是实测：同一条一句话提示，jia:latest 开思考 127s、关思考 9s；
	// 一局报告的提示词更长，开着思考基本跑不进 300s 超时。
	envOllamaThink = "CS2COACH_OLLAMA_THINK"
)

// ollamaClient 用标准库 net/http 调本机 Ollama（本地模型，不需要 API key）。
// 优先 /api/chat，缺端点时回退 /api/generate。
type ollamaClient struct {
	baseURL string // 不含尾斜杠；请求 {baseURL}/api/chat
	// apiKey 通常为空；仅在 Ollama 躲在带鉴权的反向代理后面时下发 Bearer。
	apiKey string
	// model 是配置的模型名；autoModel 为真时只是「缺省候选」。
	model string
	// autoModel 表示 CS2COACH_LLM_MODEL 未显式设置：允许按本机已装模型自动挑选/回落。
	autoModel bool
	// think 表示是否保留思考模型的推理过程（CS2COACH_OLLAMA_THINK）。
	think      bool
	httpClient *http.Client
	retryDelay time.Duration

	mu sync.Mutex
	// resolved 是自动选定并生效的模型名（Model() 与后续请求共用，避免每次重探 /api/tags）。
	resolved string
}

// ollamaMessage 是一条 chat 消息。
type ollamaMessage struct {
	Role    string `json:"role"` // system | user
	Content string `json:"content"`
}

// ollamaOptions 是 Ollama 生成参数（对应请求体 options）。
type ollamaOptions struct {
	Temperature float64 `json:"temperature"`
	NumCtx      int     `json:"num_ctx"`
}

// ollamaChatRequest 是 /api/chat 请求体。
// Stream 必须显式 false：Ollama 缺省按 NDJSON 流式返回，非流式调用拿到的会是一串多行 JSON。
type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Think    bool            `json:"think"`
	Options  *ollamaOptions  `json:"options,omitempty"`
}

// ollamaGenerateRequest 是 /api/generate 请求体（system 单列，prompt 只放用户输入）。
type ollamaGenerateRequest struct {
	Model   string         `json:"model"`
	System  string         `json:"system"`
	Prompt  string         `json:"prompt"`
	Stream  bool           `json:"stream"`
	Think   bool           `json:"think"`
	Options *ollamaOptions `json:"options,omitempty"`
}

// ollamaResponse 是 /api/chat 与 /api/generate 的共用响应体：
// 前者文本在 message.content，后者在 response。
type ollamaResponse struct {
	Model   string `json:"model"`
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Response   string `json:"response"`
	Done       bool   `json:"done"`
	DoneReason string `json:"done_reason"`
	Error      string `json:"error"`
}

// ollamaTagsResponse 是 /api/tags 响应体（只取模型名）。
type ollamaTagsResponse struct {
	Models []struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"models"`
}

// ollamaHTTPError 表示 Ollama 返回非 200（模型不存在、老版本缺端点、后端 OOM 等）。
type ollamaHTTPError struct {
	StatusCode int
	Body       string
}

// Error 实现 error：错误体按 rune 截断，避免刷屏。
func (e *ollamaHTTPError) Error() string {
	return fmt.Sprintf("Ollama 返回 HTTP %d：%s", e.StatusCode, snippet([]byte(e.Body)))
}

// modelMissing 判断是否「模型不存在」：Ollama 用 404 + body 含 not found。
// 纯文本 404（端点不存在）不算——那个由 routeMissing 处理。
func (e *ollamaHTTPError) modelMissing() bool {
	return e.StatusCode == http.StatusNotFound &&
		strings.Contains(strings.ToLower(e.Body), "not found") &&
		!e.routeMissing()
}

// routeMissing 判断是否「端点不存在」：老版本 Ollama 没有 /api/chat，返回纯文本 404 page not found。
func (e *ollamaHTTPError) routeMissing() bool {
	return e.StatusCode == http.StatusNotFound &&
		strings.Contains(strings.ToLower(e.Body), "404 page not found")
}

// newOllamaClient 按配置构造 Ollama 客户端（缺省指向本机 11434，不需要密钥）。
func newOllamaClient(cfg envConfig) *ollamaClient {
	baseURL := cfg.baseURL
	if baseURL == "" {
		baseURL = defaultOllamaBaseURL
	}
	model := cfg.model
	if model == "" {
		model = defaultOllamaModel
	}
	return &ollamaClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     cfg.apiKey,
		model:      model,
		autoModel:  !cfg.explicitModel,
		think:      ollamaThinkFromEnv(),
		httpClient: &http.Client{Timeout: requestTimeout},
		retryDelay: defaultRetryDelay,
	}
}

// Model 返回当前模型名。
// 自动模式下首次 Complete 才会探测本机模型，此后返回实际生效的模型名；
// 调用方若在首次生成前打日志，看到的可能是缺省候选名。
func (c *ollamaClient) Model() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.resolved != "" {
		return c.resolved
	}
	return c.model
}

// Complete 调本机 Ollama 生成文本：/api/chat 优先，缺端点回退 /api/generate；429/5xx 指数退避重试 2 次。
// 错误一律带可操作提示（服务没起 / 模型没拉 / 超时），绝不让上层拿到裸错误。
func (c *ollamaClient) Complete(ctx context.Context, system, user string) (string, error) {
	model := c.pickModel(ctx)
	text, err := c.completeWithModel(ctx, model, system, user)
	if err == nil {
		return text, nil
	}

	var httpErr *ollamaHTTPError
	if !errors.As(err, &httpErr) || !httpErr.modelMissing() {
		return "", c.wrapError(err)
	}

	// 优雅降级：自动模式下模型缺失就换一个本机已装模型再试一次；
	// 显式指定模型时如实报错，并列出可用清单——绝不偷偷换模型骗人。
	if c.autoModel {
		if alt, ok := c.alternateModel(ctx, model); ok {
			if altText, altErr := c.completeWithModel(ctx, alt, system, user); altErr == nil {
				c.setResolved(alt)
				return altText, nil
			}
		}
	}
	return "", fmt.Errorf("Ollama 模型 %q 不存在：本机已装 %s；用 CS2COACH_LLM_MODEL 指定已有模型，或先 `ollama pull %s`",
		model, c.modelList(ctx), model)
}

// completeWithModel 对单个模型完成一次生成；429/5xx 重试，缺 /api/chat 时退到 /api/generate。
func (c *ollamaClient) completeWithModel(ctx context.Context, model, system, user string) (string, error) {
	useGenerate := false
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", fmt.Errorf("Ollama 调用被取消：%w", ctx.Err())
			case <-time.After(backoffDelay(c.retryDelay, attempt-1)):
			}
		}

		var (
			text   string
			status int
			err    error
		)
		if useGenerate {
			text, status, err = c.completeGenerate(ctx, model, system, user)
		} else {
			text, status, err = c.completeChat(ctx, model, system, user)
		}
		if err == nil {
			return text, nil
		}
		lastErr = err

		var httpErr *ollamaHTTPError
		if errors.As(err, &httpErr) {
			// 老版本 Ollama 没有 /api/chat：换端点重试，不消耗重试次数。
			if !useGenerate && httpErr.routeMissing() {
				useGenerate = true
				attempt--
				continue
			}
			if !retryableStatus(httpErr.StatusCode) {
				return "", err
			}
			continue
		}
		if status == 0 {
			return "", err // 网络层错误（服务没起）：重试只是白等
		}
	}
	return "", fmt.Errorf("Ollama 调用重试 %d 次仍失败：%w", maxRetries, lastErr)
}

// completeChat 走 /api/chat（首选）：system/user 各就各位。
func (c *ollamaClient) completeChat(ctx context.Context, model, system, user string) (string, int, error) {
	payload, err := json.Marshal(ollamaChatRequest{
		Model: model,
		Messages: []ollamaMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Stream:  false, // 流式关闭：报告要整体做引用校验，不能边收边用
		Think:   c.think,
		Options: ollamaDefaultOptions(),
	})
	if err != nil {
		return "", 0, fmt.Errorf("构造 Ollama 请求失败：%w", err)
	}
	body, status, err := c.post(ctx, ollamaChatPath, payload)
	if err != nil {
		return "", status, err
	}
	var parsed ollamaResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", status, fmt.Errorf("解析 Ollama 响应失败：%w", err)
	}
	if parsed.Error != "" {
		// 少数错误以 200 + {"error":...} 返回，按接口错误处理。
		return "", status, &ollamaHTTPError{StatusCode: status, Body: parsed.Error}
	}
	if strings.TrimSpace(parsed.Message.Content) == "" {
		return "", status, fmt.Errorf("Ollama 返回空回答（模型 %q，done_reason=%q）", model, parsed.DoneReason)
	}
	return parsed.Message.Content, status, nil
}

// completeGenerate 走 /api/generate（兼容老版本）：system 单列，用户输入进 prompt。
func (c *ollamaClient) completeGenerate(ctx context.Context, model, system, user string) (string, int, error) {
	payload, err := json.Marshal(ollamaGenerateRequest{
		Model:   model,
		System:  system,
		Prompt:  user,
		Stream:  false,
		Think:   c.think,
		Options: ollamaDefaultOptions(),
	})
	if err != nil {
		return "", 0, fmt.Errorf("构造 Ollama 请求失败：%w", err)
	}
	body, status, err := c.post(ctx, ollamaGeneratePath, payload)
	if err != nil {
		return "", status, err
	}
	var parsed ollamaResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", status, fmt.Errorf("解析 Ollama 响应失败：%w", err)
	}
	if parsed.Error != "" {
		return "", status, &ollamaHTTPError{StatusCode: status, Body: parsed.Error}
	}
	if strings.TrimSpace(parsed.Response) == "" {
		return "", status, fmt.Errorf("Ollama 返回空回答（模型 %q，done_reason=%q）", model, parsed.DoneReason)
	}
	return parsed.Response, status, nil
}

// post 发一次 JSON 请求并读回响应体；非 200 统一转成 *ollamaHTTPError。
func (c *ollamaClient) post(ctx context.Context, path string, payload []byte) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, fmt.Errorf("构造 Ollama 请求失败：%w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("Ollama 请求失败（%s%s）：%w", c.baseURL, path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, ollamaMaxBody))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("读取 Ollama 响应失败：%w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return body, resp.StatusCode, &ollamaHTTPError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	return body, resp.StatusCode, nil
}

// pickModel 返回本次请求使用的模型名。
// 显式 CS2COACH_LLM_MODEL 优先；未显式设置时按本机已装模型自动挑：
// 缺省名（llama3.2）在本机存在就用它，否则用第一个已装模型。
// 探测失败（服务没起）不缓存，直接用配置的模型名去请求，让真实错误自然暴露。
func (c *ollamaClient) pickModel(ctx context.Context) string {
	if !c.autoModel {
		return c.model
	}
	c.mu.Lock()
	resolved := c.resolved
	c.mu.Unlock()
	if resolved != "" {
		return resolved
	}
	models, err := c.probeModels(ctx)
	if err != nil || len(models) == 0 {
		return c.model
	}
	picked := c.model
	if !hasModel(models, c.model) {
		picked = models[0]
	}
	c.setResolved(picked)
	return picked
}

// alternateModel 在模型缺失时挑另一个已装模型（自动模式的兜底重试）。
func (c *ollamaClient) alternateModel(ctx context.Context, current string) (string, bool) {
	models, err := c.probeModels(ctx)
	if err != nil {
		return "", false
	}
	for _, m := range models {
		if !sameModel(m, current) {
			return m, true
		}
	}
	return "", false
}

// modelList 返回便于阅读的已装模型清单（拿不到就说清原因）。
func (c *ollamaClient) modelList(ctx context.Context) string {
	models, err := c.probeModels(ctx)
	if err != nil || len(models) == 0 {
		return "（读不到 /api/tags，请用 `ollama list` 确认）"
	}
	return strings.Join(models, "、")
}

// probeModels 取本机已装模型名（GET /api/tags）。
func (c *ollamaClient) probeModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, ollamaProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+ollamaTagsPath, nil)
	if err != nil {
		return nil, fmt.Errorf("构造 Ollama 探测请求失败：%w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Ollama 探测失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Ollama 探测返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, ollamaMaxBody))
	if err != nil {
		return nil, fmt.Errorf("读取 Ollama 模型列表失败：%w", err)
	}
	var parsed ollamaTagsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("解析 Ollama 模型列表失败：%w", err)
	}
	names := make([]string, 0, len(parsed.Models))
	for _, m := range parsed.Models {
		name := m.Name
		if name == "" {
			name = m.Model
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// setResolved 记住自动选定的模型名。
func (c *ollamaClient) setResolved(model string) {
	c.mu.Lock()
	c.resolved = model
	c.mu.Unlock()
}

// wrapError 给底层错误补上可操作提示：本地部署最常见的失败是「服务没起」和「模型太慢超时」。
func (c *ollamaClient) wrapError(err error) error {
	var httpErr *ollamaHTTPError
	if errors.As(err, &httpErr) {
		return err // 接口已经返回可读中文，保留原文案
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("Ollama 调用超时（上限 %s）：本地模型较慢，可换更小的模型：%w", requestTimeout, err)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("Ollama 调用被取消：%w", err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return fmt.Errorf("无法连接本地 Ollama（%s）：请确认 `ollama serve` 正在运行，或用 CS2COACH_LLM_BASE_URL 指定地址：%w", c.baseURL, err)
	}
	return err
}

// ollamaThinkFromEnv 解析 CS2COACH_OLLAMA_THINK（1/true/yes/on 视为开启思考）。
func ollamaThinkFromEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envOllamaThink))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// ollamaDefaultOptions 固定生成参数：低温可复现 + 显式放大 num_ctx（见 ollamaNumCtx 注释）。
func ollamaDefaultOptions() *ollamaOptions {
	return &ollamaOptions{Temperature: ollamaTemperature, NumCtx: ollamaNumCtx}
}

// hasModel 判断模型是否在已装列表里（"llama3.2" 与 "llama3.2:latest" 视为同一个）。
func hasModel(models []string, want string) bool {
	for _, m := range models {
		if sameModel(m, want) {
			return true
		}
	}
	return false
}

// sameModel 判断两个模型名是否指向同一个模型（忽略 :latest 缺省标签）。
func sameModel(a, b string) bool {
	return strings.TrimSuffix(a, ":latest") == strings.TrimSuffix(b, ":latest")
}
