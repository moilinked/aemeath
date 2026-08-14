package main

import (
	"testing"
	"time"

	"github.com/ecol/chat-agent/internal/config"
)

func TestNewLLMClient(t *testing.T) {
	tests := []struct {
		name    string
		config  config.LLMConfig
		wantErr bool
	}{
		{
			name: "valid configuration",
			config: config.LLMConfig{
				Provider:       config.LLMProviderDeepSeek,
				BaseURL:        "https://api.deepseek.com",
				APIKey:         "test-key",
				Model:          "deepseek-v4-pro",
				RequestTimeout: time.Minute,
			},
		},
		{
			name: "missing API key",
			config: config.LLMConfig{
				BaseURL:        "https://api.deepseek.com",
				Model:          "deepseek-v4-pro",
				RequestTimeout: time.Minute,
			},
			wantErr: true,
		},
		{
			name: "missing model",
			config: config.LLMConfig{
				BaseURL:        "https://api.deepseek.com",
				APIKey:         "test-key",
				RequestTimeout: time.Minute,
			},
			wantErr: true,
		},
		{
			name: "invalid request timeout",
			config: config.LLMConfig{
				BaseURL: "https://api.deepseek.com",
				APIKey:  "test-key",
				Model:   "deepseek-v4-pro",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newLLMClient(tt.config)
			if tt.wantErr && err == nil {
				t.Fatal("newLLMClient() error = nil, want an error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("newLLMClient() error = %v", err)
			}
		})
	}
}
