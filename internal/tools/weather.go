package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ecol/chat-agent/internal/llm"
	"github.com/ecol/chat-agent/internal/retry"
)

const (
	weatherToolName           = "weather"
	weatherDescription        = "查询指定城市的当前天气，数据源为 Open-Meteo"
	openMeteoGeocodingURL     = "https://geocoding-api.open-meteo.com/v1/search"
	openMeteoForecastURL      = "https://api.open-meteo.com/v1/forecast"
	defaultWeatherTimeout     = 10 * time.Second
	maxWeatherLocationLength  = 200
	maxWeatherResponseBodyLen = 1 << 20
)

var (
	// ErrLocationNotFound 表示 Open-Meteo 无法解析指定地点。
	ErrLocationNotFound = errors.New("weather location not found")
	// ErrWeatherProvider 表示天气服务请求或响应失败。
	ErrWeatherProvider = errors.New("weather provider error")

	defaultWeatherHTTPClient = &http.Client{Timeout: defaultWeatherTimeout}
	weatherRetryPolicy       = retry.Policy{
		MaxAttempts:     3,
		InitialInterval: 100 * time.Millisecond,
		MaxInterval:     time.Second,
	}
)

// WeatherTool 通过 Open-Meteo 查询地点的当前天气。
type WeatherTool struct {
	httpClient *http.Client
}

// NewWeatherTool 创建 Weather Tool。传入 nil 时使用带默认超时的 HTTP Client。
func NewWeatherTool(httpClient *http.Client) *WeatherTool {
	if httpClient == nil {
		httpClient = defaultWeatherHTTPClient
	}
	return &WeatherTool{httpClient: httpClient}
}

// Definition 返回 Weather Tool 的 LLM 函数定义。
func (tool *WeatherTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: functionToolType,
		Function: llm.FunctionDefinition{
			Name:        weatherToolName,
			Description: weatherDescription,
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"location": {
						"type": "string",
						"description": "城市或地点名称，例如 上海、London",
						"maxLength": 200
					}
				},
				"required": ["location"],
				"additionalProperties": false
			}`),
		},
	}
}

// Execute 解析地点并返回当前天气的 JSON 文本。
func (tool *WeatherTool) Execute(ctx context.Context, arguments json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var input struct {
		Location string `json:"location"`
	}
	if err := decodeToolArguments(arguments, &input); err != nil {
		return "", err
	}

	locationName := strings.TrimSpace(input.Location)
	if locationName == "" {
		return "", fmt.Errorf("%w: location is required", ErrInvalidArguments)
	}
	if len(locationName) > maxWeatherLocationLength {
		return "", fmt.Errorf(
			"%w: location exceeds %d bytes",
			ErrInvalidArguments,
			maxWeatherLocationLength,
		)
	}

	location, err := tool.geocode(ctx, locationName)
	if err != nil {
		return "", err
	}
	forecast, err := tool.currentWeather(ctx, location.Latitude, location.Longitude)
	if err != nil {
		return "", err
	}

	result, err := json.Marshal(weatherResult{
		Location:                location.Name,
		AdministrativeArea:      location.Admin1,
		Country:                 location.Country,
		CountryCode:             location.CountryCode,
		Latitude:                location.Latitude,
		Longitude:               location.Longitude,
		Timezone:                forecast.Timezone,
		CurrentTime:             forecast.Current.Time,
		Condition:               weatherCodeDescription(forecast.Current.WeatherCode),
		WeatherCode:             forecast.Current.WeatherCode,
		TemperatureCelsius:      forecast.Current.Temperature,
		ApparentTemperatureC:    forecast.Current.ApparentTemperature,
		RelativeHumidityPercent: forecast.Current.RelativeHumidity,
		WindSpeedKmh:            forecast.Current.WindSpeed,
		Source:                  "Open-Meteo",
	})
	if err != nil {
		return "", fmt.Errorf("encode weather result: %w", err)
	}
	return string(result), nil
}

type geocodingResponse struct {
	Results []weatherLocation `json:"results"`
}

type weatherLocation struct {
	Name        string  `json:"name"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	Country     string  `json:"country"`
	CountryCode string  `json:"country_code"`
	Admin1      string  `json:"admin1"`
}

type weatherForecast struct {
	Timezone string         `json:"timezone"`
	Current  currentWeather `json:"current"`
}

type currentWeather struct {
	Time                string  `json:"time"`
	Temperature         float64 `json:"temperature_2m"`
	ApparentTemperature float64 `json:"apparent_temperature"`
	RelativeHumidity    float64 `json:"relative_humidity_2m"`
	WeatherCode         int     `json:"weather_code"`
	WindSpeed           float64 `json:"wind_speed_10m"`
}

type weatherResult struct {
	Location                string  `json:"location"`
	AdministrativeArea      string  `json:"administrative_area,omitempty"`
	Country                 string  `json:"country,omitempty"`
	CountryCode             string  `json:"country_code,omitempty"`
	Latitude                float64 `json:"latitude"`
	Longitude               float64 `json:"longitude"`
	Timezone                string  `json:"timezone"`
	CurrentTime             string  `json:"current_time"`
	Condition               string  `json:"condition"`
	WeatherCode             int     `json:"weather_code"`
	TemperatureCelsius      float64 `json:"temperature_celsius"`
	ApparentTemperatureC    float64 `json:"apparent_temperature_celsius"`
	RelativeHumidityPercent float64 `json:"relative_humidity_percent"`
	WindSpeedKmh            float64 `json:"wind_speed_kmh"`
	Source                  string  `json:"source"`
}

