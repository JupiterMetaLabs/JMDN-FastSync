package thebesync

// Newline-delimited JSON framing over libp2p streams. A frame is one JSON object
// followed by '\n'; json.Marshal never emits a literal newline inside string or
// base64 values (it escapes them), so '\n' is a safe frame delimiter even for the
// opaque [][]byte block payloads.

import (
	"bufio"
	"encoding/json"
	"io"

	"github.com/libp2p/go-libp2p/core/network"
)

// readLine reads one newline-delimited frame, capping the read at max bytes so a
// hostile/oversized peer frame cannot OOM us. A trailing EOF without a newline is
// not an error (returns what was read).
func readLine(stream network.Stream, max int64) ([]byte, error) {
	reader := bufio.NewReader(io.LimitReader(stream, max))
	b, err := reader.ReadBytes('\n')
	if err != nil && err != io.EOF {
		return nil, err
	}
	return b, nil
}

// drainLine reads and discards one bounded frame (used when the request body is
// not needed, e.g. the v1 head handshake).
func drainLine(stream network.Stream, max int64) {
	_, _ = readLine(stream, max)
}

// writeJSONLine marshals v and writes it as one newline-terminated frame,
// best-effort (server responses).
func writeJSONLine(stream network.Stream, v any) {
	_ = writeJSONLineErr(stream, v)
}

// writeJSONLineErr is the error-returning variant used by the client, which must
// surface a failed request write.
func writeJSONLineErr(stream network.Stream, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = stream.Write(b)
	return err
}
