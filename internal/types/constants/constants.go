package constants

const (
	FAILURE = "failure"
	UNKNOWN = "unknown"
)

const (
	SYNC_REQUEST          = "state:priorsync:syncrequest"
	SYNC_REQUEST_RESPONSE = "state:priorsync:syncrequestresponse"

	HEADER_SYNC_REQUEST  = "state:headersync:headersyncrequest"
	HEADER_SYNC_RESPONSE = "state:headersync:headersyncresponse"

	MERGE_REQUEST  = "state:merge:mergerequest"
	MERGE_RESPONSE = "state:merge:mergeresponse"

	REQUEST_MERKLE = "state:merkle:merklerequest"
	RESPONSE_MERKLE = "state:merkle:merkleresponse"	
)

const (
	PriorSyncVersion = 1
)
