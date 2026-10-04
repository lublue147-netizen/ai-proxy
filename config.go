package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ServerConfig 服务端配置
type ServerConfig struct {
	Host       string `yaml:"host" json:"host"`               // 监听地址，默认 127.0.0.1
	Port       int    `yaml:"port" json:"port"`               // 监听端口，默认 8080
	Verbose    bool   `yaml:"verbose" json:"verbose"`         // 是否打印详细日志
	ClientKey  string `yaml:"client_key" json:"client_key"`   // 可选：访问本地代理自身所需的 API Key
	RetryCount int    `yaml:"retry_count" json:"retry_count"` // 节点不可用时最大故障转移重试次数
}

// UpstreamConfig 单个上游 OpenAI 接口配置
type UpstreamConfig struct {
	Name     string            `yaml:"name" json:"name"`         // 节点名称/描述
	BaseURL  string            `yaml:"base_url" json:"base_url"` // 目标 Base URL，例如 https://api.openai.com
	Headers  map[string]string `yaml:"headers" json:"headers"`   // 需要覆盖/写入的 Header，如 Authorization: Bearer sk-xxx
	Weight   int               `yaml:"weight" json:"weight"`     // 轮询权重，默认为 1
	Enabled  *bool             `yaml:"enabled" json:"enabled"`   // 是否启用，默认 true
	ParsedURL *url.URL         `yaml:"-" json:"-"`
}

// Config 根配置
type Config struct {
	Server    ServerConfig     `yaml:"server" json:"server"`
	Upstreams []UpstreamConfig `yaml:"upstreams" json:"upstreams"`
}

// LoadConfig 加载并解析配置文件
func LoadConfig(path string) (*Config, error) {
	if path == "" {
		// 自动寻找默认配置
		candidates := []string{"config.yaml", "config.yml", "config.json"}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				path = c
				break
			}
		}
	}

	if path == "" {
		return nil, errors.New("未找到配置文件，请在程序同目录下创建 config.yaml 或使用 -c 指定配置文件路径")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败 (%s): %w", path, err)
	}

	cfg := &Config{
		Server: ServerConfig{
			Host:       "127.0.0.1",
			Port:       8080,
			Verbose:    true,
			RetryCount: 2,
		},
	}

	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".json" {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("解析 JSON 配置失败: %w", err)
		}
	} else {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("解析 YAML 配置失败: %w", err)
		}
	}

	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		cfg.Server.Port = 8080
	}

	validUpstreams := make([]UpstreamConfig, 0, len(cfg.Upstreams))
	for i := range cfg.Upstreams {
		u := &cfg.Upstreams[i]
		if u.Enabled != nil && !*u.Enabled {
			continue
		}
		if strings.TrimSpace(u.BaseURL) == "" {
			continue
		}
		parsed, err := url.Parse(strings.TrimSpace(u.BaseURL))
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("upstream [%s] 的 base_url 无效: %s", u.Name, u.BaseURL)
		}
		if u.Weight <= 0 {
			u.Weight = 1
		}
		if u.Name == "" {
			u.Name = fmt.Sprintf("upstream-%d", i+1)
		}
		if u.Headers == nil {
			u.Headers = make(map[string]string)
		}
		u.ParsedURL = parsed
		validUpstreams = append(validUpstreams, *u)
	}

	if len(validUpstreams) == 0 {
		return nil, errors.New("配置中没有可用的 upstreams 节点")
	}
	cfg.Upstreams = validUpstreams

	return cfg, nil
}
