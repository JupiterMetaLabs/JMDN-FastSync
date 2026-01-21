package protocol

import (
	"bytes"
	"fmt"

	"github.com/JupiterMetaLabs/JMDN-FastSync/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/types/constants"
)

// ChainReader interface defines what we need from the local blockchain
// to verify the PriorSync request.
type ChainReader interface {
	GetBlockByNumber(number uint64) (*types.PriorSync, error)
	CurrentBlockHeight() uint64
}

type PriorSyncHandler struct {
	chain ChainReader
}

func NewPriorSyncHandler(chain ChainReader) *PriorSyncHandler {
	return &PriorSyncHandler{chain: chain}
}

// SyncDecision represents the result of the PriorSync check
type SyncDecision struct {
	ShouldSync bool
	StartBlock uint64
	SyncMode   string // "FAST", "FULL", "STATE_CHECK", "None"
	Reason     string
}

// HandlePriorSync processes the client's PriorSync request
// as per the sketch: Check Checksum, BlockNum, BlockHash, StateRoot, Version
func (h *PriorSyncHandler) HandlePriorSync(clientState types.PriorSync) (SyncDecision, error) {
	// 1. Version Check
	if clientState.Metadata.Version != constants.PriorSyncVersion {
		return SyncDecision{
			ShouldSync: false,
			Reason:     fmt.Sprintf("Version mismatch: client=%d, server=%d", clientState.Metadata.Version, constants.PriorSyncVersion),
		}, nil
	}

	// 2. Fetch local block at client's height
	serverBlock, err := h.chain.GetBlockByNumber(clientState.Blocknumber)
	if err != nil {
		// If client is ahead or block not found
		localHeight := h.chain.CurrentBlockHeight()
		if clientState.Blocknumber > localHeight {
			return SyncDecision{
				ShouldSync: false,
				Reason:     "Client is ahead of Server",
			}, nil
		}
		return SyncDecision{}, err
	}

	// 3. Verify Checksum (if applicable) & Hash integrity
	if !bytes.Equal(clientState.Blockhash, serverBlock.Blockhash) {
		// Hash Mismatch -> FORK or Corruption
		return SyncDecision{
			ShouldSync: true,
			StartBlock: clientState.Blocknumber, // Needs binary search/ancestor finding in real implemention
			SyncMode:   "FORK_RECOVERY",
			Reason:     "Block hash mismatch",
		}, nil
	}

	// 4. Verify State Root
	if !bytes.Equal(clientState.Stateroot, serverBlock.Stateroot) {
		// Hash match but State mismatch -> State Corruption
		return SyncDecision{
			ShouldSync: true,
			StartBlock: clientState.Blocknumber,
			SyncMode:   "STATE_RECOVERY",
			Reason:     "State root mismatch",
		}, nil
	}

	// 5. If everything matches
	localHeight := h.chain.CurrentBlockHeight()
	if localHeight > clientState.Blocknumber {
		return SyncDecision{
			ShouldSync: true,
			StartBlock: clientState.Blocknumber + 1,
			SyncMode:   "FAST_FORWARD",
			Reason:     "Client is behind",
		}, nil
	}

	return SyncDecision{
		ShouldSync: false,
		Reason:     "In Sync",
	}, nil
}
