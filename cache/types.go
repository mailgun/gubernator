package cache

// Interface accepts any cache which returns cache stats
type Stater interface {
	Stats(bool) Stats
}

// So algorithms can interface with different cache implementations
type Cache interface {
	// Access methods
	Add(key Key, value interface{}, expireAt int64) bool
	UpdateExpiration(key Key, expireAt int64) bool
	Get(key Key) (value interface{}, ok bool)
	Remove(key Key)

	// If the cache is exclusive, this will control access to the cache
	Unlock()
	Lock()
}

// A Key may be any value that is comparable. See http://golang.org/ref/spec#Comparison_operators
type Key interface{}

// Holds stats collected about the cache
type Stats struct {
	Size int64
	Miss int64
	Hit  int64
}
