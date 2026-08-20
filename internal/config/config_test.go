package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	testAuthUsername     = "test-user"
	testAuthPasswordHash = "test-bcrypt-hash"
	testJWTSecret        = "0123456789abcdef0123456789abcdef"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name              string
		address           string
		serverPort        string
		readTimeout       string
		llmProvider       string
		llmRequestTimeout string
		agentMaxSteps     string
		openAIAPIKey      string
		openAIBaseURL     string
		openAIModel       string
		authUsername      string
		authPasswordHash  string
		missingUsername   bool
		missingPassword   bool
		jwtSecret         string
		jwtAccessTTL      string
		jwtIssuer         string
		missingJWTSecret  bool
		wantAddress       string
		wantTimeout       time.Duration
		wantLLM           LLMConfig
		wantAgent         AgentConfig
		wantAuth          AuthConfig
		wantErr           bool
	}{
		{
			name:        "uses defaults",
			wantAddress: fmt.Sprintf(":%d", defaultServerPort),
			wantTimeout: defaultReadTimeout,
			wantLLM: LLMConfig{
				Provider:       LLMProviderDeepSeek,
				BaseURL:        defaultDeepSeekBaseURL,
				Model:          defaultDeepSeekModel,
				RequestTimeout: defaultLLMRequestTimeout,
			},
			wantAgent: AgentConfig{MaxSteps: defaultAgentMaxSteps},
			wantAuth: AuthConfig{
				Username:     testAuthUsername,
				PasswordHash: testAuthPasswordHash,
				SigningKey:   testJWTSecret,
				AccessTTL:    defaultJWTAccessTTL,
				Issuer:       defaultJWTIssuer,
			},
		},
		{
			name:              "reads environment",
			address:           "127.0.0.1:9090",
			serverPort:        "9191",
			readTimeout:       "20s",
			llmProvider:       "OPENAI",
			llmRequestTimeout: "45s",
			agentMaxSteps:     "12",
			openAIAPIKey:      "secret-key",
			openAIBaseURL:     "https://gateway.example.com/v1",
			openAIModel:       "chat-gpt-luna",
			authUsername:      "configured-user",
			authPasswordHash:  "configured-bcrypt-hash",
			jwtSecret:         "abcdef0123456789abcdef0123456789",
			jwtAccessTTL:      "30m",
			jwtIssuer:         "test-issuer",
			wantAddress:       "127.0.0.1:9090",
			wantTimeout:       20 * time.Second,
			wantLLM: LLMConfig{
				Provider:       LLMProviderOpenAI,
				BaseURL:        "https://gateway.example.com/v1",
				APIKey:         "secret-key",
				Model:          "chat-gpt-luna",
				RequestTimeout: 45 * time.Second,
			},
			wantAgent: AgentConfig{MaxSteps: 12},
			wantAuth: AuthConfig{
				Username:     "configured-user",
				PasswordHash: "configured-bcrypt-hash",
				SigningKey:   "abcdef0123456789abcdef0123456789",
				AccessTTL:    30 * time.Minute,
				Issuer:       "test-issuer",
			},
		},
		{
			name:        "reads server port",
			serverPort:  "9091",
			wantAddress: ":9091",
			wantTimeout: defaultReadTimeout,
			wantLLM: LLMConfig{
				Provider:       LLMProviderDeepSeek,
				BaseURL:        defaultDeepSeekBaseURL,
				Model:          defaultDeepSeekModel,
				RequestTimeout: defaultLLMRequestTimeout,
			},
			wantAgent: AgentConfig{MaxSteps: defaultAgentMaxSteps},
			wantAuth: AuthConfig{
				Username:     testAuthUsername,
				PasswordHash: testAuthPasswordHash,
				SigningKey:   testJWTSecret,
				AccessTTL:    defaultJWTAccessTTL,
				Issuer:       defaultJWTIssuer,
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
			name:            "rejects missing auth username",
			missingUsername: true,
			wantErr:         true,
		},
		{
			name:            "rejects missing auth password hash",
			missingPassword: true,
			wantErr:         true,
		},
		{
			name:             "rejects missing JWT secret",
			missingJWTSecret: true,
			wantErr:          true,
		},
		{
			name:      "rejects short JWT secret",
			jwtSecret: "too-short",
			wantErr:   true,
		},
		{
			name:         "rejects invalid JWT access TTL",
			jwtAccessTTL: "invalid",
			wantErr:      true,
		},
		{
			name:         "rejects non-positive JWT access TTL",
			jwtAccessTTL: "0s",
			wantErr:      true,
		},
		{
			name:      "rejects blank JWT issuer",
			jwtIssuer: " ",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SERVER_ADDRESS", tt.address)
			t.Setenv("SERVER_PORT", tt.serverPort)
			t.Setenv("SERVER_READ_TIMEOUT", tt.readTimeout)
			t.Setenv("LLM_PROVIDER", tt.llmProvider)
			t.Setenv("LLM_REQUEST_TIMEOUT", tt.llmRequestTimeout)
			t.Setenv("AGENT_MAX_STEPS", tt.agentMaxSteps)
			t.Setenv("OPENAI_API_KEY", tt.openAIAPIKey)
			t.Setenv("OPENAI_BASE_URL", tt.openAIBaseURL)
			t.Setenv("OPENAI_MODEL", tt.openAIModel)
			t.Setenv("DEEPSEEK_API_KEY", "")
			t.Setenv("DEEPSEEK_BASE_URL", "")
			t.Setenv("DEEPSEEK_MODEL", "")
			username := testAuthUsername
			if tt.authUsername != "" {
				username = tt.authUsername
			}
			if tt.missingUsername {
				username = ""
			}
			passwordHash := testAuthPasswordHash
			if tt.authPasswordHash != "" {
				passwordHash = tt.authPasswordHash
			}
			if tt.missingPassword {
				passwordHash = ""
			}
			t.Setenv("AUTH_USERNAME", username)
			t.Setenv("AUTH_PASSWORD_HASH", passwordHash)
			signingKey := testJWTSecret
			if tt.jwtSecret != "" {
				signingKey = tt.jwtSecret
			}
			if tt.missingJWTSecret {
				signingKey = ""
			}
			t.Setenv("JWT_SECRET", signingKey)
			t.Setenv("JWT_ACCESS_TTL", tt.jwtAccessTTL)
			t.Setenv("JWT_ISSUER", tt.jwtIssuer)

			cfg, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatal("Load() error = nil, want an error")
				}
				if signingKey != "" && strings.Contains(err.Error(), signingKey) {
					t.Fatal("Load() error exposes JWT signing key")
				}
				if passwordHash != "" && strings.Contains(err.Error(), passwordHash) {
					t.Fatal("Load() error exposes password hash")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
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
			if cfg.Auth.SigningKey != tt.wantAuth.SigningKey {
				t.Error("Auth signing key does not match expected value")
			}
			if cfg.Auth.Username != tt.wantAuth.Username {
				t.Error("Auth username does not match expected value")
			}
			if cfg.Auth.PasswordHash != tt.wantAuth.PasswordHash {
				t.Error("Auth password hash does not match expected value")
			}
			if cfg.Auth.AccessTTL != tt.wantAuth.AccessTTL {
				t.Errorf(
					"Auth access TTL = %s, want %s",
					cfg.Auth.AccessTTL,
					tt.wantAuth.AccessTTL,
				)
			}
			if cfg.Auth.Issuer != tt.wantAuth.Issuer {
				t.Errorf("Auth issuer = %q, want %q", cfg.Auth.Issuer, tt.wantAuth.Issuer)
			}
		})
	}
}
