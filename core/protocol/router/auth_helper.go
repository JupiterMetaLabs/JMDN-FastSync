package router

import (
	"context"
	"errors"

	authpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability/auth"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/availability"
	Log "github.com/JupiterMetaLabs/JMDN-FastSync/logging"
)

/*
Let the authentication happens in the Data Router itself rather than doing it outside.
*/
func (router *Datarouter) Authenticate(ctx context.Context, auth_req *authpb.Auth, remote *types.Nodeinfo) (bool, error) {
	if auth_req == nil || auth_req.UUID == "" {
		return false, errors.New("authentication request is nil or UUID is empty")
	}
	if !availability.FastsyncReady().AmIAvailable() {
		return false, errors.New("node is not available for fast sync")
	}
	is_authenticated, err := router.Nodeinfo.BlockInfo.AUTH().IsAUTH(remote.PeerID, auth_req.UUID)
	if err != nil {
		Log.Logger(namedlogger).Error(ctx, "Error checking authentication", err)
		return false, err
	}
	return is_authenticated, nil
}

/*
After the authentication let reset the TTL
*/
func (router *Datarouter) ResetTTL(ctx context.Context, auth_req *authpb.Auth, remote *types.Nodeinfo) error {
	if auth_req == nil || auth_req.UUID == "" {
		return errors.New("authentication request is nil or UUID is empty")
	}
	// Reset the TTL for the remote node
	return router.Nodeinfo.BlockInfo.AUTH().ResetTTL(remote.PeerID)
}

/*
Check if the router is registered with a client (has a valid UUID)
*/
func (router *Datarouter) IsRegistered() bool {
	return router.clientGeneratedUUID != ""
}

/*
Get the stored client UUID
*/
func (router *Datarouter) GetClientUUID() string {
	return router.clientGeneratedUUID
}

/*
Clear the stored client UUID (for cleanup)
*/
func (router *Datarouter) ClearRegistration() {
	router.clientGeneratedUUID = ""
}