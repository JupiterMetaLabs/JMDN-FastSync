package accountssync_test

import (
	"io"
	"testing"
	"time"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
)

// mockStream satisfies types.AccountSyncStream for compile-time verification.
type mockStream struct{}

func (m *mockStream) Read(p []byte) (int, error)         { return 0, io.EOF }
func (m *mockStream) Write(p []byte) (int, error)        { return len(p), nil }
func (m *mockStream) Close() error                       { return nil }
func (m *mockStream) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockStream) SetWriteDeadline(t time.Time) error { return nil }

func TestAccountSyncStreamInterface(t *testing.T) {
	var _ types.AccountSyncStream = &mockStream{}
}

func TestDispatcherCallbacksHasOpenStream(t *testing.T) {
	cb := types.DispatcherCallbacks{}
	if cb.OpenStream != nil {
		t.Error("OpenStream should be nil on zero value")
	}
	if cb.SendPageOnStream != nil {
		t.Error("SendPageOnStream should be nil on zero value")
	}
}
