package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ecol/chat-agent/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

const testHTTPUsername = "test-user"

type stubAuthService struct {
	token           *auth.AccessToken
	authenticateErr error
}

func (service stubAuthService) Authenticate(
	context.Context,
	string,
	string,
) (*auth.AccessToken, error) {
	return service.token, service.authenticateErr
}

func (service stubAuthService) Verify(
	context.Context,
	string,
) (auth.Identity, error) {
	return auth.Identity{}, auth.ErrInvalidToken
}

func TestLoginAndMe(t *testing.T) {
	router := newAuthTestRouter(t)
	loginRecorder := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/login",
		strings.NewReader(
			`{"username":"`+testHTTPUsername+`","password":"`+
				defaultHTTPTestPassword()+`"}`,
		),
	)
	loginRequest.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(loginRecorder, loginRequest)
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", loginRecorder.Code, loginRecorder.Body)
	}
	if loginRecorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", loginRecorder.Header().Get("Cache-Control"))
	}

	var response loginResponse
	if err := json.NewDecoder(loginRecorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if response.AccessToken == "" {
		t.Fatal("access_token is empty")
	}
	if response.TokenType != "Bearer" {
		t.Fatalf("token_type = %q, want Bearer", response.TokenType)
	}
	if response.ExpiresIn <= 0 || response.ExpiresIn > int64(time.Hour.Seconds()) {
		t.Fatalf("expires_in = %d, want within one hour", response.ExpiresIn)
	}

	meRecorder := httptest.NewRecorder()
	meRequest := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meRequest.Header.Set("Authorization", "Bearer "+response.AccessToken)
	router.ServeHTTP(meRecorder, meRequest)

	if meRecorder.Code != http.StatusOK {
		t.Fatalf("me status = %d, want 200; body=%s", meRecorder.Code, meRecorder.Body)
	}
	if strings.TrimSpace(meRecorder.Body.String()) !=
		`{"username":"`+testHTTPUsername+`"}` {
		t.Fatalf("me body = %q, want fixed username", meRecorder.Body.String())
	}
}

func TestLoginRejectsInvalidCredentials(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
	}{
		{name: "wrong username", username: "unknown", password: defaultHTTPTestPassword()},
		{name: "wrong password", username: testHTTPUsername, password: "wrong-password"},
		{name: "missing credentials"},
	}

	router := newAuthTestRouter(t)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(loginRequest{
				Username: test.username,
				Password: test.password,
			})
			if err != nil {
				t.Fatalf("marshal login request: %v", err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body)
			}
			if recorder.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("WWW-Authenticate header is missing")
			}
			responseBody := recorder.Body.String()
			if (test.username != "" && strings.Contains(responseBody, test.username)) ||
				(test.password != "" && strings.Contains(responseBody, test.password)) {
				t.Fatal("response exposes supplied credentials")
			}
		})
	}
}

func TestLoginRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
	}{
		{
			name:       "missing content type",
			body:       `{}`,
			wantStatus: http.StatusUnsupportedMediaType,
		},
		{
			name:        "malformed JSON",
			contentType: "application/json",
			body:        `{`,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "unknown field",
			contentType: "application/json",
			body:        `{"username":"test-user","password":"x","extra":true}`,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "multiple JSON values",
			contentType: "application/json",
			body:        `{} {}`,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "body too large",
			contentType: "application/json",
			body:        `{"username":"` + strings.Repeat("a", maxLoginRequestBodySize) + `"}`,
			wantStatus:  http.StatusRequestEntityTooLarge,
		},
	}

	router := newAuthTestRouter(t)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/auth/login",
				strings.NewReader(test.body),
			)
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					recorder.Code,
					test.wantStatus,
					recorder.Body,
				)
			}
		})
	}
}

func TestLoginHidesInternalAuthenticationErrors(t *testing.T) {
	internalError := errors.New("signing secret detail")
	tests := []struct {
		name    string
		service AuthService
	}{
		{
			name:    "service error",
			service: stubAuthService{authenticateErr: internalError},
		},
		{
			name:    "nil token",
			service: stubAuthService{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/auth/login",
				strings.NewReader(`{"username":"test-user","password":"value"}`),
			)
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()

			login(test.service).ServeHTTP(recorder, request)
			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", recorder.Code)
			}
			if strings.Contains(recorder.Body.String(), internalError.Error()) {
				t.Fatal("response exposes internal authentication error")
			}
		})
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
			request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
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

	for _, path := range []string{"/api/auth/me", "/api/missing"} {
		protectedRecorder := httptest.NewRecorder()
		router.ServeHTTP(
			protectedRecorder,
			httptest.NewRequest(http.MethodGet, path, nil),
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

func newAuthTestRouter(t *testing.T) http.Handler {
	t.Helper()

	router, err := NewRouter(Dependencies{
		Agent: newHTTPTestAgent(t),
		Auth:  newHTTPTestAuth(t),
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	return router
}

func signedHTTPTestToken(t *testing.T, expiresAt time.Time) string {
	t.Helper()

	claims := jwt.RegisteredClaims{
		Issuer:    "test-issuer",
		Subject:   testHTTPUsername,
		ExpiresAt: jwt.NewNumericDate(expiresAt),
		IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		ID:        "test-token-id",
	}
	value, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}
	return value
}

func defaultHTTPTestPassword() string {
	return "test-password"
}
