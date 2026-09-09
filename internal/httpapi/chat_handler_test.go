package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ecol/chat-agent/internal/agent"
	"github.com/ecol/chat-agent/internal/auth"
	"github.com/ecol/chat-agent/internal/conversation"
	"github.com/ecol/chat-agent/internal/llm"
	"github.com/ecol/chat-agent/internal/tools"
)

type stubChatRunner struct {
	result         *agent.Result
	err            error
	conversationID string
	message        string
}

type countingChatRunner struct {
	mu     sync.Mutex
	count  int
	result *agent.Result
	err    error
}

func (runner *countingChatRunner) Run(
	ctx context.Context,
	_ string,
	_ string,
) (*agent.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runner.mu.Lock()
	runner.count++
	runner.mu.Unlock()
	return runner.result, runner.err
}

func (runner *countingChatRunner) RunStream(
	ctx context.Context,
	conversationID string,
	message string,
	_ agent.StreamHandler,
) (*agent.Result, error) {
	return runner.Run(ctx, conversationID, message)
}

func (runner *countingChatRunner) calls() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.count
}

type blockingChatRunner struct {
	started chan struct{}
	unblock chan struct{}
	count   atomic.Int32
	result  *agent.Result
}

func newBlockingChatRunner(result *agent.Result) *blockingChatRunner {
	return &blockingChatRunner{
		started: make(chan struct{}),
		unblock: make(chan struct{}),
		result:  result,
	}
}

func (runner *blockingChatRunner) Run(
	ctx context.Context,
	_ string,
	_ string,
) (*agent.Result, error) {
	runner.count.Add(1)
	select {
	case <-runner.started:
	default:
		close(runner.started)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-runner.unblock:
		return runner.result, nil
	}
}

func (runner *blockingChatRunner) RunStream(
	ctx context.Context,
	conversationID string,
	message string,
	_ agent.StreamHandler,
) (*agent.Result, error) {
	return runner.Run(ctx, conversationID, message)
}

func (runner *blockingChatRunner) waitUntilStarted(t *testing.T) {
	t.Helper()
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for chat runner to start")
	}
}

func (runner *blockingChatRunner) finish() {
	close(runner.unblock)
}

func (runner *blockingChatRunner) calls() int {
	return int(runner.count.Load())
}

func (runner *stubChatRunner) Run(
	ctx context.Context,
	conversationID string,
	message string,
) (*agent.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runner.conversationID = conversationID
	runner.message = message
	return runner.result, runner.err
}

type scriptedHTTPLLMClient struct {
	responses []*llm.ChatResponse
	errs      []error
	requests  []llm.ChatRequest
}

func (runner *stubChatRunner) RunStream(
	ctx context.Context,
	conversationID string,
	message string,
	emit agent.StreamHandler,
) (*agent.Result, error) {
	result, err := runner.Run(ctx, conversationID, message)
	if err != nil || result == nil || emit == nil {
		return result, err
	}
	if result.Message != "" {
		if err := emit(agent.StreamEvent{Type: agent.StreamEventDelta, Content: result.Message}); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (client *scriptedHTTPLLMClient) Chat(
	_ context.Context,
	request llm.ChatRequest,
) (*llm.ChatResponse, error) {
	client.requests = append(client.requests, request)
	index := len(client.requests) - 1
	if index < len(client.errs) && client.errs[index] != nil {
		return nil, client.errs[index]
	}
	if index >= len(client.responses) {
		return nil, errors.New("unexpected LLM request")
	}
	return client.responses[index], nil
}

func (client *scriptedHTTPLLMClient) ChatStream(
	ctx context.Context,
	request llm.ChatRequest,
	emit llm.StreamHandler,
) (*llm.ChatResponse, error) {
	return llm.ChatStreamFromChat(ctx, client.Chat, request, emit)
}

func TestChatReturnsFinalAnswer(t *testing.T) {
	llmClient := &scriptedHTTPLLMClient{
		responses: []*llm.ChatResponse{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "128 × 39 = 4992"}},
		},
	}
	store := conversation.NewMemoryStore()
	router := newChatTestRouterWithStore(t, newHTTPTestAgentWithStore(t, llmClient, store, 2), store)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedJSONRequest(
		t,
		http.MethodPost,
		"/api/chat",
		`{"message":"帮我计算 128 * 39"}`,
	))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body)
	}
	var response chatResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode chat response: %v", err)
	}
	if response.ConversationID == "" {
		t.Fatal("conversation_id is empty")
	}
	if response.Message != "128 × 39 = 4992" {
		t.Fatalf("message = %q, want calculator result", response.Message)
	}
	if len(llmClient.requests) != 1 {
		t.Fatalf("LLM request count = %d, want 1", len(llmClient.requests))
	}
}

