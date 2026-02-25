package main

import (
	"fmt"
	"os"
	"os/exec"

	wal_types "github.com/JupiterMetaLabs/JMDN-FastSync/internal/types/wal"
)

func main() {
	fmt.Println("========================================")
	fmt.Println("Starting WAL Event Sourcing Demo")
	fmt.Println("========================================")

	// Clean up previous demo data
	os.RemoveAll(wal_types.DefaultDir)

	fmt.Println("\n--- Phase 1: Writing 10 Events ---")
	ExampleMultipleEventTypes() // 4 events
	ExampleHeaderSyncUsage()    // 1 event
	ExampleMerkleSyncUsage()    // 1 event
	ExamplePriorSyncUsage()     // 1 event
	ExampleDataSyncUsage()      // 1 event
	ExampleHeaderSyncUsage()    // 1 event
	ExampleDataSyncUsage()      // 1 event
	// Total: 4 + 1 + 1 + 1 + 1 + 1 + 1 = 10 events

	fmt.Println("\n--- Phase 5: Hydration (Rebuilding State) ---")
	ExampleHydration()

	fmt.Println("\n--- Phase 7: Inspect WAL Files ---")
	fmt.Printf("Listing files in %s:\n", wal_types.DefaultDir)
	cmd := exec.Command("ls", "-R", wal_types.DefaultDir)
	cmd.Stdout = os.Stdout
	cmd.Run()

	fmt.Println("\n========================================")
	fmt.Println("WAL Demo Completed Successfully")
	fmt.Println("========================================")
}
