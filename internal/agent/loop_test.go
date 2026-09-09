package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ecol/chat-agent/internal/llm"
	"github.com/ecol/chat-agent/internal/tools"
)

type scriptedLLMClient struct {
	responses []*llm.ChatResponse
	errs      []error
	requests  []llm.ChatRequest
}

func (client *scriptedLLMClient) Chat(
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

func (client *scriptedLLMClient) ChatStream(
	ctx context.Context,
	request llm.ChatRequest,
	emit llm.StreamHandler,
) (*llm.ChatResponse, error) {
	return llm.ChatStreamFromChat(ctx, client.Chat, request, emit)
}

type recordingConversationStore struct {
	history   []llm.Message
	appended  []llm.Message
	loadErr   error
	appendErr error
}

func (store *recordingConversationStore) Load(
	context.Context,
	string,
) ([]llm.Message, error) {
	if store.loadErr != nil {
		return nil, store.loadErr
	}
	return append([]llm.Message(nil), store.history...), nil
}

func (store *recordingConversationStore) Append(
	_ context.Context,
	_ string,
	messages ...llm.Message,
) error {
	if store.appendErr != nil {
		return store.appendErr
	}
	store.appended = append(store.appended, messages...)
	store.history = append(store.history, messages...)
	return nil
}

func (store *recordingConversationStore) Create(context.Context, string, string) (Conversation, error) {
	return Conversation{}, nil
}

func (store *recordingConversationStore) GetForUser(context.Context, string, string) (Conversation, []llm.Message, error) {
	return Conversation{}, nil, nil
}

func (store *recordingConversationStore) ListForUser(context.Context, string) ([]Conversation, error) {
	return nil, nil
}

func (store *recordingConversationStore) DeleteForUser(context.Context, string, string) error {
	return nil
}

type loopTestTool struct {
	name   string
	result string
	err    error
	calls  int
}

func (tool *loopTestTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.FunctionDefinition{
			Name:       tool.name,
			Parameters: json.RawMessage(`{"type":"object"}`),
		},
	}
}

func (tool *loopTestTool) Execute(
	context.Context,
	json.RawMessage,
) (string, error) {
	tool.calls++
	return tool.result, tool.err
}

func TestAgentRunReturnsFinalAnswer(t *testing.T) {
	history := []llm.Message{{Role: llm.RoleAssistant, Content: "earlier"}}
	store := &recordingConversationStore{history: history}
	client := &scriptedLLMClient{
		responses: []*llm.ChatResponse{
			{
				Message: llm.Message{Role: llm.RoleAssistant, Content: "final answer"},
				Usage: llm.Usage{
					PromptTokens:     10,
					CompletionTokens: 3,
					TotalTokens:      13,
				},
			},
		},
	}
	created := newLoopAgent(t, client, store, 4)

	result, err := created.Run(context.Background(), " conv-1 ", "hello")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Message != "final answer" || result.Steps != 1 {
		t.Fatalf("Run() result = %#v, want final answer at step 1", result)
	}
	if result.Usage.TotalTokens != 13 {
		t.Fatalf("Run() usage = %#v, want 13 total tokens", result.Usage)
	}

	if len(client.requests) != 1 {
		t.Fatalf("LLM request count = %d, want 1", len(client.requests))
	}
	requestMessages := client.requests[0].Messages
	if len(requestMessages) != 3 {
		t.Fatalf("request message count = %d, want 3", len(requestMessages))
	}
	if !reflect.DeepEqual(requestMessages[0], SystemMessage()) ||
		!reflect.DeepEqual(requestMessages[1], history[0]) ||
		requestMessages[2].Role != llm.RoleUser ||
		requestMessages[2].Content != "hello" {
		t.Fatalf("request messages = %#v, want system, history, user", requestMessages)
	}

	wantAppended := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "final answer"},
	}
	if !reflect.DeepEqual(store.appended, wantAppended) {
		t.Fatalf("appended messages = %#v, want %#v", store.appended, wantAppended)
	}
}

