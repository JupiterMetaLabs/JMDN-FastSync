package accountssync_test

import (
	"fmt"
	"net"
	"testing"
	"time"

	accountspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/accounts"
	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/ack"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/messaging"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/pbstream"
)

// netPipeStream wraps net.Conn with deadline methods matching streamReadWriter.
type netPipeStream struct{ net.Conn }

func (p *netPipeStream) SetReadDeadline(t time.Time) error  { return p.Conn.SetDeadline(t) }
func (p *netPipeStream) SetWriteDeadline(t time.Time) error { return p.Conn.SetDeadline(t) }

func TestWriteAccountPageAndReadACK(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	msg := &accountspb.AccountSyncServerMessage{
		Payload: &accountspb.AccountSyncServerMessage_Response{
			Response: &accountspb.AccountSyncResponse{PageIndex: 7},
		},
	}
	wantAck := &accountspb.AccountSyncServerMessage{
		Payload: &accountspb.AccountSyncServerMessage_BatchAck{
			BatchAck: &accountspb.AccountBatchAck{
				Ack: &ackpb.Ack{Ok: true},
			},
		},
	}

	done := make(chan error, 1)
	go func() {
		got := &accountspb.AccountSyncServerMessage{}
		if err := pbstream.ReadDelimited(serverConn, got); err != nil {
			done <- err
			return
		}
		if got.GetResponse().GetPageIndex() != 7 {
			done <- fmt.Errorf("expected page index 7, got %d", got.GetResponse().GetPageIndex())
			return
		}
		done <- pbstream.WriteDelimited(serverConn, wantAck)
	}()

	ack, err := messaging.WriteAccountPageAndReadACK(&netPipeStream{clientConn}, msg, 2*time.Second)
	if err != nil {
		t.Fatalf("WriteAccountPageAndReadACK error: %v", err)
	}
	if !ack.GetBatchAck().GetAck().GetOk() {
		t.Fatal("expected ok=true in ACK")
	}
	if serverErr := <-done; serverErr != nil {
		t.Fatalf("server error: %v", serverErr)
	}
}
