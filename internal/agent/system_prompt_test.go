package agent

import (
	"strings"
	"testing"

	"github.com/ecol/chat-agent/internal/llm"
)

func TestSystemMessage(t *testing.T) {
	message := SystemMessage()

	if message.Role != llm.RoleSystem {
		t.Errorf("SystemMessage() role = %q, want %q", message.Role, llm.RoleSystem)
	}
	if strings.TrimSpace(message.Content) == "" {
		t.Error("SystemMessage() content is empty")
	}
	if message.Content != DefaultSystemPrompt {
		t.Error("SystemMessage() content does not match DefaultSystemPrompt")
	}
}
