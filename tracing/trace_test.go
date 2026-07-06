package tracing

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
)

func TestInitProviderEmptyEndpoint(t *testing.T) {
	shutdown, err := InitProvider(Config{})
	if err != nil {
		t.Fatalf("InitProvider 应返回 nil error: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown 失败: %v", err)
	}
}

func TestInitProvider(t *testing.T) {
	shutdown, err := InitProvider(Config{
		ServiceName: "test-service",
		Sampler: SamplerConfig{
			Type:  "const",
			Param: 1,
		},
		Reporter: ReporterConfig{CollectorEndpoint: "http://localhost:4318/v1/traces"},
	})
	if err != nil {
		t.Fatalf("初始化 OTLP Provider 失败: %v", err)
	}
	defer func() {
		if err := shutdown(context.Background()); err != nil {
			t.Errorf("关闭 tracer provider 失败: %v", err)
		}
	}()
	tp := otel.GetTracerProvider()
	if tp == nil {
		t.Fatal("全局 TracerProvider 未设置")
	}
	ctx := context.Background()
	ctx, span := Start(ctx, "root-operation")
	if span == nil {
		t.Fatal("未创建 span")
	}
	defer span.End()
	traceID := TraceID(ctx)
	if traceID == "" {
		t.Error("TraceID 不应为空")
	}
	ctx2, subSpan := Start(ctx, "child-operation")
	time.Sleep(50 * time.Millisecond)
	subSpan.End()
	traceID2 := TraceID(ctx2)
	if traceID != traceID2 {
		t.Errorf("TraceID 不一致: root=%s child=%s", traceID, traceID2)
	}
}

