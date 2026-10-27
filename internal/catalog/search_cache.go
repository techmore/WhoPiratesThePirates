package catalog

import (
	"container/list"
	"sync"
)

const (
	maxCachedSearchPages = 64
	maxSearchPageCache   = 16 << 20
)

type searchPageCacheKey struct {
	query     string
	category  string
	sort      string
	direction string
	limit     int
	offset    int
}

type cachedSearchPage struct {
	key   searchPageCacheKey
	items []Torrent
	total int64
	bytes int
}

type searchPageCache struct {
	mu      sync.Mutex
	entries map[searchPageCacheKey]*list.Element
	order   *list.List
	bytes   int
}

func newSearchPageCache() *searchPageCache {
	return &searchPageCache{entries: make(map[searchPageCacheKey]*list.Element), order: list.New()}
}

func (c *searchPageCache) get(key searchPageCacheKey) ([]Torrent, int64, bool) {
	c.mu.Lock()
	entry, ok := c.entries[key]
	if !ok {
		c.mu.Unlock()
		return nil, 0, false
	}
	c.order.MoveToFront(entry)
	page := entry.Value.(cachedSearchPage)
	c.mu.Unlock()
	return cloneTorrents(page.items), page.total, true
}

func (c *searchPageCache) put(key searchPageCacheKey, items []Torrent, total int64) {
	page := cachedSearchPage{key: key, items: cloneTorrents(items), total: total}
	page.bytes = searchPageSize(page.items)
	if page.bytes > maxSearchPageCache {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.entries[key]; ok {
		c.remove(existing)
	}
	for len(c.entries) >= maxCachedSearchPages || c.bytes+page.bytes > maxSearchPageCache {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.remove(oldest)
	}
	entry := c.order.PushFront(page)
	c.entries[key] = entry
	c.bytes += page.bytes
}

func (c *searchPageCache) remove(entry *list.Element) {
	page := entry.Value.(cachedSearchPage)
	delete(c.entries, page.key)
	c.bytes -= page.bytes
	c.order.Remove(entry)
}

func cloneTorrents(items []Torrent) []Torrent {
	if items == nil {
		return nil
	}
	cloned := append([]Torrent(nil), items...)
	for index := range cloned {
		cloned[index].Description = cloneString(cloned[index].Description)
		cloned[index].IMDB = cloneString(cloned[index].IMDB)
		cloned[index].Language = cloneString(cloned[index].Language)
		cloned[index].TextLanguage = cloneString(cloned[index].TextLanguage)
	}
	return cloned
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func searchPageSize(items []Torrent) int {
	const itemOverhead = 192
	size := 128
	for _, item := range items {
		size += itemOverhead + len(item.CategoryName) + len(item.Status) + len(item.Name) + len(item.Username) + len(item.InfoHash)
		if item.Description != nil {
			size += len(*item.Description)
		}
		if item.IMDB != nil {
			size += len(*item.IMDB)
		}
		if item.Language != nil {
			size += len(*item.Language)
		}
		if item.TextLanguage != nil {
			size += len(*item.TextLanguage)
		}
	}
	return size
}
