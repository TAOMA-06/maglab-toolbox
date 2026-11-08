package coach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// fakeClient 是测试用的 Client 假实现：记录入参、返回预定文本。
type fakeClient struct {
	model     string
	reply     string
	err       error
	gotSystem string
	gotUser   string
}

// Complete 记录提示词并返回预定回复。
func (f *fakeClient) Complete(_ context.Context, system, user string) (string, error) {
	f.gotSystem, f.gotUser = system, user
	return f.reply, f.err
}

// Model 返回假模型名。
func (f *fakeClient) Model() string { return f.model }

// clearLLMEnv 把全部相关环境变量显式清空（隔离宿主环境）。
func clearLLMEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"CS2COACH_LLM_PROVIDER", "CS2COACH_LLM_API_KEY", "CS2COACH_LLM_MODEL",
		"CS2COACH_LLM_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "OPENAI_API_KEY",
	} {
		t.Setenv(k, "")
	}
}

// TestNewClientFromEnv 表驱动覆盖环境变量解析（缺省、优先级、Claude 守卫、非法值）。
func TestNewClientFromEnv(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantErrIs error
		// wantErrContains 是错误信息子串（可空）
		wantErrContains string
		// wantModel / wantType 仅在无错误时检查
		wantModel string
		wantType  string // "anthropic" | "openai"
	}{
		{
			name:      "没有任何密钥返回 ErrNotConfigured",
			env:       map[string]string{},
			wantErrIs: ErrNotConfigured,
		},
		{
			name:      "anthropic 缺省模型",
			env:       map[string]string{"ANTHROPIC_API_KEY": "sk-test"},
			wantModel: defaultAnthropicModel, wantType: "anthropic",
		},
		{
			name:      "ANTHROPIC_AUTH_TOKEN 也可鉴权",
			env:       map[string]string{"ANTHROPIC_AUTH_TOKEN": "tok-test"},
			wantModel: defaultAnthropicModel, wantType: "anthropic",
		},
		{
			name: "CS2COACH_LLM_API_KEY 优先于原生密钥",
			env: map[string]string{
				"CS2COACH_LLM_API_KEY": "override",
				"ANTHROPIC_API_KEY":    "native",
			},
			wantModel: defaultAnthropicModel, wantType: "anthropic",
		},
		{
			name: "openai 缺省模型",
			env: map[string]string{
				"CS2COACH_LLM_PROVIDER": "openai",
				"OPENAI_API_KEY":        "sk-oai",
			},
			wantModel: defaultOpenAIModel, wantType: "openai",
		},
		{
			name: "openai 下 CS2COACH_LLM_API_KEY 兜底",
			env: map[string]string{
				"CS2COACH_LLM_PROVIDER": "openai",
				"CS2COACH_LLM_API_KEY":  "shared",
			},
			wantModel: defaultOpenAIModel, wantType: "openai",
		},
		{
			name: "Claude 模型禁止走 openai 通道",
			env: map[string]string{
				"CS2COACH_LLM_PROVIDER": "openai",
				"CS2COACH_LLM_MODEL":    "claude-opus-5-5",
				"OPENAI_API_KEY":        "sk-oai",
			},
			wantErrContains: "PROVIDER=anthropic",
		},
		{
			name: "非法 provider 报错",
			env: map[string]string{
				"CS2COACH_LLM_PROVIDER": "gemini",
				"CS2COACH_LLM_API_KEY":  "k",
			},
			wantErrContains: "只支持 anthropic / openai",
		},
		{
			name: "自定义模型",
			env: map[string]string{
				"CS2COACH_LLM_MODEL":   "claude-sonnet-4-5",
				"ANTHROPIC_API_KEY":    "sk-test",
				"CS2COACH_LLM_API_KEY": "",
			},
			wantModel: "claude-sonnet-4-5", wantType: "anthropic",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearLLMEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			c, err := NewClientFromEnv()
			switch {
			case tt.wantErrIs != nil:
				if !errors.Is(err, tt.wantErrIs) {
					t.Fatalf("err = %v，想要 %v", err, tt.wantErrIs)
				}
			case tt.wantErrContains != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Fatalf("err = %v，想要包含 %q", err, tt.wantErrContains)
				}
			default:
				if err != nil {
					t.Fatalf("意外错误：%v", err)
				}
				if c.Model() != tt.wantModel {
					t.Errorf("Model() = %q，想要 %q", c.Model(), tt.wantModel)
				}
				switch tt.wantType {
				case "anthropic":
					if _, ok := c.(*anthropicClient); !ok {
						t.Errorf("类型 = %T，想要 *anthropicClient", c)
					}
				case "openai":
					if _, ok := c.(*openaiClient); !ok {
						t.Errorf("类型 = %T，想要 *openaiClient", c)
					}
				}
			}
		})
	}
}

