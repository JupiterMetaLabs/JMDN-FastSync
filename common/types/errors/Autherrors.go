package errors

import "errors"

var (
	RecordNotFound = errors.New("record not found")
	RecordExpired = errors.New("record expired")
)
