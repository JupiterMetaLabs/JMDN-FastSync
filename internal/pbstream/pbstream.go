package pbstream

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"
)

// MaxFrameSize caps one length-prefixed frame. The length prefix comes from the
// peer, so without a cap a single frame header can demand an arbitrarily large
// allocation. 128 MiB leaves headroom over the largest legitimate frame (a
// 30-block DataSync response with proofs measured ~84 MB on testnet).
const MaxFrameSize = 128 << 20

// ErrFrameTooLarge is returned when a frame's declared length exceeds MaxFrameSize.
var ErrFrameTooLarge = errors.New("pbstream: frame exceeds MaxFrameSize")

// WriteDelimited writes: [uvarint length][protobuf bytes]
func WriteDelimited(w io.Writer, msg proto.Message) error {
	b, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if len(b) > MaxFrameSize {
		return fmt.Errorf("write %d bytes: %w", len(b), ErrFrameTooLarge)
	}

	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(b)))

	if _, err := w.Write(lenBuf[:n]); err != nil {
		return fmt.Errorf("write len: %w", err)
	}
	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("write msg: %w", err)
	}
	return nil
}

// ReadDelimited reads: [uvarint length][protobuf bytes]
//
// It consumes exactly one frame from r and nothing more, so it can be called
// repeatedly on the same stream (heartbeats, AccountSync batches).
func ReadDelimited(r io.Reader, msg proto.Message) error {
	l, err := binary.ReadUvarint(byteReader{r})
	if err != nil {
		return fmt.Errorf("read len: %w", err)
	}
	if l == 0 {
		return fmt.Errorf("invalid length 0")
	}
	if l > MaxFrameSize {
		return fmt.Errorf("read %d bytes: %w", l, ErrFrameTooLarge)
	}

	buf := make([]byte, l)
	if _, err := io.ReadFull(r, buf); err != nil {
		return fmt.Errorf("read msg: %w", err)
	}
	if err := proto.Unmarshal(buf, msg); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	return nil
}

// byteReader reads the length prefix one byte at a time. A bufio.Reader here
// would read ahead past the frame and lose those bytes when it is discarded.
type byteReader struct{ r io.Reader }

func (b byteReader) ReadByte() (byte, error) {
	if br, ok := b.r.(io.ByteReader); ok {
		return br.ReadByte()
	}
	var one [1]byte
	if _, err := io.ReadFull(b.r, one[:]); err != nil {
		return 0, err
	}
	return one[0], nil
}
