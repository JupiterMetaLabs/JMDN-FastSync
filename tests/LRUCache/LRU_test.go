package lrucache

import (
	"testing"

	"github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"
	"github.com/JupiterMetaLabs/JMDN-FastSync/core/reconsillation/LRUCache"
)

// createTestHeader creates a simple test header with the given block number
func createTestHeader(blockNum uint64) *block.Header {
	return &block.Header{
		BlockNumber: blockNum,
		BlockHash:   []byte{byte(blockNum), byte(blockNum >> 8), byte(blockNum >> 16), byte(blockNum >> 24)},
	}
}

// TestNewLRUCache tests cache initialization
func TestNewLRUCache(t *testing.T) {
	cache := LRUCache.NewLRUCache(10)
	if cache == nil {
		t.Fatal("NewLRUCache returned nil")
	}

	if cache.Len() != 0 {
		t.Errorf("Expected empty cache, got len %d", cache.Len())
	}

	if cache.CapacityLeft() != 10 {
		t.Errorf("Expected capacity 10, got %d", cache.CapacityLeft())
	}

	keys := cache.Keys()
	if len(keys) != 0 {
		t.Errorf("Expected no keys, got %d", len(keys))
	}
}

// TestPutAndGet tests basic Put and Get operations
func TestPutAndGet(t *testing.T) {
	cache := LRUCache.NewLRUCache(5)

	header := createTestHeader(100)
	cache.Put(100, header)

	// Test Get existing key
	val, ok := cache.Get(100)
	if !ok {
		t.Error("Expected to find key 100")
	}
	if val.BlockNumber != 100 {
		t.Errorf("Expected block number 100, got %d", val.BlockNumber)
	}

	// Test Get non-existing key
	val, ok = cache.Get(999)
	if ok {
		t.Error("Expected not to find key 999")
	}
	if val != nil {
		t.Error("Expected nil value for missing key")
	}
}

// TestCacheLen tests the Len method
func TestCacheLen(t *testing.T) {
	cache := LRUCache.NewLRUCache(10)

	if cache.Len() != 0 {
		t.Errorf("Expected len 0, got %d", cache.Len())
	}

	cache.Put(1, createTestHeader(1))
	if cache.Len() != 1 {
		t.Errorf("Expected len 1, got %d", cache.Len())
	}

	cache.Put(2, createTestHeader(2))
	cache.Put(3, createTestHeader(3))
	if cache.Len() != 3 {
		t.Errorf("Expected len 3, got %d", cache.Len())
	}
}

// TestCapacityLeft tests capacity tracking
func TestCapacityLeft(t *testing.T) {
	cache := LRUCache.NewLRUCache(5)

	if cache.CapacityLeft() != 5 {
		t.Errorf("Expected capacity left 5, got %d", cache.CapacityLeft())
	}

	cache.Put(1, createTestHeader(1))
	if cache.CapacityLeft() != 4 {
		t.Errorf("Expected capacity left 4, got %d", cache.CapacityLeft())
	}

	cache.Put(2, createTestHeader(2))
	cache.Put(3, createTestHeader(3))
	if cache.CapacityLeft() != 2 {
		t.Errorf("Expected capacity left 2, got %d", cache.CapacityLeft())
	}
}

// TestLRUEviction tests that least recently used items are evicted
func TestLRUEviction(t *testing.T) {
	cache := LRUCache.NewLRUCache(3)

	// Fill cache to capacity
	cache.Put(1, createTestHeader(1))
	cache.Put(2, createTestHeader(2))
	cache.Put(3, createTestHeader(3))

	if cache.Len() != 3 {
		t.Fatalf("Expected len 3, got %d", cache.Len())
	}

	// Access key 1 to make it recently used
	_, ok := cache.Get(1)
	if !ok {
		t.Error("Expected to find key 1")
	}

	// Add key 4, should evict key 2 (least recently used)
	cache.Put(4, createTestHeader(4))

	if cache.Len() != 3 {
		t.Errorf("Expected len 3 after eviction, got %d", cache.Len())
	}

	// Key 1 should still exist (was accessed recently)
	_, ok = cache.Get(1)
	if !ok {
		t.Error("Expected key 1 to still exist (was recently used)")
	}

	// Key 2 should be evicted
	_, ok = cache.Get(2)
	if ok {
		t.Error("Expected key 2 to be evicted (least recently used)")
	}

	// Keys 3 and 4 should exist
	_, ok = cache.Get(3)
	if !ok {
		t.Error("Expected key 3 to exist")
	}
	_, ok = cache.Get(4)
	if !ok {
		t.Error("Expected key 4 to exist")
	}
}

