package constants

import "os"

var (
	SERVICE_NAME                = "JMDN-FastSync"
	VERSION                     = "JMDN-FS-alpha"
	DEVELOPMENT                 = os.Getenv("ENV") != "production"
	LOG_LEVEL                   = "info"
	OTEL_EXPORTER_OTLP_ENDPOINT = ""
	OTEL_USERNAME               = ""
	OTEL_PASSWORD               = ""
)
