package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ecol/chat-agent/internal/auth"
	"github.com/ecol/chat-agent/internal/conversation"
	"github.com/golang-jwt/jwt/v5"
)

const (
	testHTTPUsername = "test-user"
	testHTTPUserID   = "test-user-id"
)

func TestAuthRoutesRemoved(t *testing.T) {
	router := newAuthTestRouter(t)
	token := signedHTTPTestToken(t, time.Now().Add(time.Hour))

	for _, item := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/auth/login"},
		{method: http.MethodGet, path: "/api/auth/me"},
	} {
		unauthenticated := httptest.NewRecorder()
		router.ServeHTTP(
			unauthenticated,
			httptest.NewRequest(item.method, item.path, nil),
		)
		if unauthenticated.Code != http.StatusUnauthorized {
			t.Fatalf(
				"unauthenticated %s %s status = %d, want 401; body=%s",
				item.method,
				item.path,
				unauthenticated.Code,
				unauthenticated.Body,
			)
		}

		authenticated := httptest.NewRecorder()
		request := httptest.NewRequest(item.method, item.path, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		router.ServeHTTP(authenticated, request)
		if authenticated.Code != http.StatusNotFound {
			t.Fatalf(
				"authenticated %s %s status = %d, want 404; body=%s",
				item.method,
				item.path,
				authenticated.Code,
				authenticated.Body,
			)
		}
	}
}

func TestBearerMiddlewareRejectsInvalidTokens(t *testing.T) {
	expiredToken := signedHTTPTestToken(t, time.Now().Add(-time.Hour))
	tests := []struct {
		name          string
		authorization string
	}{
		{name: "missing"},
		{name: "wrong scheme", authorization: "Basic value"},
		{name: "missing token", authorization: "Bearer"},
		{name: "extra fields", authorization: "Bearer one two"},
		{name: "invalid token", authorization: "Bearer invalid"},
		{name: "expired token", authorization: "Bearer " + expiredToken},
	}

	router := newAuthTestRouter(t)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			}
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body)
			}
			if recorder.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("WWW-Authenticate header is missing")
			}
		})
	}
}

func TestAPIRouteBoundary(t *testing.T) {
	router := newAuthTestRouter(t)

	healthRecorder := httptest.NewRecorder()
	router.ServeHTTP(
		healthRecorder,
		httptest.NewRequest(http.MethodGet, "/healthz", nil),
	)
	if healthRecorder.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", healthRecorder.Code)
	}

	for _, path := range []string{
		"/api/conversations",
		"/api/chat",
		"/api/chat/stream",
		"/api/missing",
	} {
		method := http.MethodGet
		if path == "/api/chat" || path == "/api/chat/stream" {
			method = http.MethodPost
		}
		protectedRecorder := httptest.NewRecorder()
		router.ServeHTTP(
			protectedRecorder,
			httptest.NewRequest(method, path, nil),
		)
		if protectedRecorder.Code != http.StatusUnauthorized {
			t.Fatalf("protected path %s status = %d, want 401", path, protectedRecorder.Code)
		}
	}

	missingRequest := httptest.NewRequest(http.MethodGet, "/api/missing", nil)
	missingRequest.Header.Set(
		"Authorization",
		"Bearer "+signedHTTPTestToken(t, time.Now().Add(time.Hour)),
	)
	missingRecorder := httptest.NewRecorder()
	router.ServeHTTP(missingRecorder, missingRequest)
	if missingRecorder.Code != http.StatusNotFound {
		t.Fatalf("authenticated missing route status = %d, want 404", missingRecorder.Code)
	}
}

func TestChatRequiresCanChat(t *testing.T) {
	router := newAuthTestRouter(t)
	token := signedHTTPTestTokenWithChat(t, time.Now().Add(time.Hour), false)

	for _, item := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/conversations"},
		{method: http.MethodPost, path: "/api/chat"},
		{method: http.MethodPost, path: "/api/chat/stream"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(item.method, item.path, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf(
				"%s %s status = %d, want 403; body=%s",
				item.method,
				item.path,
				recorder.Code,
				recorder.Body,
			)
		}
		if !strings.Contains(recorder.Body.String(), "chat is not allowed") {
			t.Fatalf("%s %s body = %s, want chat is not allowed", item.method, item.path, recorder.Body)
		}
	}
}

func newAuthTestRouter(t *testing.T) http.Handler {
	t.Helper()

	router, err := NewRouter(Dependencies{
		Agent:         newHTTPTestAgent(t),
		Auth:          newHTTPTestAuth(t),
		Conversations: conversation.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	return router
}

func signedHTTPTestToken(t *testing.T, expiresAt time.Time) string {
	t.Helper()
	return signedHTTPTestTokenWithChat(t, expiresAt, true)
}

func signedHTTPTestTokenWithChat(t *testing.T, expiresAt time.Time, canChat bool) string {
	t.Helper()

	claims := auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "test-issuer",
			Subject:   testHTTPUserID,
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ID:        "test-token-id",
		},
		Username: testHTTPUsername,
		Capabilities: auth.Capabilities{
			Chat: canChat,
		},
	}
	value, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}
	return value
}
