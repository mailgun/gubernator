package gubernator

import (
	"github.com/mailgun/gubernator/cache"
	"github.com/mailgun/holster"
)

// New creates a new Cache with a maximum size
func NewCache(maxSize int) *cache.LRUCache {
	holster.SetDefault(&maxSize, 50000)

	return cache.NewLRUCache(maxSize)
}
