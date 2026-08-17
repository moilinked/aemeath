//go:build integration

package llm_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ecol/chat-agent/internal/llm"
	"github.com/joho/godotenv"
)

func TestDeepSeekConnectivity(t *testing.T) {
	loadProjectEnv(t)

	apiKey := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	if apiKey == "" {
		t.Skip("DEEPSEEK_API_KEY is not configured")
	}

	timeout := envDuration(t, "LLM_REQUEST_TIMEOUT", 60*time.Second)
	client, err := llm.NewOpenAICompatibleClient(llm.OpenAICompatibleConfig{
		BaseURL: envOrDefault("DEEPSEEK_BASE_URL", llm.DeepSeekBaseURL),
		APIKey:  apiKey,
		Model:   envOrDefault("DEEPSEEK_MODEL", llm.DeepSeekV4Pro),
		HTTPClient: &http.Client{
			Timeout: timeout,
		},
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleClient() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	temperature := 0.0
	maxTokens := 8
	response, err := client.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "只回复 OK"},
		},
		Temperature: &temperature,
		MaxTokens:   &maxTokens,
		Thinking:    &llm.Thinking{Type: "disabled"},
	})
	if err != nil {
		t.Fatalf("DeepSeek Chat() error = %v", err)
	}
	if strings.TrimSpace(response.Message.Content) == "" {
		t.Fatal("DeepSeek Chat() returned empty content")
	}
	if response.Usage.TotalTokens <= 0 {
		t.Fatalf("DeepSeek Chat() TotalTokens = %d, want greater than zero", response.Usage.TotalTokens)
	}
}

func loadProjectEnv(t *testing.T) {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve integration test path")
	}

	envPath := filepath.Join(filepath.Dir(filename), "..", "..", ".env")
	if err := godotenv.Load(envPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("load project .env: %v", err)
	}
}

func envDuration(t *testing.T, name string, fallback time.Duration) time.Duration {
	t.Helper()

	value := os.Getenv(name)
	if value == "" {
		return fallback
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return duration
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
