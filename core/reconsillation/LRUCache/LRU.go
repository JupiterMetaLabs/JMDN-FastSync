package LRUCache

import (
	"sync"

	blockpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"
)

func NewLRUCache(capacity int) LRUCacheInterface {
	return &lru_cache{
		head:             nil,
		tail:             nil,
		mu:               sync.RWMutex{},
		total_capacity:   capacity,
		current_capacity: 0,
		cache:            make(map[uint64]*entry, capacity),
	}
}

func (l *lru_cache) Get(key uint64) (*blockpb.Header, bool) {
    l.mu.Lock()         // write lock, not RLock
    defer l.mu.Unlock()
    value, ok := l.cache[key]
    if !ok {
        return nil, false
    }
    l.moveToHead(key)
    return value.value, ok
}

func (l *lru_cache) Put(key uint64, value *blockpb.Header) {
    l.mu.Lock()
    defer l.mu.Unlock()
    if _, exists := l.cache[key]; exists {
        l.removeFromLinkedList(key)
        l.current_capacity--
    }
    if l.current_capacity >= l.total_capacity {
        entry := l.removeTail()
        if entry != nil {
            delete(l.cache, entry.key)
            l.current_capacity--
        }
    }
    l.current_capacity++
    l.cache[key] = l.insertIntoLinkedList(key, value)
}

func (l *lru_cache) Remove(key uint64) {
    l.mu.Lock()
    defer l.mu.Unlock()
    _, ok := l.cache[key]
    if !ok {
        return
    }
    l.current_capacity--
    l.removeFromLinkedList(key)
    delete(l.cache, key)
}
func (l *lru_cache) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.cache)
}

func (l *lru_cache) CapacityLeft() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.total_capacity - l.current_capacity
}

func (l *lru_cache) Keys() []uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	keys := make([]uint64, 0, len(l.cache))
	for key := range l.cache {
		keys = append(keys, key)
	}
	return keys
}

func (l *lru_cache) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cache = nil
	l.head = nil
	l.tail = nil
	return nil
}