func TestResolveOTLPTraceHTTPSettings(t *testing.T) {
	authHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte("otel:secret"))
	tests := []struct {
		name     string
		cfg      ReporterConfig
		want     otlpTraceHTTPSettings
		wantFail bool
	}{
		{
			name: "完整 HTTP URL 保持向后兼容",
			cfg: ReporterConfig{
				CollectorEndpoint: "http://localhost:4318/v1/traces",
			},
			want: otlpTraceHTTPSettings{
				useFullURL: true,
				fullURL:    "http://localhost:4318/v1/traces",
				insecure:   false,
				headers:    map[string]string{},
			},
		},
		{
			name: "完整 HTTPS URL",
			cfg: ReporterConfig{
				CollectorEndpoint: "https://otel.l3xx.cc/v1/traces",
				Auth: AuthConfig{
					Username: "otel",
					Password: "secret",
				},
			},
			want: otlpTraceHTTPSettings{
				useFullURL: true,
				fullURL:    "https://otel.l3xx.cc/v1/traces",
				insecure:   false,
				headers: map[string]string{
					"Authorization": authHeader,
				},
			},
		},
		{
			name: "host:port 默认 TLS 与 path",
			cfg: ReporterConfig{
				CollectorEndpoint: "otel.l3xx.cc:443",
				Auth: AuthConfig{
					Username: "otel",
					Password: "secret",
				},
			},
			want: otlpTraceHTTPSettings{
				useFullURL: false,
				endpoint:   "otel.l3xx.cc:443",
				urlPath:    "/v1/traces",
				insecure:   false,
				headers: map[string]string{
					"Authorization": authHeader,
				},
			},
		},
		{
			name: "host:port 自定义 path 与 insecure",
			cfg: ReporterConfig{
				CollectorEndpoint: "localhost:4318",
				URLPath:           "/custom/traces",
				Insecure:          true,
			},
			want: otlpTraceHTTPSettings{
				useFullURL: false,
				endpoint:   "localhost:4318",
				urlPath:    "/custom/traces",
				insecure:   true,
				headers:    map[string]string{},
			},
		},
		{
			name: "auth 优先于 headers.Authorization",
			cfg: ReporterConfig{
				CollectorEndpoint: "otel.l3xx.cc:443",
				Headers: map[string]string{
					"Authorization": "Basic stale",
					"X-Custom":      "value",
				},
				Auth: AuthConfig{
					Username: "otel",
					Password: "secret",
				},
			},
			want: otlpTraceHTTPSettings{
				useFullURL: false,
				endpoint:   "otel.l3xx.cc:443",
				urlPath:    "/v1/traces",
				insecure:   false,
				headers: map[string]string{
					"Authorization": authHeader,
					"X-Custom":      "value",
				},
			},
		},
		{
			name: "仅 headers 无 auth",
			cfg: ReporterConfig{
				CollectorEndpoint: "otel.l3xx.cc:443",
				Headers: map[string]string{
					"Authorization": "Bearer token",
				},
			},
			want: otlpTraceHTTPSettings{
				useFullURL: false,
				endpoint:   "otel.l3xx.cc:443",
				urlPath:    "/v1/traces",
				insecure:   false,
				headers: map[string]string{
					"Authorization": "Bearer token",
				},
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := resolveOTLPTraceHTTPSettings(testCase.cfg)
			if testCase.wantFail {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveOTLPTraceHTTPSettings: %v", err)
			}
			if got.useFullURL != testCase.want.useFullURL {
				t.Errorf("useFullURL = %v, want %v", got.useFullURL, testCase.want.useFullURL)
			}
			if got.fullURL != testCase.want.fullURL {
				t.Errorf("fullURL = %q, want %q", got.fullURL, testCase.want.fullURL)
			}
			if got.endpoint != testCase.want.endpoint {
				t.Errorf("endpoint = %q, want %q", got.endpoint, testCase.want.endpoint)
			}
			if got.urlPath != testCase.want.urlPath {
				t.Errorf("urlPath = %q, want %q", got.urlPath, testCase.want.urlPath)
			}
			if got.insecure != testCase.want.insecure {
				t.Errorf("insecure = %v, want %v", got.insecure, testCase.want.insecure)
			}
			if len(got.headers) != len(testCase.want.headers) {
				t.Fatalf("headers len = %d, want %d, got=%v want=%v", len(got.headers), len(testCase.want.headers), got.headers, testCase.want.headers)
			}
			for key, wantValue := range testCase.want.headers {
				if got.headers[key] != wantValue {
					t.Errorf("headers[%q] = %q, want %q", key, got.headers[key], wantValue)
				}
			}
		})
	}
}

func TestBuildOTLPTraceHTTPOptions(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ReporterConfig
		wantErr bool
	}{
		{
			name: "HTTPS host:port 带 auth 可构建 options",
			cfg: ReporterConfig{
				CollectorEndpoint: "otel.l3xx.cc:443",
				Auth: AuthConfig{
					Username: "otel",
					Password: "secret",
				},
			},
		},
		{
			name: "完整 URL 可构建 options",
			cfg: ReporterConfig{
				CollectorEndpoint: "https://otel.l3xx.cc/v1/traces",
				Auth: AuthConfig{
					Username: "otel",
					Password: "secret",
				},
			},
		},
		{
			name: "旧版 HTTP 完整 URL 可构建 options",
			cfg: ReporterConfig{
				CollectorEndpoint: "http://localhost:4318/v1/traces",
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			options, err := buildOTLPTraceHTTPOptions(testCase.cfg)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("buildOTLPTraceHTTPOptions: %v", err)
			}
			if len(options) == 0 {
				t.Fatal("expected non-empty options")
			}
		})
	}
}

func TestResolveReporterHeadersAuthPriority(t *testing.T) {
	cfg := ReporterConfig{
		Headers: map[string]string{
			"Authorization": "Basic stale",
		},
		Auth: AuthConfig{
			Username: "otel",
			Password: "secret",
		},
	}
	headers := resolveReporterHeaders(cfg)
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("otel:secret"))
	if headers["Authorization"] != expected {
		t.Errorf("Authorization = %q, want %q", headers["Authorization"], expected)
	}
}
