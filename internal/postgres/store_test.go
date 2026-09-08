package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/ecol/chat-agent/internal/auth"
	"github.com/ecol/chat-agent/internal/llm"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func TestUserAndSessionStore(t *testing.T) {
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
	if found.Username != username {
		t.Fatalf("FindByUsername() username = %q, want %q", found.Username, username)
	}
	if err := bcrypt.CompareHashAndPassword(found.PasswordHash, []byte("test-password")); err != nil {
		t.Fatalf("stored password hash does not match: %v", err)
	}

	_, err = users.FindByUsername(ctx, username+"-missing")
	if !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("FindByUsername() missing error = %v, want ErrUserNotFound", err)
	}

	sessions := NewSessionStore(pool)
	sessionID := fmt.Sprintf("session-%s", t.Name())
	if err := sessions.Delete(ctx, sessionID); err != nil {
		t.Fatalf("Delete() cleanup error = %v", err)
	}

	missing, err := sessions.Load(ctx, sessionID)
	if err != nil {
		t.Fatalf("Load() missing error = %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("Load() missing length = %d, want 0", len(missing))
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
	if err := sessions.Append(ctx, sessionID, first); err != nil {
		t.Fatalf("Append() first error = %v", err)
	}
	if err := sessions.Append(ctx, sessionID, second); err != nil {
		t.Fatalf("Append() second error = %v", err)
	}

	got, err := sessions.Load(ctx, sessionID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []llm.Message{first, second}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}

	if err := sessions.Delete(ctx, sessionID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	cleared, err := sessions.Load(ctx, sessionID)
	if err != nil {
		t.Fatalf("Load() after delete error = %v", err)
	}
	if len(cleared) != 0 {
		t.Fatalf("Load() after delete length = %d, want 0", len(cleared))
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

	sessions := NewSessionStore(pool)
	if _, err := sessions.Load(ctx, "session"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load() error = %v, want context.Canceled", err)
	}
	if err := sessions.Append(ctx, "session", llm.Message{Role: llm.RoleUser, Content: "x"}); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("Append() error = %v, want context.Canceled", err)
	}
	if err := sessions.Delete(ctx, "session"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete() error = %v, want context.Canceled", err)
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
