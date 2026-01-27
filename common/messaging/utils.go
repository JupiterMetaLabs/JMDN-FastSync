package messaging

import (
	"fmt"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// ParseMultiaddr parses a multiaddr string and extracts peer information.
//
// Parameters:
//   - addr: Multiaddr string (e.g., "/ip4/127.0.0.1/udp/4001/quic-v1/p2p/QmPeerID")
//
// Returns:
//   - peer.AddrInfo containing peer ID and addresses
//   - Error if parsing fails
func ParseMultiaddr(addr string) (peer.AddrInfo, error) {
	maddr, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return peer.AddrInfo{}, fmt.Errorf("invalid multiaddr: %w", err)
	}

	peerInfo, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return peer.AddrInfo{}, fmt.Errorf("failed to extract peer info: %w", err)
	}

	return *peerInfo, nil
}

// FormatMultiaddr formats a peer ID and multiaddr into a full multiaddr string.
//
// Parameters:
//   - addr: Base multiaddr (e.g., "/ip4/127.0.0.1/udp/4001/quic-v1")
//   - peerID: Peer ID to append
//
// Returns:
//   - Full multiaddr string with peer ID
func FormatMultiaddr(addr multiaddr.Multiaddr, peerID peer.ID) string {
	return fmt.Sprintf("%s/p2p/%s", addr, peerID)
}

// HasProtocol checks if a multiaddr contains a specific protocol.
//
// Parameters:
//   - addr: Multiaddr to check
//   - protocol: Protocol name to look for (e.g., "tcp", "quic", "quic-v1")
//
// Returns:
//   - true if the protocol is present in the multiaddr
func HasProtocol(addr multiaddr.Multiaddr, protocol string) bool {
	protocols := addr.Protocols()
	for _, p := range protocols {
		if strings.Contains(strings.ToLower(p.Name), strings.ToLower(protocol)) {
			return true
		}
	}
	return false
}

// FilterByTransport filters multiaddrs by transport protocol.
//
// Parameters:
//   - addrs: List of multiaddrs to filter
//   - transport: Transport protocol to filter by ("tcp", "quic")
//
// Returns:
//   - Filtered list of multiaddrs containing the specified transport
func FilterByTransport(addrs []multiaddr.Multiaddr, transport string) []multiaddr.Multiaddr {
	filtered := make([]multiaddr.Multiaddr, 0)
	for _, addr := range addrs {
		if HasProtocol(addr, transport) {
			filtered = append(filtered, addr)
		}
	}
	return filtered
}

// SelectTransportAddr selects the appropriate multiaddr based on version and transport priority.
//
// Version-based selection:
//   - V1 (version == 1): Returns first TCP address only
//   - V2 (version >= 2): Prioritizes QUIC, falls back to TCP if no QUIC available
//
// Parameters:
//   - addrs: List of multiaddrs to select from
//   - version: Protocol version (1 = TCP only, 2+ = QUIC preferred)
//
// Returns:
//   - Selected multiaddr
//   - Error if no suitable transport is available
func SelectTransportAddr(addrs []multiaddr.Multiaddr, version uint16) (multiaddr.Multiaddr, error) {
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no addresses provided")
	}

	// Default to V1 if version is 0
	if version == 0 {
		version = 1
	}

	// V1: TCP only
	if version == 1 {
		tcpAddrs := FilterByTransport(addrs, "tcp")
		if len(tcpAddrs) == 0 {
			return nil, fmt.Errorf("V1 requires TCP transport, but no TCP addresses found")
		}
		return tcpAddrs[0], nil
	}

	// V2+: Prefer QUIC, fallback to TCP
	// Try QUIC first (quic-v1 or quic)
	quicAddrs := FilterByTransport(addrs, "quic")
	if len(quicAddrs) > 0 {
		return quicAddrs[0], nil
	}

	// Fallback to TCP
	tcpAddrs := FilterByTransport(addrs, "tcp")
	if len(tcpAddrs) > 0 {
		return tcpAddrs[0], nil
	}

	return nil, fmt.Errorf("no suitable transport found (tried QUIC and TCP)")
}

// SelectTransportAddrWithFallback attempts to connect using the selected transport,
// and provides fallback logic for V2.
//
// This is a helper that returns both primary and fallback addresses.
//
// Parameters:
//   - addrs: List of multiaddrs
//   - version: Protocol version
//
// Returns:
//   - primary: Primary address to try first
//   - fallback: Fallback address to try if primary fails (nil if no fallback)
//   - Error if no suitable transport
func SelectTransportAddrWithFallback(addrs []multiaddr.Multiaddr, version uint16) (primary, fallback multiaddr.Multiaddr, err error) {
	if len(addrs) == 0 {
		return nil, nil, fmt.Errorf("no addresses provided")
	}

	// Default to V1 if version is 0
	if version == 0 {
		version = 1
	}

	// V1: TCP only, no fallback
	if version == 1 {
		tcpAddrs := FilterByTransport(addrs, "tcp")
		if len(tcpAddrs) == 0 {
			return nil, nil, fmt.Errorf("V1 requires TCP transport, but no TCP addresses found")
		}
		return tcpAddrs[0], nil, nil
	}

	// V2+: QUIC primary, TCP fallback
	quicAddrs := FilterByTransport(addrs, "quic")
	tcpAddrs := FilterByTransport(addrs, "tcp")

	// If we have QUIC, use it as primary
	if len(quicAddrs) > 0 {
		primary = quicAddrs[0]
		// TCP as fallback if available
		if len(tcpAddrs) > 0 {
			fallback = tcpAddrs[0]
		}
		return primary, fallback, nil
	}

	// No QUIC, use TCP as primary
	if len(tcpAddrs) > 0 {
		return tcpAddrs[0], nil, nil
	}

	return nil, nil, fmt.Errorf("no suitable transport found (tried QUIC and TCP)")
}
