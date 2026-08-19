package main

import (
	"context"
	"testing"
	"time"

	"github.com/ecol/chat-agent/internal/config"
	"github.com/ecol/chat-agent/internal/llm"
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

func TestNewAgent(t *testing.T) {
	tests := []struct {
		name     string
		client   llm.Client
		maxSteps int
		wantErr  bool
	}{
		{
			name:     "valid configuration",
			client:   stubServerLLMClient{},
			maxSteps: 8,
		},
		{
			name:     "missing LLM client",
			maxSteps: 8,
			wantErr:  true,
		},
		{
			name:     "invalid max steps",
			client:   stubServerLLMClient{},
			maxSteps: 0,
			wantErr:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			created, err := newAgent(test.client, config.AgentConfig{MaxSteps: test.maxSteps})
			if test.wantErr {
				if err == nil {
					t.Fatal("newAgent() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("newAgent() error = %v", err)
			}
			if created.MaxSteps() != test.maxSteps {
				t.Fatalf("MaxSteps() = %d, want %d", created.MaxSteps(), test.maxSteps)
			}
		})
	}
}

type stubServerLLMClient struct{}

func (stubServerLLMClient) Chat(
	context.Context,
	llm.ChatRequest,
) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}
