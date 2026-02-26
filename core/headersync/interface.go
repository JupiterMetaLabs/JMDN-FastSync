package headersync

import (
	"context"

	headersyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/headersync"
	taggingpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/tagging"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/libp2p/go-libp2p/core/host"
)

type Headersync_router interface {
	SetSyncVars(ctx context.Context, protocolVersion uint16, nodeInfo types.Nodeinfo, node host.Host) Headersync_router
	HeaderSync(headersyncrequest *headersyncpb.HeaderSyncRequest, remotes []*types.Nodeinfo) error
	SyncConfirmation(ctx context.Context, remotes []*types.Nodeinfo) (*taggingpb.Tag, bool, error)
	Close()
}
