package wal

type WALType string

const (
	HeaderSync WALType = "wal:headersync"
	MerkleSync WALType = "wal:merklesync"
	PriorSync  WALType = "wal:priorsync"
	DataSync   WALType = "wal:datasync"
)

const (
	DefaultBatchSize = 1000
	DefaultDir       = "tmp/wal"
)