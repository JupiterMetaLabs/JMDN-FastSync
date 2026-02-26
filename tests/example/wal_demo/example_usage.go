package main

import (
	"fmt"
	"log"
	"time"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/ack"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/headersync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/merkle"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/phase"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/priorsync"
	wal_types "github.com/JupiterMetaLabs/JMDN-FastSync/common/types/wal"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
)

// Example usage demonstrating the event sourcing architecture with adapter pattern

func ExampleHeaderSyncUsage() {
	// Initialize WAL
	wal, err := WAL.NewWAL(wal_types.DefaultDir, 1000)
	if err != nil {
		log.Fatalf("Failed to create WAL: %v", err)
	}
	defer wal.Close()

	// Create a header sync event using proto types
	headerEvent := &WAL.HeaderSyncEvent{
		BaseEvent: wal_types.BaseEvent{
			Operation: wal_types.OpAppend, // This is an APPEND operation
		},
		Response: &headersync.HeaderSyncResponse{
			Header: []*block.Header{
				{
					BlockNumber: 12345,
					BlockHash:   []byte("block_hash_bytes"),
					PrevHash:    []byte("prev_hash_bytes"),
					Timestamp:   time.Now().Unix(),
					Status:      "confirmed",
				},
			},
			Version: 1,
			Ack: &ack.Ack{
				Ok: true,
			},
			Phase: &phase.Phase{
				PresentPhase:    "header_sync",
				SuccessivePhase: "merkle_sync",
				Success:         true,
			},
		},
	}

	// Write event to WAL - LSN is automatically assigned
	lsn, err := wal.WriteEvent(headerEvent)
	if err != nil {
		log.Fatalf("Failed to write event: %v", err)
	}

	fmt.Printf("Header event written with LSN: %d, Operation: %s\n", lsn, headerEvent.Operation)
}

func ExampleMerkleSyncUsage() {
	wal, err := WAL.NewWAL(wal_types.DefaultDir, 1000)
	if err != nil {
		log.Fatalf("Failed to create WAL: %v", err)
	}
	defer wal.Close()

	// Create a Merkle sync event
	merkleEvent := &WAL.MerkleSyncEvent{
		BaseEvent: wal_types.BaseEvent{
			Operation: wal_types.OpAppend,
		},
		Message: &merkle.MerkleMessage{
			Snapshot: &merkle.MerkleSnapshot{
				Version:            1,
				TotalBlocks:        1000,
				ExpectedNextHeight: 1001,
				EnforceHeights:     true,
				Config: &merkle.SnapshotConfig{
					BlockMerge:    16,
					ExpectedTotal: 10000,
				},
			},
			Ack: &ack.Ack{Ok: true},
			Phase: &phase.Phase{
				PresentPhase: "merkle_sync",
				Success:      true,
			},
		},
	}

	lsn, err := wal.WriteEvent(merkleEvent)
	if err != nil {
		log.Fatalf("Failed to write event: %v", err)
	}

	fmt.Printf("Merkle event written with LSN: %d, Operation: %s\n", lsn, merkleEvent.Operation)
}

func ExamplePriorSyncUsage() {
	wal, err := WAL.NewWAL(wal_types.DefaultDir, 1000)
	if err != nil {
		log.Fatalf("Failed to create WAL: %v", err)
	}
	defer wal.Close()

	// Create a prior sync event
	priorEvent := &WAL.PriorSyncEvent{
		BaseEvent: wal_types.BaseEvent{
			Operation: wal_types.OpSync, // This is a SYNC checkpoint
		},
		Message: &priorsync.PriorSyncMessage{
			Priorsync: &priorsync.PriorSync{
				Blocknumber: 10000,
				Stateroot:   []byte("state_root"),
				Blockhash:   []byte("block_hash"),
				Range: &merkle.Range{
					Start: 1,
					End:   10000,
				},
			},
			Ack: &ack.Ack{Ok: true},
			Phase: &phase.Phase{
				PresentPhase: "prior_sync",
				Success:      true,
			},
		},
	}

	lsn, err := wal.WriteEvent(priorEvent)
	if err != nil {
		log.Fatalf("Failed to write event: %v", err)
	}

	fmt.Printf("Prior sync event written with LSN: %d, Operation: %s\n", lsn, priorEvent.Operation)
}