func TestChatContinuesExplicitConversation(t *testing.T) {
	llmClient := &scriptedHTTPLLMClient{
		responses: []*llm.ChatResponse{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "first-reply"}},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "second-reply"}},
		},
	}
	store := conversation.NewMemoryStore()
	chatAgent := newHTTPTestAgentWithStore(t, llmClient, store, 2)
	router := newChatTestRouterWithStore(t, chatAgent, store)

	first := httptest.NewRecorder()
	router.ServeHTTP(first, authorizedJSONRequest(
		t,
		http.MethodPost,
		"/api/chat",
		`{"message":"hi"}`,
	))
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200; body=%s", first.Code, first.Body)
	}

	var firstResponse chatResponse
	if err := json.NewDecoder(first.Body).Decode(&firstResponse); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if firstResponse.ConversationID == "" {
		t.Fatal("first conversation_id is empty")
	}

	second := httptest.NewRecorder()
	router.ServeHTTP(second, authorizedJSONRequestWithKey(
		t,
		http.MethodPost,
		"/api/chat",
		`{"conversation_id":"`+firstResponse.ConversationID+`","message":"again"}`,
		"chat-test-key-2",
	))
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200; body=%s", second.Code, second.Body)
	}
	if len(llmClient.requests) != 2 {
		t.Fatalf("LLM request count = %d, want 2", len(llmClient.requests))
	}

	var sawFirstUser, sawFirstReply bool
	for _, message := range llmClient.requests[1].Messages {
		if message.Role == llm.RoleUser && message.Content == "hi" {
			sawFirstUser = true
		}
		if message.Role == llm.RoleAssistant && message.Content == "first-reply" {
			sawFirstReply = true
		}
	}
	if !sawFirstUser || !sawFirstReply {
		t.Fatalf("second LLM request missing prior turn: %#v", llmClient.requests[1].Messages)
	}

	detail := httptest.NewRecorder()
	detailRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/conversations/"+firstResponse.ConversationID,
		nil,
	)
	detailRequest.Header.Set(
		"Authorization",
		"Bearer "+signedHTTPTestToken(t, time.Now().Add(time.Hour)),
	)
	router.ServeHTTP(detail, detailRequest)
	if detail.Code != http.StatusOK {
		t.Fatalf("get conversation status = %d, want 200; body=%s", detail.Code, detail.Body)
	}

	var current conversationDetailResponse
	if err := json.NewDecoder(detail.Body).Decode(&current); err != nil {
		t.Fatalf("decode conversation: %v", err)
	}
	if current.ID != firstResponse.ConversationID {
		t.Fatalf("conversation id = %q, want %q", current.ID, firstResponse.ConversationID)
	}
	if len(current.Messages) != 4 {
		t.Fatalf("conversation messages = %d, want 4", len(current.Messages))
	}
}

