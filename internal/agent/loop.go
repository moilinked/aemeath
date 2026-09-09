package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ecol/chat-agent/internal/llm"
)

var (
	// ErrConversationIDRequired 表示 Agent 请求缺少对话 ID。
	ErrConversationIDRequired = errors.New("agent conversation ID is required")
	// ErrConversationNotFound 表示对话不存在或不属于当前用户。
	ErrConversationNotFound = errors.New("conversation not found")
	// ErrUserMessageRequired 表示 Agent 请求缺少用户消息。
	ErrUserMessageRequired = errors.New("agent user message is required")
	// ErrMaxStepsExceeded 表示 Agent 在限制步数内未生成最终回答。
	ErrMaxStepsExceeded = errors.New("agent maximum execution steps exceeded")
	// ErrContextBudgetExceeded 表示 System Prompt、当前轮次或工具定义已超过 token 预算。
	ErrContextBudgetExceeded = errors.New("agent context exceeds token budget")
	// ErrInvalidLLMResponse 表示 LLM 返回空响应或无效消息。
	ErrInvalidLLMResponse = errors.New("invalid LLM response")
	// ErrInvalidToolCall 表示 LLM 返回的工具调用缺少必要字段。
	ErrInvalidToolCall = errors.New("invalid LLM tool call")
)

// Result 是一次成功 Agent 运行的最终结果。
type Result struct {
	Message string
	Steps   int
	Usage   llm.Usage
}

// Run 执行一次完整对话轮次，直到 LLM 返回最终回答或达到最大步数。
func (agent *Agent) Run(
	ctx context.Context,
	conversationID string,
	userMessage string,
) (*Result, error) {
	return agent.RunStream(ctx, conversationID, userMessage, nil)
}

// RunStream 执行与 Run 相同的 Agent Loop，并通过 emit 回传增量、工具调用和工具结果。
// 客户端断开导致的 context 取消会停止后续 LLM 请求和工具执行。
func (agent *Agent) RunStream(
	ctx context.Context,
	conversationID string,
	userMessage string,
	emit StreamHandler,
) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil, ErrConversationIDRequired
	}
	if strings.TrimSpace(userMessage) == "" {
		return nil, ErrUserMessageRequired
	}

	release, err := agent.acquireConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	defer release()

	history, err := agent.conversations.Load(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("load agent conversation %q: %w", conversationID, err)
	}

	user := llm.Message{Role: llm.RoleUser, Content: userMessage}
	messages := make([]llm.Message, 0, len(history)+2)
	messages = append(messages, agent.systemMessage)
	messages = append(messages, history...)
	messages = append(messages, user)

	turn := []llm.Message{user}
	definitions := agent.toolRegistry.Definitions()
	var usage llm.Usage

	for step := 1; step <= agent.maxSteps; step++ {
		prompt, err := agent.fitContext(messages, definitions)
		if err != nil {
			return nil, err
		}
		response, err := agent.completeChat(ctx, agent.chatRequest(prompt, definitions), emit)
		if err != nil {
			return nil, fmt.Errorf("agent LLM step %d: %w", step, err)
		}
		if response == nil {
			return nil, fmt.Errorf("%w: step %d returned nil", ErrInvalidLLMResponse, step)
		}
		addUsage(&usage, response.Usage)

		assistant := response.Message
		if assistant.Role == "" {
			assistant.Role = llm.RoleAssistant
		}
		if assistant.Role != llm.RoleAssistant {
			return nil, fmt.Errorf(
				"%w: step %d returned role %q",
				ErrInvalidLLMResponse,
				step,
				assistant.Role,
			)
		}

		messages = append(messages, assistant)
		turn = append(turn, assistant)

		if len(assistant.ToolCalls) == 0 {
			if strings.TrimSpace(assistant.Content) == "" {
				return nil, fmt.Errorf(
					"%w: step %d returned empty final content",
					ErrInvalidLLMResponse,
					step,
				)
			}
			if err := agent.conversations.Append(ctx, conversationID, turn...); err != nil {
				return nil, fmt.Errorf("append agent conversation %q: %w", conversationID, err)
			}
			return &Result{
				Message: assistant.Content,
				Steps:   step,
				Usage:   usage,
			}, nil
		}

		if err := validateToolCalls(assistant.ToolCalls); err != nil {
			return nil, err
		}
		if step == agent.maxSteps {
			return nil, fmt.Errorf("%w: limit=%d", ErrMaxStepsExceeded, agent.maxSteps)
		}

		for _, call := range assistant.ToolCalls {
			if err := emitStreamEvent(emit, StreamEvent{
				Type: StreamEventToolCall,
				ToolCall: &StreamToolCall{
					ID:        call.ID,
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				},
			}); err != nil {
				return nil, err
			}

			observation, err := agent.toolRegistry.Execute(
				ctx,
				call.Function.Name,
				call.Function.Arguments,
			)
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, ctxErr
				}
				observation = toolErrorObservation(err)
			}

			if err := emitStreamEvent(emit, StreamEvent{
				Type: StreamEventToolResult,
				ToolCall: &StreamToolCall{
					ID:      call.ID,
					Name:    call.Function.Name,
					Content: observation,
				},
			}); err != nil {
				return nil, err
			}

			toolMessage := llm.Message{
				Role:       llm.RoleTool,
				Content:    observation,
				ToolCallID: call.ID,
			}
			messages = append(messages, toolMessage)
			turn = append(turn, toolMessage)
		}
	}

	return nil, fmt.Errorf("%w: limit=%d", ErrMaxStepsExceeded, agent.maxSteps)
}

