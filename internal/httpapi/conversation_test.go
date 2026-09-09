package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ecol/chat-agent/internal/agent"
	"github.com/ecol/chat-agent/internal/auth"
	"github.com/ecol/chat-agent/internal/conversation"
)

func TestOwnerID(t *testing.T) {
	tests := []struct {
		name     string
		identity auth.Identity
		want     string
	}{
		{
			name:     "uses user id",
			identity: auth.Identity{ID: "user-1", Username: "alice"},
			want:     "user-1",
		},
		{
			name:     "falls back to username",
			identity: auth.Identity{Username: "alice"},
			want:     "alice",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ownerID(test.identity); got != test.want {
				t.Fatalf("ownerID() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestParseConversationID(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "empty", value: "  ", want: ""},
		{name: "uuid", value: "conv-1", want: "conv-1"},
		{name: "invalid character", value: "conv/1", wantErr: true},
		{name: "too long", value: strings.Repeat("a", maxConversationIDLength+1), wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseConversationID(test.value)
			if test.wantErr {
				if err == nil {
					t.Fatal("parseConversationID() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseConversationID() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("parseConversationID() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestConversationTitle(t *testing.T) {
	got := conversationTitle("  hello   world  ")
	if got != "hello world" {
		t.Fatalf("conversationTitle() = %q, want compacted text", got)
	}
	long := strings.Repeat("字", maxConversationTitle+3)
	got = conversationTitle(long)
	if got != strings.Repeat("字", maxConversationTitle)+"…" {
		t.Fatalf("conversationTitle() long = %q", got)
	}
}

func TestConversationsListGetAndDelete(t *testing.T) {
	store := conversation.NewMemoryStore()
	router := newChatTestRouterWithStore(t, &stubChatRunner{result: &agent.Result{Message: "hello"}}, store)

	empty := httptest.NewRecorder()
	emptyRequest := httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	emptyRequest.Header.Set("Authorization", "Bearer "+signedHTTPTestToken(t, time.Now().Add(time.Hour)))
	router.ServeHTTP(empty, emptyRequest)
	if empty.Code != http.StatusOK {
		t.Fatalf("empty list status = %d, want 200; body=%s", empty.Code, empty.Body)
	}
	var listed conversationListResponse
	if err := json.NewDecoder(empty.Body).Decode(&listed); err != nil {
		t.Fatalf("decode empty list: %v", err)
	}
	if listed.Conversations == nil || len(listed.Conversations) != 0 {
		t.Fatalf("empty conversations = %#v, want []", listed.Conversations)
	}

	created := httptest.NewRecorder()
	router.ServeHTTP(created, authorizedJSONRequest(
		t,
		http.MethodPost,
		"/api/chat",
		`{"message":"hello world"}`,
	))
	if created.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200; body=%s", created.Code, created.Body)
	}
	var chat chatResponse
	if err := json.NewDecoder(created.Body).Decode(&chat); err != nil {
		t.Fatalf("decode chat: %v", err)
	}

	list := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	listRequest.Header.Set("Authorization", "Bearer "+signedHTTPTestToken(t, time.Now().Add(time.Hour)))
	router.ServeHTTP(list, listRequest)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", list.Code, list.Body)
	}
	if err := json.NewDecoder(list.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Conversations) != 1 || listed.Conversations[0].ID != chat.ConversationID {
		t.Fatalf("listed = %#v, want %q", listed.Conversations, chat.ConversationID)
	}
	if listed.Conversations[0].Title != "hello world" {
		t.Fatalf("title = %q, want hello world", listed.Conversations[0].Title)
	}

	detail := httptest.NewRecorder()
	detailRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/conversations/"+chat.ConversationID,
		nil,
	)
	detailRequest.Header.Set("Authorization", "Bearer "+signedHTTPTestToken(t, time.Now().Add(time.Hour)))
	router.ServeHTTP(detail, detailRequest)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200; body=%s", detail.Code, detail.Body)
	}

	deleted := httptest.NewRecorder()
	deleteRequest := httptest.NewRequest(
		http.MethodDelete,
		"/api/conversations/"+chat.ConversationID,
		nil,
	)
	deleteRequest.Header.Set("Authorization", "Bearer "+signedHTTPTestToken(t, time.Now().Add(time.Hour)))
	router.ServeHTTP(deleted, deleteRequest)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", deleted.Code, deleted.Body)
	}

	missing := httptest.NewRecorder()
	router.ServeHTTP(missing, detailRequest)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("deleted detail status = %d, want 404; body=%s", missing.Code, missing.Body)
	}
}

func TestConversationsHideForeignAndUnknownIDs(t *testing.T) {
	store := conversation.NewMemoryStore()
	foreign, err := store.Create(context.Background(), "other-user", "secret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := newChatTestRouterWithStore(t, &stubChatRunner{result: &agent.Result{Message: "unused"}}, store)

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "get unknown", method: http.MethodGet, path: "/api/conversations/missing-id"},
		{name: "get foreign", method: http.MethodGet, path: "/api/conversations/" + foreign.ID},
		{name: "delete unknown", method: http.MethodDelete, path: "/api/conversations/missing-id"},
		{name: "delete foreign", method: http.MethodDelete, path: "/api/conversations/" + foreign.ID},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Header.Set("Authorization", "Bearer "+signedHTTPTestToken(t, time.Now().Add(time.Hour)))
			router.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body)
			}
			if strings.Contains(recorder.Body.String(), "forbidden") {
				t.Fatal("response distinguishes foreign conversations")
			}
		})
	}
}

func TestConversationsRequireBearerToken(t *testing.T) {
	router := newChatTestRouter(t, &stubChatRunner{result: &agent.Result{Message: "unused"}})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/conversations", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body)
	}
}
