package cache

// Interface accepts any cache which returns cache stats
type CacheStats interface {
	Stats() Stats
}

// So algorithms can interface with the cache
type Cache interface {
	Add(key Key, value interface{}, expireAt int64) bool
	Get(key Key) (value interface{}, ok bool)
	UpdateExpiration(key Key, expireAt int64) bool
}

// A Key may be any value that is comparable. See http://golang.org/ref/spec#Comparison_operators
type Key interface{}

// Holds stats collected about the cache
type Stats struct {
	Size int64
	Miss int64
	Hit  int64
}
