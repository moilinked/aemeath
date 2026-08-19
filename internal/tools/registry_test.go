package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/ecol/chat-agent/internal/llm"
	"github.com/ecol/chat-agent/internal/tools"
)

type fakeTool struct {
	definition llm.ToolDefinition
	execute    func(context.Context, json.RawMessage) (string, error)
}

func (tool *fakeTool) Definition() llm.ToolDefinition {
	return tool.definition
}

func (tool *fakeTool) Execute(ctx context.Context, arguments json.RawMessage) (string, error) {
	if tool.execute == nil {
		return "", nil
	}
	return tool.execute(ctx, arguments)
}

func TestRegistryRegisterAndLookup(t *testing.T) {
	first := &fakeTool{definition: toolDefinition("first")}
	second := &fakeTool{definition: toolDefinition("second")}

	registry, err := tools.NewRegistry(first, second)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	tests := []struct {
		name     string
		toolName string
		want     tools.Tool
		wantOK   bool
	}{
		{name: "first tool", toolName: "first", want: first, wantOK: true},
		{name: "second tool", toolName: "second", want: second, wantOK: true},
		{name: "missing tool", toolName: "missing", want: nil, wantOK: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := registry.Lookup(test.toolName)
			if ok != test.wantOK {
				t.Fatalf("Lookup() ok = %t, want %t", ok, test.wantOK)
			}
			if got != test.want {
				t.Fatalf("Lookup() tool = %v, want %v", got, test.want)
			}
		})
	}
}

func TestRegistryRejectsInvalidTools(t *testing.T) {
	tests := []struct {
		name string
		tool tools.Tool
	}{
		{name: "nil tool", tool: nil},
		{
			name: "unsupported type",
			tool: &fakeTool{definition: llm.ToolDefinition{
				Type:     "custom",
				Function: toolDefinition("valid").Function,
			}},
		},
		{name: "empty name", tool: &fakeTool{definition: toolDefinition("")}},
		{name: "name with spaces", tool: &fakeTool{definition: toolDefinition("invalid name")}},
		{name: "name with punctuation", tool: &fakeTool{definition: toolDefinition("invalid.name")}},
		{
			name: "invalid parameters",
			tool: &fakeTool{definition: llm.ToolDefinition{
				Type: "function",
				Function: llm.FunctionDefinition{
					Name:       "valid",
					Parameters: json.RawMessage(`[]`),
				},
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry, err := tools.NewRegistry()
			if err != nil {
				t.Fatalf("NewRegistry() error = %v", err)
			}

			err = registry.Register(test.tool)
			if !errors.Is(err, tools.ErrInvalidTool) {
				t.Fatalf("Register() error = %v, want ErrInvalidTool", err)
			}
		})
	}
}

func TestRegistryRejectsDuplicateTool(t *testing.T) {
	registry, err := tools.NewRegistry(&fakeTool{definition: toolDefinition("duplicate")})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	err = registry.Register(&fakeTool{definition: toolDefinition("duplicate")})
	if !errors.Is(err, tools.ErrDuplicateTool) {
		t.Fatalf("Register() error = %v, want ErrDuplicateTool", err)
	}
}

func TestRegistryDefinitionsAreOrderedSnapshots(t *testing.T) {
	first := &fakeTool{definition: toolDefinition("first")}
	second := &fakeTool{definition: toolDefinition("second")}
	registry, err := tools.NewRegistry(first, second)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	first.definition.Function.Parameters[0] = '['
	definitions := registry.Definitions()
	if got := definitionNames(definitions); !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("Definitions() names = %v, want [first second]", got)
	}
	if string(definitions[0].Function.Parameters) != `{"type":"object"}` {
		t.Fatalf("Definitions() parameters = %s, want original schema", definitions[0].Function.Parameters)
	}

	definitions[0].Function.Parameters[0] = '['
	reloaded := registry.Definitions()
	if string(reloaded[0].Function.Parameters) != `{"type":"object"}` {
		t.Fatalf("Definitions() after snapshot mutation = %s, want original schema", reloaded[0].Function.Parameters)
	}
}

func TestRegistryExecute(t *testing.T) {
	var gotArguments string
	executable := &fakeTool{
		definition: toolDefinition("echo"),
		execute: func(_ context.Context, arguments json.RawMessage) (string, error) {
			gotArguments = string(arguments)
			return "result", nil
		},
	}
	registry, err := tools.NewRegistry(executable)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	result, err := registry.Execute(context.Background(), "echo", `{"value":"hello"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result != "result" {
		t.Fatalf("Execute() result = %q, want %q", result, "result")
	}
	if gotArguments != `{"value":"hello"}` {
		t.Fatalf("Execute() arguments = %s, want original arguments", gotArguments)
	}
}

func TestRegistryExecuteErrors(t *testing.T) {
	executeError := errors.New("tool failed")
	registry, err := tools.NewRegistry(&fakeTool{
		definition: toolDefinition("failing"),
		execute: func(context.Context, json.RawMessage) (string, error) {
			return "", executeError
		},
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	tests := []struct {
		name      string
		ctx       context.Context
		toolName  string
		arguments string
		wantError error
	}{
		{
			name:      "missing tool",
			ctx:       context.Background(),
			toolName:  "missing",
			arguments: `{}`,
			wantError: tools.ErrToolNotFound,
		},
		{
			name:      "invalid JSON",
			ctx:       context.Background(),
			toolName:  "failing",
			arguments: `{`,
			wantError: tools.ErrInvalidArguments,
		},
		{
			name:      "non-object arguments",
			ctx:       context.Background(),
			toolName:  "failing",
			arguments: `[]`,
			wantError: tools.ErrInvalidArguments,
		},
		{
			name:      "tool execution",
			ctx:       context.Background(),
			toolName:  "failing",
			arguments: `{}`,
			wantError: executeError,
		},
		{
			name:      "canceled context",
			ctx:       canceledContext(),
			toolName:  "failing",
			arguments: `{}`,
			wantError: context.Canceled,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := registry.Execute(test.ctx, test.toolName, test.arguments)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("Execute() error = %v, want %v", err, test.wantError)
			}
		})
	}
}

func TestRegistryConcurrentRegistrationAndReads(t *testing.T) {
	const toolCount = 64

	registry, err := tools.NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, toolCount)
	var waitGroup sync.WaitGroup

	for index := 0; index < toolCount; index++ {
		waitGroup.Add(1)
		go func(value int) {
			defer waitGroup.Done()
			<-start

			name := fmt.Sprintf("tool_%d", value)
			errs <- registry.Register(&fakeTool{definition: toolDefinition(name)})
			registry.Lookup(name)
			registry.Definitions()
		}(index)
	}

	close(start)
	waitGroup.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("Register() concurrent error = %v", err)
		}
	}
	if got := len(registry.Definitions()); got != toolCount {
		t.Fatalf("Definitions() count = %d, want %d", got, toolCount)
	}
}

func toolDefinition(name string) llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.FunctionDefinition{
			Name:        name,
			Description: "test tool",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		},
	}
}

func definitionNames(definitions []llm.ToolDefinition) []string {
	names := make([]string, len(definitions))
	for index, definition := range definitions {
		names[index] = definition.Function.Name
	}
	return names
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
