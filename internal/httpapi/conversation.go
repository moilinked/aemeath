package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ecol/chat-agent/internal/agent"
	"github.com/ecol/chat-agent/internal/auth"
	"github.com/ecol/chat-agent/internal/llm"
	"github.com/go-chi/chi/v5"
)

const (
	maxConversationIDLength      = 128
	maxConversationTitle         = 40
	maxConversationTitleBodySize = 4 << 10
)

var (
	errInvalidConversationID = errors.New("conversation_id is invalid")
	errTitleRequired         = errors.New("title is required")
	errTitleTooLong          = errors.New("title is too long")
)

type patchConversationRequest struct {
	Title string `json:"title"`
}

type conversationSummaryResponse struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type conversationListResponse struct {
	Conversations []conversationSummaryResponse `json:"conversations"`
}

type conversationDetailResponse struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Messages  []llm.Message `json:"messages"`
}

func ownerID(identity auth.Identity) string {
	if id := strings.TrimSpace(identity.ID); id != "" {
		return id
	}
	return strings.TrimSpace(identity.Username)
}

func parseConversationID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > maxConversationIDLength {
		return "", errInvalidConversationID
	}
	for _, character := range value {
		if unicode.IsLetter(character) ||
			unicode.IsDigit(character) ||
			character == '_' ||
			character == '-' ||
			character == '.' {
			continue
		}
		return "", errInvalidConversationID
	}
	return value, nil
}

func compactTitle(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func conversationTitle(message string) string {
	message = compactTitle(message)
	if message == "" {
		return "新对话"
	}
	if utf8.RuneCountInString(message) <= maxConversationTitle {
		return message
	}
	runes := []rune(message)
	return string(runes[:maxConversationTitle]) + "…"
}

func parseConversationTitle(value string) (string, error) {
	value = compactTitle(value)
	if value == "" {
		return "", errTitleRequired
	}
	if utf8.RuneCountInString(value) > maxConversationTitle {
		return "", errTitleTooLong
	}
	return value, nil
}

func summaryResponse(item agent.Conversation) conversationSummaryResponse {
	return conversationSummaryResponse{
		ID:        item.ID,
		Title:     item.Title,
		CreatedAt: item.CreatedAt.UTC(),
		UpdatedAt: item.UpdatedAt.UTC(),
	}
}

func listConversations(store agent.ConversationStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := identityFromContext(r.Context())
		if !ok || ownerID(identity) == "" {
			writeUnauthorized(w, "authentication required")
			return
		}

		items, err := store.ListForUser(r.Context(), ownerID(identity))
		if err != nil {
			writeConversationError(w, err)
			return
		}
		if items == nil {
			items = []agent.Conversation{}
		}

		summaries := make([]conversationSummaryResponse, 0, len(items))
		for _, item := range items {
			summaries = append(summaries, summaryResponse(item))
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, conversationListResponse{Conversations: summaries})
	}
}

func getConversation(store agent.ConversationStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := identityFromContext(r.Context())
		if !ok || ownerID(identity) == "" {
			writeUnauthorized(w, "authentication required")
			return
		}

		conversationID, err := parseConversationID(chi.URLParam(r, "conversationID"))
		if err != nil || conversationID == "" {
			writeAPIError(w, http.StatusBadRequest, "conversation_id is invalid")
			return
		}

		item, messages, err := store.GetForUser(r.Context(), ownerID(identity), conversationID)
		if err != nil {
			writeConversationError(w, err)
			return
		}
		if messages == nil {
			messages = []llm.Message{}
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, conversationDetailResponse{
			ID:        item.ID,
			Title:     item.Title,
			CreatedAt: item.CreatedAt.UTC(),
			UpdatedAt: item.UpdatedAt.UTC(),
			Messages:  messages,
		})
	}
}

func patchConversation(store agent.ConversationStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := identityFromContext(r.Context())
		if !ok || ownerID(identity) == "" {
			writeUnauthorized(w, "authentication required")
			return
		}

		conversationID, err := parseConversationID(chi.URLParam(r, "conversationID"))
		if err != nil || conversationID == "" {
			writeAPIError(w, http.StatusBadRequest, "conversation_id is invalid")
			return
		}

		var request patchConversationRequest
		if !decodeJSONBody(w, r, maxConversationTitleBodySize, &request) {
			return
		}
		title, err := parseConversationTitle(request.Title)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}

		item, err := store.UpdateTitleForUser(r.Context(), ownerID(identity), conversationID, title)
		if err != nil {
			writeConversationError(w, err)
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, summaryResponse(item))
	}
}

func deleteConversation(store agent.ConversationStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := identityFromContext(r.Context())
		if !ok || ownerID(identity) == "" {
			writeUnauthorized(w, "authentication required")
			return
		}

		conversationID, err := parseConversationID(chi.URLParam(r, "conversationID"))
		if err != nil || conversationID == "" {
			writeAPIError(w, http.StatusBadRequest, "conversation_id is invalid")
			return
		}

		if err := store.DeleteForUser(r.Context(), ownerID(identity), conversationID); err != nil {
			writeConversationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func writeConversationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		writeAPIError(w, http.StatusRequestTimeout, "chat request canceled")
	case errors.Is(err, agent.ErrConversationNotFound):
		writeAPIError(w, http.StatusNotFound, "conversation not found")
	case errors.Is(err, errInvalidConversationID):
		writeAPIError(w, http.StatusBadRequest, "conversation_id is invalid")
	default:
		writeAPIError(w, http.StatusInternalServerError, "conversation request failed")
	}
}
