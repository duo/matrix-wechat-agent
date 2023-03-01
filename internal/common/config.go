package common

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultInitTimeout    = 10 * time.Second
	defaultRequestTimeout = 1 * time.Minute
	defaultPingInterval   = 30 * time.Second
)

type Configure struct {
	Wechat struct {
		Version        string        `yaml:"version"`
		ListenPort     int32         `yaml:"listen_port"`
		InitTimeout    time.Duration `yaml:"init_timeout"`
		RequestTimeout time.Duration `yaml:"request_timeout"`
		Workdir        string        `yaml:"-"`
	} `yaml:"wechat"`

	Service struct {
		Addr         string        `yaml:"addr"`
		Secret       string        `yaml:"secret"`
		PingInterval time.Duration `yaml:"ping_interval"`
	} `yaml:"service"`

	Log struct {
		Level string `yaml:"level"`
	} `yaml:"log"`
}

func LoadConfig(path string) (*Configure, error) {
	file, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	config := &Configure{}
	config.Wechat.InitTimeout = defaultInitTimeout
	config.Wechat.RequestTimeout = defaultRequestTimeout
	config.Service.PingInterval = defaultPingInterval
	if err := yaml.Unmarshal(file, &config); err != nil {
		return nil, err
	}

	return config, nil
}
