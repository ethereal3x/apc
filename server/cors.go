package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var defaultCORSAllowedMethods = []string{"GET", "HEAD", "POST", "PUT", "DELETE", "OPTIONS"}
var defaultCORSAllowedHeaders = []string{"Content-Type", "Accept", "Authorization"}

// ErrCORSWildcardCredentials 表示携带凭据时配置了通配 Origin
var ErrCORSWildcardCredentials = errors.New("cors: wildcard origin cannot be used with credentials")

// CORSConfig 定义 HTTP gateway 的跨域访问策略
type CORSConfig struct {
	Enabled          bool
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAge           time.Duration
}

// CORSPolicy 编译后的跨域访问策略，可被 net/http 和 hertz 等适配层复用
type CORSPolicy struct {
	config          CORSConfig
	allowedOrigins  map[string]struct{}
	allowedMethods  map[string]struct{}
	allowedHeaders  map[string]struct{}
	wildcardOrigins bool
}

// NewCORSPolicy 校验并编译 CORS 配置为请求匹配策略
func NewCORSPolicy(config CORSConfig) (*CORSPolicy, error) {
	if config.MaxAge < 0 {
		return nil, errors.New("cors: max age cannot be negative")
	}
	if len(config.AllowedMethods) == 0 {
		config.AllowedMethods = append([]string(nil), defaultCORSAllowedMethods...)
	}
	if len(config.AllowedHeaders) == 0 {
		config.AllowedHeaders = append([]string(nil), defaultCORSAllowedHeaders...)
	}
	policy := &CORSPolicy{
		config:         config,
		allowedOrigins: make(map[string]struct{}, len(config.AllowedOrigins)),
		allowedMethods: make(map[string]struct{}, len(config.AllowedMethods)),
		allowedHeaders: make(map[string]struct{}, len(config.AllowedHeaders)),
	}
	for _, origin := range config.AllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin == "*" {
			policy.wildcardOrigins = true
		}
		if origin != "" {
			policy.allowedOrigins[origin] = struct{}{}
		}
	}
	if config.AllowCredentials && policy.wildcardOrigins {
		return nil, ErrCORSWildcardCredentials
	}
	for _, method := range config.AllowedMethods {
		policy.allowedMethods[strings.ToUpper(strings.TrimSpace(method))] = struct{}{}
	}
	for _, header := range config.AllowedHeaders {
		policy.allowedHeaders[strings.ToLower(strings.TrimSpace(header))] = struct{}{}
	}
	policy.config.AllowedOrigins = append([]string(nil), config.AllowedOrigins...)
	policy.config.AllowedMethods = append([]string(nil), config.AllowedMethods...)
	policy.config.AllowedHeaders = append([]string(nil), config.AllowedHeaders...)
	policy.config.ExposedHeaders = append([]string(nil), config.ExposedHeaders...)
	return policy, nil
}

// Enabled 返回策略是否启用，关闭时适配层应直接透传请求
func (policy *CORSPolicy) Enabled() bool {
	return policy.config.Enabled
}

// AllowedMethods 返回策略允许的方法列表副本
func (policy *CORSPolicy) AllowedMethods() []string {
	return append([]string(nil), policy.config.AllowedMethods...)
}

// AllowedHeaders 返回策略允许请求头列表副本
func (policy *CORSPolicy) AllowedHeaders() []string {
	return append([]string(nil), policy.config.AllowedHeaders...)
}

// MaxAge 返回预检结果缓存时长
func (policy *CORSPolicy) MaxAge() time.Duration {
	return policy.config.MaxAge
}

// Middleware 执行实际请求和预检请求的统一 CORS 策略
func (policy *CORSPolicy) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		origin := strings.TrimSpace(request.Header.Get("Origin"))
		if origin == "" || !policy.IsOriginAllowed(origin) {
			next.ServeHTTP(responseWriter, request)
			return
		}
		isPreflight := request.Method == http.MethodOptions && request.Header.Get("Access-Control-Request-Method") != ""
		if isPreflight && !policy.isPreflightAllowed(request) {
			responseWriter.WriteHeader(http.StatusForbidden)
			return
		}
		policy.WriteResponseHeaders(responseWriter.Header(), origin)
		if !isPreflight {
			next.ServeHTTP(responseWriter, request)
			return
		}
		responseWriter.Header().Set("Access-Control-Allow-Methods", strings.Join(policy.config.AllowedMethods, ","))
		responseWriter.Header().Set("Access-Control-Allow-Headers", strings.Join(policy.config.AllowedHeaders, ","))
		if maxAgeSeconds := int64(policy.config.MaxAge / time.Second); maxAgeSeconds > 0 {
			responseWriter.Header().Set("Access-Control-Max-Age", strconv.FormatInt(maxAgeSeconds, 10))
		}
		responseWriter.WriteHeader(http.StatusNoContent)
	})
}

