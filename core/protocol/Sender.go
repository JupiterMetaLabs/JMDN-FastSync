package protocol

import (
  "context"
  "encoding/json"
  "time"

  "github.com/libp2p/go-libp2p/core/host"
  "github.com/libp2p/go-libp2p/core/peer"
  "github.com/libp2p/go-libp2p/core/protocol"
)

func SendMsg(ctx context.Context, h host.Host, to peer.AddrInfo, pid protocol.ID, m Msg) error {
  // Ensure we know how to reach the peer
  h.Peerstore().AddAddrs(to.ID, to.Addrs, time.Minute)

  // Connect (optional if already connected, but safe)
  if err := h.Connect(ctx, to); err != nil {
    return err
  }

  // Open stream for protocol
  s, err := h.NewStream(ctx, to.ID, pid)
  if err != nil {
    return err
  }
  defer s.Close()

  _ = s.SetWriteDeadline(time.Now().Add(10 * time.Second))
  if err := json.NewEncoder(s).Encode(&m); err != nil {
    return err
  }
  _ = s.SetWriteDeadline(time.Time{})

  // Optional: read reply
  _ = s.SetReadDeadline(time.Now().Add(10 * time.Second))
  var resp Msg
  if err := json.NewDecoder(s).Decode(&resp); err == nil {
    // use resp
  }
  _ = s.SetReadDeadline(time.Time{})

  return nil
}