func ExampleDataSyncUsage() {
	wal, err := WAL.NewWAL(wal_types.DefaultDir, 1000)
	if err != nil {
		log.Fatalf("Failed to create WAL: %v", err)
	}
	defer wal.Close()

	// Create a data sync event (full ZKBlock)
	dataEvent := &WAL.DataSyncEvent{
		BaseEvent: wal_types.BaseEvent{
			Operation: wal_types.OpAppend,
		},
		Block: &block.ZKBlock{
			BlockNumber: 12345,
			BlockHash:   []byte("block_hash"),
			PrevHash:    []byte("prev_hash"),
			StateRoot:   []byte("state_root"),
			ProofHash:   "proof_hash",
			Status:      "confirmed",
			Timestamp:   time.Now().Unix(),
			GasLimit:    8000000,
			GasUsed:     5000000,
			Transactions: []*block.Transaction{
				{
					Hash:  []byte("tx_hash"),
					From:  []byte("from_address"),
					To:    []byte("to_address"),
					Nonce: 1,
				},
			},
		},
	}

	lsn, err := wal.WriteEvent(dataEvent)
	if err != nil {
		log.Fatalf("Failed to write event: %v", err)
	}

	fmt.Printf("Data event written with LSN: %d, Operation: %s\n", lsn, dataEvent.Operation)
}

func ExampleMultipleEventTypes() {
	// Initialize WAL with custom batch size
	wal, err := WAL.NewWAL(wal_types.DefaultDir, 500)
	if err != nil {
		log.Fatalf("Failed to create WAL: %v", err)
	}
	defer wal.Close()

	// Write different event types - they all share the same LSN sequence
	events := []wal_types.EventAdapter{
		&WAL.HeaderSyncEvent{
			BaseEvent: wal_types.BaseEvent{Operation: wal_types.OpAppend},
			Response: &headersync.HeaderSyncResponse{
				Header:  []*block.Header{{BlockNumber: 1, Status: "verified"}},
				Version: 1,
			},
		},
		&WAL.MerkleSyncEvent{
			BaseEvent: wal_types.BaseEvent{Operation: wal_types.OpAppend},
			Message: &merkle.MerkleMessage{
				Snapshot: &merkle.MerkleSnapshot{Version: 1},
			},
		},
		&WAL.PriorSyncEvent{
			BaseEvent: wal_types.BaseEvent{Operation: wal_types.OpSync},
			Message: &priorsync.PriorSyncMessage{
				Priorsync: &priorsync.PriorSync{Blocknumber: 100},
			},
		},
		&WAL.DataSyncEvent{
			BaseEvent: wal_types.BaseEvent{Operation: wal_types.OpAppend},
			Block:     &block.ZKBlock{BlockNumber: 1, Status: "confirmed"},
		},
	}

	for _, event := range events {
		lsn, err := wal.WriteEvent(event)
		if err != nil {
			log.Fatalf("Failed to write event: %v", err)
		}
		fmt.Printf(">>> Writing Event to WAL at %s\n", wal.Dir)
		fmt.Printf("    Type: %s, Operation: %s, LSN: %d\n",
			event.GetType(), event.GetOperation(), lsn)
	}

	// Force flush to ensure all events are persisted
	if err := wal.Flush(); err != nil {
		log.Fatalf("Failed to flush: %v", err)
	}

	fmt.Printf("Last LSN: %d, Last Flushed LSN: %d\n",
		wal.GetLastLSN(), wal.GetLastFlushedLSN())
}

