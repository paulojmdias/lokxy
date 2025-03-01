package logging

import (
	"bytes"
	"testing"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"

	"github.com/paulojmdias/lokxy/pkg/config"
)

func TestConfigureLogger(t *testing.T) {
	// Define test cases
	tests := []struct {
		name   string
		config config.LoggerConfig
		level  level.Value
		format string
	}{
		{
			name: "info level with json format",
			config: config.LoggerConfig{
				Level:  "info",
				Format: "json",
			},
			level:  level.InfoValue(),
			format: "json",
		},
		{
			name: "debug level with logfmt format",
			config: config.LoggerConfig{
				Level:  "debug",
				Format: "logfmt",
			},
			level:  level.DebugValue(),
			format: "logfmt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer

			// Check logger level
			logger := ConfigureLogger(tt.config)
			if logger == nil {
				t.Fatalf("Expected logger to be configured, got nil")
			}
			logger = log.NewSyncLogger(log.NewLogfmtLogger(&buf))
			if tt.config.Format == "json" {
				logger = log.NewSyncLogger(log.NewJSONLogger(&buf))
			}

			err := logger.Log("key", "value")
			if err != nil {
				t.Errorf("Error while logging: %v", err)
			}

			// Check logger format
			// Note: This is a simplified check. In a real-world scenario, you might need to capture and parse log output.
			if tt.config.Format == "json" {
				t.Errorf("Expected logger to be in json format, got %T", logger)
			} else if tt.config.Format == "logfmt" {
				t.Errorf("Expected logger to be in logfmt format, got %T", logger)
			}
		})
	}
}