func TestChatOmittingIDStartsNewConversation(t *testing.T) {
	store := conversation.NewMemoryStore()
	runner := &stubChatRunner{result: &agent.Result{Message: "hello"}}
	router := newChatTestRouterWithStore(t, runner, store)

	first := httptest.NewRecorder()
	router.ServeHTTP(first, authorizedJSONRequest(
		t,
		http.MethodPost,
		"/api/chat",
		`{"message":"hi"}`,
	))
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200; body=%s", first.Code, first.Body)
	}

	var firstResponse chatResponse
	if err := json.NewDecoder(first.Body).Decode(&firstResponse); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if firstResponse.ConversationID == "" {
		t.Fatal("first conversation_id is empty")
	}

	second := httptest.NewRecorder()
	router.ServeHTTP(second, authorizedJSONRequestWithKey(
		t,
		http.MethodPost,
		"/api/chat",
		`{"message":"again"}`,
		"chat-test-key-2",
	))
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200; body=%s", second.Code, second.Body)
	}
	if runner.conversationID == firstResponse.ConversationID {
		t.Fatal("omitted conversation_id reused the previous conversation")
	}

	list := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	listRequest.Header.Set(
		"Authorization",
		"Bearer "+signedHTTPTestToken(t, time.Now().Add(time.Hour)),
	)
	router.ServeHTTP(list, listRequest)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", list.Code, list.Body)
	}
	var listed conversationListResponse
	if err := json.NewDecoder(list.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Conversations) != 2 {
		t.Fatalf("conversations = %d, want 2", len(listed.Conversations))
	}
}

func TestChatRejectsUnknownConversation(t *testing.T) {
	router := newChatTestRouter(t, &stubChatRunner{result: &agent.Result{Message: "unused"}})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedJSONRequest(
		t,
		http.MethodPost,
		"/api/chat",
		`{"conversation_id":"missing-id","message":"hello"}`,
	))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body)
	}
}

func TestChatRejectsForeignConversation(t *testing.T) {
	store := conversation.NewMemoryStore()
	foreign, err := store.Create(context.Background(), "other-user", "secret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	runner := &stubChatRunner{result: &agent.Result{Message: "unused"}}
	router := newChatTestRouterWithStore(t, runner, store)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedJSONRequest(
		t,
		http.MethodPost,
		"/api/chat",
		`{"conversation_id":"`+foreign.ID+`","message":"hello"}`,
	))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body)
	}
	if runner.conversationID != "" {
		t.Fatal("foreign conversation reached the agent")
	}
}

