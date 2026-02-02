package otelsetup

import (
	"context"
	"sync"
	"time"
	constants "github.com/JupiterMetaLabs/JMDN-FastSync/internal/types/constants"
	"github.com/JupiterMetaLabs/ion"
)

var (
	initOnce        sync.Once
	originalLogDir  string
	originalLogFile string
	globalLogger    *ion.Ion
	globalWarnings  []ion.Warning
	globalInitErr   error
)

// Setup initializes Ion with basic logging (console and optional file)
// and automatically adds OpenTelemetry support if configured via environment variables.
// logDir: directory for log files (optional, if empty file logging is disabled)
// logFileName: name of the log file (optional, if empty file logging is disabled)
// Returns the initialized Ion instance and any warnings
// Note: This function uses sync.Once to ensure initialization only happens once.
// Subsequent calls return the same logger instance from the first successful initialization.
func Setup(logDir string, logFileName string) (*ion.Ion, []ion.Warning, error) {
	initOnce.Do(func() {
		// Build Ion configuration
		cfg := ion.Default()

		// Configure console output (always enabled for development)
		// Default to pretty, switch to json if OTEL is enabled (logic below)
		cfg.Console = ion.ConsoleConfig{
			Enabled:        true,
			Format:         "pretty",
			Color:          true,
			ErrorsToStderr: true,
		}

		cfg.File = ion.FileConfig{
			Enabled: false,
		}

		cfg.ServiceName = constants.SERVICE_NAME
		cfg.Version = constants.VERSION
		cfg.Development = constants.DEVELOPMENT
		cfg.Level = constants.LOG_LEVEL

		// Check for OTEL configuration
		if constants.OTEL_EXPORTER_OTLP_ENDPOINT != "" {
			// Configure OTEL export
			cfg.OTEL = ion.OTELConfig{
				Enabled:  true,
				Endpoint: constants.OTEL_EXPORTER_OTLP_ENDPOINT,
				Protocol: "grpc",
				Insecure: true, // Testing with insecure mode // TODO: Change to false in production
				Username: constants.OTEL_USERNAME,
				Password: constants.OTEL_PASSWORD,
				// Batch configuration for high-throughput
				BatchSize:      512,
				ExportInterval: 5 * time.Second,
			}

			// Enable distributed tracing
			cfg.Tracing = ion.TracingConfig{
				Enabled: true,
				// Endpoint and Protocol are automatically inherited from OTEL config
				// Sample rate: 1.0 = 100% (adjust based on volume)
				Sampler: "always",
			}
		}

		// Initialize Ion
		globalLogger, globalWarnings, globalInitErr = ion.New(cfg)
	})

	return globalLogger, globalWarnings, globalInitErr
}

// Shutdown gracefully shuts down Ion, flushing all pending logs and traces
func Shutdown(ctx context.Context, ionInstance *ion.Ion) error {
	if ionInstance == nil {
		return nil
	}
	return ionInstance.Shutdown(ctx)
}