func (tool *WeatherTool) geocode(
	ctx context.Context,
	locationName string,
) (weatherLocation, error) {
	endpoint, err := url.Parse(openMeteoGeocodingURL)
	if err != nil {
		return weatherLocation{}, fmt.Errorf("%w: build geocoding URL: %w", ErrWeatherProvider, err)
	}

	query := endpoint.Query()
	query.Set("name", locationName)
	query.Set("count", "1")
	query.Set("language", "zh")
	query.Set("format", "json")
	endpoint.RawQuery = query.Encode()

	var response geocodingResponse
	if err := tool.getJSON(ctx, endpoint.String(), &response); err != nil {
		return weatherLocation{}, err
	}
	if len(response.Results) == 0 {
		return weatherLocation{}, fmt.Errorf("%w: %s", ErrLocationNotFound, locationName)
	}
	return response.Results[0], nil
}

func (tool *WeatherTool) currentWeather(
	ctx context.Context,
	latitude float64,
	longitude float64,
) (weatherForecast, error) {
	endpoint, err := url.Parse(openMeteoForecastURL)
	if err != nil {
		return weatherForecast{}, fmt.Errorf("%w: build forecast URL: %w", ErrWeatherProvider, err)
	}

	query := endpoint.Query()
	query.Set("latitude", strconv.FormatFloat(latitude, 'f', -1, 64))
	query.Set("longitude", strconv.FormatFloat(longitude, 'f', -1, 64))
	query.Set(
		"current",
		"temperature_2m,apparent_temperature,relative_humidity_2m,weather_code,wind_speed_10m",
	)
	query.Set("temperature_unit", "celsius")
	query.Set("wind_speed_unit", "kmh")
	query.Set("timezone", "auto")
	endpoint.RawQuery = query.Encode()

	var forecast weatherForecast
	if err := tool.getJSON(ctx, endpoint.String(), &forecast); err != nil {
		return weatherForecast{}, err
	}
	if forecast.Current.Time == "" {
		return weatherForecast{}, fmt.Errorf("%w: forecast contains no current weather", ErrWeatherProvider)
	}
	return forecast, nil
}

func (tool *WeatherTool) getJSON(ctx context.Context, endpoint string, target any) error {
	return retry.Do(ctx, weatherRetryPolicy, func() error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return fmt.Errorf("%w: create request: %w", ErrWeatherProvider, err)
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", "chat-agent")

		httpClient := tool.httpClient
		if httpClient == nil {
			httpClient = defaultWeatherHTTPClient
		}
		response, err := httpClient.Do(request)
		if err != nil {
			return fmt.Errorf("%w: request failed: %w", ErrWeatherProvider, err)
		}
		body, err := readWeatherResponseBody(response.Body)
		if err != nil {
			return err
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return decodeWeatherProviderError(response.StatusCode, body)
		}
		if err := json.Unmarshal(body, target); err != nil {
			return fmt.Errorf("%w: decode response: %w", ErrWeatherProvider, err)
		}
		return nil
	})
}

func readWeatherResponseBody(body io.ReadCloser) ([]byte, error) {
	responseBody, readErr := io.ReadAll(io.LimitReader(body, maxWeatherResponseBodyLen+1))
	closeErr := body.Close()

	if readErr != nil {
		return nil, fmt.Errorf("%w: read response: %w", ErrWeatherProvider, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("%w: close response: %w", ErrWeatherProvider, closeErr)
	}
	if len(responseBody) > maxWeatherResponseBodyLen {
		return nil, fmt.Errorf("%w: response body is too large", ErrWeatherProvider)
	}
	return responseBody, nil
}

func decodeWeatherProviderError(statusCode int, body []byte) error {
	var response struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(body, &response); err == nil && response.Reason != "" {
		return retry.WithStatus(statusCode, fmt.Errorf(
			"%w: status=%d reason=%s",
			ErrWeatherProvider,
			statusCode,
			response.Reason,
		))
	}
	return retry.WithStatus(statusCode, fmt.Errorf("%w: status=%d", ErrWeatherProvider, statusCode))
}

func weatherCodeDescription(code int) string {
	descriptions := map[int]string{
		0:  "晴朗",
		1:  "大致晴朗",
		2:  "局部多云",
		3:  "阴天",
		45: "雾",
		48: "雾凇",
		51: "小毛毛雨",
		53: "中等毛毛雨",
		55: "强毛毛雨",
		56: "轻微冻毛毛雨",
		57: "强冻毛毛雨",
		61: "小雨",
		63: "中雨",
		65: "大雨",
		66: "轻微冻雨",
		67: "强冻雨",
		71: "小雪",
		73: "中雪",
		75: "大雪",
		77: "雪粒",
		80: "小阵雨",
		81: "中等阵雨",
		82: "强阵雨",
		85: "小阵雪",
		86: "强阵雪",
		95: "雷暴",
		96: "伴有小冰雹的雷暴",
		99: "伴有大冰雹的雷暴",
	}
	if description, exists := descriptions[code]; exists {
		return description
	}
	return "未知天气"
}

var _ Tool = (*WeatherTool)(nil)
