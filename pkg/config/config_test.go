package config

import (
	"os"
	"testing"
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
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}
	defer os.Remove(configFile.Name())

	if _, err := configFile.Write([]byte(configContent)); err != nil {
		t.Fatalf("Failed to write to temp config file: %v", err)
	}
	configFile.Close()

	// Load configuration
	cfg, err := LoadConfig(configFile.Name())
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify the loaded configuration
	if len(cfg.ServerGroups) != 2 {
		t.Errorf("Expected 2 server groups, got %d", len(cfg.ServerGroups))
	}

	if cfg.ServerGroups[0].Name != "loki1" {
		t.Errorf("Expected first server group name to be 'loki1', got '%s'", cfg.ServerGroups[0].Name)
	}

	if cfg.ServerGroups[1].URL != "http://loki2.example.com" {
		t.Errorf("Expected second server group URL to be 'http://loki2.example.com', got '%s'", cfg.ServerGroups[1].URL)
	}

	if cfg.Logging.Level != "info" {
		t.Errorf("Expected logging level to be 'info', got '%s'", cfg.Logging.Level)
	}

	if cfg.Logging.Format != "json" {
		t.Errorf("Expected logging format to be 'json', got '%s'", cfg.Logging.Format)
	}
}