func TestChatRequiresBearerToken(t *testing.T) {
	router := newChatTestRouter(t, newHTTPTestAgent(t))
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/chat",
		strings.NewReader(`{"message":"hello"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body)
	}
	if recorder.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("WWW-Authenticate header is missing")
	}
}

func TestChatRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
		wantError   string
	}{
		{
			name:       "missing content type",
			body:       `{"message":"hello"}`,
			wantStatus: http.StatusUnsupportedMediaType,
		},
		{
			name:        "malformed JSON",
			contentType: "application/json",
			body:        `{`,
			wantStatus:  http.StatusBadRequest,
			wantError:   "invalid JSON request body",
		},
		{
			name:        "unknown field",
			contentType: "application/json",
			body:        `{"message":"hello","extra":true}`,
			wantStatus:  http.StatusBadRequest,
			wantError:   "invalid JSON request body",
		},
		{
			name:        "invalid conversation id",
			contentType: "application/json",
			body:        `{"conversation_id":"bad/id","message":"hello"}`,
			wantStatus:  http.StatusBadRequest,
			wantError:   "conversation_id is invalid",
		},
		{
			name:        "missing message",
			contentType: "application/json",
			body:        `{"conversation_id":"conv-1"}`,
			wantStatus:  http.StatusBadRequest,
			wantError:   "message is required",
		},
		{
			name:        "body too large",
			contentType: "application/json",
			body:        `{"message":"` + strings.Repeat("a", maxChatRequestBodySize) + `"}`,
			wantStatus:  http.StatusRequestEntityTooLarge,
		},
	}

	router := newChatTestRouter(t, &stubChatRunner{
		result: &agent.Result{Message: "unused"},
	})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(test.body))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			request.Header.Set(
				"Authorization",
				"Bearer "+signedHTTPTestToken(t, time.Now().Add(time.Hour)),
			)
			request.Header.Set(idempotencyHeader, "chat-test-key")
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
			if test.wantError != "" && !strings.Contains(recorder.Body.String(), test.wantError) {
				t.Fatalf("body = %q, want error %q", recorder.Body.String(), test.wantError)
			}
		})
	}
}

func TestChatMapsAgentErrors(t *testing.T) {
	internalError := errors.New("llm upstream secret")
	tests := []struct {
		name       string
		result     *agent.Result
		err        error
		wantStatus int
		wantError  string
	}{
		{
			name:       "max steps exceeded",
			err:        agent.ErrMaxStepsExceeded,
			wantStatus: http.StatusGatewayTimeout,
			wantError:  "agent exceeded maximum execution steps",
		},
		{
			name:       "context budget exceeded",
			err:        agent.ErrContextBudgetExceeded,
			wantStatus: http.StatusBadRequest,
			wantError:  "chat context exceeds token budget",
		},
		{
			name:       "invalid LLM response",
			err:        agent.ErrInvalidLLMResponse,
			wantStatus: http.StatusBadGateway,
			wantError:  "chat completion failed",
		},
		{
			name:       "upstream error",
			err:        internalError,
			wantStatus: http.StatusBadGateway,
			wantError:  "chat completion failed",
		},
		{
			name:       "empty result",
			result:     &agent.Result{},
			wantStatus: http.StatusInternalServerError,
			wantError:  "chat completion failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &stubChatRunner{result: test.result, err: test.err}
			router := newChatTestRouter(t, runner)
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, authorizedJSONRequest(
				t,
				http.MethodPost,
				"/api/chat",
				`{"message":"hello"}`,
			))

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body)
			}
			if !strings.Contains(recorder.Body.String(), test.wantError) {
				t.Fatalf("body = %q, want error %q", recorder.Body.String(), test.wantError)
			}
			if strings.Contains(recorder.Body.String(), internalError.Error()) {
				t.Fatal("response exposes internal chat error")
			}
			if runner.conversationID == "" || runner.message != "hello" {
				t.Fatalf("Run() args = (%q, %q)", runner.conversationID, runner.message)
			}
		})
	}
}

func TestChatHonorsCanceledContext(t *testing.T) {
	store := newIdempotencyStore(time.Hour)
	runner := &stubChatRunner{result: &agent.Result{Message: "unused"}}
	handler := chat(runner, conversation.NewMemoryStore(), store)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx = context.WithValue(ctx, identityContextKey{}, auth.Identity{Username: testHTTPUsername})

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/chat",
		strings.NewReader(`{"message":"hello"}`),
	).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(idempotencyHeader, "canceled-key")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusRequestTimeout {
		t.Fatalf("status = %d, want 408; body=%s", recorder.Code, recorder.Body)
	}

	retry := httptest.NewRequest(
		http.MethodPost,
		"/api/chat",
		strings.NewReader(`{"message":"hello"}`),
	).WithContext(context.WithValue(
		context.Background(),
		identityContextKey{},
		auth.Identity{Username: testHTTPUsername},
	))
	retry.Header.Set("Content-Type", "application/json")
	retry.Header.Set(idempotencyHeader, "canceled-key")
	retryRecorder := httptest.NewRecorder()
	handler.ServeHTTP(retryRecorder, retry)
	if retryRecorder.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200; body=%s", retryRecorder.Code, retryRecorder.Body)
	}
}

func TestChatRejectsInvalidIdempotencyKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "missing"},
		{name: "blank", key: " "},
		{name: "invalid character", key: "key/1"},
		{name: "too long", key: strings.Repeat("a", maxIdempotencyKeyLength+1)},
	}

	router := newChatTestRouter(t, &stubChatRunner{result: &agent.Result{Message: "unused"}})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := authorizedJSONRequestWithKey(
				t,
				http.MethodPost,
				"/api/chat",
				`{"message":"hello"}`,
				test.key,
			)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body)
			}
		})
	}
}

func TestChatReplaysIdempotentSuccess(t *testing.T) {
	runner := &countingChatRunner{result: &agent.Result{Message: "cached-answer"}}
	router := newChatTestRouter(t, runner)
	body := `{"message":"hello"}`

	first := httptest.NewRecorder()
	router.ServeHTTP(first, authorizedJSONRequestWithKey(
		t, http.MethodPost, "/api/chat", body, "same-chat-key",
	))
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200; body=%s", first.Code, first.Body)
	}

	second := httptest.NewRecorder()
	router.ServeHTTP(second, authorizedJSONRequestWithKey(
		t, http.MethodPost, "/api/chat", body, "same-chat-key",
	))
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200; body=%s", second.Code, second.Body)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replay body = %q, want %q", second.Body.String(), first.Body.String())
	}
	if runner.calls() != 1 {
		t.Fatalf("Run() calls = %d, want 1", runner.calls())
	}
}

func TestChatReplaysIdempotentAgentError(t *testing.T) {
	runner := &countingChatRunner{err: agent.ErrInvalidLLMResponse}
	router := newChatTestRouter(t, runner)
	body := `{"message":"hello"}`

	first := httptest.NewRecorder()
	router.ServeHTTP(first, authorizedJSONRequestWithKey(
		t, http.MethodPost, "/api/chat", body, "error-chat-key",
	))
	if first.Code != http.StatusBadGateway {
		t.Fatalf("first status = %d, want 502; body=%s", first.Code, first.Body)
	}

	second := httptest.NewRecorder()
	router.ServeHTTP(second, authorizedJSONRequestWithKey(
		t, http.MethodPost, "/api/chat", body, "error-chat-key",
	))
	if second.Code != http.StatusBadGateway {
		t.Fatalf("second status = %d, want 502; body=%s", second.Code, second.Body)
	}
	if runner.calls() != 1 {
		t.Fatalf("Run() calls = %d, want 1", runner.calls())
	}
}

func TestChatRejectsIdempotencyKeyReuseWithDifferentPayload(t *testing.T) {
	runner := &countingChatRunner{result: &agent.Result{Message: "first-answer"}}
	router := newChatTestRouter(t, runner)

	first := httptest.NewRecorder()
	router.ServeHTTP(first, authorizedJSONRequestWithKey(
		t,
		http.MethodPost,
		"/api/chat",
		`{"message":"hello"}`,
		"reused-key",
	))
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200; body=%s", first.Code, first.Body)
	}

	second := httptest.NewRecorder()
	router.ServeHTTP(second, authorizedJSONRequestWithKey(
		t,
		http.MethodPost,
		"/api/chat",
		`{"message":"another"}`,
		"reused-key",
	))
	if second.Code != http.StatusConflict {
		t.Fatalf("second status = %d, want 409; body=%s", second.Code, second.Body)
	}
	if runner.calls() != 1 {
		t.Fatalf("Run() calls = %d, want 1", runner.calls())
	}
}

func TestChatRejectsConcurrentIdempotentRequests(t *testing.T) {
	runner := newBlockingChatRunner(&agent.Result{Message: "slow-answer"})
	router := newChatTestRouter(t, runner)
	body := `{"message":"hello"}`

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, authorizedJSONRequestWithKey(
			t, http.MethodPost, "/api/chat", body, "inflight-key",
		))
		if recorder.Code != http.StatusOK {
			t.Errorf("owner status = %d, want 200; body=%s", recorder.Code, recorder.Body)
		}
	}()
	runner.waitUntilStarted(t)

	conflict := httptest.NewRecorder()
	router.ServeHTTP(conflict, authorizedJSONRequestWithKey(
		t, http.MethodPost, "/api/chat", body, "inflight-key",
	))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, want 409; body=%s", conflict.Code, conflict.Body)
	}

	runner.finish()
	wg.Wait()
	if runner.calls() != 1 {
		t.Fatalf("Run() calls = %d, want 1", runner.calls())
	}
}

func TestChatRunsAgentLoop(t *testing.T) {
	llmClient := &scriptedHTTPLLMClient{
		responses: []*llm.ChatResponse{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{
						{
							ID:   "call-1",
							Type: "function",
							Function: llm.FunctionCall{
								Name:      "calculator",
								Arguments: `{"expression":"128 * 39"}`,
							},
						},
					},
				},
			},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "128 × 39 = 4992"}},
		},
	}
	store := conversation.NewMemoryStore()
	registry, err := tools.NewRegistry(tools.NewCalculatorTool())
	if err != nil {
		t.Fatalf("tools.NewRegistry() error = %v", err)
	}
	chatAgent, err := agent.New(agent.Config{
		LLM:           llmClient,
		Conversations: store,
		Tools:         registry,
		MaxSteps:      3,
	})
	if err != nil {
		t.Fatalf("agent.New() error = %v", err)
	}

	router := newChatTestRouterWithStore(t, chatAgent, store)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedJSONRequest(
		t,
		http.MethodPost,
		"/api/chat",
		`{"message":"帮我计算 128 * 39"}`,
	))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body)
	}

	var response chatResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode chat response: %v", err)
	}
	if response.Message != "128 × 39 = 4992" {
		t.Fatalf("message = %q, want calculator result", response.Message)
	}
	if len(llmClient.requests) != 2 {
		t.Fatalf("LLM request count = %d, want 2", len(llmClient.requests))
	}
}

func TestChatStreamReturnsSSEEvents(t *testing.T) {
	llmClient := &scriptedHTTPLLMClient{
		responses: []*llm.ChatResponse{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{
						{
							ID:   "call-1",
							Type: "function",
							Function: llm.FunctionCall{
								Name:      "calculator",
								Arguments: `{"expression":"128 * 39"}`,
							},
						},
					},
				},
			},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "128 × 39 = 4992"}},
		},
	}
	store := conversation.NewMemoryStore()
	registry, err := tools.NewRegistry(tools.NewCalculatorTool())
	if err != nil {
		t.Fatalf("tools.NewRegistry() error = %v", err)
	}
	chatAgent, err := agent.New(agent.Config{
		LLM:           llmClient,
		Conversations: store,
		Tools:         registry,
		MaxSteps:      3,
	})
	if err != nil {
		t.Fatalf("agent.New() error = %v", err)
	}

	router := newChatTestRouterWithStore(t, chatAgent, store)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedJSONRequest(
		t,
		http.MethodPost,
		"/api/chat/stream",
		`{"message":"帮我计算 128 * 39"}`,
	))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body)
	}
	if !strings.Contains(recorder.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", recorder.Header().Get("Content-Type"))
	}

	events := parseSSEEvents(recorder.Body.String())
	if len(events) < 4 {
		t.Fatalf("SSE events = %#v, want tool_call, tool_result, delta, done", events)
	}
	if events[0].Event != "tool_call" || !strings.Contains(events[0].Data, `"name":"calculator"`) {
		t.Fatalf("first event = %#v, want tool_call", events[0])
	}
	if events[1].Event != "tool_result" {
		t.Fatalf("second event = %#v, want tool_result", events[1])
	}
	if events[2].Event != "delta" || !strings.Contains(events[2].Data, "128 × 39 = 4992") {
		t.Fatalf("third event = %#v, want delta", events[2])
	}
	if events[len(events)-1].Event != "done" {
		t.Fatalf("last event = %#v, want done", events[len(events)-1])
	}
	if !strings.Contains(events[len(events)-1].Data, `"conversation_id"`) {
		t.Fatalf("done event = %#v, want conversation_id", events[len(events)-1])
	}
}

func TestChatStreamRequiresBearerToken(t *testing.T) {
	router := newChatTestRouter(t, newHTTPTestAgent(t))
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/chat/stream",
		strings.NewReader(`{"message":"hello"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body)
	}
}

func TestChatStreamHonorsCanceledContext(t *testing.T) {
	store := newIdempotencyStore(time.Hour)
	runner := newBlockingChatRunner(&agent.Result{Message: "unused"})
	handler := chatStream(runner, conversation.NewMemoryStore(), store)

	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, identityContextKey{}, auth.Identity{Username: testHTTPUsername})
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/chat/stream",
		strings.NewReader(`{"message":"hello"}`),
	).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(idempotencyHeader, "stream-canceled-key")

	done := make(chan struct{})
	go func() {
		defer close(done)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
	}()
	runner.waitUntilStarted(t)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for canceled stream to stop")
	}
	runner.finish()

	retry := httptest.NewRequest(
		http.MethodPost,
		"/api/chat/stream",
		strings.NewReader(`{"message":"hello"}`),
	).WithContext(context.WithValue(
		context.Background(),
		identityContextKey{},
		auth.Identity{Username: testHTTPUsername},
	))
	retry.Header.Set("Content-Type", "application/json")
	retry.Header.Set(idempotencyHeader, "stream-canceled-key")
	retryRecorder := httptest.NewRecorder()
	handler.ServeHTTP(retryRecorder, retry)
	if retryRecorder.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200; body=%s", retryRecorder.Code, retryRecorder.Body)
	}
	if !strings.Contains(retryRecorder.Body.String(), "event: done") {
		t.Fatalf("retry body = %q, want SSE done", retryRecorder.Body.String())
	}
}

