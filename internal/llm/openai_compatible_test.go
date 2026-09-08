package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ecol/chat-agent/internal/retry"
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
				RetryPolicy: retry.Policy{
					MaxAttempts: 1,
				},
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

func TestOpenAICompatibleClientRetriesTransientError(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"message":"temporary"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chat-retry",
			"model":"test-model",
			"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	t.Cleanup(server.Close)

	client, err := NewOpenAICompatibleClient(OpenAICompatibleConfig{
		BaseURL:    server.URL,
		APIKey:     "test-key",
		Model:      "test-model",
		HTTPClient: server.Client(),
		RetryPolicy: retry.Policy{
			MaxAttempts:     3,
			InitialInterval: time.Nanosecond,
			MaxInterval:     time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleClient() error = %v", err)
	}

	response, err := client.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "你好"}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if response == nil || response.Message.Content != "ok" {
		t.Fatalf("Chat() response = %#v, want retried success", response)
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
}

func TestOpenAICompatibleClientChatStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model         string `json:"model"`
			Stream        bool   `json:"stream"`
			StreamOptions *struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if !request.Stream {
			t.Error("stream = false, want true")
		}
		if request.StreamOptions == nil || !request.StreamOptions.IncludeUsage {
			t.Errorf("stream_options = %#v", request.StreamOptions)
		}
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chat-stream\",\"model\":\"test-model\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"你\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"好\",\"reasoning_content\":\"think\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":2,\"total_tokens\":4}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
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

	var deltas []StreamDelta
	response, err := client.ChatStream(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "你好"}},
	}, func(delta StreamDelta) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if response.Message.Content != "你好" {
		t.Fatalf("content = %q, want 你好", response.Message.Content)
	}
	if response.Message.ReasoningContent != "think" {
		t.Fatalf("reasoning = %q, want think", response.Message.ReasoningContent)
	}
	if response.FinishReason != "stop" || response.Usage.TotalTokens != 4 {
		t.Fatalf("response = %#v", response)
	}
	if len(deltas) != 3 {
		t.Fatalf("delta count = %d, want 3: %#v", len(deltas), deltas)
	}
}

func TestOpenAICompatibleClientChatStreamToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"weather\",\"arguments\":\"\"}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"city\\\":\\\"北京\\\"}\"}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
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

	response, err := client.ChatStream(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "天气"}},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if response.FinishReason != "tool_calls" {
		t.Fatalf("FinishReason = %q, want tool_calls", response.FinishReason)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %#v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call-1" || call.Function.Name != "weather" || call.Function.Arguments != `{"city":"北京"}` {
		t.Fatalf("tool call = %#v", call)
	}
}

func TestOpenAICompatibleClientChatStreamHonorsContext(t *testing.T) {
	client, err := NewOpenAICompatibleClient(OpenAICompatibleConfig{
		BaseURL: "https://example.com/v1",
		APIKey:  "test-key",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleClient() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.ChatStream(ctx, ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "你好"}},
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ChatStream() error = %v, want context.Canceled", err)
	}
}

func TestOpenAICompatibleClientChatStreamRetriesBeforeOutput(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"message":"temporary"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(server.Close)

	client, err := NewOpenAICompatibleClient(OpenAICompatibleConfig{
		BaseURL:    server.URL,
		APIKey:     "test-key",
		Model:      "test-model",
		HTTPClient: server.Client(),
		RetryPolicy: retry.Policy{
			MaxAttempts:     3,
			InitialInterval: time.Nanosecond,
			MaxInterval:     time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleClient() error = %v", err)
	}

	response, err := client.ChatStream(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "你好"}},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if response == nil || response.Message.Content != "ok" {
		t.Fatalf("ChatStream() response = %#v", response)
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
}
