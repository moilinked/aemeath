package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/ecol/chat-agent/internal/retry"
)

const maxResponseBodySize = 4 << 20

// OpenAICompatibleConfig 配置一个 OpenAI Chat Completions 兼容客户端。
// BaseURL 可填写 OpenAI、DeepSeek 或内部兼容网关的基础地址。
type OpenAICompatibleConfig struct {
	BaseURL     string
	APIKey      string
	Model       string
	HTTPClient  *http.Client
	RetryPolicy retry.Policy
}

// OpenAICompatibleClient 调用 OpenAI 格式的非流式 Chat Completions API。
type OpenAICompatibleClient struct {
	endpoint    string
	apiKey      string
	model       string
	httpClient  *http.Client
	retryPolicy retry.Policy
}

// NewOpenAICompatibleClient 创建 OpenAI 格式兼容客户端。
func NewOpenAICompatibleClient(config OpenAICompatibleConfig) (*OpenAICompatibleClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if err := validateBaseURL(baseURL); err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, errors.New("llm API key is required")
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, errors.New("llm model is required")
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	return &OpenAICompatibleClient{
		endpoint:    baseURL + "/chat/completions",
		apiKey:      config.APIKey,
		model:       config.Model,
		httpClient:  httpClient,
		retryPolicy: retry.Normalize(config.RetryPolicy),
	}, nil
}

// Chat 发送一次非流式对话请求。
func (client *OpenAICompatibleClient) Chat(
	ctx context.Context,
	request ChatRequest,
) (*ChatResponse, error) {
	if len(request.Messages) == 0 {
		return nil, errors.New("llm messages are required")
	}

	payload := chatCompletionRequest{
		Model:           client.model,
		Messages:        request.Messages,
		Tools:           request.Tools,
		ToolChoice:      request.ToolChoice,
		Temperature:     request.Temperature,
		MaxTokens:       request.MaxTokens,
		Thinking:        request.Thinking,
		ReasoningEffort: request.ReasoningEffort,
		Stream:          false,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal llm request: %w", err)
	}

	var response *ChatResponse
	err = retry.Do(ctx, client.retryPolicy, func() error {
		completed, attemptErr := client.sendChat(ctx, body)
		if attemptErr != nil {
			return attemptErr
		}
		response = completed
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (client *OpenAICompatibleClient) sendChat(
	ctx context.Context,
	body []byte,
) (*ChatResponse, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create llm request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+client.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")

	httpResponse, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("send llm request: %w", err)
	}

	responseBody, err := readResponseBody(httpResponse.Body)
	if err != nil {
		return nil, err
	}
	if httpResponse.StatusCode < http.StatusOK || httpResponse.StatusCode >= http.StatusMultipleChoices {
		return nil, decodeAPIError(httpResponse.StatusCode, responseBody)
	}

	var completion chatCompletionResponse
	if err := json.Unmarshal(responseBody, &completion); err != nil {
		return nil, fmt.Errorf("decode llm response: %w", err)
	}
	if len(completion.Choices) == 0 {
		return nil, errors.New("llm response contains no choices")
	}

	choice := completion.Choices[0]
	return &ChatResponse{
		ID:           completion.ID,
		Model:        completion.Model,
		Message:      choice.Message,
		FinishReason: choice.FinishReason,
		Usage:        completion.Usage,
	}, nil
}

type chatCompletionRequest struct {
	Model           string           `json:"model"`
	Messages        []Message        `json:"messages"`
	Tools           []ToolDefinition `json:"tools,omitempty"`
	ToolChoice      json.RawMessage  `json:"tool_choice,omitempty"`
	Temperature     *float64         `json:"temperature,omitempty"`
	MaxTokens       *int             `json:"max_tokens,omitempty"`
	Thinking        *Thinking        `json:"thinking,omitempty"`
	ReasoningEffort string           `json:"reasoning_effort,omitempty"`
	Stream          bool             `json:"stream"`
}

type chatCompletionResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

// APIError 表示模型服务返回的非成功响应。
type APIError struct {
	StatusCode int
	Message    string
	Type       string
	Code       string
}

// Error 返回适合日志记录的模型服务错误。
func (err *APIError) Error() string {
	if err.Code != "" {
		return fmt.Sprintf("llm API error: status=%d code=%s message=%s", err.StatusCode, err.Code, err.Message)
	}
	return fmt.Sprintf("llm API error: status=%d message=%s", err.StatusCode, err.Message)
}

// HTTPStatus 返回模型服务的 HTTP 状态码，供指数重试判断。
func (err *APIError) HTTPStatus() int {
	return err.StatusCode
}

func validateBaseURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("parse llm base URL: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("llm base URL must be an absolute HTTP or HTTPS URL")
	}
	return nil
}

func readResponseBody(body io.ReadCloser) ([]byte, error) {
	responseBody, readErr := io.ReadAll(io.LimitReader(body, maxResponseBodySize+1))
	closeErr := body.Close()

	if readErr != nil {
		return nil, fmt.Errorf("read llm response: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close llm response: %w", closeErr)
	}
	if len(responseBody) > maxResponseBodySize {
		return nil, fmt.Errorf("llm response exceeds %d bytes", maxResponseBodySize)
	}
	return responseBody, nil
}

func decodeAPIError(statusCode int, body []byte) error {
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &payload); err == nil && payload.Error.Message != "" {
		return &APIError{
			StatusCode: statusCode,
			Message:    payload.Error.Message,
			Type:       payload.Error.Type,
			Code:       payload.Error.Code,
		}
	}

	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(statusCode)
	}
	return &APIError{StatusCode: statusCode, Message: message}
}
