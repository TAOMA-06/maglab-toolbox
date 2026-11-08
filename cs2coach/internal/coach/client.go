package coach

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// 环境变量与默认值（契约见 docs/CONTRACTS.md internal/coach 节）。
const (
	// providerAnthropic / providerOpenAI / providerOllama 是 CS2COACH_LLM_PROVIDER 的合法取值。
	providerAnthropic = "anthropic"
	providerOpenAI    = "openai"
	providerOllama    = "ollama"

	// defaultAnthropicModel 是 Anthropic 路径缺省模型（Opus 5.5，thinking 自适应，无需显式开启）。
	defaultAnthropicModel = "claude-opus-5-5"
	// defaultOpenAIModel 是 OpenAI 兼容路径缺省模型。
	defaultOpenAIModel = "gpt-4o-mini"
	// defaultOllamaModel 是 Ollama 路径缺省模型名。本机没装它时自动改用已装模型
	// （见 ollama.go pickModel）——本地模型名千差万别，死守一个名字只会让首跑直接 404。
	defaultOllamaModel = "llama3.2"
	// defaultAnthropicBaseURL 是 Anthropic 官方端点。
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	// defaultOpenAIBaseURL 是 OpenAI 官方端点（可指向 DeepSeek/Kimi 等兼容端点）。
	defaultOpenAIBaseURL = "https://api.openai.com/v1"
	// defaultOllamaBaseURL 是本机 Ollama 缺省地址（Ollama 不需要 API key）。
	defaultOllamaBaseURL = "http://localhost:11434"

	// requestTimeout 是单次 LLM 请求超时（契约：300s）。
	requestTimeout = 300 * time.Second
	// maxRetries 是 429/5xx 的额外重试次数（契约：重试 2 次，指数退避）。
	maxRetries = 2
	// defaultRetryDelay 是重试退避基数（测试可注入更小值）。
	defaultRetryDelay = time.Second
)

// envConfig 是从环境变量解析出的 LLM 配置。
type envConfig struct {
	provider string // anthropic | openai | ollama
	model    string
	baseURL  string
	apiKey   string
	// explicitKey 表示密钥来自 CS2COACH_LLM_API_KEY（需显式覆盖 SDK 默认解析）。
	explicitKey bool
	// explicitModel 表示模型名来自 CS2COACH_LLM_MODEL（Ollama 路径据此决定是否自动选模型）。
	explicitModel bool
	// baseURLOverridden 表示设置了 CS2COACH_LLM_BASE_URL。
	baseURLOverridden bool
}

// NewClientFromEnv 按环境变量构造 Client：
//
//	CS2COACH_LLM_PROVIDER   anthropic | openai | ollama（缺省 anthropic）
//	CS2COACH_LLM_API_KEY    优先于 provider 原生密钥（ollama 不需要密钥）
//	CS2COACH_LLM_MODEL      anthropic 缺省 claude-opus-5-5；openai 缺省 gpt-4o-mini；ollama 缺省 llama3.2（本机无此模型时自动用已装模型）
//	CS2COACH_LLM_BASE_URL   anthropic 缺省 https://api.anthropic.com；openai 缺省 https://api.openai.com/v1；ollama 缺省 http://localhost:11434
//
// 原生密钥：anthropic 取 ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN；openai 取 OPENAI_API_KEY。
// 完全没有密钥来源时返回 ErrNotConfigured（ollama 路径不需要密钥，直接可构造）。
// Claude 系列模型（名字以 claude 开头）强制走 Anthropic 官方 SDK，禁止走 OpenAI 兼容 / Ollama 通道。
func NewClientFromEnv() (Client, error) {
	cfg, err := loadEnvConfig()
	if err != nil {
		return nil, err
	}
	switch cfg.provider {
	case providerAnthropic:
		return newAnthropicClient(cfg)
	case providerOpenAI:
		return newOpenAIClient(cfg)
	case providerOllama:
		return newOllamaClient(cfg), nil
	default:
		// loadEnvConfig 已校验，这里只是兜底。
		return nil, fmt.Errorf("不支持的 LLM provider：%q（只支持 anthropic / openai / ollama）", cfg.provider)
	}
}

