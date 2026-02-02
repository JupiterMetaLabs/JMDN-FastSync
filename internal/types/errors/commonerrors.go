package errors

import "errors"

var(
	InvalidChecksum = errors.New("invalid checksum")
	UnsupportedChecksumVersion = errors.New("unsupported checksum version")
	NilData = errors.New("data is nil")
)
