package conversation_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/ecol/chat-agent/internal/agent"
	"github.com/ecol/chat-agent/internal/conversation"
	"github.com/ecol/chat-agent/internal/llm"
)

var _ agent.ConversationStore = (*conversation.MemoryStore)(nil)

func TestMemoryStoreCreateLoadAndAppend(t *testing.T) {
	store := conversation.NewMemoryStore()
	ctx := context.Background()

	created, err := store.Create(ctx, "user-1", "hello")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create() id is empty")
	}

	source := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call-1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "calculator",
						Arguments: `{"expression":"1+1"}`,
					},
				},
			},
		},
	}
	if err := store.Append(ctx, created.ID, source...); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	got, err := store.Load(ctx, created.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(got, source) {
		t.Fatalf("Load() = %#v, want %#v", got, source)
	}

	item, messages, err := store.GetForUser(ctx, "user-1", created.ID)
	if err != nil {
		t.Fatalf("GetForUser() error = %v", err)
	}
	if item.ID != created.ID || item.Title != "hello" {
		t.Fatalf("GetForUser() conversation = %#v", item)
	}
	if !reflect.DeepEqual(messages, source) {
		t.Fatalf("GetForUser() messages = %#v, want %#v", messages, source)
	}

	if _, _, err := store.GetForUser(ctx, "user-2", created.ID); !errors.Is(err, agent.ErrConversationNotFound) {
		t.Fatalf("GetForUser() foreign error = %v, want ErrConversationNotFound", err)
	}
}

func TestMemoryStoreListAndDelete(t *testing.T) {
	store := conversation.NewMemoryStore()
	ctx := context.Background()

	first, err := store.Create(ctx, "user-1", "one")
	if err != nil {
		t.Fatalf("Create() first error = %v", err)
	}
	second, err := store.Create(ctx, "user-1", "two")
	if err != nil {
		t.Fatalf("Create() second error = %v", err)
	}
	if err := store.Append(ctx, second.ID, llm.Message{Role: llm.RoleUser, Content: "later"}); err != nil {
		t.Fatalf("Append() second error = %v", err)
	}
	if _, err := store.Create(ctx, "user-2", "other"); err != nil {
		t.Fatalf("Create() other error = %v", err)
	}

	listed, err := store.ListForUser(ctx, "user-1")
	if err != nil {
		t.Fatalf("ListForUser() error = %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("ListForUser() length = %d, want 2", len(listed))
	}
	if listed[0].ID != second.ID {
		t.Fatalf("ListForUser() first = %q, want latest %q", listed[0].ID, second.ID)
	}

	if err := store.DeleteForUser(ctx, "user-1", first.ID); err != nil {
		t.Fatalf("DeleteForUser() error = %v", err)
	}
	if err := store.DeleteForUser(ctx, "user-1", first.ID); !errors.Is(err, agent.ErrConversationNotFound) {
		t.Fatalf("DeleteForUser() missing error = %v, want ErrConversationNotFound", err)
	}

	listed, err = store.ListForUser(ctx, "user-1")
	if err != nil {
		t.Fatalf("ListForUser() after delete error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != second.ID {
		t.Fatalf("ListForUser() after delete = %#v", listed)
	}
}

func TestMemoryStoreCanceledContext(t *testing.T) {
	store := conversation.NewMemoryStore()
	created, err := store.Create(context.Background(), "user-1", "seed")
	if err != nil {
		t.Fatalf("Create() seed error = %v", err)
	}

	tests := []struct {
		name      string
		operation func(context.Context, *conversation.MemoryStore) error
	}{
		{
			name: "load",
			operation: func(ctx context.Context, store *conversation.MemoryStore) error {
				_, err := store.Load(ctx, created.ID)
				return err
			},
		},
		{
			name: "append",
			operation: func(ctx context.Context, store *conversation.MemoryStore) error {
				return store.Append(ctx, created.ID, llm.Message{Role: llm.RoleUser, Content: "x"})
			},
		},
		{
			name: "create",
			operation: func(ctx context.Context, store *conversation.MemoryStore) error {
				_, err := store.Create(ctx, "user-1", "title")
				return err
			},
		},
		{
			name: "get",
			operation: func(ctx context.Context, store *conversation.MemoryStore) error {
				_, _, err := store.GetForUser(ctx, "user-1", created.ID)
				return err
			},
		},
		{
			name: "list",
			operation: func(ctx context.Context, store *conversation.MemoryStore) error {
				_, err := store.ListForUser(ctx, "user-1")
				return err
			},
		},
		{
			name: "delete",
			operation: func(ctx context.Context, store *conversation.MemoryStore) error {
				return store.DeleteForUser(ctx, "user-1", created.ID)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := test.operation(ctx, store); !errors.Is(err, context.Canceled) {
				t.Fatalf("operation error = %v, want context.Canceled", err)
			}
		})
	}
}

func TestMemoryStoreConcurrentAppend(t *testing.T) {
	const writers = 64
	store := conversation.NewMemoryStore()
	created, err := store.Create(context.Background(), "user-1", "concurrent")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, writers)
	var waitGroup sync.WaitGroup
	for index := 0; index < writers; index++ {
		waitGroup.Add(1)
		go func(value int) {
			defer waitGroup.Done()
			<-start
			errs <- store.Append(context.Background(), created.ID, llm.Message{
				Role:    llm.RoleUser,
				Content: fmt.Sprintf("message-%d", value),
			})
		}(index)
	}

	close(start)
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Append() concurrent error = %v", err)
		}
	}

	history, err := store.Load(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(history) != writers {
		t.Fatalf("Load() returned %d messages, want %d", len(history), writers)
	}
}
