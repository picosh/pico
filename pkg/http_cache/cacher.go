package http_cache

type Cacher interface {
	Add(key string, val []byte) (evicted bool)
	Get(key string) (val []byte, ok bool)
	Keys() []string
	Len() int
	Values() [][]byte
	Purge()
	Remove(key string) (present bool)
}
