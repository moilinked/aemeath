package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/ecol/chat-agent/internal/agent"
	"github.com/ecol/chat-agent/internal/llm"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConversationStore(t *testing.T) {
	pool := testPool(t)
	t.Cleanup(pool.Close)

	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	conversations := NewConversationStore(pool)
	userID := fmt.Sprintf("id-%s", t.Name())
	created, err := conversations.Create(ctx, userID, "hello")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	first := llm.Message{Role: llm.RoleUser, Content: "hello"}
	second := llm.Message{
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
	}
	if err := conversations.Append(ctx, created.ID, first); err != nil {
		t.Fatalf("Append() first error = %v", err)
	}
	if err := conversations.Append(ctx, created.ID, second); err != nil {
		t.Fatalf("Append() second error = %v", err)
	}

	got, err := conversations.Load(ctx, created.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []llm.Message{first, second}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}

	item, messages, err := conversations.GetForUser(ctx, userID, created.ID)
	if err != nil {
		t.Fatalf("GetForUser() error = %v", err)
	}
	if item.ID != created.ID || item.Title != "hello" {
		t.Fatalf("GetForUser() conversation = %#v", item)
	}
	if !reflect.DeepEqual(messages, want) {
		t.Fatalf("GetForUser() messages = %#v, want %#v", messages, want)
	}

	otherID := fmt.Sprintf("other-%s", t.Name())
	if _, _, err := conversations.GetForUser(ctx, otherID, created.ID); !errors.Is(err, agent.ErrConversationNotFound) {
		t.Fatalf("GetForUser() foreign error = %v, want ErrConversationNotFound", err)
	}

	renamed, err := conversations.UpdateTitleForUser(ctx, userID, created.ID, "renamed")
	if err != nil {
		t.Fatalf("UpdateTitleForUser() error = %v", err)
	}
	if renamed.Title != "renamed" {
		t.Fatalf("UpdateTitleForUser() title = %q, want renamed", renamed.Title)
	}
	if !renamed.UpdatedAt.After(item.UpdatedAt) {
		t.Fatalf("UpdateTitleForUser() updated_at = %v, want after %v", renamed.UpdatedAt, item.UpdatedAt)
	}
	if _, err := conversations.UpdateTitleForUser(ctx, otherID, created.ID, "nope"); !errors.Is(err, agent.ErrConversationNotFound) {
		t.Fatalf("UpdateTitleForUser() foreign error = %v, want ErrConversationNotFound", err)
	}
	if _, err := conversations.UpdateTitleForUser(ctx, userID, created.ID, "  "); err == nil {
		t.Fatal("UpdateTitleForUser() empty title error = nil, want an error")
	}

	cleared, err := conversations.ClearMessagesForUser(ctx, userID, created.ID)
	if err != nil {
		t.Fatalf("ClearMessagesForUser() error = %v", err)
	}
	if cleared.ID != created.ID || cleared.Title != "renamed" {
		t.Fatalf("ClearMessagesForUser() conversation = %#v", cleared)
	}
	if !cleared.UpdatedAt.After(renamed.UpdatedAt) {
		t.Fatalf("ClearMessagesForUser() updated_at = %v, want after %v", cleared.UpdatedAt, renamed.UpdatedAt)
	}
	history, err := conversations.Load(ctx, created.ID)
	if err != nil {
		t.Fatalf("Load() after clear error = %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("Load() after clear = %#v, want empty", history)
	}
	item, messages, err = conversations.GetForUser(ctx, userID, created.ID)
	if err != nil {
		t.Fatalf("GetForUser() after clear error = %v", err)
	}
	if item.Title != "renamed" || len(messages) != 0 {
		t.Fatalf("GetForUser() after clear = %#v messages=%#v", item, messages)
	}
	if _, err := conversations.ClearMessagesForUser(ctx, otherID, created.ID); !errors.Is(err, agent.ErrConversationNotFound) {
		t.Fatalf("ClearMessagesForUser() foreign error = %v, want ErrConversationNotFound", err)
	}

	listed, err := conversations.ListForUser(ctx, userID)
	if err != nil {
		t.Fatalf("ListForUser() error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("ListForUser() = %#v", listed)
	}

	if err := conversations.DeleteForUser(ctx, userID, created.ID); err != nil {
		t.Fatalf("DeleteForUser() error = %v", err)
	}
	if _, _, err := conversations.GetForUser(ctx, userID, created.ID); !errors.Is(err, agent.ErrConversationNotFound) {
		t.Fatalf("GetForUser() after delete error = %v, want ErrConversationNotFound", err)
	}
}

func TestStoresHonorCanceledContext(t *testing.T) {
	pool := testPool(t)
	t.Cleanup(pool.Close)
	if err := Migrate(context.Background(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	conversations := NewConversationStore(pool)
	if _, err := conversations.Load(ctx, "conversation"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load() error = %v, want context.Canceled", err)
	}
	if err := conversations.Append(ctx, "conversation", llm.Message{Role: llm.RoleUser, Content: "x"}); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("Append() error = %v, want context.Canceled", err)
	}
	if _, err := conversations.Create(ctx, "user", "title"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Create() error = %v", err)
	}
	if _, _, err := conversations.GetForUser(ctx, "user", "conversation"); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetForUser() error = %v, want context.Canceled", err)
	}
	if _, err := conversations.ListForUser(ctx, "user"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListForUser() error = %v", err)
	}
	if _, err := conversations.UpdateTitleForUser(ctx, "user", "conversation", "title"); !errors.Is(err, context.Canceled) {
		t.Fatalf("UpdateTitleForUser() error = %v", err)
	}
	if _, err := conversations.ClearMessagesForUser(ctx, "user", "conversation"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ClearMessagesForUser() error = %v", err)
	}
	if err := conversations.DeleteForUser(ctx, "user", "conversation"); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeleteForUser() error = %v", err)
	}
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	pool, err := Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	return pool
}
