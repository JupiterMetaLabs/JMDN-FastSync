package errors

import "errors"

var(
	MetadataRequired = errors.New("metadata is required")
	InvalidChecksum = errors.New("invalid checksum")
	ChecksumMismatch = errors.New("checksum mismatch")
	UnsupportedChecksumVersion = errors.New("unsupported checksum version")
	NilData = errors.New("data is nil")
	BlockInfoNil = errors.New("block info is nil")
	AuthRequired = errors.New("authentication is required")
)
