package agent

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/ecol/chat-agent/internal/llm"
	"github.com/ecol/chat-agent/internal/tools"
)

type stubLLMClient struct{}

func (client *stubLLMClient) Chat(
	context.Context,
	llm.ChatRequest,
) (*llm.ChatResponse, error) {
	return nil, nil
}

func (client *stubLLMClient) ChatStream(
	ctx context.Context,
	request llm.ChatRequest,
	emit llm.StreamHandler,
) (*llm.ChatResponse, error) {
	return llm.ChatStreamFromChat(ctx, client.Chat, request, emit)
}

type stubConversationStore struct{}

func (store *stubConversationStore) Load(context.Context, string) ([]llm.Message, error) {
	return nil, nil
}

func (store *stubConversationStore) Append(context.Context, string, ...llm.Message) error {
	return nil
}

func (store *stubConversationStore) Create(context.Context, string, string) (Conversation, error) {
	return Conversation{}, nil
}

func (store *stubConversationStore) GetForUser(context.Context, string, string) (Conversation, []llm.Message, error) {
	return Conversation{}, nil, nil
}

func (store *stubConversationStore) ListForUser(context.Context, string) ([]Conversation, error) {
	return nil, nil
}

func (store *stubConversationStore) UpdateTitleForUser(context.Context, string, string, string) (Conversation, error) {
	return Conversation{}, nil
}

func (store *stubConversationStore) ClearMessagesForUser(context.Context, string, string) (Conversation, error) {
	return Conversation{}, nil
}

func (store *stubConversationStore) DeleteForUser(context.Context, string, string) error {
	return nil
}

func TestNew(t *testing.T) {
	llmClient := &stubLLMClient{}
	store := &stubConversationStore{}
	toolRegistry := newTestToolRegistry(t)

	created, err := New(Config{
		LLM:           llmClient,
		Conversations: store,
		Tools:         toolRegistry,
		MaxSteps:      8,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if created.llmClient != llmClient {
		t.Error("New() did not retain LLM client")
	}
	if created.conversations != store {
		t.Error("New() did not retain conversation store")
	}
	if created.toolRegistry != toolRegistry {
		t.Error("New() did not retain tool registry")
	}
	if created.systemMessage.Role != llm.RoleSystem ||
		created.systemMessage.Content != DefaultSystemPrompt {
		t.Fatalf("New() system message = %#v, want default System Prompt", created.systemMessage)
	}
	if created.MaxSteps() != 8 {
		t.Fatalf("MaxSteps() = %d, want 8", created.MaxSteps())
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*Config)
		errorPhrase string
	}{
		{
			name: "missing LLM client",
			mutate: func(config *Config) {
				config.LLM = nil
			},
			errorPhrase: "LLM client",
		},
		{
			name: "missing conversation store",
			mutate: func(config *Config) {
				config.Conversations = nil
			},
			errorPhrase: "conversation store",
		},
		{
			name: "missing tool registry",
			mutate: func(config *Config) {
				config.Tools = nil
			},
			errorPhrase: "tool registry",
		},
		{
			name: "zero max steps",
			mutate: func(config *Config) {
				config.MaxSteps = 0
			},
			errorPhrase: "max steps",
		},
		{
			name: "negative context tokens",
			mutate: func(config *Config) {
				config.ContextTokens = -1
			},
			errorPhrase: "context tokens",
		},
		{
			name: "negative max output tokens",
			mutate: func(config *Config) {
				config.MaxOutputTokens = -1
			},
			errorPhrase: "max output tokens",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validAgentConfig(t)
			test.mutate(&config)

			_, err := New(config)
			if err == nil {
				t.Fatal("New() error = nil, want an error")
			}
			if !strings.Contains(err.Error(), test.errorPhrase) {
				t.Fatalf("New() error = %q, want phrase %q", err, test.errorPhrase)
			}
		})
	}
}

func TestAgentMaxSteps(t *testing.T) {
	tests := []int{1, 8, 100}
	for _, maxSteps := range tests {
		t.Run(strconv.Itoa(maxSteps), func(t *testing.T) {
			config := validAgentConfig(t)
			config.MaxSteps = maxSteps

			created, err := New(config)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if created.MaxSteps() != maxSteps {
				t.Fatalf("MaxSteps() = %d, want %d", created.MaxSteps(), maxSteps)
			}
		})
	}
}

func validAgentConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		LLM:           &stubLLMClient{},
		Conversations: &stubConversationStore{},
		Tools:         newTestToolRegistry(t),
		MaxSteps:      8,
	}
}

func newTestToolRegistry(t *testing.T) *tools.Registry {
	t.Helper()

	registry, err := tools.NewRegistry()
	if err != nil {
		t.Fatalf("tools.NewRegistry() error = %v", err)
	}
	return registry
}
