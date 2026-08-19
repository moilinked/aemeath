package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ecol/chat-agent/internal/tools"
)

func TestCalculatorToolExecute(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{name: "operator precedence", expression: "128 * 39", want: "4992"},
		{name: "parentheses", expression: "(2 + 3) * 4", want: "20"},
		{name: "unary operators", expression: "-(-2) + +3", want: "5"},
		{name: "decimal numbers", expression: ".5 + 1.25", want: "1.75"},
		{name: "scientific notation", expression: "1e3 / 4", want: "250"},
		{name: "left associativity", expression: "8 / 4 / 2", want: "1"},
		{name: "floating point formatting", expression: "0.1 + 0.2", want: "0.3"},
		{name: "negative zero", expression: "-0", want: "0"},
		{name: "whitespace", expression: "\t( 10 - 3 )\n* 2", want: "14"},
	}

	tool := tools.NewCalculatorTool()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := tool.Execute(
				context.Background(),
				calculatorArguments(t, test.expression),
			)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if result != test.want {
				t.Fatalf("Execute() result = %q, want %q", result, test.want)
			}
		})
	}
}

func TestCalculatorToolRejectsInvalidExpressions(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		wantError  error
	}{
		{name: "empty", expression: "", wantError: tools.ErrInvalidExpression},
		{name: "missing operand", expression: "1 +", wantError: tools.ErrInvalidExpression},
		{name: "missing parenthesis", expression: "(1 + 2", wantError: tools.ErrInvalidExpression},
		{name: "unexpected token", expression: "1 2", wantError: tools.ErrInvalidExpression},
		{name: "unsupported operator", expression: "2 ^ 3", wantError: tools.ErrInvalidExpression},
		{name: "invalid exponent", expression: "1e+", wantError: tools.ErrInvalidExpression},
		{name: "non-finite number", expression: "1e309", wantError: tools.ErrInvalidExpression},
		{name: "division by zero", expression: "1 / (2 - 2)", wantError: tools.ErrDivisionByZero},
		{
			name:       "expression too long",
			expression: strings.Repeat("1", 1025),
			wantError:  tools.ErrInvalidExpression,
		},
		{
			name:       "parentheses too deep",
			expression: strings.Repeat("(", 65) + "1" + strings.Repeat(")", 65),
			wantError:  tools.ErrInvalidExpression,
		},
		{
			name:       "unary nesting too deep",
			expression: strings.Repeat("-", 66) + "1",
			wantError:  tools.ErrInvalidExpression,
		},
	}

	tool := tools.NewCalculatorTool()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := tool.Execute(
				context.Background(),
				calculatorArguments(t, test.expression),
			)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("Execute() error = %v, want %v", err, test.wantError)
			}
		})
	}
}

func TestCalculatorToolRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
		wantError error
	}{
		{name: "malformed JSON", arguments: `{`, wantError: tools.ErrInvalidArguments},
		{name: "array", arguments: `[]`, wantError: tools.ErrInvalidArguments},
		{
			name:      "unknown property",
			arguments: `{"expression":"1+1","extra":true}`,
			wantError: tools.ErrInvalidArguments,
		},
		{
			name:      "multiple values",
			arguments: `{"expression":"1+1"} {}`,
			wantError: tools.ErrInvalidArguments,
		},
		{
			name:      "missing expression",
			arguments: `{}`,
			wantError: tools.ErrInvalidExpression,
		},
	}

	tool := tools.NewCalculatorTool()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), json.RawMessage(test.arguments))
			if !errors.Is(err, test.wantError) {
				t.Fatalf("Execute() error = %v, want %v", err, test.wantError)
			}
		})
	}
}

func TestCalculatorToolHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tools.NewCalculatorTool().Execute(ctx, calculatorArguments(t, "1 + 1"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want context.Canceled", err)
	}
}

func TestCalculatorToolRegistryIntegration(t *testing.T) {
	registry, err := tools.NewRegistry(tools.NewCalculatorTool())
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	definitions := registry.Definitions()
	if len(definitions) != 1 {
		t.Fatalf("Definitions() count = %d, want 1", len(definitions))
	}
	if definitions[0].Function.Name != "calculator" {
		t.Fatalf("Definitions() name = %q, want calculator", definitions[0].Function.Name)
	}

	result, err := registry.Execute(
		context.Background(),
		"calculator",
		`{"expression":"6 * 7"}`,
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result != "42" {
		t.Fatalf("Execute() result = %q, want 42", result)
	}
}

func calculatorArguments(t *testing.T, expression string) json.RawMessage {
	t.Helper()

	arguments, err := json.Marshal(struct {
		Expression string `json:"expression"`
	}{
		Expression: expression,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return arguments
}
