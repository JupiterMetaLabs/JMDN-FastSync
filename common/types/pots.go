package types

import (
	"time"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
)

type PoTS struct {
	// SyncStartTime is the moment FastSync began — used as the timestamp anchor
	// sent to the server in Phase 1 so it can resolve the nearest block.
	SyncStartTime time.Time

	// WAL is the dedicated PoTS WAL instance (separate from the FastSync WAL).
	WAL *WAL.WAL

	// ProtocolVersion is the negotiated wire protocol version, forwarded to the
	// communication layer when opening streams.
	ProtocolVersion uint16
}
