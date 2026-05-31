package config

import (
	"log"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

var (
	once sync.Once
	conf *Config
)

type ServerConf struct {
	GrpcAddr    string `yaml:"grpc_addr"`
	GatewayAddr string `yaml:"gateway_addr"`
	Env         string `yaml:"env"`
}

type ClientConf struct {
	Name  string   `yaml:"name"`
	Addr  string   `yaml:"addr"`
	Slave []string `yaml:"slave"`
}

type Config struct {
	Server     ServerConf    `yaml:"server"`
	ClientList []*ClientConf `yaml:"client"`
	Plugin     PluginConf    `yaml:"plugin"`
}

type PluginConf struct {
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
