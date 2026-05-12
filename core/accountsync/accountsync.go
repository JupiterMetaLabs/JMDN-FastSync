package accountsync

import (
	"context"
	"fmt"
	"sync"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/WAL"
	checksum_accountsync "github.com/JupiterMetaLabs/JMDN-FastSync/common/checksum/checksum_accountsync"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/messaging"
	accountspb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/accounts"
	availabilitypb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability"
	authpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/availability/auth"
	phasepb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/phase"
	tagpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/tagging"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types"
	"github.com/JupiterMetaLabs/JMDN-FastSync/common/types/constants"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/communication"
	routerhelper "github.com/JupiterMetaLabs/JMDN-FastSync/core/protocol/router/helper"
	Log "github.com/JupiterMetaLabs/JMDN-FastSync/logging"
	art "github.com/JupiterMetaLabs/JMDN_Merkletree/art"
	"github.com/JupiterMetaLabs/ion"
	"github.com/libp2p/go-libp2p/core/host"
)

const encodeWorkers = 4 // parallel ART encode goroutines

// AccountSync is the client-side implementation of Phase 5.
// Holds session-scoped vars identical in shape to HeaderSync and DataSync.
type AccountSync struct {
	SyncVars   *types.Syncvars
	Comm       communication.Communicator
	ServerAuth *authpb.Auth
}

func NewAccountSync() AccountSync_router {
	return &AccountSync{}
}

func (as *AccountSync) SetSyncVars(ctx context.Context, protocolVersion uint16, nodeInfo types.Nodeinfo, node host.Host, wal *WAL.WAL) AccountSync_router {
	if as.SyncVars == nil {
		as.SyncVars = &types.Syncvars{}
	}
	as.Comm = communication.NewCommunication(node, protocolVersion)
	as.SyncVars.Version = protocolVersion
	as.SyncVars.NodeInfo = nodeInfo
	as.SyncVars.Ctx = ctx
	as.SyncVars.WAL = wal
	as.SyncVars.Node = node
	return as
}

func (as *AccountSync) GetSyncVars() *types.Syncvars {
	return as.SyncVars
}

// AccountSync uploads the local account nonce ART to the server and waits for
// the server to stream missing accounts back via dial-back pages.
//
// The AccountsSyncDataProtocol handler is already registered by
// priorsync.SetupNetworkHandlers — no need to register it here.
// This method only drives the client upload side and reads the final EndOfStream.
//
// Returns TotalAccounts from the server's EndOfStream once all pages are
// delivered and acked.
func (as *AccountSync) AccountSync(remote *availabilitypb.AvailabilityResponse) (uint64, error) {
	if remote == nil || remote.Nodeinfo == nil {
		return 0, fmt.Errorf("accountsync: remote is nil")
	}
	if remote.Auth == nil || remote.Auth.UUID == "" {
		return 0, fmt.Errorf("accountsync: remote has no auth UUID")
	}
	if as.Comm == nil {
		return 0, fmt.Errorf("accountsync: communicator not set — call SetSyncVars first")
	}

	ctx := as.SyncVars.Ctx
	as.ServerAuth = remote.Auth

	// buildCtx is cancelled on any return path, which unblocks any buildChunks
	// goroutine stuck waiting to send to the out channel after a stream error.
	buildCtx, buildCancel := context.WithCancel(ctx)
	defer buildCancel()

	remoteNodeInfo, err := routerhelper.NewNodeInfoHelper().ToNodeinfo(remote.Nodeinfo)
	if err != nil {
		return 0, fmt.Errorf("accountsync: parse remote nodeinfo: %w", err)
	}

	chunkCh, errCh := buildChunks(buildCtx, as.SyncVars.NodeInfo.BlockInfo, as.ServerAuth)

	var totalAccounts uint64

	handlers := messaging.AccountSyncHandlers{
		OnBatchAck: func(ack *accountspb.AccountBatchAck) error {
			if !ack.GetAck().GetOk() {
				return fmt.Errorf("accountsync: server rejected chunk: %s", ack.GetAck().GetError())
			}
			Log.Logger(Log.Sync).Info(ctx, "accountsync: chunk acked")
			return nil
		},
		OnResponse: func(resp *accountspb.AccountSyncResponse) error {
			// Pages come via dial-back on AccountsSyncDataProtocol, not here.
			Log.Logger(Log.Sync).Warn(ctx, "accountsync: unexpected Response on upload stream",
				ion.Int("page_index", int(resp.GetPageIndex())))
			return nil
		},
		OnEnd: func(eos *accountspb.AccountSyncEndOfStream) error {
			if !eos.GetAck().GetOk() {
				return fmt.Errorf("accountsync server error: %s", eos.GetAck().GetError())
			}
			totalAccounts = eos.TotalAccounts
			Log.Logger(Log.Sync).Info(ctx, "accountsync: complete",
				ion.Uint64("total_accounts", eos.TotalAccounts),
				ion.Int("total_pages", int(eos.TotalPages)))
			return nil
		},
	}

	if err := as.Comm.StreamAllAccountSyncChunks(ctx, *remoteNodeInfo, chunkCh, handlers); err != nil {
		return 0, fmt.Errorf("accountsync: stream: %w", err)
	}

	if err := <-errCh; err != nil {
		return 0, fmt.Errorf("accountsync: chunk pipeline: %w", err)
	}

	return totalAccounts, nil
}

func (as *AccountSync) Close() {
	if as.SyncVars != nil {
		as.SyncVars = nil
	}
	if as.Comm != nil {
		as.Comm = nil
	}
	as.ServerAuth = nil
}

// ─── buildChunks ─────────────────────────────────────────────────────────────

