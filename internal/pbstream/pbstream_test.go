package pbstream

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	ackpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/ack"
)

// plainReader hides any ReadByte method so ReadDelimited sees what a libp2p
// stream looks like: an io.Reader and nothing else.
type plainReader struct{ io.Reader }

func TestReadDelimitedRejectsOversizedLength(t *testing.T) {
	for _, l := range []uint64{MaxFrameSize + 1, 1 << 62} {
		var hdr [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(hdr[:], l)
		err := ReadDelimited(plainReader{bytes.NewReader(hdr[:n])}, &ackpb.Ack{})
		if !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("length %d: err = %v, want ErrFrameTooLarge", l, err)
		}
	}
}

// Two frames written back to back must both be readable from the same stream:
// the reader may not consume bytes past the frame it returns.
func TestReadDelimitedConsumesOneFrame(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteDelimited(&buf, &ackpb.Ack{Ok: true}); err != nil {
		t.Fatal(err)
	}
	if err := WriteDelimited(&buf, &ackpb.Ack{Error: "second"}); err != nil {
		t.Fatal(err)
	}
	r := plainReader{&buf}

	first := &ackpb.Ack{}
	if err := ReadDelimited(r, first); err != nil || !first.Ok {
		t.Fatalf("first frame: %v %+v", err, first)
	}
	second := &ackpb.Ack{}
	if err := ReadDelimited(r, second); err != nil || second.Error != "second" {
		t.Fatalf("second frame: %v %+v", err, second)
	}
}
