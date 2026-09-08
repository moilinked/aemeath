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
	"time"

	"github.com/ecol/chat-agent/internal/agent"
	"github.com/ecol/chat-agent/internal/auth"
	"github.com/ecol/chat-agent/internal/config"
	"github.com/ecol/chat-agent/internal/httpapi"
	"github.com/ecol/chat-agent/internal/llm"
	"github.com/ecol/chat-agent/internal/postgres"
	"github.com/ecol/chat-agent/internal/retry"
	"github.com/ecol/chat-agent/internal/server"
	"github.com/ecol/chat-agent/internal/tools"
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

	startCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := postgres.Open(startCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := postgres.Migrate(startCtx, pool); err != nil {
		return fmt.Errorf("migrate postgres: %w", err)
	}

	userStore := postgres.NewUserStore(pool)

	llmClient, err := newLLMClient(cfg.LLM)
	if err != nil {
		return err
	}

	chatAgent, err := newAgent(llmClient, cfg.Agent, postgres.NewSessionStore(pool))
	if err != nil {
		return err
	}

	authService, err := newAuthService(cfg.Auth, userStore)
	if err != nil {
		return err
	}

	router, err := httpapi.NewRouter(httpapi.Dependencies{
		Agent: chatAgent,
		Auth:  authService,
	})
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
		RetryPolicy: retry.Policy{
			MaxAttempts:     cfg.RetryMaxAttempts,
			InitialInterval: cfg.RetryInitialInterval,
			MaxInterval:     cfg.RetryMaxInterval,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create LLM client: %w", err)
	}

	return client, nil
}

func newAgent(
	llmClient llm.Client,
	cfg config.AgentConfig,
	sessions agent.SessionStore,
) (*agent.Agent, error) {
	toolRegistry, err := tools.NewRegistry(
		tools.NewCalculatorTool(),
		tools.NewWeatherTool(nil),
	)
	if err != nil {
		return nil, fmt.Errorf("create tool registry: %w", err)
	}

	chatAgent, err := agent.New(agent.Config{
		LLM:      llmClient,
		Sessions: sessions,
		Tools:    toolRegistry,
		MaxSteps: cfg.MaxSteps,
	})
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}
	return chatAgent, nil
}

func newAuthService(cfg config.AuthConfig, users auth.UserStore) (*auth.Service, error) {
	service, err := auth.New(auth.Config{
		Users:      users,
		SigningKey: []byte(cfg.SigningKey),
		AccessTTL:  cfg.AccessTTL,
		Issuer:     cfg.Issuer,
	})
	if err != nil {
		return nil, fmt.Errorf("create auth service: %w", err)
	}
	return service, nil
}