func TestAgentRunTrimsOldHistory(t *testing.T) {
	history := []llm.Message{
		{Role: llm.RoleUser, Content: strings.Repeat("旧问题", 40)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("旧回答", 40)},
	}
	store := &recordingConversationStore{history: history}
	client := &scriptedLLMClient{
		responses: []*llm.ChatResponse{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "now"}},
		},
	}
	budget := estimateMessageTokens([]llm.Message{
		SystemMessage(),
		{Role: llm.RoleUser, Content: "现在"},
	}) + 64
	created := newLoopAgentWithBudget(t, client, store, 2, budget, 0)

	if _, err := created.Run(context.Background(), "conv-1", "现在"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("LLM request count = %d, want 1", len(client.requests))
	}
	for _, message := range client.requests[0].Messages {
		if strings.Contains(message.Content, "旧问题") || strings.Contains(message.Content, "旧回答") {
			t.Fatalf("LLM still received trimmed history: %#v", client.requests[0].Messages)
		}
	}
	if store.appended[0].Content != "现在" {
		t.Fatalf("persisted turn = %#v, want current user message", store.appended)
	}
}

func TestAgentRunSetsMaxOutputTokens(t *testing.T) {
	client := &scriptedLLMClient{
		responses: []*llm.ChatResponse{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"}},
		},
	}
	created := newLoopAgentWithBudget(t, client, &recordingConversationStore{}, 1, 0, 128)
	if _, err := created.Run(context.Background(), "conv-1", "hi"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if client.requests[0].MaxTokens == nil || *client.requests[0].MaxTokens != 128 {
		t.Fatalf("MaxTokens = %#v, want 128", client.requests[0].MaxTokens)
	}
}

func TestAgentRunExecutesToolLoop(t *testing.T) {
	store := &recordingConversationStore{}
	client := &scriptedLLMClient{
		responses: []*llm.ChatResponse{
			{
				Message: llm.Message{
					Role:             llm.RoleAssistant,
					ReasoningContent: "need calculation",
					ToolCalls: []llm.ToolCall{
						{
							ID:   "call-1",
							Type: "function",
							Function: llm.FunctionCall{
								Name:      "calculator",
								Arguments: `{"expression":"6 * 7"}`,
							},
						},
					},
				},
				Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14},
			},
			{
				Message: llm.Message{Role: llm.RoleAssistant, Content: "6 × 7 = 42"},
				Usage:   llm.Usage{PromptTokens: 20, CompletionTokens: 5, TotalTokens: 25},
			},
		},
	}
	created := newLoopAgent(t, client, store, 4, tools.NewCalculatorTool())

	result, err := created.Run(context.Background(), "conv-1", "calculate 6 * 7")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Message != "6 × 7 = 42" || result.Steps != 2 {
		t.Fatalf("Run() result = %#v, want final answer at step 2", result)
	}
	if result.Usage != (llm.Usage{
		PromptTokens:     30,
		CompletionTokens: 9,
		TotalTokens:      39,
	}) {
		t.Fatalf("Run() usage = %#v, want accumulated usage", result.Usage)
	}

	if len(client.requests) != 2 {
		t.Fatalf("LLM request count = %d, want 2", len(client.requests))
	}
	secondMessages := client.requests[1].Messages
	if len(secondMessages) != 4 {
		t.Fatalf("second request message count = %d, want 4", len(secondMessages))
	}
	if secondMessages[2].ReasoningContent != "need calculation" {
		t.Fatalf("reasoning content = %q, want preserved content", secondMessages[2].ReasoningContent)
	}
	observation := secondMessages[3]
	if observation.Role != llm.RoleTool ||
		observation.ToolCallID != "call-1" ||
		observation.Content != "42" {
		t.Fatalf("tool observation = %#v, want calculator result", observation)
	}
	if len(client.requests[0].Tools) != 1 ||
		client.requests[0].Tools[0].Function.Name != "calculator" {
		t.Fatalf("tool definitions = %#v, want calculator", client.requests[0].Tools)
	}
	if len(store.appended) != 4 {
		t.Fatalf("appended message count = %d, want 4", len(store.appended))
	}
}

func TestAgentRunReturnsToolFailureAsObservation(t *testing.T) {
	failingTool := &loopTestTool{name: "failing", err: errors.New("tool failed")}
	client := &scriptedLLMClient{
		responses: []*llm.ChatResponse{
			{Message: toolCallMessage("call-1", "failing", `{}`)},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "tool unavailable"}},
		},
	}
	store := &recordingConversationStore{}
	created := newLoopAgent(t, client, store, 3, failingTool)

	result, err := created.Run(context.Background(), "conv-1", "use tool")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Message != "tool unavailable" {
		t.Fatalf("Run() message = %q, want tool unavailable", result.Message)
	}
	observation := client.requests[1].Messages[3]
	if !strings.Contains(observation.Content, `"error"`) ||
		!strings.Contains(observation.Content, "tool failed") {
		t.Fatalf("tool error observation = %q, want structured error", observation.Content)
	}
}

