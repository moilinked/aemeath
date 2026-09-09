package agent

import (
	"encoding/json"
	"fmt"

	"github.com/ecol/chat-agent/internal/llm"
)

const messageTokenOverhead = 8

// estimateText 用启发式估算文本 token 数，不是 tiktoken。
// ASCII 约 4 字符 1 token；非 ASCII（含中文）按 1 字 1 token，偏向多估。
func estimateText(value string) int {
	if value == "" {
		return 0
	}
	ascii := 0
	tokens := 0
	for _, character := range value {
		if character <= 0x7F {
			ascii++
			continue
		}
		tokens++
	}
	tokens += (ascii + 3) / 4
	if tokens == 0 {
		return 1
	}
	return tokens
}

func estimateMessageTokens(messages []llm.Message) int {
	total := 0
	for _, message := range messages {
		total += messageTokenOverhead
		total += estimateText(message.Content)
		total += estimateText(message.ReasoningContent)
		total += estimateText(message.Name)
		total += estimateText(message.ToolCallID)
		for _, call := range message.ToolCalls {
			total += messageTokenOverhead
			total += estimateText(call.ID)
			total += estimateText(call.Type)
			total += estimateText(call.Function.Name)
			total += estimateText(call.Function.Arguments)
		}
	}
	return total
}

func estimateToolTokens(definitions []llm.ToolDefinition) int {
	if len(definitions) == 0 {
		return 0
	}
	raw, err := json.Marshal(definitions)
	if err != nil {
		return 0
	}
	return estimateText(string(raw)) + messageTokenOverhead
}

func contextFits(messages []llm.Message, extraTokens, budget int) bool {
	return estimateMessageTokens(messages)+extraTokens <= budget
}

func cloneMessages(messages []llm.Message) []llm.Message {
	if messages == nil {
		return nil
	}
	cloned := make([]llm.Message, len(messages))
	copy(cloned, messages)
	return cloned
}

func splitTurns(messages []llm.Message) [][]llm.Message {
	groups := make([][]llm.Message, 0)
	current := make([]llm.Message, 0)
	for _, message := range messages {
		if message.Role == llm.RoleUser && len(current) > 0 {
			groups = append(groups, current)
			current = nil
		}
		current = append(current, message)
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}

func joinMessageGroups(prefix []llm.Message, groups [][]llm.Message, suffix []llm.Message) []llm.Message {
	size := len(prefix) + len(suffix)
	for _, group := range groups {
		size += len(group)
	}
	joined := make([]llm.Message, 0, size)
	joined = append(joined, prefix...)
	for _, group := range groups {
		joined = append(joined, group...)
	}
	joined = append(joined, suffix...)
	return joined
}

func lastUserIndex(messages []llm.Message, start int) int {
	for index := len(messages) - 1; index >= start; index-- {
		if messages[index].Role == llm.RoleUser {
			return index
		}
	}
	return -1
}

// trimMessages 在预算内保留 System Prompt 与最后一轮用户消息及其后续内容，
// 从最旧的完整轮次开始丢弃。不修改数据库中的历史。
func trimMessages(messages []llm.Message, extraTokens, budget int) ([]llm.Message, error) {
	if budget <= 0 || contextFits(messages, extraTokens, budget) {
		return cloneMessages(messages), nil
	}

	systemEnd := 0
	for systemEnd < len(messages) && messages[systemEnd].Role == llm.RoleSystem {
		systemEnd++
	}
	pinnedFrom := lastUserIndex(messages, systemEnd)
	if pinnedFrom < 0 {
		pinnedFrom = systemEnd
	}

	prefix := messages[:systemEnd]
	droppable := messages[systemEnd:pinnedFrom]
	suffix := messages[pinnedFrom:]
	groups := splitTurns(droppable)

	for {
		candidate := joinMessageGroups(prefix, groups, suffix)
		if contextFits(candidate, extraTokens, budget) {
			return cloneMessages(candidate), nil
		}
		if len(groups) == 0 {
			return nil, fmt.Errorf("%w: pinned messages exceed %d tokens", ErrContextBudgetExceeded, budget)
		}
		groups = groups[1:]
	}
}
