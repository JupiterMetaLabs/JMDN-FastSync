package errors

import "errors"

var(
	SameBlockHeight = errors.New("on the same block height")
	SameBlockHeight_DifferentStateroot = errors.New("on the same block height, stateroot mismatch")
	SameBlockHeight_DifferentBlockhash = errors.New("on the same block height, blockhash mismatch")
)