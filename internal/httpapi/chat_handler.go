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
	ConversationID string `json:"conversation_id"`
	Message        string `json:"message"`
}

type chatResponse struct {
	ConversationID string `json:"conversation_id"`
	Message        string `json:"message"`
}

func chat(runner ChatRunner, conversations agent.ConversationStore, store *idempotencyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID, message, key, record, ok := prepareChat(w, r, conversations, store)
		if !ok {
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

		result, err := runner.Run(r.Context(), conversationID, message)
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

		body, err := encodeJSON(chatResponse{ConversationID: conversationID, Message: result.Message})
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "chat completion failed")
			return
		}
		store.Complete(key, http.StatusOK, body)
		committed = true
		writeJSONBytes(w, http.StatusOK, body)
	}
}

func prepareChat(
	w http.ResponseWriter,
	r *http.Request,
	conversations agent.ConversationStore,
	store *idempotencyStore,
) (conversationID string, message string, key string, record idempotencyRecord, ok bool) {
	var request chatRequest
	if !decodeJSONBody(w, r, maxChatRequestBodySize, &request) {
		return "", "", "", idempotencyRecord{}, false
	}

	message = strings.TrimSpace(request.Message)
	if message == "" {
		writeAPIError(w, http.StatusBadRequest, "message is required")
		return "", "", "", idempotencyRecord{}, false
	}

	identity, authenticated := identityFromContext(r.Context())
	if !authenticated || ownerID(identity) == "" {
		writeUnauthorized(w, "authentication required")
		return "", "", "", idempotencyRecord{}, false
	}

	requestedID, err := parseConversationID(request.ConversationID)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "conversation_id is invalid")
		return "", "", "", idempotencyRecord{}, false
	}

	userID := ownerID(identity)
	if requestedID != "" {
		if _, _, err := conversations.GetForUser(r.Context(), userID, requestedID); err != nil {
			writeConversationError(w, err)
			return "", "", "", idempotencyRecord{}, false
		}
	}

	idempotencyKey, err := parseIdempotencyKey(r.Header.Get(idempotencyHeader))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return "", "", "", idempotencyRecord{}, false
	}

	key = scopedIdempotencyKey(identity.Username, idempotencyKey)
	record, err = store.Begin(key, chatPayloadHash(requestedID, message))
	if errors.Is(err, errIdempotencyInProgress) {
		writeAPIError(w, http.StatusConflict, "chat request is already in progress")
		return "", "", "", idempotencyRecord{}, false
	}
	if errors.Is(err, errIdempotencyPayloadMismatch) {
		writeAPIError(w, http.StatusConflict, "Idempotency-Key already used with a different request")
		return "", "", "", idempotencyRecord{}, false
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "chat completion failed")
		return "", "", "", idempotencyRecord{}, false
	}
	if record.Cached {
		return requestedID, message, key, record, true
	}

	conversationID = requestedID
	if conversationID == "" {
		created, err := conversations.Create(r.Context(), userID, conversationTitle(message))
		if err != nil {
			store.Abort(key)
			writeConversationError(w, err)
			return "", "", "", idempotencyRecord{}, false
		}
		conversationID = created.ID
	}
	return conversationID, message, key, record, true
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
	case errors.Is(err, agent.ErrConversationNotFound):
		return http.StatusNotFound, "conversation not found"
	case errors.Is(err, agent.ErrConversationIDRequired):
		return http.StatusBadRequest, "conversation_id is required"
	case errors.Is(err, agent.ErrUserMessageRequired):
		return http.StatusBadRequest, "message is required"
	case errors.Is(err, agent.ErrMaxStepsExceeded):
		return http.StatusGatewayTimeout, "agent exceeded maximum execution steps"
	case errors.Is(err, agent.ErrContextBudgetExceeded):
		return http.StatusBadRequest, "chat context exceeds token budget"
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
