package coach

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestOllama 构造指向 httptest 的 ollamaClient：退避压到 1ms，模型名/自动模式可控。
func newTestOllama(srv *httptest.Server, model string, auto bool) *ollamaClient {
	return &ollamaClient{
		baseURL:    srv.URL,
		model:      model,
		autoModel:  auto,
		httpClient: srv.Client(),
		retryDelay: time.Millisecond,
	}
}

// TestOllamaCompleteChat 校验 /api/chat 请求形态（路径、非流式、system/user 顺序、options）与响应解析。
func TestOllamaCompleteChat(t *testing.T) {
	var (
		gotPath, gotMethod, gotAuth string
		gotReq                      ollamaChatRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotAuth = r.URL.Path, r.Method, r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("请求体不是 JSON：%v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"jia:latest","message":{"role":"assistant","content":"回合 2 [tick 5100] 首杀白给。"},"done":true,"done_reason":"stop"}`)
	}))
	defer srv.Close()

	c := newTestOllama(srv, "jia:latest", false)
	got, err := c.Complete(context.Background(), "系统提示", "用户问题")
	if err != nil {
		t.Fatal(err)
	}
	if got != "回合 2 [tick 5100] 首杀白给。" {
		t.Errorf("回答 = %q", got)
	}
	if c.Model() != "jia:latest" {
		t.Errorf("Model() = %q", c.Model())
	}
	if gotPath != "/api/chat" || gotMethod != http.MethodPost {
		t.Errorf("请求 %s %s，想要 POST /api/chat", gotMethod, gotPath)
	}
	if gotAuth != "" {
		t.Errorf("本地 Ollama 不该带 Authorization，实际 %q", gotAuth)
	}
	if gotReq.Model != "jia:latest" || len(gotReq.Messages) != 2 {
		t.Fatalf("请求体 = %+v", gotReq)
	}
	if gotReq.Messages[0].Role != "system" || gotReq.Messages[0].Content != "系统提示" ||
		gotReq.Messages[1].Role != "user" || gotReq.Messages[1].Content != "用户问题" {
		t.Errorf("消息顺序 = %+v", gotReq.Messages)
	}
	if gotReq.Stream {
		t.Error("stream 必须为 false：流式会让响应变成多行 NDJSON")
	}
	if gotReq.Think {
		t.Error("think 缺省必须关闭：思考模型慢十几倍，300s 内跑不完一局报告")
	}
	if gotReq.Options == nil || gotReq.Options.NumCtx != ollamaNumCtx || gotReq.Options.Temperature != ollamaTemperature {
		t.Errorf("options = %+v，想要 num_ctx=%d temperature=%v", gotReq.Options, ollamaNumCtx, ollamaTemperature)
	}
}