// loadEnvConfig 解析并校验环境变量。
func loadEnvConfig() (envConfig, error) {
	var cfg envConfig

	provider := strings.ToLower(strings.TrimSpace(os.Getenv("CS2COACH_LLM_PROVIDER")))
	switch provider {
	case "":
		provider = providerAnthropic
	case providerAnthropic, providerOpenAI, providerOllama:
	default:
		return cfg, fmt.Errorf("不支持的 CS2COACH_LLM_PROVIDER：%q（只支持 anthropic / openai / ollama）", provider)
	}
	cfg.provider = provider

	cfg.model = strings.TrimSpace(os.Getenv("CS2COACH_LLM_MODEL"))
	cfg.explicitModel = cfg.model != ""
	if cfg.model == "" {
		switch provider {
		case providerAnthropic:
			cfg.model = defaultAnthropicModel
		case providerOllama:
			cfg.model = defaultOllamaModel
		default:
			cfg.model = defaultOpenAIModel
		}
	}

	// Claude 模型必须走官方 SDK：openai / ollama 通道直接报错，提示改 provider。
	if provider != providerAnthropic && strings.HasPrefix(strings.ToLower(cfg.model), "claude") {
		return cfg, fmt.Errorf("模型 %q 是 Claude 系列，禁止走 %s 通道：请设置 CS2COACH_LLM_PROVIDER=anthropic", cfg.model, provider)
	}

	if base := strings.TrimSpace(os.Getenv("CS2COACH_LLM_BASE_URL")); base != "" {
		cfg.baseURL = strings.TrimRight(base, "/")
		cfg.baseURLOverridden = true
	} else {
		switch provider {
		case providerAnthropic:
			cfg.baseURL = defaultAnthropicBaseURL
		case providerOllama:
			cfg.baseURL = defaultOllamaBaseURL
		default:
			cfg.baseURL = defaultOpenAIBaseURL
		}
	}

	cfg.apiKey, cfg.explicitKey = resolveAPIKey(provider)
	// 本地 Ollama 不需要密钥：跳过密钥校验，避免「没配 key 就 ErrNotConfigured」。
	if provider == providerOllama {
		return cfg, nil
	}
	if cfg.apiKey == "" {
		return cfg, ErrNotConfigured
	}
	return cfg, nil
}

// resolveAPIKey 按优先级取密钥：CS2COACH_LLM_API_KEY > provider 原生密钥。
// 返回值 explicit 表示是否来自 CS2COACH_LLM_API_KEY。
func resolveAPIKey(provider string) (key string, explicit bool) {
	if k := strings.TrimSpace(os.Getenv("CS2COACH_LLM_API_KEY")); k != "" {
		return k, true
	}
	switch provider {
	case providerAnthropic:
		for _, env := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"} {
			if k := strings.TrimSpace(os.Getenv(env)); k != "" {
				return k, false
			}
		}
	case providerOpenAI:
		if k := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); k != "" {
			return k, false
		}
	case providerOllama:
		// 本地 Ollama 无需鉴权；CS2COACH_LLM_API_KEY 若设置仍会被解析（远端反向代理场景）。
	}
	return "", false
}

// retryableStatus 判断 HTTP 状态码是否值得重试（429/5xx）。
func retryableStatus(status int) bool {
	return status == 429 || status >= 500
}

// backoffDelay 返回第 attempt 次失败后的退避时长（指数退避：1s、2s…）。
func backoffDelay(base time.Duration, attempt int) time.Duration {
	d := base
	for i := 0; i < attempt; i++ {
		d *= 2
	}
	return d
}
