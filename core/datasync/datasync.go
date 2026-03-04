package datasync

import (
	"context"

	datasyncpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/datasync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/communication"
	"github.com/libp2p/go-libp2p/core/host"
)

const(
	maxRetries = 2
	maxWorkers = 4
)

type DataSync struct {
	SyncVars *types.Syncvars
	Comm     communication.Communicator
}

func NewDataSync() *DataSync {
	return &DataSync{}
}

func (ds *DataSync) SetSyncVars(ctx context.Context, protocolVersion uint16, nodeInfo types.Nodeinfo, node host.Host, wal *WAL.WAL) DataSync_router {
	if ds.SyncVars == nil {
		ds.SyncVars = &types.Syncvars{}
	}
	ds.Comm = communication.NewCommunication(node, protocolVersion)
	ds.SyncVars.Version = protocolVersion
	ds.SyncVars.NodeInfo = nodeInfo
	ds.SyncVars.Ctx = ctx
	ds.SyncVars.WAL = wal
	ds.SyncVars.Node = node
	return ds
}

func (ds *DataSync) GetSyncVars() *types.Syncvars {
	return ds.SyncVars
}

func (ds *DataSync) DataSync(datasyncrequest *datasyncpb.DataSyncRequest, remotes []*types.Nodeinfo) error {
	return nil
}

func (ds *DataSync) Close() {
	ds.SyncVars.Ctx.Done()
	ds.SyncVars = nil
	ds.Comm = nil
}