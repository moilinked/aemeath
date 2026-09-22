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

	"github.com/ecol/chat-agent/internal/auth"
)

// AuthService 是 HTTP 认证边界依赖的最小能力。
type AuthService interface {
	Verify(ctx context.Context, tokenValue string) (auth.Identity, error)
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, maxBytes int64, dest any) bool {
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		writeAPIError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request body is too large")
			return false
		}
		writeAPIError(w, http.StatusBadRequest, "invalid JSON request body")
		return false
	}
	if err := ensureJSONBodyEnd(decoder); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid JSON request body")
		return false
	}
	return true
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

func writeForbidden(w http.ResponseWriter, message string) {
	writeAPIError(w, http.StatusForbidden, message)
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
