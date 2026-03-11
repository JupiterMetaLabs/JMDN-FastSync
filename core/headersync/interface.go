package headersync

import (
	"context"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	datasyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/datasync"
	headersyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/headersync"
	availabilitypb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability"
	taggingpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/tagging"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/libp2p/go-libp2p/core/host"
)

type Headersync_router interface {
	// Set the information prior so that this variables can be reused
	SetSyncVars(ctx context.Context, protocolVersion uint16, nodeInfo types.Nodeinfo, node host.Host, wal *WAL.WAL) Headersync_router

	// HeaderSync is the main function that will be called by the user to sync headers
	// remotes[0] would be primary always - following nodes would be the extra nodes for data sourcing
	// Reason for taking availabilityresponse is it have both nodeinfo and the auth info.
	HeaderSync(headersyncrequest *headersyncpb.HeaderSyncRequest, remotes []*availabilitypb.AvailabilityResponse) (*datasyncpb.DataSyncRequest, error)

	// SyncConfirmation is the function that will be called by the HeaderSync function to confirm the sync
	// remotes[0] would be primary always - following nodes would be the extra nodes for data sourcing
	// Reason for taking availabilityresponse is it have both nodeinfo and the auth info.
	SyncConfirmation(ctx context.Context, remotes []*availabilitypb.AvailabilityResponse) (*taggingpb.Tag, bool, error)

	// Get the SyncVars - helpful for other modules to get the sync vars
	GetSyncVars() *types.Syncvars

	// Close the connection
	Close()
}
