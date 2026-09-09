package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/ecol/chat-agent/internal/llm"
)

func TestEstimateText(t *testing.T) {
	if estimateText("") != 0 {
		t.Fatal("empty text should be 0 tokens")
	}
	ascii := estimateText("abcd")
	if ascii != 1 {
		t.Fatalf("ASCII estimate = %d, want 1", ascii)
	}
	chinese := estimateText("你好")
	if chinese != 2 {
		t.Fatalf("Chinese estimate = %d, want 2", chinese)
	}
	if chinese <= ascii {
		t.Fatal("Chinese text should cost at least as many tokens as short ASCII")
	}
}

func TestTrimMessagesKeepsSystemAndLatestTurn(t *testing.T) {
	system := llm.Message{Role: llm.RoleSystem, Content: "sys"}
	oldUser := llm.Message{Role: llm.RoleUser, Content: strings.Repeat("旧", 80)}
	oldAssistant := llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("答", 80)}
	current := llm.Message{Role: llm.RoleUser, Content: "现在"}
	messages := []llm.Message{system, oldUser, oldAssistant, current}

	budget := estimateMessageTokens([]llm.Message{system, current}) + 4
	got, err := trimMessages(messages, 0, budget)
	if err != nil {
		t.Fatalf("trimMessages() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (system + current user)", len(got))
	}
	if got[0].Content != "sys" || got[1].Content != "现在" {
		t.Fatalf("trimmed = %#v, want system and current user", got)
	}
}

func TestTrimMessagesDropsToolTurnsTogether(t *testing.T) {
	system := llm.Message{Role: llm.RoleSystem, Content: "sys"}
	oldUser := llm.Message{Role: llm.RoleUser, Content: strings.Repeat("算", 80)}
	oldAssistant := llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{
			{
				ID:   "call-1",
				Type: "function",
				Function: llm.FunctionCall{
					Name:      "calculator",
					Arguments: `{"expression":"` + strings.Repeat("1+", 40) + `1"}`,
				},
			},
		},
	}
	oldTool := llm.Message{
		Role:       llm.RoleTool,
		ToolCallID: "call-1",
		Content:    strings.Repeat("9", 80),
	}
	current := llm.Message{Role: llm.RoleUser, Content: "下一问"}
	messages := []llm.Message{system, oldUser, oldAssistant, oldTool, current}

	budget := estimateMessageTokens([]llm.Message{system, current}) + 4
	got, err := trimMessages(messages, 0, budget)
	if err != nil {
		t.Fatalf("trimMessages() error = %v", err)
	}
	for _, message := range got {
		if message.Role == llm.RoleTool || len(message.ToolCalls) > 0 {
			t.Fatalf("trimmed leftover tool turn: %#v", got)
		}
	}
	if len(got) != 2 || got[1].Content != "下一问" {
		t.Fatalf("trimmed = %#v, want system and current user", got)
	}
}

func TestTrimMessagesNoOpWhenUnderBudget(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, Content: "hello"},
		{Role: llm.RoleUser, Content: "again"},
	}
	got, err := trimMessages(messages, 0, 10_000)
	if err != nil {
		t.Fatalf("trimMessages() error = %v", err)
	}
	if len(got) != len(messages) {
		t.Fatalf("len = %d, want %d", len(got), len(messages))
	}
}

func TestTrimMessagesUnlimitedBudget(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: strings.Repeat("x", 100)},
	}
	got, err := trimMessages(messages, 0, 0)
	if err != nil {
		t.Fatalf("trimMessages() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
}

func TestTrimMessagesPinnedExceedsBudget(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: strings.Repeat("系", 40)},
		{Role: llm.RoleUser, Content: strings.Repeat("问", 40)},
	}
	_, err := trimMessages(messages, 0, 8)
	if !errors.Is(err, ErrContextBudgetExceeded) {
		t.Fatalf("error = %v, want ErrContextBudgetExceeded", err)
	}
}
