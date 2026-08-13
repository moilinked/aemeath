package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ecol/chat-agent/internal/config"
	"github.com/ecol/chat-agent/internal/httpapi"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("starting HTTP server", "address", cfg.Address)
	return server.Run(ctx, cfg, httpapi.NewRouter())
}
