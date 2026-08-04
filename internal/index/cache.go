package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"barreplays/internal/demo"
)

// cacheVersion is bumped whenever the cached shape or the decoding that
// produced it changes, so an old cache is discarded and rebuilt rather than
// serving results the current code would not produce.
const cacheVersion = 3

// cacheFile is the on-disk form of the index.
type cacheFile struct {
	Version int            `json:"version"`
	Entries []*cacheRecord `json:"entries"`
}

// cacheRecord stores one fully-parsed replay minus its time series. The
// fingerprint fields let a cached entry be invalidated when the underlying
// file changes.
type cacheRecord struct {
	Path    string       `json:"path"`
	Size    int64        `json:"size"`
	ModTime time.Time    `json:"modTime"`
	Replay  *demo.Replay `json:"replay"`
}

// cache persists enriched index entries between runs. Rebuilding the index
// means decompressing every demo in full, so this is the difference between a
// slow first start and an instant one thereafter.
type cache struct {
	path string
}

func newCache(dir string) *cache {
	return &cache{path: filepath.Join(dir, "index.json")}
}

// load returns cached records keyed by absolute path. Any error — missing,
// corrupt, or stale-versioned — yields an empty map, and the index simply
// rebuilds.
func (c *cache) load() map[string]*cacheRecord {
	out := map[string]*cacheRecord{}
	if c == nil {
		return out
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		return out
	}
	var f cacheFile
	if err := json.Unmarshal(data, &f); err != nil || f.Version != cacheVersion {
		return out
	}
	for _, e := range f.Entries {
		if e != nil && e.Replay != nil {
			out[e.Path] = e
		}
	}
	return out
}

// save writes the cache atomically.
func (c *cache) save(records []*cacheRecord) error {
	if c == nil {
		return nil
	}
	data, err := json.Marshal(cacheFile{Version: cacheVersion, Entries: records})
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}
