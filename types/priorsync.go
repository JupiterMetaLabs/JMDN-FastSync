package types

type PriorSync struct {
	Blocknumber uint64
	Stateroot   []byte
	Blockhash   []byte
	Metadata    Metadata
}

type Metadata struct {
	Checksum []byte
	State    string
	Version  uint64
}

