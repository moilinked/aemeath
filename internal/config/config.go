// Package config 负责从 .env 和系统环境变量加载应用配置。
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	defaultServerPort        = 8080
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 15 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 60 * time.Second
	defaultShutdownTimeout   = 10 * time.Second
	defaultLLMProvider       = LLMProviderDeepSeek
	defaultLLMRequestTimeout = 60 * time.Second
	defaultOpenAIBaseURL     = "https://api.openai.com/v1"
	defaultDeepSeekBaseURL   = "https://api.deepseek.com"
	defaultDeepSeekModel     = "deepseek-v4-pro"
	defaultAgentMaxSteps     = 8
	defaultLLMRetryAttempts  = 3
	defaultLLMRetryInitial   = 200 * time.Millisecond
	defaultLLMRetryMax       = 2 * time.Second
	defaultJWTAccessTTL      = 7 * 24 * time.Hour
	defaultJWTIssuer         = "chat-agent"
	minimumJWTSecretLength   = 32
)

// LLMProvider 表示当前启用的模型供应商。
type LLMProvider string

const (
	// LLMProviderOpenAI 表示 OpenAI 或 OpenAI 兼容网关。
	LLMProviderOpenAI LLMProvider = "openai"
	// LLMProviderDeepSeek 表示 DeepSeek OpenAI 兼容接口。
	LLMProviderDeepSeek LLMProvider = "deepseek"
)

// LLMConfig 包含当前模型供应商所需的连接配置。
// APIKey 只从环境变量读取，禁止写入日志或提交到版本控制。
type LLMConfig struct {
	Provider             LLMProvider
	BaseURL              string
	APIKey               string
	Model                string
	RequestTimeout       time.Duration
	RetryMaxAttempts     int
	RetryInitialInterval time.Duration
	RetryMaxInterval     time.Duration
}

// AgentConfig 包含 Agent 的执行边界配置。
type AgentConfig struct {
	MaxSteps int
}

// TODO: 用户信息迁移到数据库后，从 AuthConfig 移除 Username 和 PasswordHash。
// AuthConfig 包含临时单用户凭据与 JWT Access Token 配置。
// PasswordHash 和 SigningKey 只从环境变量读取，禁止写入日志或提交到版本控制。
type AuthConfig struct {
	Username     string
	PasswordHash string
	SigningKey   string
	AccessTTL    time.Duration
	Issuer       string
}

// Config 包含 HTTP 服务、LLM、Agent 和认证配置。
type Config struct {
	Address           string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	LLM               LLMConfig
	Agent             AgentConfig
	Auth              AuthConfig
}

// Load 优先保留系统环境变量，并使用项目根目录的 .env 补充本地配置。
// 生产环境可以不提供 .env，直接通过部署平台注入环境变量。
func Load() (Config, error) {
	if err := loadDotEnv(); err != nil {
		return Config{}, err
	}

	address, err := loadServerAddress()
	if err != nil {
		return Config{}, err
	}
	llmConfig, err := loadLLMConfig()
	if err != nil {
		return Config{}, err
	}
	agentMaxSteps, err := positiveIntOrDefault("AGENT_MAX_STEPS", defaultAgentMaxSteps)
	if err != nil {
		return Config{}, err
	}
	authConfig, err := loadAuthConfig()
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Address:           address,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		ReadTimeout:       defaultReadTimeout,
		WriteTimeout:      defaultWriteTimeout,
		IdleTimeout:       defaultIdleTimeout,
		ShutdownTimeout:   defaultShutdownTimeout,
		LLM:               llmConfig,
		Agent: AgentConfig{
			MaxSteps: agentMaxSteps,
		},
		Auth: authConfig,
	}

	durationSettings := []struct {
		name   string
		target *time.Duration
	}{
		{name: "SERVER_READ_HEADER_TIMEOUT", target: &cfg.ReadHeaderTimeout},
		{name: "SERVER_READ_TIMEOUT", target: &cfg.ReadTimeout},
		{name: "SERVER_WRITE_TIMEOUT", target: &cfg.WriteTimeout},
		{name: "SERVER_IDLE_TIMEOUT", target: &cfg.IdleTimeout},
		{name: "SERVER_SHUTDOWN_TIMEOUT", target: &cfg.ShutdownTimeout},
		{name: "LLM_REQUEST_TIMEOUT", target: &cfg.LLM.RequestTimeout},
	}

	for _, setting := range durationSettings {
		value := os.Getenv(setting.name)
		if value == "" {
			continue
		}

		duration, err := time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse %s: %w", setting.name, err)
		}
		*setting.target = duration
	}

	return cfg, nil
}

