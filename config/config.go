package config

import (
	"log"
	"os"
	"sync"

	"github.com/ethereal3x/apc/logger"
	"github.com/ethereal3x/apc/storage"
	"github.com/ethereal3x/apc/tracing"
	"gopkg.in/yaml.v3"
)

var (
	once sync.Once
	conf *Config
)

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

func initConfig() {
	once.Do(func() {
		bytes, err := os.ReadFile("config.yaml")
		if err != nil {
			log.Fatalf("config read fail: %s", err)
		}
		conf = &Config{}
		if err := yaml.Unmarshal(bytes, conf); err != nil {
			log.Fatalf("config unmarshal fail: %s", err)
		}
	})
}

func GetConf() *Config {
	if conf == nil {
		initConfig()
	}
	return conf
}

func GetClientConf(name string) *ClientConf {
	for _, conf := range GetConf().ClientList {
		if conf.Name == name {
			return conf
		}
	}
	return nil
}
