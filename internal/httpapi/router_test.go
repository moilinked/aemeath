package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

	router := NewRouter()

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
