package session_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/ecol/chat-agent/internal/agent"
	"github.com/ecol/chat-agent/internal/llm"
	"github.com/ecol/chat-agent/internal/session"
)

var _ agent.SessionStore = (*session.MemoryStore)(nil)

func TestMemoryStoreLoadAndAppend(t *testing.T) {
	store := session.NewMemoryStore()
	ctx := context.Background()

	missing, err := store.Load(ctx, "missing")
	if err != nil {
		t.Fatalf("Load() missing session error = %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("Load() missing session returned %d messages, want 0", len(missing))
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
	if err := store.Append(ctx, "session-1", source...); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	want := cloneMessages(source)
	source[0].Content = "changed"
	source[1].ToolCalls[0].Function.Name = "changed"

	got, err := store.Load(ctx, "session-1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}

	got[0].Content = "mutated snapshot"
	got[1].ToolCalls[0].Function.Name = "mutated snapshot"

	reloaded, err := store.Load(ctx, "session-1")
	if err != nil {
		t.Fatalf("Load() after snapshot mutation error = %v", err)
	}
	if !reflect.DeepEqual(reloaded, want) {
		t.Fatalf("Load() after snapshot mutation = %#v, want %#v", reloaded, want)
	}
}

func TestMemoryStoreDelete(t *testing.T) {
	tests := []struct {
		name string
		seed bool
	}{
		{name: "existing session", seed: true},
		{name: "missing session", seed: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := session.NewMemoryStore()
			ctx := context.Background()

			if test.seed {
				if err := store.Append(ctx, "session-1", llm.Message{
					Role:    llm.RoleUser,
					Content: "hello",
				}); err != nil {
					t.Fatalf("Append() error = %v", err)
				}
			}

			if err := store.Delete(ctx, "session-1"); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}

			history, err := store.Load(ctx, "session-1")
			if err != nil {
				t.Fatalf("Load() after Delete() error = %v", err)
			}
			if len(history) != 0 {
				t.Fatalf("Load() after Delete() returned %d messages, want 0", len(history))
			}
		})
	}
}

func TestMemoryStoreCanceledContext(t *testing.T) {
	tests := []struct {
		name      string
		operation func(context.Context, *session.MemoryStore) error
	}{
		{
			name: "load",
			operation: func(ctx context.Context, store *session.MemoryStore) error {
				_, err := store.Load(ctx, "session-1")
				return err
			},
		},
		{
			name: "append",
			operation: func(ctx context.Context, store *session.MemoryStore) error {
				return store.Append(ctx, "session-1", llm.Message{
					Role:    llm.RoleAssistant,
					Content: "must not be stored",
				})
			},
		},
		{
			name: "delete",
			operation: func(ctx context.Context, store *session.MemoryStore) error {
				return store.Delete(ctx, "session-1")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := session.NewMemoryStore()
			seed := llm.Message{Role: llm.RoleUser, Content: "preserved"}
			if err := store.Append(context.Background(), "session-1", seed); err != nil {
				t.Fatalf("Append() seed error = %v", err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			err := test.operation(ctx, store)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("operation error = %v, want context.Canceled", err)
			}

			history, err := store.Load(context.Background(), "session-1")
			if err != nil {
				t.Fatalf("Load() after canceled operation error = %v", err)
			}
			if !reflect.DeepEqual(history, []llm.Message{seed}) {
				t.Fatalf("history after canceled operation = %#v, want seed message", history)
			}
		})
	}
}

func TestMemoryStoreConcurrentAppend(t *testing.T) {
	const writers = 64

	store := session.NewMemoryStore()
	start := make(chan struct{})
	errs := make(chan error, writers)
	var waitGroup sync.WaitGroup

	for index := 0; index < writers; index++ {
		waitGroup.Add(1)
		go func(value int) {
			defer waitGroup.Done()
			<-start

			errs <- store.Append(context.Background(), "session-1", llm.Message{
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

	history, err := store.Load(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(history) != writers {
		t.Fatalf("Load() returned %d messages, want %d", len(history), writers)
	}

	contents := make(map[string]struct{}, writers)
	for _, message := range history {
		contents[message.Content] = struct{}{}
	}
	for index := 0; index < writers; index++ {
		content := fmt.Sprintf("message-%d", index)
		if _, ok := contents[content]; !ok {
			t.Errorf("Load() missing %q", content)
		}
	}
}

func cloneMessages(messages []llm.Message) []llm.Message {
	cloned := make([]llm.Message, len(messages))
	copy(cloned, messages)
	for index := range cloned {
		cloned[index].ToolCalls = append([]llm.ToolCall(nil), messages[index].ToolCalls...)
	}
	return cloned
}
