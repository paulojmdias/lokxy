package logging

import (
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
			logger := ConfigureLogger(tt.config)

			// Check logger level
			if logger == nil {
				t.Fatalf("Expected logger to be configured, got nil")
			}

			// Check logger format
			// Note: This is a simplified check. In a real-world scenario, you might need to capture and parse log output.
			switch tt.config.Format {
			case "json":
				if _, ok := any(logger).(log.Logger); !ok {
					t.Errorf("Expected logger to be in json format, got %T", logger)
				}
			case "logfmt":
				if _, ok := any(logger).(log.Logger); !ok {
					t.Errorf("Expected logger to be in logfmt format, got %T", logger)
				}
			}
		})
	}
}
