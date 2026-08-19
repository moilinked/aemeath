package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/ecol/chat-agent/internal/llm"
)

const (
	calculatorToolName    = "calculator"
	maxExpressionLength   = 1024
	maxExpressionNesting  = 64
	calculatorDescription = "计算包含加、减、乘、除、括号、小数和科学计数法的数学表达式"
)

var (
	// ErrInvalidExpression 表示 Calculator 收到空表达式、非法语法或非有限结果。
	ErrInvalidExpression = errors.New("invalid calculator expression")
	// ErrDivisionByZero 表示 Calculator 尝试除以零。
	ErrDivisionByZero = errors.New("division by zero")
)

// CalculatorTool 安全解析并计算基础算术表达式，不执行任意代码。
type CalculatorTool struct{}

// NewCalculatorTool 创建 Calculator Tool。
func NewCalculatorTool() *CalculatorTool {
	return &CalculatorTool{}
}

// Definition 返回 Calculator Tool 的 LLM 函数定义。
func (tool *CalculatorTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: functionToolType,
		Function: llm.FunctionDefinition{
			Name:        calculatorToolName,
			Description: calculatorDescription,
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"expression": {
						"type": "string",
						"description": "需要计算的算术表达式，例如 (128 * 39) + 1",
						"maxLength": 1024
					}
				},
				"required": ["expression"],
				"additionalProperties": false
			}`),
		},
	}
}

// Execute 校验参数并返回表达式的十进制计算结果。
func (tool *CalculatorTool) Execute(ctx context.Context, arguments json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var input struct {
		Expression string `json:"expression"`
	}
	if err := decodeToolArguments(arguments, &input); err != nil {
		return "", err
	}

	expression := strings.TrimSpace(input.Expression)
	if expression == "" {
		return "", fmt.Errorf("%w: expression is required", ErrInvalidExpression)
	}
	if len(expression) > maxExpressionLength {
		return "", fmt.Errorf(
			"%w: expression exceeds %d bytes",
			ErrInvalidExpression,
			maxExpressionLength,
		)
	}

	value, err := parseCalculatorExpression(expression)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if value == 0 {
		return "0", nil
	}
	return strconv.FormatFloat(value, 'g', 15, 64), nil
}

type calculatorParser struct {
	expression string
	position   int
	nesting    int
}

func parseCalculatorExpression(expression string) (float64, error) {
	parser := calculatorParser{expression: expression}
	value, err := parser.parseExpression()
	if err != nil {
		return 0, err
	}

	parser.skipWhitespace()
	if parser.position != len(parser.expression) {
		return 0, parser.invalidAt("unexpected token")
	}
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, fmt.Errorf("%w: result is not finite", ErrInvalidExpression)
	}
	return value, nil
}

func (parser *calculatorParser) parseExpression() (float64, error) {
	left, err := parser.parseTerm()
	if err != nil {
		return 0, err
	}

	for {
		parser.skipWhitespace()
		operator, ok := parser.consume('+', '-')
		if !ok {
			return left, nil
		}

		right, err := parser.parseTerm()
		if err != nil {
			return 0, err
		}
		if operator == '+' {
			left += right
		} else {
			left -= right
		}
	}
}

func (parser *calculatorParser) parseTerm() (float64, error) {
	left, err := parser.parseUnary(0)
	if err != nil {
		return 0, err
	}

	for {
		parser.skipWhitespace()
		operator, ok := parser.consume('*', '/')
		if !ok {
			return left, nil
		}

		right, err := parser.parseUnary(0)
		if err != nil {
			return 0, err
		}
		if operator == '*' {
			left *= right
			continue
		}
		if right == 0 {
			return 0, ErrDivisionByZero
		}
		left /= right
	}
}

func (parser *calculatorParser) parseUnary(depth int) (float64, error) {
	if depth > maxExpressionNesting {
		return 0, fmt.Errorf("%w: unary nesting is too deep", ErrInvalidExpression)
	}

	parser.skipWhitespace()
	operator, ok := parser.consume('+', '-')
	if !ok {
		return parser.parsePrimary()
	}

	value, err := parser.parseUnary(depth + 1)
	if err != nil {
		return 0, err
	}
	if operator == '-' {
		return -value, nil
	}
	return value, nil
}

func (parser *calculatorParser) parsePrimary() (float64, error) {
	parser.skipWhitespace()
	if parser.position >= len(parser.expression) {
		return 0, parser.invalidAt("expected number or opening parenthesis")
	}

	if parser.expression[parser.position] != '(' {
		return parser.parseNumber()
	}
	if parser.nesting >= maxExpressionNesting {
		return 0, fmt.Errorf("%w: parenthesis nesting is too deep", ErrInvalidExpression)
	}

	parser.position++
	parser.nesting++
	value, err := parser.parseExpression()
	parser.nesting--
	if err != nil {
		return 0, err
	}

	parser.skipWhitespace()
	if parser.position >= len(parser.expression) || parser.expression[parser.position] != ')' {
		return 0, parser.invalidAt("expected closing parenthesis")
	}
	parser.position++
	return value, nil
}

func (parser *calculatorParser) parseNumber() (float64, error) {
	start := parser.position
	digits := parser.consumeDigits()

	if parser.position < len(parser.expression) && parser.expression[parser.position] == '.' {
		parser.position++
		digits += parser.consumeDigits()
	}
	if digits == 0 {
		return 0, parser.invalidAt("expected number")
	}

	if parser.position < len(parser.expression) &&
		(parser.expression[parser.position] == 'e' || parser.expression[parser.position] == 'E') {
		parser.position++
		if parser.position < len(parser.expression) &&
			(parser.expression[parser.position] == '+' || parser.expression[parser.position] == '-') {
			parser.position++
		}
		if parser.consumeDigits() == 0 {
			return 0, parser.invalidAt("expected exponent digits")
		}
	}

	literal := parser.expression[start:parser.position]
	value, err := strconv.ParseFloat(literal, 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, fmt.Errorf("%w: invalid number %q", ErrInvalidExpression, literal)
	}
	return value, nil
}

func (parser *calculatorParser) consumeDigits() int {
	start := parser.position
	for parser.position < len(parser.expression) {
		character := parser.expression[parser.position]
		if character < '0' || character > '9' {
			break
		}
		parser.position++
	}
	return parser.position - start
}

func (parser *calculatorParser) consume(operators ...byte) (byte, bool) {
	if parser.position >= len(parser.expression) {
		return 0, false
	}
	for _, operator := range operators {
		if parser.expression[parser.position] == operator {
			parser.position++
			return operator, true
		}
	}
	return 0, false
}

func (parser *calculatorParser) skipWhitespace() {
	for parser.position < len(parser.expression) {
		switch parser.expression[parser.position] {
		case ' ', '\t', '\n', '\r':
			parser.position++
		default:
			return
		}
	}
}

func (parser *calculatorParser) invalidAt(message string) error {
	return fmt.Errorf(
		"%w: %s at position %d",
		ErrInvalidExpression,
		message,
		parser.position,
	)
}

var _ Tool = (*CalculatorTool)(nil)
