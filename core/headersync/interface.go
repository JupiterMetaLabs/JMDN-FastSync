package headersync

import (
	"context"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
)

type Headersync_router interface{
	SetSyncVars(ctx context.Context, protocolVersion uint16, nodeInfo types.Nodeinfo) Headersync_router
	HeaderSync(ctx context.Context, remotes []*types.Nodeinfo) error
	Close()
}