// buildChunks streams ready-to-send AccountNonceSyncRequests in BatchIndex order.
//
// Pipeline (three stages running concurrently):
//
//	Stage 1 — Producer (1 goroutine)
//	  Calls iter.NextBatch() one window at a time. Never holds more than one
//	  batch of MAX_ACCOUNT_NONCES nonces in memory at a time.
//
//	Stage 2 — Encode workers (encodeWorkers goroutines, parallel)
//	  Each worker builds an ART, encodes (zstd), and checksums one batch.
//	  Workers run concurrently so encoding overlaps with the network send.
//	  Peak memory: encodeWorkers × MAX_ACCOUNT_NONCES nonces simultaneously.
//
//	Stage 3 — Reorder buffer
//	  Workers finish out of order; this stage holds a pending map and emits
//	  chunks only when the next expected BatchIndex is ready. The out channel
//	  is buffered (encodeWorkers slots) so workers can stay encodeWorkers
//	  chunks ahead of the sender — peak-peak-peak / flat-flat-flat rhythm.
func buildChunks(ctx context.Context, blockInfo types.BlockInfo, auth *authpb.Auth) (<-chan *accountspb.AccountNonceSyncRequest, <-chan error) {
	out := make(chan *accountspb.AccountNonceSyncRequest, encodeWorkers)
	errCh := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(errCh)

		iter := blockInfo.NewAccountManager().NewAccountNonceIterator(constants.MAX_ACCOUNT_NONCES)
		defer iter.Close()

		totalKeys, err := iter.TotalAccounts()
		if err != nil {
			errCh <- fmt.Errorf("buildChunks: total accounts: %w", err)
			return
		}

		// Zero-account case: one empty-ART final chunk so the server runs
		// its diff and streams back all accounts it has.
		if totalKeys == 0 {
			req, err := makeChunk(nil, 0, 0, true, auth)
			if err != nil {
				errCh <- err
				return
			}
			select {
			case out <- req:
			case <-ctx.Done():
				errCh <- ctx.Err()
			}
			return
		}

		numBatches := uint32((int(totalKeys) + constants.MAX_ACCOUNT_NONCES - 1) / constants.MAX_ACCOUNT_NONCES)

		type encodeJob struct {
			batchIndex uint32
			nonces     []uint64
		}
		type encodeResult struct {
			batchIndex uint32
			req        *accountspb.AccountNonceSyncRequest
			err        error
		}

		jobChan := make(chan encodeJob, encodeWorkers*2)
		resultChan := make(chan encodeResult, encodeWorkers*2)

		var wg sync.WaitGroup
		for i := 0; i < encodeWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for job := range jobChan {
					isLast := job.batchIndex == numBatches-1
					req, err := makeChunk(job.nonces, job.batchIndex, totalKeys, isLast, auth)
					select {
					case resultChan <- encodeResult{job.batchIndex, req, err}:
					case <-ctx.Done():
						return
					}
				}
			}()
		}
		go func() {
			wg.Wait()
			close(resultChan)
		}()

		producerErr := make(chan error, 1)
		go func() {
			defer close(jobChan)
			var idx uint32
			for {
				select {
				case <-ctx.Done():
					producerErr <- ctx.Err()
					return
				default:
				}
				batch, err := iter.NextBatch()
				if err != nil {
					producerErr <- fmt.Errorf("buildChunks: iterator: %w", err)
					return
				}
				if len(batch) == 0 {
					producerErr <- nil
					return
				}
				nonces := make([]uint64, len(batch))
				for i, acc := range batch {
					nonces[i] = acc.Nonce
				}
				select {
				case jobChan <- encodeJob{idx, nonces}:
				case <-ctx.Done():
					producerErr <- ctx.Err()
					return
				}
				idx++
			}
		}()

		pending := make(map[uint32]*accountspb.AccountNonceSyncRequest)
		var nextExpected uint32

		for r := range resultChan {
			if r.err != nil {
				errCh <- r.err
				return
			}
			pending[r.batchIndex] = r.req
			for {
				req, ok := pending[nextExpected]
				if !ok {
					break
				}
				delete(pending, nextExpected)
				select {
				case out <- req:
				case <-ctx.Done():
					errCh <- ctx.Err()
					return
				}
				nextExpected++
			}
		}

		if err := <-producerErr; err != nil {
			errCh <- err
		}
	}()

	return out, errCh
}

// makeChunk builds one AccountNonceSyncRequest from a nonce slice.
// This is the CPU-heavy work that runs in parallel across encode workers.
func makeChunk(nonces []uint64, batchIndex uint32, totalKeys uint64, isLast bool, auth *authpb.Auth) (*accountspb.AccountNonceSyncRequest, error) {
	chunkART := art.New()
	var minNonce, maxNonce uint64
	for i, n := range nonces {
		chunkART.Insert(n)
		if i == 0 || n < minNonce {
			minNonce = n
		}
		if n > maxNonce {
			maxNonce = n
		}
	}

	encoded := art.Encode(chunkART)

	cs, err := checksum_accountsync.AccountSyncChecksum().CreateProto(encoded, 1)
	if err != nil {
		return nil, fmt.Errorf("makeChunk %d: checksum: %w", batchIndex, err)
	}

	return &accountspb.AccountNonceSyncRequest{
		Art:        encoded,
		TotalKeys:  totalKeys,
		KeysRange:  &tagpb.RangeTag{Start: minNonce, End: maxNonce},
		Checksum:   cs,
		BatchIndex: batchIndex,
		IsLast:     isLast,
		Phase: &phasepb.Phase{
			PresentPhase:    constants.ACCOUNTS_SYNC_REQUEST,
			SuccessivePhase: constants.ACCOUNTS_SYNC_REQUEST,
			Auth:            auth,
		},
	}, nil
}