// IsOriginAllowed 判断请求 Origin 是否匹配精确白名单或无凭据通配策略
func (policy *CORSPolicy) IsOriginAllowed(origin string) bool {
	if policy.wildcardOrigins {
		return true
	}
	_, allowed := policy.allowedOrigins[origin]
	return allowed
}

// IsPreflightAllowed 校验预检请求的方法和请求头均在允许列表内
func (policy *CORSPolicy) IsPreflightAllowed(requestedMethod, requestedHeaders string) bool {
	requestedMethod = strings.ToUpper(strings.TrimSpace(requestedMethod))
	if _, allowed := policy.allowedMethods[requestedMethod]; !allowed {
		return false
	}
	for _, header := range strings.Split(requestedHeaders, ",") {
		header = strings.ToLower(strings.TrimSpace(header))
		if header == "" {
			continue
		}
		if _, allowed := policy.allowedHeaders[header]; !allowed {
			return false
		}
	}
	return true
}

// isPreflightAllowed 校验 net/http 预检请求是否在允许列表内
func (policy *CORSPolicy) isPreflightAllowed(request *http.Request) bool {
	return policy.IsPreflightAllowed(
		request.Header.Get("Access-Control-Request-Method"),
		request.Header.Get("Access-Control-Request-Headers"),
	)
}

// WriteResponseHeaders 写入已授权 Origin 对应的 CORS 响应头
func (policy *CORSPolicy) WriteResponseHeaders(headers http.Header, origin string) {
	if policy.wildcardOrigins {
		headers.Set("Access-Control-Allow-Origin", "*")
	} else {
		headers.Set("Access-Control-Allow-Origin", origin)
		headers.Add("Vary", "Origin")
	}
	if policy.config.AllowCredentials {
		headers.Set("Access-Control-Allow-Credentials", "true")
	}
	if len(policy.config.ExposedHeaders) > 0 {
		headers.Set("Access-Control-Expose-Headers", strings.Join(policy.config.ExposedHeaders, ","))
	}
}

// allowCORSWithHeaders 保留旧版兼容路径；预检回显具体 Origin 并允许 credentials，禁止返回 *
// 生产环境请改用 SetCORSConfig 做 Origin 白名单收紧
func allowCORSWithHeaders(next http.Handler, allowedHeaders []string) http.Handler {
	if len(allowedHeaders) == 0 {
		allowedHeaders = defaultCORSAllowedHeaders
	}
	allowedHeadersValue := strings.Join(allowedHeaders, ",")
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		origin := strings.TrimSpace(request.Header.Get("Origin"))
		if origin == "" {
			next.ServeHTTP(responseWriter, request)
			return
		}
		isPreflight := request.Method == http.MethodOptions && request.Header.Get("Access-Control-Request-Method") != ""
		if isPreflight {
			responseWriter.Header().Set("Access-Control-Allow-Origin", origin)
			responseWriter.Header().Set("Access-Control-Allow-Credentials", "true")
			responseWriter.Header().Add("Vary", "Origin")
			responseWriter.Header().Set("Access-Control-Allow-Headers", allowedHeadersValue)
			responseWriter.Header().Set("Access-Control-Allow-Methods", strings.Join(defaultCORSAllowedMethods, ","))
			responseWriter.WriteHeader(http.StatusNoContent)
			return
		}
		if responseWriter.Header().Get("Access-Control-Allow-Origin") == "" {
			responseWriter.Header().Set("Access-Control-Allow-Origin", origin)
			responseWriter.Header().Set("Access-Control-Allow-Credentials", "true")
			responseWriter.Header().Add("Vary", "Origin")
		}
		next.ServeHTTP(responseWriter, request)
	})
}
