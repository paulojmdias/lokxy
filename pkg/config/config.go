package config

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v2"
)

var isReady atomic.Bool

// TransportConfig holds HTTP transport tuning parameters.
// All fields are optional; zero values are replaced with sensible defaults
// in the proxy layer.
type TransportConfig struct {
	DisableKeepAlives     bool          `yaml:"disable_keep_alives"`
	MaxIdleConns          int           `yaml:"max_idle_conns"`
	MaxIdleConnsPerHost   int           `yaml:"max_idle_conns_per_host"`
	IdleConnTimeout       time.Duration `yaml:"idle_conn_timeout"`
	ExpectContinueTimeout time.Duration `yaml:"expect_continue_timeout"`
	ResponseHeaderTimeout time.Duration `yaml:"response_header_timeout"`
	ForceAttemptHTTP2     *bool         `yaml:"force_attempt_http2"`
}

// HTTPClientConfig holds the HTTP client settings such as timeouts and TLS configurations
type HTTPClientConfig struct {
	DialTimeout time.Duration   `yaml:"dial_timeout"`
	Transport   TransportConfig `yaml:"transport"`
	TLSConfig   struct {
		InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
		CAFile             string `yaml:"ca_file"`
		CertFile           string `yaml:"cert_file"`
		KeyFile            string `yaml:"key_file"`
	} `yaml:"tls_config"`
}

// ServerGroup represents a single Loki instance configuration
type ServerGroup struct {
	Name             string            `yaml:"name"`
	URL              string            `yaml:"url"`
	Timeout          int               `yaml:"timeout"`
	Headers          map[string]string `yaml:"headers"`
	HTTPClientConfig HTTPClientConfig  `yaml:"http_client_config"` // Add HTTP config
}

// LoggerConfig contains the logger configuration details.
type LoggerConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// Config represents the overall proxy configuration
type Config struct {
	ServerGroups []ServerGroup `yaml:"server_groups"`
	Logging      LoggerConfig  `yaml:"logging"`
}

// LoadConfig loads and parses the YAML configuration file
func LoadConfig(configFile string) (*Config, error) {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}

	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return &config, nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if len(c.ServerGroups) == 0 {
		return fmt.Errorf("at least one server group must be configured")
	}

	for i, sg := range c.ServerGroups {
		if sg.Name == "" {
			return fmt.Errorf("server_groups[%d]: name is required", i)
		}
		if sg.URL == "" {
			return fmt.Errorf("server_groups[%d]: url is required", i)
		}
	}

	return nil
}

func SetReady(ready bool) {
	isReady.Store(ready)
}

func IsReady() bool {
	return isReady.Load()
}
