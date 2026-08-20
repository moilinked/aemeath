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

	"github.com/ecol/chat-agent/internal/agent"
	"github.com/ecol/chat-agent/internal/auth"
	"github.com/ecol/chat-agent/internal/config"
	"github.com/ecol/chat-agent/internal/httpapi"
	"github.com/ecol/chat-agent/internal/llm"
	"github.com/ecol/chat-agent/internal/server"
	"github.com/ecol/chat-agent/internal/session"
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

	llmClient, err := newLLMClient(cfg.LLM)
	if err != nil {
		return err
	}

	chatAgent, err := newAgent(llmClient, cfg.Agent)
	if err != nil {
		return err
	}

	authService, err := newAuthService(cfg.Auth)
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
	})
	if err != nil {
		return nil, fmt.Errorf("create LLM client: %w", err)
	}

	return client, nil
}

func newAgent(llmClient llm.Client, cfg config.AgentConfig) (*agent.Agent, error) {
	toolRegistry, err := tools.NewRegistry(
		tools.NewCalculatorTool(),
		tools.NewWeatherTool(nil),
	)
	if err != nil {
		return nil, fmt.Errorf("create tool registry: %w", err)
	}

	chatAgent, err := agent.New(agent.Config{
		LLM:      llmClient,
		Sessions: session.NewMemoryStore(),
		Tools:    toolRegistry,
		MaxSteps: cfg.MaxSteps,
	})
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}
	return chatAgent, nil
}

func newAuthService(cfg config.AuthConfig) (*auth.Service, error) {
	// TODO: 接入用户数据库后，在此注入 UserStore，停止从配置装配单用户凭据。
	service, err := auth.New(auth.Config{
		Username:     cfg.Username,
		PasswordHash: []byte(cfg.PasswordHash),
		SigningKey:   []byte(cfg.SigningKey),
		AccessTTL:    cfg.AccessTTL,
		Issuer:       cfg.Issuer,
	})
	if err != nil {
		return nil, fmt.Errorf("create auth service: %w", err)
	}
	return service, nil
}
