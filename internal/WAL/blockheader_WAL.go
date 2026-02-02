package WAL

import (
	"log"

	"github.com/JupiterMetaLabs/JMDN-FastSync/internal/types"
)

// Write Ahead Log: this helps us to log the transactions before appending to the db so that if the node crashed we have the checkpoint to match.
// This would be bulk insert, all the block headers in the range of 1 to 1000 computed to the memory and dumped to the log file before putting it int he db.
// By doing this we can have the checkpoint of the block headers and we can replay the transactions from the log file to the db on the node restart if the node crashed.

type BlockHeaderWAL struct{
	WAL types.WAL
}

func NewBlockHeaderWAL(dir string, batchSize int) *BlockHeaderWAL {
	if dir == "" {
		log.Println("No directory provided, using default directory")
		dir = types.DefaultDir
	}
	if batchSize == 0 {
		log.Println("No batch size provided, using default batch size")
		batchSize = types.DefaultBatchSize
	}
	return &BlockHeaderWAL{
		WAL: types.WAL{
			Dir:       dir ,
			BatchSize: batchSize,
		},
	}
}
