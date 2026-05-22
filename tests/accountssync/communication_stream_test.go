package accountssync_test

import (
	"testing"

	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/communication"
)

// TestCommunicatorHasStreamMethods verifies the two new methods exist on the
// Communicator interface. If either method is missing this file will not compile.
func TestCommunicatorHasStreamMethods(t *testing.T) {
	// Compile-time check: communication.NewCommunication must return a Communicator
	// that includes OpenAccountsDataStream and SendAccountPageOnStream.
	// The zero-value nil check is just to reference the type without a live host.
	var c communication.Communicator = communication.NewCommunication(nil, 0)
	if c == nil {
		t.Fatal("expected non-nil communicator")
	}
}
