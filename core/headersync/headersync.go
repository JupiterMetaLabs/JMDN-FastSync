package headersync

import (
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/router"
)

type HeaderSync struct {
	nodeinfo   *types.Nodeinfo
	Datarouter *router.Datarouter
}