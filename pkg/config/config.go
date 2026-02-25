package config

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/common/model"
	"gopkg.in/yaml.v2"
)

var isReady atomic.Bool

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

// QueryRangeConfig holds configuration for the query_range endpoint
type QueryRangeConfig struct {
	Step string `yaml:"step"` // Step duration to force on backend queries (e.g., "1m", "60s")
}

// VolumeRangeConfig holds configuration for the volume_range endpoint
type VolumeRangeConfig struct {
	Step string `yaml:"step"` // Step duration to force on backend queries (e.g., "1m", "60s")
}

// APIConfig holds configuration for API endpoint behavior
type APIConfig struct {
	QueryRange  QueryRangeConfig  `yaml:"query_range"`
	VolumeRange VolumeRangeConfig `yaml:"volume_range"`
}

// Config represents the overall proxy configuration
type Config struct {
	ServerGroups []ServerGroup `yaml:"server_groups"`
	Logging      LoggerConfig  `yaml:"logging"`
	API          APIConfig     `yaml:"api"`
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

	// Validate API configuration
	if err := c.API.Validate(); err != nil {
		return err
	}

	return nil
}

// Validate checks if the API configuration is valid
func (a *APIConfig) Validate() error {
	if a.QueryRange.Step != "" {
		if err := validateStepDuration(a.QueryRange.Step); err != nil {
			return fmt.Errorf("api.query_range.step: %w", err)
		}
	}

	if a.VolumeRange.Step != "" {
		if err := validateStepDuration(a.VolumeRange.Step); err != nil {
			return fmt.Errorf("api.volume_range.step: %w", err)
		}
	}

	return nil
}

// validateStepDuration validates a step duration string
// Accepts Prometheus-style duration (e.g., "1m", "30s", "1h", "1d", "1w")
// Note: Loki does not support milliseconds (ms) for step parameter
func validateStepDuration(step string) error {
	// Loki does not support milliseconds
	if strings.Contains(step, "ms") {
		return fmt.Errorf("invalid step duration %q: milliseconds (ms) not supported by Loki", step)
	}

	_, err := model.ParseDuration(step)
	if err != nil {
		return fmt.Errorf("invalid step duration %q: %w", step, err)
	}
	return nil
}

func SetReady(ready bool) {
	isReady.Store(ready)
}

func IsReady() bool {
	return isReady.Load()
}