// TestLoadEnvConfigBaseURL 校验 CS2COACH_LLM_BASE_URL 解析与缺省端点。
func TestLoadEnvConfigBaseURL(t *testing.T) {
	clearLLMEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	cfg, err := loadEnvConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.baseURL != defaultAnthropicBaseURL || cfg.baseURLOverridden {
		t.Errorf("缺省 baseURL = %q（overridden=%v）", cfg.baseURL, cfg.baseURLOverridden)
	}

	t.Setenv("CS2COACH_LLM_BASE_URL", "https://proxy.example.com/v1/")
	cfg, err = loadEnvConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.baseURL != "https://proxy.example.com/v1" || !cfg.baseURLOverridden {
		t.Errorf("自定义 baseURL = %q（overridden=%v）", cfg.baseURL, cfg.baseURLOverridden)
	}
}

// TestFakeClientPipeline 用 fake Client 串起「提示词 → LLM → 证据校验」管线。
func TestFakeClientPipeline(t *testing.T) {
	a := testAnalysis()
	system, user := ReportPrompt(a)

	fake := &fakeClient{
		model: "fake-model",
		reply: "回合 2 [tick 5100] 首杀白给。\n应该多练枪。\n回合 99 [tick 1] 有人送。",
	}
	got, err := fake.Complete(context.Background(), system, user)
	if err != nil {
		t.Fatal(err)
	}
	if fake.gotSystem != system || fake.gotUser != user {
		t.Error("fake Client 未收到完整提示词")
	}
	if fake.Model() != "fake-model" {
		t.Errorf("Model() = %q", fake.Model())
	}

	cleaned, errs := ValidateCitations(got, a)
	if strings.Contains(cleaned, "应该多练枪") || strings.Contains(cleaned, "回合 99") {
		t.Errorf("无证据论断未被删除：%q", cleaned)
	}
	if !strings.Contains(cleaned, "回合 2 [tick 5100]") {
		t.Errorf("合法引用被误删：%q", cleaned)
	}
	if len(errs) != 2 {
		t.Errorf("CitationError 数 = %d，想要 2：%+v", len(errs), errs)
	}
}