func TestChatStreamReplaysIdempotentSuccess(t *testing.T) {
	runner := &countingChatRunner{result: &agent.Result{Message: "cached-stream"}}
	router := newChatTestRouter(t, runner)
	body := `{"message":"hello"}`

	first := httptest.NewRecorder()
	router.ServeHTTP(first, authorizedJSONRequestWithKey(
		t, http.MethodPost, "/api/chat/stream", body, "stream-same-key",
	))
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200; body=%s", first.Code, first.Body)
	}

	second := httptest.NewRecorder()
	router.ServeHTTP(second, authorizedJSONRequestWithKey(
		t, http.MethodPost, "/api/chat/stream", body, "stream-same-key",
	))
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200; body=%s", second.Code, second.Body)
	}
	if !strings.Contains(second.Body.String(), `"message":"cached-stream"`) {
		t.Fatalf("replay body = %q, want cached done event", second.Body.String())
	}
	if runner.calls() != 1 {
		t.Fatalf("Run() calls = %d, want 1", runner.calls())
	}
}

type sseTestEvent struct {
	Event string
	Data  string
}

func parseSSEEvents(body string) []sseTestEvent {
	var events []sseTestEvent
	var current sseTestEvent
	for _, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, "event: "):
			current.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			current.Data = strings.TrimPrefix(line, "data: ")
		case strings.TrimSpace(line) == "" && current.Event != "":
			events = append(events, current)
			current = sseTestEvent{}
		}
	}
	return events
}

