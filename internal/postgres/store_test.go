package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/ecol/chat-agent/internal/agent"
	"github.com/ecol/chat-agent/internal/auth"
	"github.com/ecol/chat-agent/internal/llm"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func TestUserAndConversationStore(t *testing.T) {
	pool := testPool(t)
	t.Cleanup(pool.Close)

	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	username := fmt.Sprintf("user-%s", t.Name())
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}

	if _, err := pool.Exec(
		ctx,
		`INSERT INTO users (id, username, password_hash) VALUES ($1, $2, $3)`,
		fmt.Sprintf("id-%s", t.Name()),
		username,
		string(passwordHash),
	); err != nil {
		t.Fatalf("insert test user: %v", err)
	}

	users := NewUserStore(pool)

	found, err := users.FindByUsername(ctx, username)
	if err != nil {
		t.Fatalf("FindByUsername() error = %v", err)
	}
	wantID := fmt.Sprintf("id-%s", t.Name())
	if found.ID != wantID {
		t.Fatalf("FindByUsername() id = %q, want %q", found.ID, wantID)
	}
	if found.Username != username {
		t.Fatalf("FindByUsername() username = %q, want %q", found.Username, username)
	}
	if found.CreatedAt.IsZero() || found.UpdatedAt.IsZero() {
		t.Fatal("FindByUsername() timestamps are zero")
	}
	if err := bcrypt.CompareHashAndPassword(found.PasswordHash, []byte("test-password")); err != nil {
		t.Fatalf("stored password hash does not match: %v", err)
	}

	_, err = users.FindByUsername(ctx, username+"-missing")
	if !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("FindByUsername() missing error = %v, want ErrUserNotFound", err)
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
	if _, err := pool.Exec(
		ctx,
		`INSERT INTO users (id, username, password_hash) VALUES ($1, $2, $3)`,
		otherID,
		fmt.Sprintf("other-%s", t.Name()),
		string(passwordHash),
	); err != nil {
		t.Fatalf("insert other test user: %v", err)
	}
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

	users := NewUserStore(pool)
	if _, err := users.FindByUsername(ctx, "anyone"); !errors.Is(err, context.Canceled) {
		t.Fatalf("FindByUsername() error = %v, want context.Canceled", err)
	}

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
		t.Fatalf("Create() error = %v, want context.Canceled", err)
	}
	if _, _, err := conversations.GetForUser(ctx, "user", "conversation"); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetForUser() error = %v, want context.Canceled", err)
	}
	if _, err := conversations.ListForUser(ctx, "user"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListForUser() error = %v, want context.Canceled", err)
	}
	if _, err := conversations.UpdateTitleForUser(ctx, "user", "conversation", "title"); !errors.Is(err, context.Canceled) {
		t.Fatalf("UpdateTitleForUser() error = %v, want context.Canceled", err)
	}
	if err := conversations.DeleteForUser(ctx, "user", "conversation"); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeleteForUser() error = %v, want context.Canceled", err)
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
