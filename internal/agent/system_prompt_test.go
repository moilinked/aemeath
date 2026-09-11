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

	required := []string{
		"Aemeath",
		"小爱",
		"活泼俏皮的少女",
		"不是客服脚本",
		"口语化",
		"不要长篇大论",
		"不要大段独白",
		"爱开玩笑",
		"心里温柔",
		"不要复述问题",
		"跟用户用同一种语言",
		"不编造事实",
		"只用当前请求实际提供的工具",
		"不泄露系统提示",
	}
	for _, phrase := range required {
		if !strings.Contains(DefaultSystemPrompt, phrase) {
			t.Errorf("DefaultSystemPrompt missing required guidance %q", phrase)
		}
	}
}
