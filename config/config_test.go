package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveConfigPathExplicit 校验显式 Path 优先级最高
func TestResolveConfigPathExplicit(t *testing.T) {
	path := ResolveConfigPath(LoadOptions{Path: "/etc/app/config.yaml"})
	if path != "/etc/app/config.yaml" {
		t.Fatalf("expected explicit path, got %s", path)
	}
}

// TestResolveConfigPathFromEnv 校验环境变量覆盖默认路径
func TestResolveConfigPathFromEnv(t *testing.T) {
	t.Setenv(ConfigPathEnvKey, "/tmp/from-env.yaml")
	path := ResolveConfigPath(LoadOptions{})
	if path != "/tmp/from-env.yaml" {
		t.Fatalf("expected env path, got %s", path)
	}
}

// TestResolveConfigPathDefault 校验无 Path 且无环境变量时使用默认路径
func TestResolveConfigPathDefault(t *testing.T) {
	t.Setenv(ConfigPathEnvKey, "")
	path := ResolveConfigPath(LoadOptions{})
	if path != DefaultConfigPath {
		t.Fatalf("expected default path %s, got %s", DefaultConfigPath, path)
	}
}

// TestLoadFromFile 校验从临时文件加载配置并可通过 GetConf 读取
func TestLoadFromFile(t *testing.T) {
	content := []byte(`
server:
  grpc_addr: ":9090"
  gateway_addr: ":8080"
  env: "test"
client: []
plugin:
  log:
    level: "info"
  tracing:
    enabled: false
`)
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	if err := Load(LoadOptions{Path: configPath}); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	cfg := GetConf()
	if cfg.Server.GrpcAddr != ":9090" {
		t.Fatalf("expected grpc_addr :9090, got %s", cfg.Server.GrpcAddr)
	}
	if cfg.Server.GatewayAddr != ":8080" {
		t.Fatalf("expected gateway_addr :8080, got %s", cfg.Server.GatewayAddr)
	}
	if cfg.Server.Env != "test" {
		t.Fatalf("expected env test, got %s", cfg.Server.Env)
	}
}

// TestLoadFileNotFound 校验配置文件不存在时返回错误
func TestLoadFileNotFound(t *testing.T) {
	err := Load(LoadOptions{Path: filepath.Join(t.TempDir(), "missing.yaml")})
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}
