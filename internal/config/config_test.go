package config

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

const testDatabaseURL = "postgres://chat_agent:chat_agent@127.0.0.1:5432/chat_agent?sslmode=disable"

var testJWTPublicKey = ed25519.NewKeyFromSeed([]byte("0123456789abcdef0123456789abcdef")).Public().(ed25519.PublicKey)

func TestLoad(t *testing.T) {
	tests := []struct {
		name                 string
		address              string
		serverPort           string
		readTimeout          string
		llmProvider          string
		llmRequestTimeout    string
		llmRetryAttempts     string
		llmRetryInitial      string
		llmRetryMax          string
		agentMaxSteps        string
		agentContextTokens   string
		agentMaxOutputTokens string
		openAIAPIKey         string
		openAIBaseURL        string
		openAIModel          string
		jwtPublicKey         string
		jwtIssuer            string
		missingJWTPublicKey  bool
		databaseURL          string
		missingDatabase      bool
		wantAddress          string
		wantTimeout          time.Duration
		wantLLM              LLMConfig
		wantAgent            AgentConfig
		wantAuth             AuthConfig
		wantErr              bool
	}{
		{
			name:        "uses defaults",
			wantAddress: fmt.Sprintf(":%d", defaultServerPort),
			wantTimeout: defaultReadTimeout,
			wantLLM: LLMConfig{
				Provider:             LLMProviderDeepSeek,
				BaseURL:              defaultDeepSeekBaseURL,
				Model:                defaultDeepSeekModel,
				RequestTimeout:       defaultLLMRequestTimeout,
				RetryMaxAttempts:     defaultLLMRetryAttempts,
				RetryInitialInterval: defaultLLMRetryInitial,
				RetryMaxInterval:     defaultLLMRetryMax,
			},
			wantAgent: defaultWantAgent(defaultAgentMaxSteps),
			wantAuth: AuthConfig{
				PublicKey: testJWTPublicKey,
				Issuer:    defaultJWTIssuer,
			},
		},
		{
			name:                 "reads environment",
			address:              "127.0.0.1:9090",
			serverPort:           "9191",
			readTimeout:          "20s",
			llmProvider:          "OPENAI",
			llmRequestTimeout:    "45s",
			llmRetryAttempts:     "4",
			llmRetryInitial:      "100ms",
			llmRetryMax:          "1s",
			agentMaxSteps:        "12",
			agentContextTokens:   "4096",
			agentMaxOutputTokens: "1024",
			openAIAPIKey:         "secret-key",
			openAIBaseURL:        "https://gateway.example.com/v1",
			openAIModel:          "chat-gpt-luna",
			jwtPublicKey:         base64.StdEncoding.EncodeToString(testJWTPublicKey),
			jwtIssuer:            "test-issuer",
			wantAddress:          "127.0.0.1:9090",
			wantTimeout:          20 * time.Second,
			wantLLM: LLMConfig{
				Provider:             LLMProviderOpenAI,
				BaseURL:              "https://gateway.example.com/v1",
				APIKey:               "secret-key",
				Model:                "chat-gpt-luna",
				RequestTimeout:       45 * time.Second,
				RetryMaxAttempts:     4,
				RetryInitialInterval: 100 * time.Millisecond,
				RetryMaxInterval:     time.Second,
			},
			wantAgent: AgentConfig{
				MaxSteps:        12,
				ContextTokens:   4096,
				MaxOutputTokens: 1024,
			},
			wantAuth: AuthConfig{
				PublicKey: testJWTPublicKey,
				Issuer:    "test-issuer",
			},
		},
		{
			name:        "reads server port",
			serverPort:  "9091",
			wantAddress: ":9091",
			wantTimeout: defaultReadTimeout,
			wantLLM: LLMConfig{
				Provider:             LLMProviderDeepSeek,
				BaseURL:              defaultDeepSeekBaseURL,
				Model:                defaultDeepSeekModel,
				RequestTimeout:       defaultLLMRequestTimeout,
				RetryMaxAttempts:     defaultLLMRetryAttempts,
				RetryInitialInterval: defaultLLMRetryInitial,
				RetryMaxInterval:     defaultLLMRetryMax,
			},
			wantAgent: defaultWantAgent(defaultAgentMaxSteps),
			wantAuth: AuthConfig{
				PublicKey: testJWTPublicKey,
				Issuer:    defaultJWTIssuer,
			},
		},
		{
			name:        "rejects invalid duration",
			readTimeout: "invalid",
			wantErr:     true,
		},
		{
			name:        "rejects unsupported provider",
			llmProvider: "unknown",
			wantErr:     true,
		},
		{
			name:          "rejects invalid agent max steps",
			agentMaxSteps: "invalid",
			wantErr:       true,
		},
		{
			name:          "rejects non-positive agent max steps",
			agentMaxSteps: "0",
			wantErr:       true,
		},
		{
			name:               "rejects invalid agent context tokens",
			agentContextTokens: "invalid",
			wantErr:            true,
		},
		{
			name:               "rejects non-positive agent context tokens",
			agentContextTokens: "0",
			wantErr:            true,
		},
		{
			name:                 "rejects invalid agent max output tokens",
			agentMaxOutputTokens: "invalid",
			wantErr:              true,
		},
		{
			name:       "rejects invalid server port",
			serverPort: "invalid",
			wantErr:    true,
		},
		{
			name:       "rejects out-of-range server port",
			serverPort: "65536",
			wantErr:    true,
		},
		{
			name:                "rejects missing JWT public key",
			missingJWTPublicKey: true,
			wantErr:             true,
		},
		{
			name:            "rejects missing DATABASE_URL",
			missingDatabase: true,
			wantErr:         true,
		},
		{
			name:         "rejects invalid JWT public key",
			jwtPublicKey: base64.StdEncoding.EncodeToString([]byte("short")),
			wantErr:      true,
		},
		{
			name:      "rejects blank JWT issuer",
			jwtIssuer: " ",
			wantErr:   true,
		},
		{
			name:             "rejects invalid LLM retry attempts",
			llmRetryAttempts: "invalid",
			wantErr:          true,
		},
		{
			name:             "rejects non-positive LLM retry attempts",
			llmRetryAttempts: "0",
			wantErr:          true,
		},
		{
			name:            "rejects invalid LLM retry interval",
			llmRetryInitial: "invalid",
			wantErr:         true,
		},
		{
			name:            "rejects LLM retry max smaller than initial",
			llmRetryInitial: "2s",
			llmRetryMax:     "1s",
			wantErr:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SERVER_ADDRESS", tt.address)
			t.Setenv("SERVER_PORT", tt.serverPort)
			t.Setenv("SERVER_READ_TIMEOUT", tt.readTimeout)
			t.Setenv("LLM_PROVIDER", tt.llmProvider)
			t.Setenv("LLM_REQUEST_TIMEOUT", tt.llmRequestTimeout)
			t.Setenv("LLM_RETRY_MAX_ATTEMPTS", tt.llmRetryAttempts)
			t.Setenv("LLM_RETRY_INITIAL_INTERVAL", tt.llmRetryInitial)
			t.Setenv("LLM_RETRY_MAX_INTERVAL", tt.llmRetryMax)
			t.Setenv("AGENT_MAX_STEPS", tt.agentMaxSteps)
			t.Setenv("AGENT_CONTEXT_TOKENS", tt.agentContextTokens)
			t.Setenv("AGENT_MAX_OUTPUT_TOKENS", tt.agentMaxOutputTokens)
			t.Setenv("OPENAI_API_KEY", tt.openAIAPIKey)
			t.Setenv("OPENAI_BASE_URL", tt.openAIBaseURL)
			t.Setenv("OPENAI_MODEL", tt.openAIModel)
			t.Setenv("DEEPSEEK_API_KEY", "")
			t.Setenv("DEEPSEEK_BASE_URL", "")
			t.Setenv("DEEPSEEK_MODEL", "")
			publicKey := base64.StdEncoding.EncodeToString(testJWTPublicKey)
			if tt.jwtPublicKey != "" {
				publicKey = tt.jwtPublicKey
			}
			if tt.missingJWTPublicKey {
				publicKey = ""
			}
			t.Setenv("JWT_PUBLIC_KEY", publicKey)
			t.Setenv("JWT_ISSUER", tt.jwtIssuer)
			databaseURL := testDatabaseURL
			if tt.databaseURL != "" {
				databaseURL = tt.databaseURL
			}
			if tt.missingDatabase {
				databaseURL = ""
			}
			t.Setenv("DATABASE_URL", databaseURL)

			cfg, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatal("Load() error = nil, want an error")
				}
				if publicKey != "" && strings.Contains(err.Error(), publicKey) {
					t.Fatal("Load() error exposes JWT public key")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.DatabaseURL != testDatabaseURL && tt.databaseURL == "" {
				t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, testDatabaseURL)
			}
			if tt.databaseURL != "" && cfg.DatabaseURL != tt.databaseURL {
				t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, tt.databaseURL)
			}
			if cfg.Address != tt.wantAddress {
				t.Errorf("Address = %q, want %q", cfg.Address, tt.wantAddress)
			}
			if cfg.ReadTimeout != tt.wantTimeout {
				t.Errorf("ReadTimeout = %s, want %s", cfg.ReadTimeout, tt.wantTimeout)
			}
			if cfg.LLM != tt.wantLLM {
				t.Errorf("LLM = %#v, want %#v", cfg.LLM, tt.wantLLM)
			}
			if cfg.Agent != tt.wantAgent {
				t.Errorf("Agent = %#v, want %#v", cfg.Agent, tt.wantAgent)
			}
			if !slices.Equal(cfg.Auth.PublicKey, tt.wantAuth.PublicKey) {
				t.Error("Auth public key does not match expected value")
			}
			if cfg.Auth.Issuer != tt.wantAuth.Issuer {
				t.Errorf("Auth issuer = %q, want %q", cfg.Auth.Issuer, tt.wantAuth.Issuer)
			}
		})
	}
}

func defaultWantAgent(maxSteps int) AgentConfig {
	return AgentConfig{
		MaxSteps:        maxSteps,
		ContextTokens:   defaultAgentContextTokens,
		MaxOutputTokens: defaultAgentMaxOutputTokens,
	}
}
