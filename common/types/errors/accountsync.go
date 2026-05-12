package errors

import "errors"

var(
	AccountsSyncRequestNil = errors.New("accounts sync request is nil")
	AccountsSyncArtNil = errors.New("accounts sync art is nil")
)