func TestAgentRunStopsBeforeToolAtMaxSteps(t *testing.T) {
	countingTool := &loopTestTool{name: "counting", result: "result"}
	client := &scriptedLLMClient{
		responses: []*llm.ChatResponse{
			{Message: toolCallMessage("call-1", "counting", `{}`)},
		},
	}
	store := &recordingConversationStore{}
	created := newLoopAgent(t, client, store, 1, countingTool)

	_, err := created.Run(context.Background(), "conv-1", "use tool")
	if !errors.Is(err, ErrMaxStepsExceeded) {
		t.Fatalf("Run() error = %v, want ErrMaxStepsExceeded", err)
	}
	if countingTool.calls != 0 {
		t.Fatalf("tool calls = %d, want 0", countingTool.calls)
	}
	if len(store.appended) != 0 {
		t.Fatalf("appended messages = %d, want 0", len(store.appended))
	}
}

func TestAgentRunStreamEmitsEvents(t *testing.T) {
	calculator := &loopTestTool{name: "calculator", result: "42"}
	client := &scriptedLLMClient{
		responses: []*llm.ChatResponse{
			{
				Message: llm.Message{
					Role:             llm.RoleAssistant,
					ReasoningContent: "need calculation",
					ToolCalls: []llm.ToolCall{
						{
							ID:   "call-1",
							Type: "function",
							Function: llm.FunctionCall{
								Name:      "calculator",
								Arguments: `{"expression":"6*7"}`,
							},
						},
					},
				},
			},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "answer is 42"}},
		},
	}
	store := &recordingConversationStore{}
	created := newLoopAgent(t, client, store, 3, calculator)

	var events []StreamEvent
	result, err := created.RunStream(
		context.Background(),
		"conv-1",
		"calculate",
		func(event StreamEvent) error {
			events = append(events, event)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}
	if result.Message != "answer is 42" {
		t.Fatalf("RunStream() message = %q", result.Message)
	}

	wantTypes := []StreamEventType{
		StreamEventReasoning,
		StreamEventToolCall,
		StreamEventToolResult,
		StreamEventDelta,
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("event count = %d, want %d: %#v", len(events), len(wantTypes), events)
	}
	for index, wantType := range wantTypes {
		if events[index].Type != wantType {
			t.Fatalf("event[%d] type = %q, want %q", index, events[index].Type, wantType)
		}
	}
	if events[0].Content != "need calculation" {
		t.Fatalf("reasoning = %q", events[0].Content)
	}
	if events[1].ToolCall == nil || events[1].ToolCall.Name != "calculator" {
		t.Fatalf("tool_call = %#v", events[1].ToolCall)
	}
	if events[2].ToolCall == nil || events[2].ToolCall.Content != "42" {
		t.Fatalf("tool_result = %#v", events[2].ToolCall)
	}
	if events[3].Content != "answer is 42" {
		t.Fatalf("delta = %q", events[3].Content)
	}
}

func TestAgentRunStreamStopsWhenHandlerFails(t *testing.T) {
	handlerErr := errors.New("client disconnected")
	client := &scriptedLLMClient{
		responses: []*llm.ChatResponse{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "hello"}},
		},
	}
	store := &recordingConversationStore{}
	created := newLoopAgent(t, client, store, 2)

	_, err := created.RunStream(
		context.Background(),
		"conv-1",
		"hi",
		func(StreamEvent) error {
			return handlerErr
		},
	)
	if !errors.Is(err, handlerErr) {
		t.Fatalf("RunStream() error = %v, want handler error", err)
	}
	if len(store.appended) != 0 {
		t.Fatalf("appended messages = %d, want 0", len(store.appended))
	}
}