// TestRemove tests explicit removal of items
func TestRemove(t *testing.T) {
	cache := LRUCache.NewLRUCache(5)

	cache.Put(1, createTestHeader(1))
	cache.Put(2, createTestHeader(2))
	cache.Put(3, createTestHeader(3))

	if cache.Len() != 3 {
		t.Fatalf("Expected len 3, got %d", cache.Len())
	}

	// Remove existing key
	cache.Remove(2)

	if cache.Len() != 2 {
		t.Errorf("Expected len 2 after removal, got %d", cache.Len())
	}

	// Key 2 should not exist
	_, ok := cache.Get(2)
	if ok {
		t.Error("Expected key 2 to be removed")
	}

	// Keys 1 and 3 should still exist
	_, ok = cache.Get(1)
	if !ok {
		t.Error("Expected key 1 to exist")
	}
	_, ok = cache.Get(3)
	if !ok {
		t.Error("Expected key 3 to exist")
	}

	// Remove non-existing key should not panic
	cache.Remove(999)
}

// TestKeys tests the Keys method
func TestKeys(t *testing.T) {
	cache := LRUCache.NewLRUCache(5)

	// Empty cache
	keys := cache.Keys()
	if len(keys) != 0 {
		t.Errorf("Expected 0 keys for empty cache, got %d", len(keys))
	}

	// Add keys
	cache.Put(10, createTestHeader(10))
	cache.Put(20, createTestHeader(20))
	cache.Put(30, createTestHeader(30))

	keys = cache.Keys()
	if len(keys) != 3 {
		t.Errorf("Expected 3 keys, got %d", len(keys))
	}

	// Check all expected keys exist
	keyMap := make(map[uint64]bool)
	for _, k := range keys {
		keyMap[k] = true
	}

	if !keyMap[10] || !keyMap[20] || !keyMap[30] {
		t.Error("Expected keys 10, 20, 30 to be in Keys() result")
	}
}

// TestUpdateExistingKey tests updating an existing key
func TestUpdateExistingKey(t *testing.T) {
	cache := LRUCache.NewLRUCache(5)

	header1 := createTestHeader(1)
	cache.Put(1, header1)

	// Update with new header
	header2 := &block.Header{
		BlockNumber: 999, // Different block number to verify update
		BlockHash:   []byte{0x99},
	}
	cache.Put(1, header2)

	if cache.Len() != 1 {
		t.Errorf("Expected len 1 after update, got %d", cache.Len())
	}

	val, ok := cache.Get(1)
	if !ok {
		t.Fatal("Expected to find key 1 after update")
	}

	if val.BlockNumber != 999 {
		t.Errorf("Expected updated block number 999, got %d", val.BlockNumber)
	}
}

// TestGetUpdatesLRUOrder tests that Get updates the LRU order
func TestGetUpdatesLRUOrder(t *testing.T) {
	cache := LRUCache.NewLRUCache(2)

	cache.Put(1, createTestHeader(1))
	cache.Put(2, createTestHeader(2))

	// Access key 1, making it most recently used
	cache.Get(1)

	// Add key 3, should evict key 2 (now least recently used)
	cache.Put(3, createTestHeader(3))

	// Key 1 should still exist
	_, ok := cache.Get(1)
	if !ok {
		t.Error("Expected key 1 to exist after Get made it recently used")
	}

	// Key 2 should be evicted
	_, ok = cache.Get(2)
	if ok {
		t.Error("Expected key 2 to be evicted")
	}
}

// TestZeroCapacity tests edge case with zero capacity
func TestZeroCapacity(t *testing.T) {
	cache := LRUCache.NewLRUCache(0)

	// Try to add item to zero-capacity cache
	cache.Put(1, createTestHeader(1))

	// Should not be able to retrieve it (evicted immediately or never added)
	if cache.Len() > 0 {
		t.Logf("Zero capacity cache has %d items (implementation dependent)", cache.Len())
	}
}

// TestSequentialAccess tests accessing items in insertion order
func TestSequentialAccess(t *testing.T) {
	cache := LRUCache.NewLRUCache(100)

	// Add many items
	for i := uint64(1); i <= 50; i++ {
		cache.Put(i, createTestHeader(i))
	}

	if cache.Len() != 50 {
		t.Errorf("Expected len 50, got %d", cache.Len())
	}

	// Access all items in order
	for i := uint64(1); i <= 50; i++ {
		val, ok := cache.Get(i)
		if !ok {
			t.Errorf("Expected to find key %d", i)
			continue
		}
		if val.BlockNumber != i {
			t.Errorf("Expected block number %d, got %d", i, val.BlockNumber)
		}
	}
}

// TestDuplicatePuts tests multiple puts of the same key
func TestDuplicatePuts(t *testing.T) {
	cache := LRUCache.NewLRUCache(5)

	// Put same key multiple times
	for i := 0; i < 10; i++ {
		header := createTestHeader(uint64(i))
		cache.Put(1, header)
	}

	// Should only have 1 item
	if cache.Len() != 1 {
		t.Errorf("Expected len 1 after duplicate puts, got %d", cache.Len())
	}

	// Should still have capacity left
	if cache.CapacityLeft() != 4 {
		t.Errorf("Expected capacity left 4, got %d", cache.CapacityLeft())
	}
}
