package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/ecol/chat-agent/internal/llm"
)

const (
	functionToolType  = "function"
	maxToolNameLength = 64
)

var (
	// ErrInvalidTool 表示工具或其 LLM 定义不符合注册要求。
	ErrInvalidTool = errors.New("invalid tool")
	// ErrDuplicateTool 表示注册表中已经存在同名工具。
	ErrDuplicateTool = errors.New("duplicate tool")
	// ErrToolNotFound 表示注册表中不存在指定工具。
	ErrToolNotFound = errors.New("tool not found")
	// ErrInvalidArguments 表示模型返回的工具参数不是 JSON 对象。
	ErrInvalidArguments = errors.New("invalid tool arguments")
)

type registryEntry struct {
	tool       Tool
	definition llm.ToolDefinition
}

// Registry 并发安全地管理工具定义、查找与执行。
type Registry struct {
	mu      sync.RWMutex
	entries map[string]registryEntry
	order   []string
}

// NewRegistry 创建注册表，并按传入顺序注册工具。
func NewRegistry(toolset ...Tool) (*Registry, error) {
	registry := &Registry{
		entries: make(map[string]registryEntry, len(toolset)),
		order:   make([]string, 0, len(toolset)),
	}
	for _, tool := range toolset {
		if err := registry.Register(tool); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// Register 注册工具及其定义；同名工具不会覆盖已有工具。
func (registry *Registry) Register(tool Tool) error {
	if tool == nil {
		return fmt.Errorf("%w: tool is nil", ErrInvalidTool)
	}

	definition := cloneDefinition(tool.Definition())
	if err := validateDefinition(definition); err != nil {
		return err
	}
	name := definition.Function.Name

	registry.mu.Lock()
	defer registry.mu.Unlock()

	if registry.entries == nil {
		registry.entries = make(map[string]registryEntry)
	}
	if _, exists := registry.entries[name]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateTool, name)
	}

	registry.entries[name] = registryEntry{
		tool:       tool,
		definition: definition,
	}
	registry.order = append(registry.order, name)
	return nil
}

// Lookup 按名称查找工具。
func (registry *Registry) Lookup(name string) (Tool, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()

	entry, exists := registry.entries[name]
	return entry.tool, exists
}

// Definitions 按注册顺序返回供 LLM 使用的工具定义快照。
func (registry *Registry) Definitions() []llm.ToolDefinition {
	registry.mu.RLock()
	defer registry.mu.RUnlock()

	definitions := make([]llm.ToolDefinition, 0, len(registry.order))
	for _, name := range registry.order {
		definitions = append(definitions, cloneDefinition(registry.entries[name].definition))
	}
	return definitions
}

// Execute 查找并执行指定工具。
func (registry *Registry) Execute(ctx context.Context, name, arguments string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	tool, exists := registry.Lookup(name)
	if !exists {
		return "", fmt.Errorf("%w: %s", ErrToolNotFound, name)
	}

	rawArguments := json.RawMessage(arguments)
	if !isJSONObject(rawArguments) {
		return "", fmt.Errorf("%w: %s", ErrInvalidArguments, name)
	}

	result, err := tool.Execute(ctx, rawArguments)
	if err != nil {
		return "", fmt.Errorf("execute tool %q: %w", name, err)
	}
	return result, nil
}

func validateDefinition(definition llm.ToolDefinition) error {
	if definition.Type != functionToolType {
		return fmt.Errorf("%w: type must be %q", ErrInvalidTool, functionToolType)
	}

	name := definition.Function.Name
	if !validToolName(name) {
		return fmt.Errorf("%w: invalid function name %q", ErrInvalidTool, name)
	}
	if !isJSONObject(definition.Function.Parameters) {
		return fmt.Errorf("%w: parameters for %q must be a JSON object", ErrInvalidTool, name)
	}
	return nil
}

func validToolName(name string) bool {
	if name == "" || len(name) > maxToolNameLength || strings.TrimSpace(name) != name {
		return false
	}
	for _, character := range name {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func isJSONObject(value json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}

func cloneDefinition(definition llm.ToolDefinition) llm.ToolDefinition {
	definition.Function.Parameters = slices.Clone(definition.Function.Parameters)
	return definition
}
