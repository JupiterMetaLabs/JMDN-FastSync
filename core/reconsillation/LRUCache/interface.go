package LRUCache

import(
	"sync"
	blockpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"
)

type lru_cache struct{
	head *entry
	tail *entry
	mu sync.RWMutex
	total_capacity int
	current_capacity int
	cache map[uint64]*entry
}

// Double linked list node
type entry struct {
	key   uint64
	value *blockpb.Header
	next  *entry
	prev  *entry
}

type LRUCacheInterface interface {
	Get(key uint64) (*blockpb.Header, bool)
	Put(key uint64, value *blockpb.Header)
	Remove(key uint64)
	Len() int
	CapacityLeft() int
	Keys() []uint64
	Close() error
}