func ExampleReplayByType() {
	wal, err := WAL.NewWAL(wal_types.DefaultDir+"/typed", 1000)
	if err != nil {
		log.Fatalf("Failed to create WAL: %v", err)
	}
	defer wal.Close()

	// Replay only header sync events
	fmt.Println("Replaying only header sync events...")
	err = wal.ReplayEventsByType(wal_types.HeaderSync, func(entry WAL.WALEntry) error {
		event := &WAL.HeaderSyncEvent{}
		if err := event.Deserialize(entry.Data); err != nil {
			return err
		}

		if event.Response != nil && len(event.Response.Header) > 0 {
			header := event.Response.Header[0]
			fmt.Printf("Header Block: %d, Operation: %s, LSN: %d\n",
				header.BlockNumber, event.Operation, entry.LSN)
		}

		// Hydrate header state...
		return nil
	})

	if err != nil {
		log.Fatalf("Failed to replay: %v", err)
	}
}

func ExampleTruncateOldEvents() {
	wal, err := WAL.NewWAL(wal_types.DefaultDir+"/truncate", 1000)
	if err != nil {
		log.Fatalf("Failed to create WAL: %v", err)
	}
	defer wal.Close()

	// After successful hydration, you can truncate old events
	// to save disk space
	lastProcessedLSN := uint64(1000)

	err = wal.TruncateBefore(lastProcessedLSN)
	if err != nil {
		log.Fatalf("Failed to truncate: %v", err)
	}

	fmt.Printf("Truncated all events before LSN: %d\n", lastProcessedLSN)
}

func ExampleHydration() {
	dir := wal_types.DefaultDir // Use events from Phase 1

	// --- Hydration Phase ---
	fmt.Printf(">>> Hydrating state from existing WAL at: %s\n", dir)
	newWal, err := WAL.NewWAL(dir, 10)
	if err != nil {
		log.Fatalf("Failed to open WAL for hydration: %v", err)
	}
	defer newWal.Close()

	// Representing our in-memory state
	headerState := make(map[uint64]string)

	err = newWal.ReplayEvents(func(entry WAL.WALEntry) error {
		event, err := WAL.EventFactory(entry.Type)
		if err != nil {
			return err
		}
		if err := event.Deserialize(entry.Data); err != nil {
			return err
		}

		// Hydrate our state based on event data
		switch event.GetType() {
		case wal_types.HeaderSync:
			if hEvent, ok := event.(*WAL.HeaderSyncEvent); ok && hEvent.Response != nil && len(hEvent.Response.Header) > 0 {
				h := hEvent.Response.Header[0]
				headerState[h.BlockNumber] = h.Status
				fmt.Printf("    [Hydration] Loaded Header: Block %d, Status: %s\n", h.BlockNumber, h.Status)
			}
		case wal_types.DataSync:
			if dEvent, ok := event.(*WAL.DataSyncEvent); ok && dEvent.Block != nil {
				fmt.Printf("    [Hydration] Loaded Data: Block %d, Status: %s\n", dEvent.Block.BlockNumber, dEvent.Block.Status)
			}
		case wal_types.MerkleSync:
			fmt.Printf("    [Hydration] Loaded Merkle Sync Event (LSN: %d)\n", entry.LSN)
		case wal_types.PriorSync:
			fmt.Printf("    [Hydration] Loaded Prior Sync Event (LSN: %d)\n", entry.LSN)
		default:
			fmt.Printf("    [Hydration] Loaded Other Event: %s (LSN: %d)\n", event.GetType(), entry.LSN)
		}
		return nil
	})

	if err != nil {
		log.Fatalf("Hydration failed: %v", err)
	}

	fmt.Println(">>> Step 4: Hydration Complete. Final In-Memory State:")
	for blockNum, status := range headerState {
		fmt.Printf("    Block %d: %s\n", blockNum, status)
	}
}
