package datasync

import (
	"context"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	availabilitypb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability"
	datasyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/datasync"
	taggingpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/tagging"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/libp2p/go-libp2p/core/host"
)

type DataSync_router interface {
	// Set the information prior so that this variables can be reused
	SetSyncVars(ctx context.Context, protocolVersion uint16, nodeInfo types.Nodeinfo, node host.Host, wal *WAL.WAL) DataSync_router

	// Get the SyncVars - helpful for other modules to get the sync vars
	GetSyncVars() *types.Syncvars

	// DataSync is the main function that will be called by the user to sync data
	// remotes[0] would be primary always - following nodes would be the extra nodes for data sourcing
	// Reason for taking availabilityresponse is it have both nodeinfo and the auth info.
	DataSync(datasyncrequest *datasyncpb.DataSyncRequest, remotes []*availabilitypb.AvailabilityResponse) (*taggingpb.TaggedAccounts, error)

	// Close the connection
	Close()
}
