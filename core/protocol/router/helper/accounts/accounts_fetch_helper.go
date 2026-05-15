package accounts

import (
	"context"
	"strings"

	accountspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/accounts"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	Log "github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	"github.com/JupiterMetaLabs/ion"
)

const fetchLogger = "log:accountsfetch"

// lookupKeyForFetch returns the on-chain account address string used for DB lookup.
// Keys may be raw addresses or DID strings ("did:...:address"); the last ':' segment
// of a DID is treated as the address.
func lookupKeyForFetch(addr string) string {
	if strings.HasPrefix(addr, "did:") {
		parts := strings.Split(addr, ":")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	return addr
}

// FetchAccountsByAddresses looks up each address key in the provided set,
// converts found records to proto, and returns the slice.
// Not-found addresses are silently skipped (not an error).
func FetchAccountsByAddresses(ctx context.Context, blockInfo types.BlockInfo, addresses map[string]bool) ([]*accountspb.Account, error) {
	accountMgr := blockInfo.NewAccountManager()
	protoAccounts := make([]*accountspb.Account, 0, len(addresses))

	for addr := range addresses {
		lookup := lookupKeyForFetch(addr)
		acc, err := accountMgr.GetAccountByAddress(lookup)
		if err != nil {
			Log.Logger(fetchLogger).Warn(ctx, "AccountsFetch: lookup failed",
				ion.String("address", addr),
				ion.String("lookup", lookup),
				ion.Err(err))
			continue
		}
		if acc == nil {
			continue
		}
		protoAccounts = append(protoAccounts, &accountspb.Account{
			DidAddress:  acc.DIDAddress,
			Address:     acc.Address.Bytes(),
			Balance:     acc.Balance,
			Nonce:       acc.Nonce,
			AccountType: acc.AccountType,
			CreatedAt:   acc.CreatedAt,
			UpdatedAt:   acc.UpdatedAt,
		})
	}

	Log.Logger(fetchLogger).Info(ctx, "AccountsFetch complete",
		ion.Int("requested", len(addresses)),
		ion.Int("found", len(protoAccounts)))

	return protoAccounts, nil
}
