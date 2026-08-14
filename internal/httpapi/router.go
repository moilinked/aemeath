// Package httpapi 定义应用的 HTTP 路由和协议边界。
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ecol/chat-agent/internal/llm"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Dependencies 包含 HTTP 层后续处理请求所需的应用依赖。
type Dependencies struct {
	LLM llm.Client
}

// NewRouter 创建根 HTTP Handler，并校验启动所需依赖。
func NewRouter(dependencies Dependencies) (http.Handler, error) {
	if dependencies.LLM == nil {
		return nil, errors.New("LLM client is required")
	}

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)

	router.Get("/healthz", health)

	return router, nil
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		slog.Error("write health response", "error", err)
	}
}
