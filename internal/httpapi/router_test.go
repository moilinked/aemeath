package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ecol/chat-agent/internal/llm"
)

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

	router, err := NewRouter(Dependencies{LLM: stubLLMClient{}})
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
	_, err := NewRouter(Dependencies{})
	if err == nil {
		t.Fatal("NewRouter() error = nil, want an error")
	}
}

type stubLLMClient struct{}

func (stubLLMClient) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}
