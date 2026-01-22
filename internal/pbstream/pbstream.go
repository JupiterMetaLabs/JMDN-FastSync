package pbstream

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"
)

// WriteDelimited writes: [uvarint length][protobuf bytes]
func WriteDelimited(w io.Writer, msg proto.Message) error {
	b, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
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
func ReadDelimited(r io.Reader, msg proto.Message) error {
	br := bufio.NewReader(r)

	l, err := binary.ReadUvarint(br)
	if err != nil {
		return fmt.Errorf("read len: %w", err)
	}
	if l == 0 {
		return fmt.Errorf("invalid length 0")
	}

	buf := make([]byte, l)
	if _, err := io.ReadFull(br, buf); err != nil {
		return fmt.Errorf("read msg: %w", err)
	}
	if err := proto.Unmarshal(buf, msg); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	return nil
}