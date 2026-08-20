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
	"github.com/ecol/chat-agent/internal/llm"
	"github.com/ecol/chat-agent/internal/session"
	"github.com/ecol/chat-agent/internal/tools"
)

type stubChatRunner struct {
	result    *agent.Result
	err       error
	sessionID string
	message   string
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
	sessionID string,
	message string,
) (*agent.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runner.sessionID = sessionID
	runner.message = message
	return runner.result, runner.err
}

type scriptedHTTPLLMClient struct {
	responses []*llm.ChatResponse
	errs      []error
	requests  []llm.ChatRequest
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

func TestChatReturnsFinalAnswer(t *testing.T) {
	llmClient := &scriptedHTTPLLMClient{
		responses: []*llm.ChatResponse{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "128 × 39 = 4992"}},
		},
	}
	router := newChatTestRouter(t, newHTTPTestAgentWithLLM(t, llmClient, 2))

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedJSONRequest(
		t,
		http.MethodPost,
		"/api/chat",
		`{"session_id":"session-1","message":"帮我计算 128 * 39"}`,
	))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body)
	}
	if strings.TrimSpace(recorder.Body.String()) != `{"message":"128 × 39 = 4992"}` {
		t.Fatalf("body = %q, want chat message", recorder.Body.String())
	}
	if len(llmClient.requests) != 1 {
		t.Fatalf("LLM request count = %d, want 1", len(llmClient.requests))
	}
}

func TestChatRequiresBearerToken(t *testing.T) {
	router := newChatTestRouter(t, newHTTPTestAgent(t))
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/chat",
		strings.NewReader(`{"session_id":"session-1","message":"hello"}`),
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
			body:       `{"session_id":"session-1","message":"hello"}`,
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
			body:        `{"session_id":"session-1","message":"hello","extra":true}`,
			wantStatus:  http.StatusBadRequest,
			wantError:   "invalid JSON request body",
		},
		{
			name:        "missing session id",
			contentType: "application/json",
			body:        `{"message":"hello"}`,
			wantStatus:  http.StatusBadRequest,
			wantError:   "session_id is required",
		},
		{
			name:        "blank session id",
			contentType: "application/json",
			body:        `{"session_id":"  ","message":"hello"}`,
			wantStatus:  http.StatusBadRequest,
			wantError:   "session_id is required",
		},
		{
			name:        "missing message",
			contentType: "application/json",
			body:        `{"session_id":"session-1"}`,
			wantStatus:  http.StatusBadRequest,
			wantError:   "message is required",
		},
		{
			name:        "body too large",
			contentType: "application/json",
			body:        `{"session_id":"session-1","message":"` + strings.Repeat("a", maxChatRequestBodySize) + `"}`,
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
				`{"session_id":"session-1","message":"hello"}`,
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
			if runner.sessionID != "session-1" || runner.message != "hello" {
				t.Fatalf("Run() args = (%q, %q)", runner.sessionID, runner.message)
			}
		})
	}
}

func TestChatHonorsCanceledContext(t *testing.T) {
	store := newIdempotencyStore(time.Hour)
	runner := &stubChatRunner{result: &agent.Result{Message: "unused"}}
	handler := chat(runner, store)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx = context.WithValue(ctx, identityContextKey{}, auth.Identity{Username: testHTTPUsername})

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/chat",
		strings.NewReader(`{"session_id":"session-1","message":"hello"}`),
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
		strings.NewReader(`{"session_id":"session-1","message":"hello"}`),
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
				`{"session_id":"session-1","message":"hello"}`,
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
	body := `{"session_id":"session-1","message":"hello"}`

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
	body := `{"session_id":"session-1","message":"hello"}`

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
		`{"session_id":"session-1","message":"hello"}`,
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
		`{"session_id":"session-1","message":"another"}`,
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
	body := `{"session_id":"session-1","message":"hello"}`

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
	registry, err := tools.NewRegistry(tools.NewCalculatorTool())
	if err != nil {
		t.Fatalf("tools.NewRegistry() error = %v", err)
	}
	chatAgent, err := agent.New(agent.Config{
		LLM:      llmClient,
		Sessions: session.NewMemoryStore(),
		Tools:    registry,
		MaxSteps: 3,
	})
	if err != nil {
		t.Fatalf("agent.New() error = %v", err)
	}

	router := newChatTestRouter(t, chatAgent)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedJSONRequest(
		t,
		http.MethodPost,
		"/api/chat",
		`{"session_id":"session-1","message":"帮我计算 128 * 39"}`,
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

func newChatTestRouter(t *testing.T, runner ChatRunner) http.Handler {
	t.Helper()

	router, err := NewRouter(Dependencies{
		Agent: runner,
		Auth:  newHTTPTestAuth(t),
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	return router
}

func newHTTPTestAgentWithLLM(t *testing.T, client llm.Client, maxSteps int) *agent.Agent {
	t.Helper()

	registry, err := tools.NewRegistry()
	if err != nil {
		t.Fatalf("tools.NewRegistry() error = %v", err)
	}
	chatAgent, err := agent.New(agent.Config{
		LLM:      client,
		Sessions: session.NewMemoryStore(),
		Tools:    registry,
		MaxSteps: maxSteps,
	})
	if err != nil {
		t.Fatalf("agent.New() error = %v", err)
	}
	return chatAgent
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
