package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ecol/chat-agent/internal/agent"
	"github.com/ecol/chat-agent/internal/auth"
	"github.com/ecol/chat-agent/internal/conversation"
	"github.com/ecol/chat-agent/internal/llm"
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

func TestParseConversationTitle(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr error
	}{
		{name: "compacts whitespace", value: "  hello   world  ", want: "hello world"},
		{name: "empty", value: "   ", wantErr: errTitleRequired},
		{name: "too long", value: strings.Repeat("字", maxConversationTitle+1), wantErr: errTitleTooLong},
		{name: "max length", value: strings.Repeat("字", maxConversationTitle), want: strings.Repeat("字", maxConversationTitle)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseConversationTitle(test.value)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("parseConversationTitle() error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseConversationTitle() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("parseConversationTitle() = %q, want %q", got, test.want)
			}
		})
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

	renamed := httptest.NewRecorder()
	router.ServeHTTP(renamed, authorizedJSONRequest(
		t,
		http.MethodPatch,
		"/api/conversations/"+chat.ConversationID,
		`{"title":"  new   name  "}`,
	))
	if renamed.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", renamed.Code, renamed.Body)
	}
	var summary conversationSummaryResponse
	if err := json.NewDecoder(renamed.Body).Decode(&summary); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if summary.ID != chat.ConversationID || summary.Title != "new name" {
		t.Fatalf("patched = %#v, want id %q title new name", summary, chat.ConversationID)
	}
	if !summary.UpdatedAt.After(listed.Conversations[0].UpdatedAt) {
		t.Fatalf("patched updated_at = %v, want after %v", summary.UpdatedAt, listed.Conversations[0].UpdatedAt)
	}

	list = httptest.NewRecorder()
	listRequest = httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	listRequest.Header.Set("Authorization", "Bearer "+signedHTTPTestToken(t, time.Now().Add(time.Hour)))
	router.ServeHTTP(list, listRequest)
	if list.Code != http.StatusOK {
		t.Fatalf("list after patch status = %d, want 200; body=%s", list.Code, list.Body)
	}
	if err := json.NewDecoder(list.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list after patch: %v", err)
	}
	if len(listed.Conversations) != 1 || listed.Conversations[0].Title != "new name" {
		t.Fatalf("listed after patch = %#v", listed.Conversations)
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
		body   string
	}{
		{name: "get unknown", method: http.MethodGet, path: "/api/conversations/missing-id"},
		{name: "get foreign", method: http.MethodGet, path: "/api/conversations/" + foreign.ID},
		{name: "patch unknown", method: http.MethodPatch, path: "/api/conversations/missing-id", body: `{"title":"nope"}`},
		{name: "patch foreign", method: http.MethodPatch, path: "/api/conversations/" + foreign.ID, body: `{"title":"nope"}`},
		{name: "clear unknown", method: http.MethodDelete, path: "/api/conversations/missing-id/messages"},
		{name: "clear foreign", method: http.MethodDelete, path: "/api/conversations/" + foreign.ID + "/messages"},
		{name: "delete unknown", method: http.MethodDelete, path: "/api/conversations/missing-id"},
		{name: "delete foreign", method: http.MethodDelete, path: "/api/conversations/" + foreign.ID},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			var request *http.Request
			if test.body != "" {
				request = authorizedJSONRequest(t, test.method, test.path, test.body)
			} else {
				request = httptest.NewRequest(test.method, test.path, nil)
				request.Header.Set("Authorization", "Bearer "+signedHTTPTestToken(t, time.Now().Add(time.Hour)))
			}
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

func TestConversationsRejectInvalidTitleUpdates(t *testing.T) {
	store := conversation.NewMemoryStore()
	router := newChatTestRouterWithStore(t, &stubChatRunner{result: &agent.Result{Message: "hello"}}, store)

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

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "empty", body: `{"title":"   "}`, want: "title is required"},
		{name: "missing", body: `{}`, want: "title is required"},
		{name: "too long", body: `{"title":"` + strings.Repeat("字", maxConversationTitle+1) + `"}`, want: "title is too long"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, authorizedJSONRequest(
				t,
				http.MethodPatch,
				"/api/conversations/"+chat.ConversationID,
				test.body,
			))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body)
			}
			if !strings.Contains(recorder.Body.String(), test.want) {
				t.Fatalf("body = %s, want %q", recorder.Body.String(), test.want)
			}
		})
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
	var got conversationDetailResponse
	if err := json.NewDecoder(detail.Body).Decode(&got); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if got.Title != "hello world" {
		t.Fatalf("title after rejected patch = %q, want original", got.Title)
	}
}

func TestConversationsClearMessages(t *testing.T) {
	store := conversation.NewMemoryStore()
	router := newChatTestRouterWithStore(t, &stubChatRunner{result: &agent.Result{Message: "hello"}}, store)

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
	if err := store.Append(
		context.Background(),
		chat.ConversationID,
		llm.Message{Role: llm.RoleUser, Content: "hello world"},
		llm.Message{Role: llm.RoleAssistant, Content: "hello"},
	); err != nil {
		t.Fatalf("Append() error = %v", err)
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
	var before conversationDetailResponse
	if err := json.NewDecoder(detail.Body).Decode(&before); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if len(before.Messages) == 0 {
		t.Fatal("detail messages are empty before clear")
	}

	cleared := httptest.NewRecorder()
	clearRequest := httptest.NewRequest(
		http.MethodDelete,
		"/api/conversations/"+chat.ConversationID+"/messages",
		nil,
	)
	clearRequest.Header.Set("Authorization", "Bearer "+signedHTTPTestToken(t, time.Now().Add(time.Hour)))
	router.ServeHTTP(cleared, clearRequest)
	if cleared.Code != http.StatusOK {
		t.Fatalf("clear status = %d, want 200; body=%s", cleared.Code, cleared.Body)
	}
	var after conversationDetailResponse
	if err := json.NewDecoder(cleared.Body).Decode(&after); err != nil {
		t.Fatalf("decode clear: %v", err)
	}
	if after.ID != chat.ConversationID || after.Title != "hello world" {
		t.Fatalf("cleared = %#v, want id %q title hello world", after, chat.ConversationID)
	}
	if after.Messages == nil || len(after.Messages) != 0 {
		t.Fatalf("cleared messages = %#v, want []", after.Messages)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("cleared updated_at = %v, want after %v", after.UpdatedAt, before.UpdatedAt)
	}

	again := httptest.NewRecorder()
	router.ServeHTTP(again, clearRequest)
	if again.Code != http.StatusOK {
		t.Fatalf("second clear status = %d, want 200; body=%s", again.Code, again.Body)
	}

	history, err := store.Load(context.Background(), chat.ConversationID)
	if err != nil {
		t.Fatalf("Load() after clear error = %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("Load() after clear = %#v, want empty", history)
	}

	listed := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	listRequest.Header.Set("Authorization", "Bearer "+signedHTTPTestToken(t, time.Now().Add(time.Hour)))
	router.ServeHTTP(listed, listRequest)
	if listed.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listed.Code, listed.Body)
	}
	var list conversationListResponse
	if err := json.NewDecoder(listed.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Conversations) != 1 || list.Conversations[0].ID != chat.ConversationID {
		t.Fatalf("listed after clear = %#v", list.Conversations)
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
