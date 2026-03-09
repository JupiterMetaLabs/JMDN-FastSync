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
)

const (
	PriorSyncVersion = 1
)

const (
	AUTH_TTL = 2 * time.Minute
)

const (
	MAX_HEADERS_PER_REQUEST = 1500
	MAX_DATA_PER_REQUEST    = 30
	MIN_BLOCKS              = 500 // if number of blocks in the client is less than 500 then do the full sync.
)

// Heartbeat keepalive timing for long-running PriorSync streams.
var (
	StreamDeadline    = 15 * time.Second                                                                       // per-message read/write deadline
	HeartbeatInterval = time.Duration(math.Ceil(float64(StreamDeadline)/float64(3*time.Second))) * time.Second // ceil(deadline / 3) = 5s
)
