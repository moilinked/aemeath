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
	// ErrSessionIDRequired 表示 Agent 请求缺少 Session ID。
	ErrSessionIDRequired = errors.New("agent session ID is required")
	// ErrUserMessageRequired 表示 Agent 请求缺少用户消息。
	ErrUserMessageRequired = errors.New("agent user message is required")
	// ErrMaxStepsExceeded 表示 Agent 在限制步数内未生成最终回答。
	ErrMaxStepsExceeded = errors.New("agent maximum execution steps exceeded")
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
	sessionID string,
	userMessage string,
) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, ErrSessionIDRequired
	}
	if strings.TrimSpace(userMessage) == "" {
		return nil, ErrUserMessageRequired
	}

	release, err := agent.acquireSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	defer release()

	history, err := agent.sessionStore.Load(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load agent session %q: %w", sessionID, err)
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
		response, err := agent.llmClient.Chat(ctx, llm.ChatRequest{
			Messages: messages,
			Tools:    definitions,
		})
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
			if err := agent.sessionStore.Append(ctx, sessionID, turn...); err != nil {
				return nil, fmt.Errorf("append agent session %q: %w", sessionID, err)
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

type sessionGate struct {
	token chan struct{}
	refs  int
}

func newSessionGate() *sessionGate {
	gate := &sessionGate{token: make(chan struct{}, 1)}
	gate.token <- struct{}{}
	return gate
}

func (agent *Agent) acquireSession(
	ctx context.Context,
	sessionID string,
) (func(), error) {
	agent.sessionGatesMu.Lock()
	gate := agent.sessionGates[sessionID]
	if gate == nil {
		gate = newSessionGate()
		agent.sessionGates[sessionID] = gate
	}
	gate.refs++
	agent.sessionGatesMu.Unlock()

	select {
	case <-ctx.Done():
		agent.releaseSessionGate(sessionID, gate)
		return nil, ctx.Err()
	case <-gate.token:
		if err := ctx.Err(); err != nil {
			gate.token <- struct{}{}
			agent.releaseSessionGate(sessionID, gate)
			return nil, err
		}
		return func() {
			gate.token <- struct{}{}
			agent.releaseSessionGate(sessionID, gate)
		}, nil
	}
}

func (agent *Agent) releaseSessionGate(sessionID string, gate *sessionGate) {
	agent.sessionGatesMu.Lock()
	defer agent.sessionGatesMu.Unlock()

	gate.refs--
	if gate.refs == 0 && agent.sessionGates[sessionID] == gate {
		delete(agent.sessionGates, sessionID)
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
