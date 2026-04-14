package constants

import (
	"math"
	"time"
)

const (
	FAILURE = "failure"
	SUCCESS = "success"
	UNKNOWN = "unknown"
)

const (
	SYNC_REQUEST             = "state:priorsync:syncrequest"
	SYNC_REQUEST_AUTOPROCEED = "state:priorsync:syncrequest:auto"
	SYNC_REQUEST_RESPONSE    = "state:priorsync:syncrequestresponse"
	FULL_SYNC_REQUEST        = "state:priorsync:fullsyncrequest"

	HEADER_SYNC_REQUEST  = "state:headersync:headersyncrequest"
	HEADER_SYNC_RESPONSE = "state:headersync:headersyncresponse"

	MERGE_REQUEST  = "state:merge:mergerequest"
	MERGE_RESPONSE = "state:merge:mergeresponse"

	REQUEST_MERKLE  = "state:merkle:merklerequest"
	RESPONSE_MERKLE = "state:merkle:merkleresponse"

	DATA_SYNC_REQUEST  = "state:datasync:datasyncrequest"
	DATA_SYNC_RESPONSE = "state:datasync:datasyncresponse"

	AVAILABILITY_REQUEST = "state:availability:availabilityrequest"
	AVAILABILITY_RESPONSE = "state:availability:availabilityresponse"

	PoTS_REQUEST = "state:pots:potsrequest"
	PoTS_RESPONSE = "state:pots:potsresponse"

	ACCOUNTS_SYNC_REQUEST = "state:accountssync:accountssyncrequest"
	ACCOUNTS_SYNC_RESPONSE = "state:accountssync:accountssyncresponse"
)

const (
	PriorSyncVersion = 1
)

const (
	AUTH_TTL = 2 * time.Minute
)

const (
	PoTSVersion  = 1
	// This is to isolate PoTS WAL from SYNC WAL
	// PoTS WAL have the full block information that were processed during the FastSync process.
	PoTS_WALName = "pots.wal"
)

const (
	MAX_HEADERS_PER_REQUEST = 1500
	MAX_DATA_PER_REQUEST    = 30
	MIN_BLOCKS              = 500 // if number of blocks in the client is less than 500 then do the full sync.
	MAX_PARALLEL_REQUESTS   = 10
	ATMOST_ACCOUNT_ROUTINES = 15
	LRU_CACHE_CAPACITY      = 200
)

// Heartbeat keepalive timing for long-running PriorSync streams.
var (
	StreamDeadline    = 15 * time.Second                                                                       // per-message read/write deadline
	HeartbeatInterval = time.Duration(math.Ceil(float64(StreamDeadline)/float64(3*time.Second))) * time.Second // ceil(deadline / 3) = 5s
)
