package checksum

import (
	"crypto/sha256"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/errors"
	"encoding/binary"
	"hash/crc32"
)

type Checksum struct{}

// Supported checksum versions
const (
	VersionCRC32  uint16 = 1
	VersionSHA256 uint16 = 2
)

type checksumFunc interface {
	Create(data []byte, version uint16) ([]byte, error)
	Verify(data []byte, version uint16, expected []byte) (bool, error)
}

func NewChecksum() checksumFunc {
	return &Checksum{}
}

// Create returns the checksum bytes for the given version.
// For CRC32 it returns 4 bytes (big-endian).
// For SHA256 it returns 32 bytes.
func (cs *Checksum) Create(data []byte, version uint16) ([]byte, error) {
	if data == nil {
		return nil, errors.NilData
	}

	switch version {
	case VersionCRC32:
		sum := crc32.ChecksumIEEE(data)
		out := make([]byte, 4)
		binary.BigEndian.PutUint32(out, sum)
		return out, nil

	case VersionSHA256:
		sum := sha256.Sum256(data)
		return sum[:], nil

	default:
		return nil, errors.UnsupportedChecksumVersion
	}
}

func (cs *Checksum) Verify(data []byte, version uint16, expected []byte) (bool, error) {
	got, err := cs.Create(data, version)
	if err != nil {
		return false, err
	}
	if len(got) != len(expected) {
		return false, nil
	}
	for i := range got {
		if got[i] != expected[i] {
			return false, nil
		}
	}
	return true, nil
}
