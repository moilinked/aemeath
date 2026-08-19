package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ecol/chat-agent/internal/tools"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestWeatherToolExecute(t *testing.T) {
	var requestCount atomic.Int32
	client := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requestCount.Add(1)
			if request.Header.Get("Accept") != "application/json" {
				t.Errorf("Accept header = %q, want application/json", request.Header.Get("Accept"))
			}

			switch request.URL.Host {
			case "geocoding-api.open-meteo.com":
				if request.URL.Path != "/v1/search" {
					t.Errorf("geocoding path = %q, want /v1/search", request.URL.Path)
				}
				if got := request.URL.Query().Get("name"); got != "上海" {
					t.Errorf("geocoding name = %q, want 上海", got)
				}
				if got := request.URL.Query().Get("count"); got != "1" {
					t.Errorf("geocoding count = %q, want 1", got)
				}
				return jsonResponse(http.StatusOK, `{
					"results": [{
						"name": "上海",
						"latitude": 31.22222,
						"longitude": 121.45806,
						"country": "中国",
						"country_code": "CN",
						"admin1": "上海市"
					}]
				}`), nil

			case "api.open-meteo.com":
				query := request.URL.Query()
				if query.Get("latitude") != "31.22222" {
					t.Errorf("forecast latitude = %q, want 31.22222", query.Get("latitude"))
				}
				if query.Get("longitude") != "121.45806" {
					t.Errorf("forecast longitude = %q, want 121.45806", query.Get("longitude"))
				}
				if !strings.Contains(query.Get("current"), "temperature_2m") {
					t.Errorf("forecast current = %q, want temperature_2m", query.Get("current"))
				}
				if query.Get("timezone") != "auto" {
					t.Errorf("forecast timezone = %q, want auto", query.Get("timezone"))
				}
				return jsonResponse(http.StatusOK, `{
					"timezone": "Asia/Shanghai",
					"current": {
						"time": "2026-08-19T10:15",
						"temperature_2m": 31.5,
						"apparent_temperature": 36.2,
						"relative_humidity_2m": 68,
						"weather_code": 95,
						"wind_speed_10m": 12.4
					}
				}`), nil
			default:
				t.Fatalf("unexpected request host %q", request.URL.Host)
				return nil, nil
			}
		}),
	}

	result, err := tools.NewWeatherTool(client).Execute(
		context.Background(),
		json.RawMessage(`{"location":" 上海 "}`),
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if requestCount.Load() != 2 {
		t.Fatalf("request count = %d, want 2", requestCount.Load())
	}

	var got struct {
		Location                string  `json:"location"`
		AdministrativeArea      string  `json:"administrative_area"`
		Country                 string  `json:"country"`
		CountryCode             string  `json:"country_code"`
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
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if got.Location != "上海" || got.AdministrativeArea != "上海市" ||
		got.Country != "中国" || got.CountryCode != "CN" {
		t.Fatalf("location result = %#v, want resolved Shanghai location", got)
	}
	if got.Timezone != "Asia/Shanghai" || got.CurrentTime != "2026-08-19T10:15" {
		t.Fatalf("time result = %#v, want Shanghai current time", got)
	}
	if got.Condition != "雷暴" || got.WeatherCode != 95 {
		t.Fatalf("weather condition = %q code=%d, want 雷暴 code=95", got.Condition, got.WeatherCode)
	}
	if got.TemperatureCelsius != 31.5 || got.ApparentTemperatureC != 36.2 ||
		got.RelativeHumidityPercent != 68 || got.WindSpeedKmh != 12.4 {
		t.Fatalf("weather values = %#v, want provider values", got)
	}
	if got.Source != "Open-Meteo" {
		t.Fatalf("source = %q, want Open-Meteo", got.Source)
	}
}

func TestWeatherToolRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
	}{
		{name: "malformed JSON", arguments: `{`},
		{name: "missing location", arguments: `{}`},
		{name: "blank location", arguments: `{"location":"  "}`},
		{name: "unknown property", arguments: `{"location":"上海","extra":true}`},
		{
			name:      "location too long",
			arguments: `{"location":"` + strings.Repeat("a", 201) + `"}`,
		},
	}

	tool := tools.NewWeatherTool(&http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("HTTP request must not run for invalid arguments")
			return nil, nil
		}),
	})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), json.RawMessage(test.arguments))
			if !errors.Is(err, tools.ErrInvalidArguments) {
				t.Fatalf("Execute() error = %v, want ErrInvalidArguments", err)
			}
		})
	}
}

func TestWeatherToolLocationNotFound(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"results":[]}`), nil
		}),
	}

	_, err := tools.NewWeatherTool(client).Execute(
		context.Background(),
		json.RawMessage(`{"location":"不存在的地点"}`),
	)
	if !errors.Is(err, tools.ErrLocationNotFound) {
		t.Fatalf("Execute() error = %v, want ErrLocationNotFound", err)
	}
}

func TestWeatherToolProviderErrors(t *testing.T) {
	networkError := errors.New("network unavailable")
	tests := []struct {
		name      string
		transport http.RoundTripper
	}{
		{
			name: "HTTP error",
			transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusBadRequest, `{"reason":"invalid request"}`), nil
			}),
		},
		{
			name: "malformed response",
			transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, `{`), nil
			}),
		},
		{
			name: "network error",
			transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, networkError
			}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tool := tools.NewWeatherTool(&http.Client{Transport: test.transport})
			_, err := tool.Execute(
				context.Background(),
				json.RawMessage(`{"location":"上海"}`),
			)
			if !errors.Is(err, tools.ErrWeatherProvider) {
				t.Fatalf("Execute() error = %v, want ErrWeatherProvider", err)
			}
		})
	}
}

func TestWeatherToolRejectsForecastWithoutCurrentWeather(t *testing.T) {
	var requestCount int
	client := &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requestCount++
			if requestCount == 1 {
				return jsonResponse(http.StatusOK, `{
					"results": [{
						"name": "上海",
						"latitude": 31.2,
						"longitude": 121.4
					}]
				}`), nil
			}
			return jsonResponse(http.StatusOK, `{"timezone":"Asia/Shanghai","current":{}}`), nil
		}),
	}

	_, err := tools.NewWeatherTool(client).Execute(
		context.Background(),
		json.RawMessage(`{"location":"上海"}`),
	)
	if !errors.Is(err, tools.ErrWeatherProvider) {
		t.Fatalf("Execute() error = %v, want ErrWeatherProvider", err)
	}
}

func TestWeatherToolHonorsCanceledContext(t *testing.T) {
	var called atomic.Bool
	tool := tools.NewWeatherTool(&http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			called.Store(true)
			return nil, errors.New("unexpected request")
		}),
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tool.Execute(ctx, json.RawMessage(`{"location":"上海"}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want context.Canceled", err)
	}
	if called.Load() {
		t.Fatal("HTTP request ran for canceled context")
	}
}

func TestWeatherToolRegistryIntegration(t *testing.T) {
	registry, err := tools.NewRegistry(tools.NewWeatherTool(nil))
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	definitions := registry.Definitions()
	if len(definitions) != 1 {
		t.Fatalf("Definitions() count = %d, want 1", len(definitions))
	}
	if definitions[0].Function.Name != "weather" {
		t.Fatalf("Definitions() name = %q, want weather", definitions[0].Function.Name)
	}
}

func jsonResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
