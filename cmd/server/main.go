package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/ecol/chat-agent/internal/config"
	"github.com/ecol/chat-agent/internal/httpapi"
	"github.com/ecol/chat-agent/internal/llm"
	"github.com/ecol/chat-agent/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	llmClient, err := newLLMClient(cfg.LLM)
	if err != nil {
		return err
	}

	router, err := httpapi.NewRouter(httpapi.Dependencies{LLM: llmClient})
	if err != nil {
		return fmt.Errorf("create HTTP router: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("starting HTTP server", "address", cfg.Address)
	return server.Run(ctx, cfg, router)
}

func newLLMClient(cfg config.LLMConfig) (llm.Client, error) {
	if cfg.RequestTimeout <= 0 {
		return nil, errors.New("LLM request timeout must be greater than zero")
	}

	client, err := llm.NewOpenAICompatibleClient(llm.OpenAICompatibleConfig{
		BaseURL: cfg.BaseURL,
		APIKey:  cfg.APIKey,
		Model:   cfg.Model,
		HTTPClient: &http.Client{
			Timeout: cfg.RequestTimeout,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create LLM client: %w", err)
	}

	return client, nil
}
