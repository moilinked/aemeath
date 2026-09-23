package httpapi

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ecol/chat-agent/internal/agent"
	"github.com/ecol/chat-agent/internal/auth"
	"github.com/ecol/chat-agent/internal/conversation"
	"github.com/ecol/chat-agent/internal/llm"
	"github.com/ecol/chat-agent/internal/tools"
)

var httpTestSigningKey = ed25519.NewKeyFromSeed([]byte("0123456789abcdef0123456789abcdef"))

func TestRouter(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		wantStatus  int
		wantBody    string
		contentType string
	}{
		{
			name:        "health check",
			path:        "/healthz",
			wantStatus:  http.StatusOK,
			wantBody:    `{"status":"ok"}`,
			contentType: "application/json",
		},
		{
			name:       "unknown route",
			path:       "/missing",
			wantStatus: http.StatusNotFound,
		},
	}

	router, err := NewRouter(Dependencies{
		Agent:         newHTTPTestAgent(t),
		Auth:          newHTTPTestAuth(t),
		Conversations: conversation.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", response.Code, tt.wantStatus)
			}
			if tt.wantBody != "" && strings.TrimSpace(response.Body.String()) != tt.wantBody {
				t.Errorf("body = %q, want %q", response.Body.String(), tt.wantBody)
			}
			if tt.contentType != "" && response.Header().Get("Content-Type") != tt.contentType {
				t.Errorf("Content-Type = %q, want %q", response.Header().Get("Content-Type"), tt.contentType)
			}
		})
	}
}

func TestNewRouterRejectsMissingDependencies(t *testing.T) {
	tests := []struct {
		name         string
		dependencies Dependencies
	}{
		{
			name:         "missing agent",
			dependencies: Dependencies{Auth: newHTTPTestAuth(t), Conversations: conversation.NewMemoryStore()},
		},
		{
			name:         "missing auth service",
			dependencies: Dependencies{Agent: newHTTPTestAgent(t), Conversations: conversation.NewMemoryStore()},
		},
		{
			name:         "missing conversation store",
			dependencies: Dependencies{Agent: newHTTPTestAgent(t), Auth: newHTTPTestAuth(t)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewRouter(test.dependencies)
			if err == nil {
				t.Fatal("NewRouter() error = nil, want an error")
			}
		})
	}
}

type stubLLMClient struct{}

func (stubLLMClient) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (client stubLLMClient) ChatStream(
	ctx context.Context,
	request llm.ChatRequest,
	emit llm.StreamHandler,
) (*llm.ChatResponse, error) {
	return llm.ChatStreamFromChat(ctx, client.Chat, request, emit)
}

func newHTTPTestAgent(t *testing.T) *agent.Agent {
	t.Helper()

	registry, err := tools.NewRegistry()
	if err != nil {
		t.Fatalf("tools.NewRegistry() error = %v", err)
	}
	chatAgent, err := agent.New(agent.Config{
		LLM:           stubLLMClient{},
		Conversations: conversation.NewMemoryStore(),
		Tools:         registry,
		MaxSteps:      1,
	})
	if err != nil {
		t.Fatalf("agent.New() error = %v", err)
	}
	return chatAgent
}

func newHTTPTestAuth(t *testing.T) *auth.Service {
	t.Helper()

	service, err := auth.New(auth.Config{
		PublicKey: httpTestSigningKey.Public().(ed25519.PublicKey),
		Issuer:    "test-issuer",
	})
	if err != nil {
		t.Fatalf("auth.New() error = %v", err)
	}
	return service
}
