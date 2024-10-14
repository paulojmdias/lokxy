package config

import (
	"gopkg.in/yaml.v2"
	"os"
	"time"
)

// HTTPClientConfig holds the HTTP client settings such as timeouts and TLS configurations
type HTTPClientConfig struct {
	DialTimeout time.Duration `yaml:"dial_timeout"`
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

	return &config, nil
}
