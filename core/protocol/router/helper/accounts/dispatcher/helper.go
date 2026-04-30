package dispatcher

import (
	"errors"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
)

// DefaultDispatcherConfig returns a DispatcherConfig populated from
// common/types/constants/accounts_constants.go.
// Override individual fields before passing to NewAccountDispatcher.
//
// Time: O(1). Space: O(1).
func Default() types.DispatcherConfig {
	return types.DispatcherConfig{
		DispatchWorkers:           constants.DispatchWorkers,
		NonceBufferPauseThreshold: constants.NonceBufferPauseThreshold,
		NonceBufferResumePct:      constants.NonceBufferResumePct,
		NoncePageSize:             constants.NoncePageSize,
		DispatchMaxRetries:        constants.DispatchMaxRetries,
		DispatchACKTimeout:        constants.DispatchACKTimeout,
	}
}

func validateDispatcherConfig(cfg types.DispatcherConfig, callbacks types.DispatcherCallbacks) error {
	if cfg.DispatchWorkers <= 0 {
		return errors.New("dispatcher: DispatchWorkers must be > 0")
	}
	if cfg.NonceBufferPauseThreshold <= 0 {
		return errors.New("dispatcher: NonceBufferPauseThreshold must be > 0")
	}
	if cfg.NonceBufferResumePct <= 0 || cfg.NonceBufferResumePct >= 100 {
		return errors.New("dispatcher: NonceBufferResumePct must be in (0,100)")
	}
	if cfg.NoncePageSize <= 0 {
		return errors.New("dispatcher: NoncePageSize must be > 0")
	}
	if int64(cfg.NoncePageSize) > cfg.NonceBufferPauseThreshold {
		return errors.New("dispatcher: NoncePageSize cannot exceed NonceBufferPauseThreshold")
	}
	if cfg.DispatchMaxRetries < 0 {
		return errors.New("dispatcher: DispatchMaxRetries must be >= 0")
	}
	if cfg.DispatchACKTimeout <= 0 {
		return errors.New("dispatcher: DispatchACKTimeout must be > 0")
	}
	if callbacks.FetchAccounts == nil {
		return errors.New("dispatcher: FetchAccounts callback is required")
	}
	if callbacks.SendPage == nil {
		return errors.New("dispatcher: SendPage callback is required")
	}
	return nil
}