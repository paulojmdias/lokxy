package logging

import (
	"os"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/paulojmdias/lokxy/pkg/config" // Import the config package
)

// ConfigureLogger sets up the logging level and format based on the configuration.
func ConfigureLogger(cfg config.LoggerConfig) log.Logger { // Use config.LoggerConfig
	var logger log.Logger

	// Configure log format: "json" or "logfmt"
	if cfg.Format == "json" {
		logger = log.NewJSONLogger(log.NewSyncWriter(os.Stdout))
	} else {
		logger = log.NewLogfmtLogger(log.NewSyncWriter(os.Stdout))
	}

	// Add timestamp to logs
	logger = log.With(logger, "ts", log.DefaultTimestampUTC)

	// Set log level
	switch cfg.Level {
	case "debug":
		logger = level.NewFilter(logger, level.AllowDebug())
	case "warn":
		logger = level.NewFilter(logger, level.AllowWarn())
	case "error":
		logger = level.NewFilter(logger, level.AllowError())
	default:
		logger = level.NewFilter(logger, level.AllowInfo())
	}

	return logger
}
