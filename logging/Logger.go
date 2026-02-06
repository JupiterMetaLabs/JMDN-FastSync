package logging

import "github.com/JupiterMetaLabs/ion"

func Logger(name string) *ion.Ion {
	logger, err := NewAsyncLogger().Get().NamedLogger(name, "")
	if err != nil {
		return nil
	}
	return logger.NamedLogger
}
