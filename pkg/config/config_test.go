package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	// Set up a temporary config file
	configContent := `
server_groups:
  - name: loki1
    url: http://loki1.example.com
    timeout: 10
    headers:
      Authorization: Bearer token1
  - name: loki2
    url: http://loki2.example.com
    timeout: 15
    headers:
      Authorization: Bearer token2
logging:
  level: info
  format: json
`
	configFile, err := os.CreateTemp("", "config.yaml")
	require.NoError(t, err, "Failed to create temp config file")
	defer os.Remove(configFile.Name())

	_, err = configFile.Write([]byte(configContent))
	require.NoError(t, err, "Failed to write to temp config file")
	configFile.Close()

	// Load configuration
	cfg, err := LoadConfig(configFile.Name())
	require.NoError(t, err, "Failed to load config")

	// Verify the loaded configuration
	require.Len(t, cfg.ServerGroups, 2, "Expected 2 server groups")
	require.Equal(t, "loki1", cfg.ServerGroups[0].Name, "First server group name")
	require.Equal(t, "http://loki2.example.com", cfg.ServerGroups[1].URL, "Second server group URL")
	require.Equal(t, "info", cfg.Logging.Level, "Logging level")
	require.Equal(t, "json", cfg.Logging.Format, "Logging format")
}

func TestValidate_EmptyServerGroups(t *testing.T) {
	cfg := &Config{
		ServerGroups: []ServerGroup{},
	}

	err := cfg.Validate()
	require.Error(t, err, "Expected validation error for empty server groups")
	require.EqualError(t, err, "at least one server group must be configured")
}

func TestValidate_MissingName(t *testing.T) {
	cfg := &Config{
		ServerGroups: []ServerGroup{
			{
				Name: "",
				URL:  "http://loki1.example.com",
			},
		},
	}

	err := cfg.Validate()
	require.Error(t, err, "Expected validation error for missing name")
	require.EqualError(t, err, "server_groups[0]: name is required")
}

func TestValidate_MissingURL(t *testing.T) {
	cfg := &Config{
		ServerGroups: []ServerGroup{
			{
				Name: "loki1",
				URL:  "",
			},
		},
	}

	err := cfg.Validate()
	require.Error(t, err, "Expected validation error for missing URL")
	require.EqualError(t, err, "server_groups[0]: url is required")
}

func TestValidate_ValidConfig(t *testing.T) {
	cfg := &Config{
		ServerGroups: []ServerGroup{
			{
				Name: "loki1",
				URL:  "http://loki1.example.com",
			},
			{
				Name: "loki2",
				URL:  "http://loki2.example.com",
			},
		},
	}

	err := cfg.Validate()
	require.NoError(t, err, "Expected no validation error for valid config")
}

func TestLoadConfig_InvalidConfig(t *testing.T) {
	// Test that LoadConfig fails when validation fails
	configContent := `
server_groups:
  - name: ""
    url: http://loki1.example.com
logging:
  level: info
  format: json
`
	configFile, err := os.CreateTemp("", "config.yaml")
	require.NoError(t, err, "Failed to create temp config file")
	defer os.Remove(configFile.Name())

	_, err = configFile.Write([]byte(configContent))
	require.NoError(t, err, "Failed to write to temp config file")
	configFile.Close()

	// Load configuration - should fail validation
	_, err = LoadConfig(configFile.Name())
	require.Error(t, err, "Expected LoadConfig to fail for invalid config")
	require.EqualError(t, err, "server_groups[0]: name is required")
}