// TestOllamaFallsBackToGenerate 老版本 Ollama 无 /api/chat（纯文本 404）时退回 /api/generate。
func TestOllamaFallsBackToGenerate(t *testing.T) {
	var paths []string
	var gotGen ollamaGenerateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/api/chat":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, "404 page not found")
		case "/api/generate":
			if err := json.NewDecoder(r.Body).Decode(&gotGen); err != nil {
				t.Errorf("generate 请求体不是 JSON：%v", err)
			}
			fmt.Fprint(w, `{"model":"wen:latest","response":"回合 7 [tick 8300] 残局 1v1 被架死。","done":true}`)
		default:
			t.Errorf("意外路径 %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := newTestOllama(srv, "wen:latest", false)
	got, err := c.Complete(context.Background(), "系统提示", "用户问题")
	if err != nil {
		t.Fatal(err)
	}
	if got != "回合 7 [tick 8300] 残局 1v1 被架死。" {
		t.Errorf("回答 = %q", got)
	}
	if len(paths) < 2 || paths[0] != "/api/chat" || paths[1] != "/api/generate" {
		t.Errorf("请求路径序列 = %v，想要先 /api/chat 再 /api/generate", paths)
	}
	if gotGen.System != "系统提示" || gotGen.Prompt != "用户问题" {
		t.Errorf("generate 请求体 = %+v", gotGen)
	}
	if gotGen.Stream {
		t.Error("generate 的 stream 必须为 false")
	}
}

// TestOllamaModelNotFoundExplicit 显式指定模型不存在：如实报错，并列出本机已装模型。
func TestOllamaModelNotFoundExplicit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":"model \"qwen:1b\" not found, try pulling it first"}`)
		case "/api/tags":
			fmt.Fprint(w, `{"models":[{"name":"jia:latest"},{"name":"wen:latest"}]}`)
		}
	}))
	defer srv.Close()

	c := newTestOllama(srv, "qwen:1b", false)
	_, err := c.Complete(context.Background(), "s", "u")
	if err == nil {
		t.Fatal("模型不存在时必须报错")
	}
	for _, want := range []string{"qwen:1b", "jia:latest", "CS2COACH_LLM_MODEL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息缺 %q：%v", want, err)
		}
	}
}

// TestOllamaAutoPicksInstalledModel 未显式指定模型且缺省 llama3.2 没装时，自动用本机已装模型。
func TestOllamaAutoPicksInstalledModel(t *testing.T) {
	var used string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			fmt.Fprint(w, `{"models":[{"name":"jia:latest"},{"name":"wen:latest"}]}`)
		case "/api/chat":
			var req ollamaChatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("请求体不是 JSON：%v", err)
			}
			used = req.Model
			fmt.Fprint(w, `{"message":{"content":"ok"},"done":true}`)
		}
	}))
	defer srv.Close()

	c := newTestOllama(srv, defaultOllamaModel, true)
	got, err := c.Complete(context.Background(), "s", "u")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ok" {
		t.Errorf("回答 = %q", got)
	}
	if used != "jia:latest" {
		t.Errorf("实际请求模型 = %q，想要本机首个模型 jia:latest", used)
	}
	if c.Model() != "jia:latest" {
		t.Errorf("Model() = %q，想要 jia:latest", c.Model())
	}
}

// TestOllamaAutoPrefersDefaultModel 缺省模型已装时优先用它（不因自动模式乱换）。
func TestOllamaAutoPrefersDefaultModel(t *testing.T) {
	var used string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			fmt.Fprint(w, `{"models":[{"name":"jia:latest"},{"name":"llama3.2:latest"}]}`)
		case "/api/chat":
			var req ollamaChatRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			used = req.Model
			fmt.Fprint(w, `{"message":{"content":"ok"},"done":true}`)
		}
	}))
	defer srv.Close()

	c := newTestOllama(srv, defaultOllamaModel, true)
	if _, err := c.Complete(context.Background(), "s", "u"); err != nil {
		t.Fatal(err)
	}
	if used != defaultOllamaModel {
		t.Errorf("实际请求模型 = %q，想要 %q（tags 里的 llama3.2:latest 应被视为同一个）", used, defaultOllamaModel)
	}
	if c.Model() != defaultOllamaModel {
		t.Errorf("Model() = %q", c.Model())
	}
}

// TestOllamaAutoFallbackOnVanishedModel 自动模式选中的模型突然 404 时，换另一个已装模型重试成功。
func TestOllamaAutoFallbackOnVanishedModel(t *testing.T) {
	var tried []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			fmt.Fprint(w, `{"models":[{"name":"jia:latest"},{"name":"wen:latest"}]}`)
		case "/api/chat":
			var req ollamaChatRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			tried = append(tried, req.Model)
			if req.Model == "jia:latest" {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"error":"model \"jia:latest\" not found, try pulling it first"}`)
				return
			}
			fmt.Fprint(w, `{"message":{"content":"回合 3 [tick 4200] 换弹期被抓。"},"done":true}`)
		}
	}))
	defer srv.Close()

	c := newTestOllama(srv, defaultOllamaModel, true)
	got, err := c.Complete(context.Background(), "s", "u")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "回合 3") {
		t.Errorf("回答 = %q", got)
	}
	if len(tried) != 2 || tried[0] != "jia:latest" || tried[1] != "wen:latest" {
		t.Errorf("尝试序列 = %v，想要 [jia:latest wen:latest]", tried)
	}
	if c.Model() != "wen:latest" {
		t.Errorf("Model() = %q，想要 wen:latest（切换后应记住新模型）", c.Model())
	}
}

// TestOllamaRetry 覆盖 5xx 重试 2 次后成功。
func TestOllamaRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"out of memory"}`)
			return
		}
		fmt.Fprint(w, `{"message":{"content":"ok"},"done":true}`)
	}))
	defer srv.Close()

	c := newTestOllama(srv, "luo:32b", false)
	got, err := c.Complete(context.Background(), "s", "u")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ok" {
		t.Errorf("回答 = %q", got)
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Errorf("调用次数 = %d，想要 3（首发 + 2 次重试）", n)
	}
}

// TestOllamaRetryExhausted 连续 5xx 时重试 2 次后报错，不再多发请求。
func TestOllamaRetryExhausted(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"boom"}`)
	}))
	defer srv.Close()

	c := newTestOllama(srv, "luo:32b", false)
	_, err := c.Complete(context.Background(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "重试 2 次仍失败") {
		t.Fatalf("err = %v，想要「重试 2 次仍失败」", err)
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Errorf("调用次数 = %d，想要 3", n)
	}
}

