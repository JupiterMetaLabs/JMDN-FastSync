package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/WAL"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types/wal"
	wal_types "github.com/JupiterMetaLabs/JMDN-FastSync/internal/types/wal"
)

func demo_checkpoint() {
	dir := wal_types.DefaultDir
	os.RemoveAll(dir)
	// defer os.RemoveAll(dir) // Disabled so user can inspect files

	fmt.Println("=== WAL Segmentation & Checkpoint Demo ===")

	// 1. Initialize WAL
	w, err := WAL.NewWAL(dir, 100)
	if err != nil {
		log.Fatalf("Failed to create WAL: %v", err)
	}

	// 2. Trigger Segmentation (write ~110MB)
	fmt.Println("Phase 1: Triggering Segmentation (Writing ~110MB)...")
	largeData := make([]byte, 1024*1024) // 1MB
	for i := 0; i < 110; i++ {
		event := &WAL.DataSyncEvent{
			BaseEvent: wal.BaseEvent{Operation: wal.OpAppend},
			Block: &block.ZKBlock{
				BlockNumber: uint64(i),
				BlockHash:   []byte(fmt.Sprintf("hash_%d", i)),
				ProofHash:   string(largeData), // Large payload to force segmentation
			},
		}
		// Directly writing large data to force segmentation
		// We'll simulate a large payload in the event
		// Actually, let's just write many events to hit 100MB if we want to be realistic,
		// but for a demo, we can just write 110 iterations of a 1MB event if we modify the serialize.
		// For now, let's just write 110 events and check file count.
		// Wait, tidwall/wal segments are based on the Options.SegmentSize.

		_, err := w.WriteEvent(event)
		if err != nil {
			log.Fatalf("Failed to write event %d: %v", i, err)
		}
		if i%20 == 0 {
			fmt.Printf("  Written %d MB...\n", i)
		}
	}
	w.Flush()

	fmt.Println("Checking segment files...")
	files, _ := os.ReadDir(dir)
	segmentCount := 0
	for _, f := range files {
		if !f.IsDir() && len(f.Name()) == 20 { // tidwall default is 20 digit names
			segmentCount++
		}
	}
	fmt.Printf("Found %d WAL segment files.\n", segmentCount)

	// 3. Checkpoints
	fmt.Println("\nPhase 2: Checkpointing & Truncation...")
	for i := 1; i <= 6; i++ {
		// Write a few more events to change LSN
		for j := 0; j < 5; j++ {
			w.WriteEvent(&WAL.DataSyncEvent{
				BaseEvent: wal.BaseEvent{Operation: wal.OpAppend},
				Block:     &block.ZKBlock{BlockNumber: uint64(100 + i*10 + j)},
			})
		}

		lsn, _ := w.CreateCheckpoint()
		checkpointFiles, _ := os.ReadDir(filepath.Join(dir, wal.CheckpointDir))
		fmt.Printf("Created Checkpoint %d at LSN %d | Active markers: %d\n", i, lsn, len(checkpointFiles))
	}

	w.Close()

	// 4. Verification of Hydration post-truncation
	fmt.Println("\nPhase 3: Hydration after Truncation...")
	newW, _ := WAL.NewWAL(dir, 10)
	count := 0
	newW.ReplayEvents(func(entry WAL.WALEntry) error {
		count++
		return nil
	})
	fmt.Printf("Replayed %d events (should be significantly less than 140)\n", count)
	newW.Close()

	fmt.Println("\n=== Demo Completed ===")
}