func newChatTestRouter(t *testing.T, runner ChatRunner) http.Handler {
	t.Helper()
	return newChatTestRouterWithStore(t, runner, conversation.NewMemoryStore())
}

func newChatTestRouterWithStore(
	t *testing.T,
	runner ChatRunner,
	store agent.ConversationStore,
) http.Handler {
	t.Helper()

	router, err := NewRouter(Dependencies{
		Agent:         runner,
		Auth:          newHTTPTestAuth(t),
		Conversations: store,
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	return router
}

func newHTTPTestAgentWithStore(
	t *testing.T,
	client llm.Client,
	store agent.ConversationStore,
	maxSteps int,
) *agent.Agent {
	t.Helper()

	registry, err := tools.NewRegistry()
	if err != nil {
		t.Fatalf("tools.NewRegistry() error = %v", err)
	}
	chatAgent, err := agent.New(agent.Config{
		LLM:           client,
		Conversations: store,
		Tools:         registry,
		MaxSteps:      maxSteps,
	})
	if err != nil {
		t.Fatalf("agent.New() error = %v", err)
	}
	return chatAgent
}

func newHTTPTestAgentWithLLM(t *testing.T, client llm.Client, maxSteps int) *agent.Agent {
	t.Helper()
	return newHTTPTestAgentWithStore(t, client, conversation.NewMemoryStore(), maxSteps)
}

func authorizedJSONRequest(t *testing.T, method, path, body string) *http.Request {
	t.Helper()
	return authorizedJSONRequestWithKey(t, method, path, body, "chat-test-key")
}

func authorizedJSONRequestWithKey(
	t *testing.T,
	method string,
	path string,
	body string,
	idempotencyKey string,
) *http.Request {
	t.Helper()

	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(
		"Authorization",
		"Bearer "+signedHTTPTestToken(t, time.Now().Add(time.Hour)),
	)
	if idempotencyKey != "" {
		request.Header.Set(idempotencyHeader, idempotencyKey)
	}
	return request
}
