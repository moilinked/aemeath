package postgres

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/ecol/chat-agent/internal/llm"
)

func TestDecodeMessages(t *testing.T) {
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
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	got, err := decodeMessages(raw)
	if err != nil {
		t.Fatalf("decodeMessages() error = %v", err)
	}
	if !reflect.DeepEqual(got, source) {
		t.Fatalf("decodeMessages() = %#v, want %#v", got, source)
	}

	empty, err := decodeMessages([]byte("[]"))
	if err != nil {
		t.Fatalf("decodeMessages() empty error = %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("decodeMessages() empty length = %d, want 0", len(empty))
	}
}

func TestSplitSQLStatements(t *testing.T) {
	statements := splitSQLStatements(`
		CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY);
		CREATE TABLE IF NOT EXISTS sessions (id TEXT PRIMARY KEY);
	`)
	if len(statements) != 2 {
		t.Fatalf("splitSQLStatements() = %d, want 2", len(statements))
	}
}

func TestOpenRejectsInvalidURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "empty"},
		{name: "invalid", url: "://not-a-url"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Open(t.Context(), test.url)
			if err == nil {
				t.Fatal("Open() error = nil, want an error")
			}
			if test.url != "" && strings.Contains(err.Error(), test.url) {
				t.Fatal("Open() error exposes DATABASE_URL")
			}
		})
	}
}
