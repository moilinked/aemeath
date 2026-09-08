package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

type chatCompletionStreamChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta        chatCompletionStreamDelta `json:"delta"`
		FinishReason string                    `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

type chatCompletionStreamDelta struct {
	Role             Role                      `json:"role"`
	Content          json.RawMessage           `json:"content"`
	ReasoningContent string                    `json:"reasoning_content"`
	ToolCalls        []chatCompletionToolDelta `json:"tool_calls"`
}

type chatCompletionToolDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type toolCallAccumulator struct {
	id        string
	callType  string
	name      string
	arguments string
}

func readChatStream(
	ctx context.Context,
	body io.Reader,
	emit StreamHandler,
) (*ChatResponse, error) {
	reader := bufio.NewReaderSize(body, 64*1024)
	accumulators := make(map[int]*toolCallAccumulator)
	response := &ChatResponse{}
	sawChoice := false
	totalBytes := 0

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		line, err := reader.ReadString('\n')
		totalBytes += len(line)
		if totalBytes > maxResponseBodySize {
			return nil, fmt.Errorf("llm stream exceeds %d bytes", maxResponseBodySize)
		}
		if err != nil && !errors.Is(err, io.EOF) {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, fmt.Errorf("read llm stream: %w", err)
		}

		payload, ok, done := parseSSEDataLine(line)
		if done {
			break
		}
		if ok {
			if applyErr := applyStreamChunk(response, accumulators, &sawChoice, payload, emit); applyErr != nil {
				return nil, applyErr
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}

	if !sawChoice && response.Message.Role == "" &&
		response.Message.Content == "" &&
		len(accumulators) == 0 {
		return nil, errors.New("llm stream contains no choices")
	}
	if response.Message.Role == "" {
		response.Message.Role = RoleAssistant
	}
	response.Message.ToolCalls = assembleToolCalls(accumulators)
	return response, nil
}

func parseSSEDataLine(line string) (payload string, ok bool, done bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, ":") {
		return "", false, false
	}
	if !strings.HasPrefix(trimmed, "data:") {
		return "", false, false
	}
	payload = strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if payload == "[DONE]" {
		return "", false, true
	}
	if payload == "" {
		return "", false, false
	}
	return payload, true, false
}

func applyStreamChunk(
	response *ChatResponse,
	accumulators map[int]*toolCallAccumulator,
	sawChoice *bool,
	payload string,
	emit StreamHandler,
) error {
	var chunk chatCompletionStreamChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return fmt.Errorf("decode llm stream chunk: %w", err)
	}
	if chunk.ID != "" {
		response.ID = chunk.ID
	}
	if chunk.Model != "" {
		response.Model = chunk.Model
	}
	if chunk.Usage.TotalTokens > 0 || chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
		response.Usage = chunk.Usage
	}
	if len(chunk.Choices) == 0 {
		return nil
	}

	*sawChoice = true
	choice := chunk.Choices[0]
	if choice.FinishReason != "" {
		response.FinishReason = choice.FinishReason
	}
	if choice.Delta.Role != "" {
		response.Message.Role = choice.Delta.Role
	}

	content := rawJSONString(choice.Delta.Content)
	if content != "" {
		response.Message.Content += content
		if err := emitStreamDelta(emit, StreamDelta{Content: content}); err != nil {
			return err
		}
	}
	if choice.Delta.ReasoningContent != "" {
		response.Message.ReasoningContent += choice.Delta.ReasoningContent
		if err := emitStreamDelta(emit, StreamDelta{ReasoningContent: choice.Delta.ReasoningContent}); err != nil {
			return err
		}
	}
	for _, call := range choice.Delta.ToolCalls {
		acc := accumulators[call.Index]
		if acc == nil {
			acc = &toolCallAccumulator{}
			accumulators[call.Index] = acc
		}
		if call.ID != "" {
			acc.id = call.ID
		}
		if call.Type != "" {
			acc.callType = call.Type
		}
		if call.Function.Name != "" {
			acc.name = call.Function.Name
		}
		acc.arguments += call.Function.Arguments
	}
	return nil
}

func emitStreamDelta(emit StreamHandler, delta StreamDelta) error {
	if emit == nil {
		return nil
	}
	return emit(delta)
}

func assembleToolCalls(accumulators map[int]*toolCallAccumulator) []ToolCall {
	if len(accumulators) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(accumulators))
	for index := range accumulators {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	calls := make([]ToolCall, 0, len(indexes))
	for _, index := range indexes {
		acc := accumulators[index]
		callType := acc.callType
		if callType == "" {
			callType = "function"
		}
		calls = append(calls, ToolCall{
			ID:   acc.id,
			Type: callType,
			Function: FunctionCall{
				Name:      acc.name,
				Arguments: acc.arguments,
			},
		})
	}
	return calls
}

func rawJSONString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}
