package constants

const (
	FAILURE = "failure"
	SUCCESS = "success"
	UNKNOWN = "unknown"
)

const (
	SYNC_REQUEST          = "state:priorsync:syncrequest"
	SYNC_REQUEST_AUTOPROCEED = "state:priorsync:syncrequest:auto"
	SYNC_REQUEST_RESPONSE = "state:priorsync:syncrequestresponse"

	HEADER_SYNC_REQUEST  = "state:headersync:headersyncrequest"
	HEADER_SYNC_RESPONSE = "state:headersync:headersyncresponse"

	MERGE_REQUEST  = "state:merge:mergerequest"
	MERGE_RESPONSE = "state:merge:mergeresponse"

	REQUEST_MERKLE = "state:merkle:merklerequest"
	RESPONSE_MERKLE = "state:merkle:merkleresponse"

	DATA_SYNC_REQUEST = "state:datasync:datasyncrequest"
	DATA_SYNC_RESPONSE = "state:datasync:datasyncresponse"
)

const (
	PriorSyncVersion = 1
)

const (
	MAX_HEADERS_PER_REQUEST = 1500
)