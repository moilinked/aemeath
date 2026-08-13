package config

import (
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name              string
		address           string
		readTimeout       string
		llmProvider       string
		llmRequestTimeout string
		openAIAPIKey      string
		openAIBaseURL     string
		openAIModel       string
		wantAddress       string
		wantTimeout       time.Duration
		wantLLM           LLMConfig
		wantErr           bool
	}{
		{
			name:        "uses defaults",
			wantAddress: defaultAddress,
			wantTimeout: defaultReadTimeout,
			wantLLM: LLMConfig{
				Provider:       LLMProviderDeepSeek,
				BaseURL:        defaultDeepSeekBaseURL,
				Model:          defaultDeepSeekModel,
				RequestTimeout: defaultLLMRequestTimeout,
			},
		},
		{
			name:              "reads environment",
			address:           "127.0.0.1:9090",
			readTimeout:       "20s",
			llmProvider:       "OPENAI",
			llmRequestTimeout: "45s",
			openAIAPIKey:      "secret-key",
			openAIBaseURL:     "https://gateway.example.com/v1",
			openAIModel:       "chat-gpt-luna",
			wantAddress:       "127.0.0.1:9090",
			wantTimeout:       20 * time.Second,
			wantLLM: LLMConfig{
				Provider:       LLMProviderOpenAI,
				BaseURL:        "https://gateway.example.com/v1",
				APIKey:         "secret-key",
				Model:          "chat-gpt-luna",
				RequestTimeout: 45 * time.Second,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SERVER_ADDRESS", tt.address)
			t.Setenv("SERVER_READ_TIMEOUT", tt.readTimeout)
			t.Setenv("LLM_PROVIDER", tt.llmProvider)
			t.Setenv("LLM_REQUEST_TIMEOUT", tt.llmRequestTimeout)
			t.Setenv("OPENAI_API_KEY", tt.openAIAPIKey)
			t.Setenv("OPENAI_BASE_URL", tt.openAIBaseURL)
			t.Setenv("OPENAI_MODEL", tt.openAIModel)
			t.Setenv("DEEPSEEK_API_KEY", "")
			t.Setenv("DEEPSEEK_BASE_URL", "")
			t.Setenv("DEEPSEEK_MODEL", "")

			cfg, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatal("Load() error = nil, want an error")
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
		})
	}
}