func (agent *Agent) chatRequest(messages []llm.Message, definitions []llm.ToolDefinition) llm.ChatRequest {
	request := llm.ChatRequest{
		Messages: messages,
		Tools:    definitions,
	}
	if agent.maxOutputTokens > 0 {
		maxTokens := agent.maxOutputTokens
		request.MaxTokens = &maxTokens
	}
	return request
}

func (agent *Agent) fitContext(
	messages []llm.Message,
	definitions []llm.ToolDefinition,
) ([]llm.Message, error) {
	return trimMessages(messages, estimateToolTokens(definitions), agent.contextTokens)
}

func (agent *Agent) completeChat(
	ctx context.Context,
	request llm.ChatRequest,
	emit StreamHandler,
) (*llm.ChatResponse, error) {
	if emit == nil {
		return agent.llmClient.Chat(ctx, request)
	}
	return agent.llmClient.ChatStream(ctx, request, func(delta llm.StreamDelta) error {
		if delta.ReasoningContent != "" {
			if err := emitStreamEvent(emit, StreamEvent{
				Type:    StreamEventReasoning,
				Content: delta.ReasoningContent,
			}); err != nil {
				return err
			}
		}
		if delta.Content != "" {
			if err := emitStreamEvent(emit, StreamEvent{
				Type:    StreamEventDelta,
				Content: delta.Content,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func emitStreamEvent(emit StreamHandler, event StreamEvent) error {
	if emit == nil {
		return nil
	}
	return emit(event)
}

type conversationGate struct {
	token chan struct{}
	refs  int
}

func newConversationGate() *conversationGate {
	gate := &conversationGate{token: make(chan struct{}, 1)}
	gate.token <- struct{}{}
	return gate
}

func (agent *Agent) acquireConversation(
	ctx context.Context,
	conversationID string,
) (func(), error) {
	agent.conversationGatesMu.Lock()
	gate := agent.conversationGates[conversationID]
	if gate == nil {
		gate = newConversationGate()
		agent.conversationGates[conversationID] = gate
	}
	gate.refs++
	agent.conversationGatesMu.Unlock()

	select {
	case <-ctx.Done():
		agent.releaseConversationGate(conversationID, gate)
		return nil, ctx.Err()
	case <-gate.token:
		if err := ctx.Err(); err != nil {
			gate.token <- struct{}{}
			agent.releaseConversationGate(conversationID, gate)
			return nil, err
		}
		return func() {
			gate.token <- struct{}{}
			agent.releaseConversationGate(conversationID, gate)
		}, nil
	}
}

func (agent *Agent) releaseConversationGate(conversationID string, gate *conversationGate) {
	agent.conversationGatesMu.Lock()
	defer agent.conversationGatesMu.Unlock()

	gate.refs--
	if gate.refs == 0 && agent.conversationGates[conversationID] == gate {
		delete(agent.conversationGates, conversationID)
	}
}

func validateToolCalls(calls []llm.ToolCall) error {
	ids := make(map[string]struct{}, len(calls))
	for index, call := range calls {
		if strings.TrimSpace(call.ID) == "" {
			return fmt.Errorf("%w: call %d has no ID", ErrInvalidToolCall, index)
		}
		if call.Type != "function" {
			return fmt.Errorf(
				"%w: call %q has type %q",
				ErrInvalidToolCall,
				call.ID,
				call.Type,
			)
		}
		if strings.TrimSpace(call.Function.Name) == "" {
			return fmt.Errorf("%w: call %q has no function name", ErrInvalidToolCall, call.ID)
		}
		if _, exists := ids[call.ID]; exists {
			return fmt.Errorf("%w: duplicate call ID %q", ErrInvalidToolCall, call.ID)
		}
		ids[call.ID] = struct{}{}
	}
	return nil
}

func toolErrorObservation(toolErr error) string {
	content, err := json.Marshal(struct {
		Error string `json:"error"`
	}{
		Error: toolErr.Error(),
	})
	if err != nil {
		return `{"error":"tool execution failed"}`
	}
	return string(content)
}

func addUsage(total *llm.Usage, current llm.Usage) {
	total.PromptTokens += current.PromptTokens
	total.CompletionTokens += current.CompletionTokens
	total.TotalTokens += current.TotalTokens
}