// TestOllamaEmptyAnswer 200 但内容为空时报错，不把空串当答案交给上层。
func TestOllamaEmptyAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"message":{"role":"assistant","content":"   "},"done":true,"done_reason":"stop"}`)
	}))
	defer srv.Close()

	c := newTestOllama(srv, "jia:latest", false)
	_, err := c.Complete(context.Background(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "空回答") {
		t.Fatalf("err = %v，想要「空回答」", err)
	}
}

// TestOllamaThinkForwarded CS2COACH_OLLAMA_THINK 开启后请求体必须带 think:true。
func TestOllamaThinkForwarded(t *testing.T) {
	var gotReq ollamaChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("请求体不是 JSON：%v", err)
		}
		fmt.Fprint(w, `{"message":{"content":"ok"},"done":true}`)
	}))
	defer srv.Close()

	c := newTestOllama(srv, "jia:latest", false)
	c.think = true
	if _, err := c.Complete(context.Background(), "s", "u"); err != nil {
		t.Fatal(err)
	}
	if !gotReq.Think {
		t.Error("客户端开启思考时请求体 think 必须为 true")
	}
}

// TestOllamaServerUnreachable 服务没起时给出可操作提示（含 ollama serve）。
func TestOllamaServerUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // 立刻关掉，模拟 Ollama 没运行

	c := &ollamaClient{
		baseURL:    url,
		model:      "jia:latest",
		httpClient: &http.Client{Timeout: time.Second},
		retryDelay: time.Millisecond,
	}
	_, err := c.Complete(context.Background(), "s", "u")
	if err == nil {
		t.Fatal("服务不可达时必须报错")
	}
	for _, want := range []string{"无法连接本地 Ollama", "ollama serve"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息缺 %q：%v", want, err)
		}
	}
}

// TestOllamaBearerWhenConfigured 给 Ollama 套反向代理时，CS2COACH_LLM_API_KEY 以 Bearer 下发。
func TestOllamaBearerWhenConfigured(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"message":{"content":"ok"},"done":true}`)
	}))
	defer srv.Close()

	c := newTestOllama(srv, "jia:latest", false)
	c.apiKey = "sk-proxy"
	if _, err := c.Complete(context.Background(), "s", "u"); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer sk-proxy" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

// TestOllamaEnvWiring 校验 CS2COACH_LLM_PROVIDER=ollama 的环境变量解析：
// 不需要 API key、缺省模型与端点、显式模型/端点覆盖、Claude 守卫。
func TestOllamaEnvWiring(t *testing.T) {
	clearLLMEnv(t)
	t.Setenv("CS2COACH_LLM_PROVIDER", "ollama")

	c, err := NewClientFromEnv()
	if err != nil {
		t.Fatalf("ollama 不需要 API key，构造不该报错：%v", err)
	}
	oc, ok := c.(*ollamaClient)
	if !ok {
		t.Fatalf("类型 = %T，想要 *ollamaClient", c)
	}
	if oc.Model() != defaultOllamaModel || oc.autoModel != true {
		t.Errorf("缺省模型 = %q（auto=%v），想要 %q 且自动选模型", oc.Model(), oc.autoModel, defaultOllamaModel)
	}
	if oc.baseURL != defaultOllamaBaseURL {
		t.Errorf("缺省 baseURL = %q，想要 %q", oc.baseURL, defaultOllamaBaseURL)
	}
	if oc.think {
		t.Error("缺省必须关闭思考（CS2COACH_OLLAMA_THINK 未设置）")
	}

	t.Run("CS2COACH_OLLAMA_THINK 开启思考", func(t *testing.T) {
		t.Setenv(envOllamaThink, "1")
		c, err := NewClientFromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if !c.(*ollamaClient).think {
			t.Error("CS2COACH_OLLAMA_THINK=1 时 think 必须为 true")
		}
	})

	t.Run("显式模型与端点", func(t *testing.T) {
		t.Setenv("CS2COACH_LLM_MODEL", "luo:32b")
		t.Setenv("CS2COACH_LLM_BASE_URL", "http://127.0.0.1:12345/")
		c, err := NewClientFromEnv()
		if err != nil {
			t.Fatal(err)
		}
		oc := c.(*ollamaClient)
		if oc.Model() != "luo:32b" || oc.autoModel {
			t.Errorf("模型 = %q（auto=%v），想要 luo:32b 且不自动切换", oc.Model(), oc.autoModel)
		}
		if oc.baseURL != "http://127.0.0.1:12345" {
			t.Errorf("baseURL = %q，尾斜杠应被去掉", oc.baseURL)
		}
	})

	t.Run("Claude 模型禁止走 ollama", func(t *testing.T) {
		t.Setenv("CS2COACH_LLM_MODEL", "claude-opus-5-5")
		if _, err := NewClientFromEnv(); err == nil || !strings.Contains(err.Error(), "PROVIDER=anthropic") {
			t.Errorf("err = %v，想要提示 CS2COACH_LLM_PROVIDER=anthropic", err)
		}
	})
}
