package LRUCache

import blockpb "github.com/JupiterMetaLabs/JMDN-FastSync/common/proto/block"

func NewLinkedList() *entry {
	return &entry{
		key:  0,
		next: nil,
		prev: nil,
	}
}

// Every new insert should be O(1) -  But this function is to make the node not to make a head
func (e *entry) newnode(key uint64, value *blockpb.Header) *entry {
	return &entry{
		key:  key,
		value: value,
		next: nil,
		prev: nil,
	}
}

func(l *lru_cache) insertIntoLinkedList(key uint64, value *blockpb.Header) *entry {
	node := NewLinkedList().newnode(key, value)

	if l.head == nil {
		l.head = node
		l.tail = node
		return node
	}

	l.head.prev = node
	node.next = l.head
	l.head = node
	return node
}

func (l *lru_cache) moveToHead(key uint64) {
    node := l.removeFromLinkedList(key)
    if node != nil {
        // insertIntoLinkedList creates a new node — update the map!
        l.cache[key] = l.insertIntoLinkedList(key, node.value)
    }
}

func (l *lru_cache) removeFromLinkedList(key uint64) (*entry) {
	current := l.head
	for current != nil {
		if current.key == key {
			if current.prev != nil {
				current.prev.next = current.next
			} else {
				l.head = current.next
			}

			if current.next != nil {
				current.next.prev = current.prev
			} else {
				l.tail = current.prev
			}

			current.prev = nil
			current.next = nil
			return current
		}
		current = current.next
	}
	return nil
}

func (l *lru_cache) removeTail() *entry {
    if l.tail == nil {
        return nil
    }
    node := l.tail
    if l.head == l.tail {
        l.head = nil
        l.tail = nil
    } else {
        l.tail = node.prev
        l.tail.next = nil
    }
    node.prev = nil
    return node
}