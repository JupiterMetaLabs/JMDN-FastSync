package GRO

import (
	"github.com/JupiterMetaLabs/goroutine-orchestrator/manager/interfaces"
)

type appmanager struct {
	Apps map[string]interfaces.AppGoroutineManagerInterface
}

var (
	GlobalGRO interfaces.GlobalGoroutineManagerInterface
	apps      *appmanager
)

// This is the variable tracking for all pulled up app manager
const (
	LoggingLocal = "local:logging"
)

// threads - goroutines
const (
	CLIThread         = "thread:cli"
)

// Apps
const (
	LoggingApp        = "app:logging"
)

// waitgroups
var (
	FacadeWG = "waitgroup:facade"
)
