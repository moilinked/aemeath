package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ecol/chat-agent/internal/agent"
)

func chatStream(runner ChatRunner, conversations agent.ConversationStore, store *idempotencyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID, message, key, record, ok := prepareChat(w, r, conversations, store)
		if !ok {
			return
		}

		if record.Cached {
			writeCachedChatStream(w, record)
			return
		}

		flusher, canFlush := w.(http.Flusher)
		if !canFlush {
			store.Abort(key)
			writeAPIError(w, http.StatusInternalServerError, "streaming is not supported")
			return
		}

		committed := false
		defer func() {
			if !committed {
				store.Abort(key)
			}
		}()

		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		writeSSEHeaders(w)
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		result, err := runner.RunStream(r.Context(), conversationID, message, func(event agent.StreamEvent) error {
			if err := r.Context().Err(); err != nil {
				return err
			}
			return writeAgentStreamEvent(w, flusher, event)
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(r.Context().Err(), context.Canceled) {
				return
			}
			_, clientMessage := mapChatError(err)
			_ = writeSSEEvent(w, flusher, "error", map[string]string{"error": clientMessage})
			return
		}
		if result == nil || strings.TrimSpace(result.Message) == "" {
			_ = writeSSEEvent(w, flusher, "error", map[string]string{"error": "chat completion failed"})
			return
		}

		body, err := encodeJSON(chatResponse{ConversationID: conversationID, Message: result.Message})
		if err != nil {
			_ = writeSSEEvent(w, flusher, "error", map[string]string{"error": "chat completion failed"})
			return
		}
		if err := writeSSEEvent(w, flusher, "done", chatResponse{ConversationID: conversationID, Message: result.Message}); err != nil {
			return
		}
		store.Complete(key, http.StatusOK, body)
		committed = true
	}
}

func writeCachedChatStream(w http.ResponseWriter, record idempotencyRecord) {
	if record.StatusCode != http.StatusOK {
		writeJSONBytes(w, record.StatusCode, record.Body)
		return
	}

	var response chatResponse
	if err := json.Unmarshal(record.Body, &response); err != nil || strings.TrimSpace(response.Message) == "" {
		writeJSONBytes(w, record.StatusCode, record.Body)
		return
	}

	flusher, canFlush := w.(http.Flusher)
	if !canFlush {
		writeJSONBytes(w, record.StatusCode, record.Body)
		return
	}
	writeSSEHeaders(w)
	w.WriteHeader(http.StatusOK)
	_ = writeSSEEvent(w, flusher, "done", chatResponse{
		ConversationID: response.ConversationID,
		Message:        response.Message,
	})
}

func writeAgentStreamEvent(w http.ResponseWriter, flusher http.Flusher, event agent.StreamEvent) error {
	switch event.Type {
	case agent.StreamEventDelta:
		return writeSSEEvent(w, flusher, "delta", map[string]string{"content": event.Content})
	case agent.StreamEventReasoning:
		return writeSSEEvent(w, flusher, "reasoning", map[string]string{"content": event.Content})
	case agent.StreamEventToolCall:
		if event.ToolCall == nil {
			return nil
		}
		return writeSSEEvent(w, flusher, "tool_call", map[string]string{
			"id":        event.ToolCall.ID,
			"name":      event.ToolCall.Name,
			"arguments": event.ToolCall.Arguments,
		})
	case agent.StreamEventToolResult:
		if event.ToolCall == nil {
			return nil
		}
		return writeSSEEvent(w, flusher, "tool_result", map[string]string{
			"id":      event.ToolCall.ID,
			"name":    event.ToolCall.Name,
			"content": event.ToolCall.Content,
		})
	default:
		return nil
	}
}
