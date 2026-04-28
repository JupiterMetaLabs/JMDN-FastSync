// Package checksum_accountsync exposes integrity-check helpers for the
// AccountSync ART upload protocol. It wraps the internal checksum primitives
// so callers outside the JMDN-FastSync module (e.g. the test suite) can
// compute and verify ART checksums without importing internal packages.
package checksum_accountsync

import (
	checksumpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/checksum"
	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/checksum"
)

// ARTChecksum provides Create / Verify / CreateProto over raw ART byte slices
// (the output of art.Encode). It is stateless; reuse freely.
type ARTChecksum struct{}

// AccountSyncChecksum returns an ARTChecksum instance.
//
// Time: O(1). Space: O(1).
func AccountSyncChecksum() *ARTChecksum {
	return &ARTChecksum{}
}

// Create computes a checksum over artBytes using the given version.
// version 1 = CRC32 (4 bytes big-endian), version 2 = SHA256 (32 bytes).
//
// Time: O(n) where n = len(artBytes). Space: O(1).
func (c *ARTChecksum) Create(artBytes []byte, version uint16) ([]byte, error) {
	return checksum.NewChecksum().Create(artBytes, version)
}

// Verify reports whether the checksum of artBytes matches expected for the
// given version. Returns false (not an error) on a mismatch.
//
// Time: O(n) where n = len(artBytes). Space: O(1).
func (c *ARTChecksum) Verify(artBytes []byte, version uint16, expected []byte) (bool, error) {
	return checksum.NewChecksum().Verify(artBytes, version, expected)
}

// CreateProto computes a CRC32-IEEE checksum over artBytes and returns it as
// a *checksumpb.Checksum proto message ready to set on AccountNonceSyncRequest.
//
// Time: O(n) where n = len(artBytes). Space: O(1).
func (c *ARTChecksum) CreateProto(artBytes []byte, version uint16) (*checksumpb.Checksum, error) {
	csBytes, err := checksum.NewChecksum().Create(artBytes, version)
	if err != nil {
		return nil, err
	}
	return &checksumpb.Checksum{
		Checksum: csBytes,
		Version:  checksumpb.ChecksumVersion(version),
	}, nil
}