// TestOpenAIComplete 用 httptest 假装 OpenAI 兼容端点，校验请求形态与响应解析。
func TestOpenAIComplete(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		buf := make([]byte, 1<<16)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"回合 2 [tick 5100] 首杀白给。"}}]}`)
	}))
	defer srv.Close()

	c := &openaiClient{
		baseURL:    srv.URL,
		apiKey:     "sk-test",
		model:      "deepseek-chat",
		httpClient: srv.Client(),
		retryDelay: time.Millisecond,
	}
	got, err := c.Complete(context.Background(), "系统", "用户")
	if err != nil {
		t.Fatal(err)
	}
	if got != "回合 2 [tick 5100] 首杀白给。" {
		t.Errorf("回答 = %q", got)
	}
	if gotPath != "/chat/completions" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	var req openaiChatRequest
	if err := json.Unmarshal([]byte(gotBody), &req); err != nil {
		t.Fatalf("请求体不是 JSON：%v（%s）", err, gotBody)
	}
	if req.Model != "deepseek-chat" || len(req.Messages) != 2 ||
		req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
		t.Errorf("请求体 = %+v", req)
	}
}

// TestOpenAIRetry 覆盖 5xx 重试 2 次后成功。
func TestOpenAIRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"boom"}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	c := &openaiClient{
		baseURL:    srv.URL,
		apiKey:     "sk-test",
		model:      "kimi-k2",
		httpClient: srv.Client(),
		retryDelay: time.Millisecond,
	}
	got, err := c.Complete(context.Background(), "s", "u")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ok" {
		t.Errorf("回答 = %q", got)
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Errorf("调用次数 = %d，想要 3（2 次重试）", n)
	}
}

// TestOpenAIClaudeGuard Claude 模型走 OpenAI 通道时必须报错。
func TestOpenAIClaudeGuard(t *testing.T) {
	c := &openaiClient{model: "claude-opus-5-5", apiKey: "k", baseURL: "http://127.0.0.1:0"}
	_, err := c.Complete(context.Background(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "PROVIDER=anthropic") {
		t.Errorf("err = %v，想要提示 CS2COACH_LLM_PROVIDER=anthropic", err)
	}
}

// TestMessageText 覆盖 Anthropic 响应解析：文本块、拒绝降级、空文本。
func TestMessageText(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		hand    *anthropic.Message
		want    string
		wantErr string
	}{
		{
			name: "文本块拼接",
			raw:  `{"content":[{"type":"text","text":"你好，"},{"type":"text","text":"教练。"}],"stop_reason":"end_turn"}`,
			want: "你好，教练。",
		},
		{
			name: "thinking 块忽略",
			raw:  `{"content":[{"type":"thinking","thinking":"内心戏"},{"type":"text","text":"正文"}],"stop_reason":"end_turn"}`,
			want: "正文",
		},
		{
			name:    "refusal 返回中文错误并带类别",
			raw:     `{"content":[],"stop_reason":"refusal","stop_details":{"category":"cyber"}}`,
			wantErr: "cyber",
		},
		{
			name:    "空文本报错",
			raw:     `{"content":[{"type":"text","text":"  "}],"stop_reason":"end_turn"}`,
			wantErr: "空文本",
		},
		{
			name: "手工构造走 Text 字段兜底",
			hand: &anthropic.Message{
				Content: []anthropic.ContentBlockUnion{{Type: "text", Text: "手工文本"}},
			},
			want: "手工文本",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.hand
			if msg == nil {
				msg = &anthropic.Message{}
				if err := json.Unmarshal([]byte(tt.raw), msg); err != nil {
					t.Fatalf("构造 Message 失败：%v", err)
				}
			}
			got, err := messageText(msg)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v，想要包含 %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("文本 = %q，想要 %q", got, tt.want)
			}
		})
	}
}

// TestAnthropicRetry 覆盖 Anthropic 路径 5xx 重试 2 次后成功；
// WithMaxRetries(0) 与生产构造一致——重试只走 Complete 外层，不再叠 SDK 内建重试。
func TestAnthropicRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"type":"error","error":{"type":"api_error","message":"boom"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer srv.Close()

	c := &anthropicClient{
		inner: anthropic.NewClient(
			option.WithAPIKey("sk-test"),
			option.WithBaseURL(srv.URL),
			option.WithMaxRetries(0),
		),
		model:      "claude-opus-5-5",
		retryDelay: time.Millisecond,
	}
	got, err := c.Complete(context.Background(), "s", "u")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ok" {
		t.Errorf("回答 = %q", got)
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Errorf("调用次数 = %d，想要 3（2 次重试）", n)
	}
}

// TestAnthropicRetryExhausted 连续失败时重试 2 次后报错，不再多发请求。
func TestAnthropicRetryExhausted(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
	}))
	defer srv.Close()

	c := &anthropicClient{
		inner: anthropic.NewClient(
			option.WithAPIKey("sk-test"),
			option.WithBaseURL(srv.URL),
			option.WithMaxRetries(0),
		),
		model:      "claude-opus-5-5",
		retryDelay: time.Millisecond,
	}
	_, err := c.Complete(context.Background(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "重试 2 次仍失败") {
		t.Errorf("err = %v，想要「重试 2 次仍失败」", err)
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Errorf("调用次数 = %d，想要 3（首发 + 2 次重试）", n)
	}
}

// TestSnippetUTF8 截断错误体不得切断多字节 UTF-8。
func TestSnippetUTF8(t *testing.T) {
	body := []byte(strings.Repeat("错", 300)) // 900 字节，必超 200 字节上限
	s := snippet(body)
	if !utf8.ValidString(s) {
		t.Errorf("截断后不是合法 UTF-8：%q", s)
	}
	if len(s) >= len(body) {
		t.Errorf("未截断：%d 字节", len(s))
	}
	if !strings.HasSuffix(s, "…") {
		t.Errorf("缺省略号后缀：%q", s)
	}
	if n := utf8.RuneCountInString(s); n < 50 {
		t.Errorf("截断过短：%d rune", n)
	}
}

// TestRetryableStatus 校验重试状态码判定。
func TestRetryableStatus(t *testing.T) {
	tests := []struct {
		status int
		want   bool
	}{
		{429, true},
		{500, true},
		{503, true},
		{400, false},
		{401, false},
		{200, false},
	}
	for _, tt := range tests {
		if got := retryableStatus(tt.status); got != tt.want {
			t.Errorf("retryableStatus(%d) = %v，想要 %v", tt.status, got, tt.want)
		}
	}
}