func TestAgentRunValidatesInputAndResponse(t *testing.T) {
	tests := []struct {
		name           string
		conversationID string
		userMessage    string
		response       *llm.ChatResponse
		wantError      error
	}{
		{
			name:        "missing conversation ID",
			userMessage: "hello",
			wantError:   ErrConversationIDRequired,
		},
		{
			name:           "missing user message",
			conversationID: "conv-1",
			userMessage:    "  ",
			wantError:      ErrUserMessageRequired,
		},
		{
			name:           "nil LLM response",
			conversationID: "conv-1",
			userMessage:    "hello",
			wantError:      ErrInvalidLLMResponse,
		},
		{
			name:           "empty final answer",
			conversationID: "conv-1",
			userMessage:    "hello",
			response: &llm.ChatResponse{
				Message: llm.Message{Role: llm.RoleAssistant},
			},
			wantError: ErrInvalidLLMResponse,
		},
		{
			name:           "invalid tool call",
			conversationID: "conv-1",
			userMessage:    "hello",
			response: &llm.ChatResponse{
				Message: toolCallMessage("", "calculator", `{}`),
			},
			wantError: ErrInvalidToolCall,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &scriptedLLMClient{}
			if test.response != nil {
				client.responses = []*llm.ChatResponse{test.response}
			} else if test.conversationID == "" || strings.TrimSpace(test.userMessage) == "" {
				client.responses = []*llm.ChatResponse{}
			} else {
				client.responses = []*llm.ChatResponse{nil}
			}
			created := newLoopAgent(t, client, &recordingConversationStore{}, 2)

			_, err := created.Run(
				context.Background(),
				test.conversationID,
				test.userMessage,
			)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("Run() error = %v, want %v", err, test.wantError)
			}
		})
	}
}

func TestAgentRunPropagatesDependencyErrors(t *testing.T) {
	llmError := errors.New("LLM unavailable")
	storeError := errors.New("conversation store unavailable")
	tests := []struct {
		name      string
		client    *scriptedLLMClient
		store     *recordingConversationStore
		wantError error
	}{
		{
			name:      "load conversation",
			client:    &scriptedLLMClient{},
			store:     &recordingConversationStore{loadErr: storeError},
			wantError: storeError,
		},
		{
			name: "LLM request",
			client: &scriptedLLMClient{
				errs: []error{llmError},
			},
			store:     &recordingConversationStore{},
			wantError: llmError,
		},
		{
			name: "append conversation",
			client: &scriptedLLMClient{
				responses: []*llm.ChatResponse{
					{Message: llm.Message{Role: llm.RoleAssistant, Content: "answer"}},
				},
			},
			store:     &recordingConversationStore{appendErr: storeError},
			wantError: storeError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			created := newLoopAgent(t, test.client, test.store, 2)
			_, err := created.Run(context.Background(), "conv-1", "hello")
			if !errors.Is(err, test.wantError) {
				t.Fatalf("Run() error = %v, want %v", err, test.wantError)
			}
		})
	}
}

func TestAgentConversationGateHonorsContext(t *testing.T) {
	created := newLoopAgent(
		t,
		&scriptedLLMClient{},
		&recordingConversationStore{},
		1,
	)
	release, err := created.acquireConversation(context.Background(), "conv-1")
	if err != nil {
		t.Fatalf("acquireConversation() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = created.acquireConversation(ctx, "conv-1")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquireConversation() error = %v, want context deadline", err)
	}

	release()
	nextRelease, err := created.acquireConversation(context.Background(), "conv-1")
	if err != nil {
		t.Fatalf("acquireConversation() after release error = %v", err)
	}
	nextRelease()
	if len(created.conversationGates) != 0 {
		t.Fatalf("conversation gate count = %d, want 0 after release", len(created.conversationGates))
	}
}

func newLoopAgent(
	t *testing.T,
	client llm.Client,
	store ConversationStore,
	maxSteps int,
	toolset ...tools.Tool,
) *Agent {
	t.Helper()
	return newLoopAgentWithBudget(t, client, store, maxSteps, 0, 0, toolset...)
}

func newLoopAgentWithBudget(
	t *testing.T,
	client llm.Client,
	store ConversationStore,
	maxSteps int,
	contextTokens int,
	maxOutputTokens int,
	toolset ...tools.Tool,
) *Agent {
	t.Helper()

	registry, err := tools.NewRegistry(toolset...)
	if err != nil {
		t.Fatalf("tools.NewRegistry() error = %v", err)
	}
	created, err := New(Config{
		LLM:             client,
		Conversations:   store,
		Tools:           registry,
		MaxSteps:        maxSteps,
		ContextTokens:   contextTokens,
		MaxOutputTokens: maxOutputTokens,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return created
}

func toolCallMessage(id, name, arguments string) llm.Message {
	return llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{
			{
				ID:   id,
				Type: "function",
				Function: llm.FunctionCall{
					Name:      name,
					Arguments: arguments,
				},
			},
		},
	}
}
