package config

import (
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/ethereal3x/apc/logger"
	"github.com/ethereal3x/apc/storage"
	"github.com/ethereal3x/apc/tracing"
	"gopkg.in/yaml.v3"
)

const (
	// DefaultConfigPath 默认配置文件相对路径
	DefaultConfigPath = "config.yaml"
	// ConfigPathEnvKey 配置文件路径环境变量名
	ConfigPathEnvKey = "APC_CONFIG_PATH"
)

var (
	once sync.Once
	mu   sync.RWMutex
	conf *Config
)

// LoadOptions 配置文件加载选项
type LoadOptions struct {
	// Path 配置文件路径，为空时从 EnvKey 环境变量或 DefaultConfigPath 解析
	Path string
	// EnvKey 路径环境变量名，为空时使用 ConfigPathEnvKey
	EnvKey string
}

type ServerConf struct {
	GrpcAddr    string `yaml:"grpc_addr" json:"grpc_addr"`
	GatewayAddr string `yaml:"gateway_addr" json:"gateway_addr"`
	Env         string `yaml:"env" json:"env"`
}

type ClientConf struct {
	Name  string   `yaml:"name" json:"name"`
	Addr  string   `yaml:"addr" json:"addr"`
	Slave []string `yaml:"slave" json:"slave"`
}

type Config struct {
	Server     ServerConf    `yaml:"server" json:"server"`
	ClientList []*ClientConf `yaml:"client" json:"client"`
	Plugin     PluginConf    `yaml:"plugin" json:"plugin"`
}

type PluginConf struct {
	Log     logger.Config  `yaml:"log" json:"log"`
	Tracing tracing.Config `yaml:"tracing" json:"tracing"`
	Minio   storage.Config `yaml:"minio" json:"minio"`
	RustFS  storage.Config `yaml:"rustfs" json:"rustfs"`
}

// ResolveConfigPath 解析最终配置文件路径
func ResolveConfigPath(opts LoadOptions) string {
	if opts.Path != "" {
		return opts.Path
	}
	envKey := opts.EnvKey
	if envKey == "" {
		envKey = ConfigPathEnvKey
	}
	if path := os.Getenv(envKey); path != "" {
		return path
	}
	return DefaultConfigPath
}

// Load 从指定路径或环境变量加载配置并写入全局实例
func Load(opts LoadOptions) error {
	path := ResolveConfigPath(opts)
	bytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(bytes, cfg); err != nil {
		return fmt.Errorf("unmarshal %s: %w", path, err)
	}
	mu.Lock()
	conf = cfg
	mu.Unlock()
	return nil
}

// MustLoad 加载配置，失败时终止进程
func MustLoad(opts LoadOptions) {
	if err := Load(opts); err != nil {
		log.Fatalf("config load fail: %s", err)
	}
}

// GetConf 获取全局配置，首次调用时按默认路径懒加载
func GetConf() *Config {
	once.Do(func() {
		mu.RLock()
		loaded := conf != nil
		mu.RUnlock()
		if loaded {
			return
		}
		if err := Load(LoadOptions{}); err != nil {
			log.Fatalf("config load fail: %s", err)
		}
	})
	mu.RLock()
	defer mu.RUnlock()
	return conf
}

// GetClientConf 按名称查找客户端配置
func GetClientConf(name string) *ClientConf {
	for _, clientConf := range GetConf().ClientList {
		if clientConf.Name == name {
			return clientConf
		}
	}
	return nil
}
