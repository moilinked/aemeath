package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ecol/chat-agent/internal/agent"
)

const maxChatRequestBodySize = 64 << 10

type chatRequest struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

type chatResponse struct {
	Message string `json:"message"`
}

func chat(runner ChatRunner, store *idempotencyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request chatRequest
		if !decodeJSONBody(w, r, maxChatRequestBodySize, &request) {
			return
		}

		sessionID := strings.TrimSpace(request.SessionID)
		message := strings.TrimSpace(request.Message)
		if sessionID == "" {
			writeAPIError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		if message == "" {
			writeAPIError(w, http.StatusBadRequest, "message is required")
			return
		}

		identity, ok := identityFromContext(r.Context())
		if !ok {
			writeUnauthorized(w, "authentication required")
			return
		}
		idempotencyKey, err := parseIdempotencyKey(r.Header.Get(idempotencyHeader))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}

		key := scopedIdempotencyKey(identity.Username, idempotencyKey)
		record, err := store.Begin(key, chatPayloadHash(sessionID, message))
		if errors.Is(err, errIdempotencyInProgress) {
			writeAPIError(w, http.StatusConflict, "chat request is already in progress")
			return
		}
		if errors.Is(err, errIdempotencyPayloadMismatch) {
			writeAPIError(w, http.StatusConflict, "Idempotency-Key already used with a different request")
			return
		}
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "chat completion failed")
			return
		}
		if record.Cached {
			writeJSONBytes(w, record.StatusCode, record.Body)
			return
		}

		committed := false
		defer func() {
			if !committed {
				store.Abort(key)
			}
		}()

		result, err := runner.Run(r.Context(), sessionID, message)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				writeAPIError(w, http.StatusRequestTimeout, "chat request canceled")
				return
			}
			status, clientMessage := mapChatError(err)
			writeCachedChatError(w, store, key, &committed, status, clientMessage)
			return
		}
		if result == nil || strings.TrimSpace(result.Message) == "" {
			writeCachedChatError(
				w,
				store,
				key,
				&committed,
				http.StatusInternalServerError,
				"chat completion failed",
			)
			return
		}

		body, err := encodeJSON(chatResponse{Message: result.Message})
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "chat completion failed")
			return
		}
		store.Complete(key, http.StatusOK, body)
		committed = true
		writeJSONBytes(w, http.StatusOK, body)
	}
}

func writeCachedChatError(
	w http.ResponseWriter,
	store *idempotencyStore,
	key string,
	committed *bool,
	statusCode int,
	message string,
) {
	body, err := encodeJSON(map[string]string{"error": message})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "chat completion failed")
		return
	}
	store.Complete(key, statusCode, body)
	*committed = true
	writeJSONBytes(w, statusCode, body)
}

func mapChatError(err error) (int, string) {
	switch {
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, "chat request canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "chat request timed out"
	case errors.Is(err, agent.ErrSessionIDRequired):
		return http.StatusBadRequest, "session_id is required"
	case errors.Is(err, agent.ErrUserMessageRequired):
		return http.StatusBadRequest, "message is required"
	case errors.Is(err, agent.ErrMaxStepsExceeded):
		return http.StatusGatewayTimeout, "agent exceeded maximum execution steps"
	case errors.Is(err, agent.ErrInvalidLLMResponse), errors.Is(err, agent.ErrInvalidToolCall):
		return http.StatusBadGateway, "chat completion failed"
	default:
		return http.StatusBadGateway, "chat completion failed"
	}
}

func encodeJSON(value any) ([]byte, error) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(value); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}

func writeJSONBytes(w http.ResponseWriter, statusCode int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if _, err := w.Write(body); err != nil {
		return
	}
}
