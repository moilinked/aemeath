package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/ecol/chat-agent/internal/auth"
)

const maxLoginRequestBodySize = 4 << 10

// AuthService 是 HTTP 认证边界依赖的最小能力。
type AuthService interface {
	Authenticate(
		ctx context.Context,
		username string,
		password string,
	) (*auth.AccessToken, error)
	Verify(ctx context.Context, tokenValue string) (auth.Identity, error)
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

func login(service AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isJSONContentType(r.Header.Get("Content-Type")) {
			writeAPIError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxLoginRequestBodySize)
		var request loginRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				writeAPIError(w, http.StatusRequestEntityTooLarge, "request body is too large")
				return
			}
			writeAPIError(w, http.StatusBadRequest, "invalid JSON request body")
			return
		}
		if err := ensureJSONBodyEnd(decoder); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid JSON request body")
			return
		}

		token, err := service.Authenticate(r.Context(), request.Username, request.Password)
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeUnauthorized(w, "invalid username or password")
			return
		}
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "authentication failed")
			return
		}
		if token == nil || token.Value == "" || !token.ExpiresAt.After(time.Now()) {
			writeAPIError(w, http.StatusInternalServerError, "authentication failed")
			return
		}

		expiresIn := int64(time.Until(token.ExpiresAt).Seconds())
		if expiresIn < 0 {
			expiresIn = 0
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		writeJSON(w, http.StatusOK, loginResponse{
			AccessToken: token.Value,
			TokenType:   "Bearer",
			ExpiresIn:   expiresIn,
		})
	}
}

func me(w http.ResponseWriter, r *http.Request) {
	identity, ok := identityFromContext(r.Context())
	if !ok {
		writeUnauthorized(w, "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": identity.Username})
}

func isJSONContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && strings.EqualFold(mediaType, "application/json")
}

func ensureJSONBodyEnd(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body contains multiple JSON values")
		}
		return err
	}
	return nil
}

func writeUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
	writeAPIError(w, http.StatusUnauthorized, message)
}

func writeAPIError(w http.ResponseWriter, statusCode int, message string) {
	writeJSON(w, statusCode, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, statusCode int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("write JSON response", "error", err)
	}
}