func loadDotEnv() error {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("load .env: %w", err)
	}
	return nil
}

func loadLLMConfig() (LLMConfig, error) {
	provider := LLMProvider(strings.ToLower(strings.TrimSpace(
		envOrDefault("LLM_PROVIDER", string(defaultLLMProvider)),
	)))

	cfg := LLMConfig{
		Provider:       provider,
		RequestTimeout: defaultLLMRequestTimeout,
	}

	switch provider {
	case LLMProviderOpenAI:
		cfg.BaseURL = envOrDefault("OPENAI_BASE_URL", defaultOpenAIBaseURL)
		cfg.APIKey = os.Getenv("OPENAI_API_KEY")
		cfg.Model = os.Getenv("OPENAI_MODEL")
	case LLMProviderDeepSeek:
		cfg.BaseURL = envOrDefault("DEEPSEEK_BASE_URL", defaultDeepSeekBaseURL)
		cfg.APIKey = os.Getenv("DEEPSEEK_API_KEY")
		cfg.Model = envOrDefault("DEEPSEEK_MODEL", defaultDeepSeekModel)
	default:
		return LLMConfig{}, fmt.Errorf("unsupported LLM_PROVIDER %q", provider)
	}

	if err := applyLLMRetryConfig(&cfg); err != nil {
		return LLMConfig{}, err
	}

	return cfg, nil
}

func applyLLMRetryConfig(cfg *LLMConfig) error {
	maxAttempts, err := positiveIntOrDefault("LLM_RETRY_MAX_ATTEMPTS", defaultLLMRetryAttempts)
	if err != nil {
		return err
	}
	initialInterval, err := durationGreaterThanZeroOrDefault(
		"LLM_RETRY_INITIAL_INTERVAL",
		defaultLLMRetryInitial,
	)
	if err != nil {
		return err
	}
	maxInterval, err := durationGreaterThanZeroOrDefault(
		"LLM_RETRY_MAX_INTERVAL",
		defaultLLMRetryMax,
	)
	if err != nil {
		return err
	}
	if maxInterval < initialInterval {
		return errors.New("LLM_RETRY_MAX_INTERVAL must be greater than or equal to LLM_RETRY_INITIAL_INTERVAL")
	}

	cfg.RetryMaxAttempts = maxAttempts
	cfg.RetryInitialInterval = initialInterval
	cfg.RetryMaxInterval = maxInterval
	return nil
}

func durationGreaterThanZeroOrDefault(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", name)
	}
	return parsed, nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func positiveIntOrDefault(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func loadServerAddress() (string, error) {
	if address := strings.TrimSpace(os.Getenv("SERVER_ADDRESS")); address != "" {
		return address, nil
	}

	port, err := positiveIntOrDefault("SERVER_PORT", defaultServerPort)
	if err != nil {
		return "", err
	}
	if port > 65535 {
		return "", errors.New("SERVER_PORT must not exceed 65535")
	}
	return fmt.Sprintf(":%d", port), nil
}

func loadAuthConfig() (AuthConfig, error) {
	username := strings.TrimSpace(os.Getenv("AUTH_USERNAME"))
	if username == "" {
		return AuthConfig{}, errors.New("AUTH_USERNAME is required")
	}
	passwordHash := os.Getenv("AUTH_PASSWORD_HASH")
	if strings.TrimSpace(passwordHash) == "" {
		return AuthConfig{}, errors.New("AUTH_PASSWORD_HASH is required")
	}

	signingKey := os.Getenv("JWT_SECRET")
	if len(signingKey) < minimumJWTSecretLength {
		return AuthConfig{}, fmt.Errorf(
			"JWT_SECRET must be at least %d bytes",
			minimumJWTSecretLength,
		)
	}

	accessTTL := defaultJWTAccessTTL
	if value := os.Getenv("JWT_ACCESS_TTL"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return AuthConfig{}, fmt.Errorf("parse JWT_ACCESS_TTL: %w", err)
		}
		if parsed <= 0 {
			return AuthConfig{}, errors.New("JWT_ACCESS_TTL must be greater than zero")
		}
		accessTTL = parsed
	}

	issuer := strings.TrimSpace(envOrDefault("JWT_ISSUER", defaultJWTIssuer))
	if issuer == "" {
		return AuthConfig{}, errors.New("JWT_ISSUER is required")
	}
	return AuthConfig{
		Username:     username,
		PasswordHash: passwordHash,
		SigningKey:   signingKey,
		AccessTTL:    accessTTL,
		Issuer:       issuer,
	}, nil
}
