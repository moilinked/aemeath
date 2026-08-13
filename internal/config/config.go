// Package config 负责从 .env 和系统环境变量加载应用配置。
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	defaultAddress           = ":8080"
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
	Provider       LLMProvider
	BaseURL        string
	APIKey         string
	Model          string
	RequestTimeout time.Duration
}

// Config 包含 HTTP 服务和 LLM 配置。
type Config struct {
	Address           string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	LLM               LLMConfig
}

// Load 优先保留系统环境变量，并使用项目根目录的 .env 补充本地配置。
// 生产环境可以不提供 .env，直接通过部署平台注入环境变量。
func Load() (Config, error) {
	if err := loadDotEnv(); err != nil {
		return Config{}, err
	}

	llmConfig, err := loadLLMConfig()
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Address:           envOrDefault("SERVER_ADDRESS", defaultAddress),
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		ReadTimeout:       defaultReadTimeout,
		WriteTimeout:      defaultWriteTimeout,
		IdleTimeout:       defaultIdleTimeout,
		ShutdownTimeout:   defaultShutdownTimeout,
		LLM:               llmConfig,
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

	return cfg, nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
