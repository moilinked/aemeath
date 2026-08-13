package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewOpenAICompatibleClient(t *testing.T) {
	tests := []struct {
		name    string
		config  OpenAICompatibleConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: OpenAICompatibleConfig{
				BaseURL: "https://api.example.com/v1",
				APIKey:  "test-key",
				Model:   "chat-gpt-luna",
			},
		},
		{
			name: "invalid base URL",
			config: OpenAICompatibleConfig{
				BaseURL: "api.example.com",
				APIKey:  "test-key",
				Model:   "chat-gpt-luna",
			},
			wantErr: true,
		},
		{
			name: "malformed base URL",
			config: OpenAICompatibleConfig{
				BaseURL: "http://%",
				APIKey:  "test-key",
				Model:   "chat-gpt-luna",
			},
			wantErr: true,
		},
		{
			name: "missing API key",
			config: OpenAICompatibleConfig{
				BaseURL: OpenAIBaseURL,
				Model:   "chat-gpt-luna",
			},
			wantErr: true,
		},
		{
			name: "missing model",
			config: OpenAICompatibleConfig{
				BaseURL: OpenAIBaseURL,
				APIKey:  "test-key",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewOpenAICompatibleClient(tt.config)
			if tt.wantErr && err == nil {
				t.Fatal("NewOpenAICompatibleClient() error = nil, want an error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("NewOpenAICompatibleClient() error = %v", err)
			}
		})
	}
}

func TestOpenAICompatibleClientChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s, want /v1/chat/completions", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}

		var request struct {
			Model    string    `json:"model"`
			Messages []Message `json:"messages"`
			Thinking *Thinking `json:"thinking"`
			Stream   bool      `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.Model != DeepSeekV4Pro {
			t.Errorf("model = %q, want %q", request.Model, DeepSeekV4Pro)
		}
		if len(request.Messages) != 1 || request.Messages[0].Content != "你好" {
			t.Errorf("messages = %#v", request.Messages)
		}
		if request.Thinking == nil || request.Thinking.Type != "enabled" {
			t.Errorf("thinking = %#v", request.Thinking)
		}
		if request.Stream {
			t.Error("stream = true, want false")
		}

		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{
			"id":"chat-1",
			"model":"deepseek-v4-pro",
			"choices":[{
				"message":{
					"role":"assistant",
					"content":"",
					"reasoning_content":"需要调用天气工具",
					"tool_calls":[{
						"id":"call-1",
						"type":"function",
						"function":{"name":"weather","arguments":"{\"city\":\"北京\"}"}
					}]
				},
				"finish_reason":"tool_calls"
			}],
			"usage":{"prompt_tokens":10,"completion_tokens":8,"total_tokens":18}
		}`))
		if err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewOpenAICompatibleClient(OpenAICompatibleConfig{
		BaseURL:    server.URL + "/v1/",
		APIKey:     "test-key",
		Model:      DeepSeekV4Pro,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleClient() error = %v", err)
	}

	response, err := client.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "你好"}},
		Thinking: &Thinking{Type: "enabled"},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if response.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, want tool_calls", response.FinishReason)
	}
	if response.Message.ReasoningContent != "需要调用天气工具" {
		t.Errorf("ReasoningContent = %q", response.Message.ReasoningContent)
	}
	if len(response.Message.ToolCalls) != 1 || response.Message.ToolCalls[0].Function.Name != "weather" {
		t.Errorf("ToolCalls = %#v", response.Message.ToolCalls)
	}
	if response.Usage.TotalTokens != 18 {
		t.Errorf("TotalTokens = %d, want 18", response.Usage.TotalTokens)
	}
}

func TestOpenAICompatibleClientChatErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantAPIErr bool
	}{
		{
			name:       "API error",
			statusCode: http.StatusUnauthorized,
			body:       `{"error":{"message":"invalid key","type":"authentication_error","code":"invalid_api_key"}}`,
			wantAPIErr: true,
		},
		{
			name:       "invalid response JSON",
			statusCode: http.StatusOK,
			body:       `{`,
		},
		{
			name:       "response without choices",
			statusCode: http.StatusOK,
			body:       `{"id":"chat-1","choices":[]}`,
		},
		{
			name:       "plain text API error",
			statusCode: http.StatusBadGateway,
			body:       "upstream unavailable",
			wantAPIErr: true,
		},
		{
			name:       "empty API error",
			statusCode: http.StatusServiceUnavailable,
			wantAPIErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				if _, err := w.Write([]byte(tt.body)); err != nil {
					t.Errorf("write response: %v", err)
				}
			}))
			t.Cleanup(server.Close)

			client, err := NewOpenAICompatibleClient(OpenAICompatibleConfig{
				BaseURL:    server.URL,
				APIKey:     "test-key",
				Model:      "test-model",
				HTTPClient: server.Client(),
			})
			if err != nil {
				t.Fatalf("NewOpenAICompatibleClient() error = %v", err)
			}

			_, err = client.Chat(context.Background(), ChatRequest{
				Messages: []Message{{Role: RoleUser, Content: "你好"}},
			})
			if err == nil {
				t.Fatal("Chat() error = nil, want an error")
			}

			var apiErr *APIError
			if errors.As(err, &apiErr) != tt.wantAPIErr {
				t.Errorf("errors.As(APIError) = %t, want %t", errors.As(err, &apiErr), tt.wantAPIErr)
			}
		})
	}
}

func TestOpenAICompatibleClientChatHonorsContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	client, err := NewOpenAICompatibleClient(OpenAICompatibleConfig{
		BaseURL:    server.URL,
		APIKey:     "test-key",
		Model:      "test-model",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleClient() error = %v", err)
	}

	_, err = client.Chat(context.Background(), ChatRequest{})
	if err == nil {
		t.Fatal("Chat() without messages error = nil, want an error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = client.Chat(ctx, ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "你好"}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Chat() error = %v, want context.Canceled", err)
	}
}
