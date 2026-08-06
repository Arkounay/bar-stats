// Package index scans a folder of replays and keeps a queryable view of it in
// memory.
//
// Indexing runs in two phases because the two kinds of information have very
// different costs. Header metadata — map, date, players — sits at the front of
// each file and is read in milliseconds. The statistics live behind the demo
// stream, which in a gzip container can only be reached by decompressing the
// whole file. So the listing appears immediately from a cheap header pass, and
// a background pass fills in outcomes and totals.
package index

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"barreplays/internal/demo"
)

// Phase describes what the index is currently doing.
type Phase string

const (
	PhaseIdle     Phase = "idle"
	PhaseScanning Phase = "scanning" // reading headers; listing is filling up
	PhaseEnriching Phase = "enriching" // full-parsing in the background
	PhaseReady    Phase = "ready"
)

// Progress is a snapshot of indexing state for the UI.
type Progress struct {
	Phase  Phase `json:"phase"`
	Total  int   `json:"total"`
	Done   int   `json:"done"`
	Failed int   `json:"failed"`
}

// Record is one indexed replay. Replay carries no sample series; those are
// re-read on demand by [Index.Detail].
type Record struct {
	Replay *demo.Replay
	// Enriched reports whether the full parse has run, meaning the outcome
	// and end-of-match totals are populated.
	Enriched bool

	size    int64
	modTime time.Time
}

// Index is a concurrent-safe view of a replay folder.
type Index struct {
	mu       sync.RWMutex
	dir      string
	records  map[string]*Record
	order    []string // record IDs, newest match first
	progress Progress
	cache    *cache
	// revision increments whenever the visible contents change, so a client
	// can poll a tiny endpoint and refetch the list only when it must.
	revision uint64

	// scanMu serialises whole scans so a refresh cannot overlap itself.
	scanMu sync.Mutex
}

// New creates an index over dir. cacheDir may be empty to disable persistence.
func New(dir, cacheDir string) *Index {
	idx := &Index{
		dir:      dir,
		records:  map[string]*Record{},
		progress: Progress{Phase: PhaseIdle},
	}
	if cacheDir != "" {
		idx.cache = newCache(cacheDir)
	}
	return idx
}

// Dir returns the indexed folder.
func (ix *Index) Dir() string { return ix.dir }

// Progress returns the current indexing state.
func (ix *Index) Progress() Progress {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.progress
}

// Revision identifies the current contents. It changes whenever records are
// added, replaced or enriched.
func (ix *Index) Revision() uint64 {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.revision
}

// Pending marks a scan as queued.
//
// Callers start Scan on a goroutine, so without this the index would still
// report itself idle for the first moments — long enough for a client that
// polls immediately after asking for a rescan to conclude there is no work
// happening and stop watching.
func (ix *Index) Pending() {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.progress.Phase != PhaseScanning && ix.progress.Phase != PhaseEnriching {
		ix.progress = Progress{Phase: PhaseScanning, Total: ix.progress.Total}
	}
}

// List returns every indexed replay, newest match first.
func (ix *Index) List() []*Record {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	out := make([]*Record, 0, len(ix.order))
	for _, id := range ix.order {
		if r, ok := ix.records[id]; ok {
			out = append(out, r)
		}
	}
	return out
}

// Get returns the indexed record for an ID.
func (ix *Index) Get(id string) (*Record, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	r, ok := ix.records[id]
	return r, ok
}

// ErrNotFound reports an unknown replay ID.
var ErrNotFound = errors.New("replay not found")

// Detail fully parses a replay, including its time series.
//
// This is done on demand rather than held in memory: the series for a large
// team game is several megabytes, and the user views one replay at a time.
func (ix *Index) Detail(id string) (*demo.Replay, error) {
	rec, ok := ix.Get(id)
	if !ok {
		return nil, ErrNotFound
	}
	return demo.Parse(rec.Replay.Path)
}

// Scan indexes the folder. It returns once the fast header pass is complete;
// the slower enrichment pass continues in the background until ctx is done.
func (ix *Index) Scan(ctx context.Context) error {
	// A scan already in flight covers whatever prompted this one; queueing a
	// second would only re-read the same folder behind it.
	if !ix.scanMu.TryLock() {
		return nil
	}
	defer ix.scanMu.Unlock()

	paths, _, err := listDemos(ix.dir)
	if err != nil {
		ix.setProgress(Progress{Phase: PhaseIdle})
		return err
	}

	cached := map[string]*cacheRecord{}
	if ix.cache != nil {
		cached = ix.cache.load()
	}

	ix.setProgress(Progress{Phase: PhaseScanning, Total: len(paths)})

	var (
		mu       sync.Mutex
		records  = make(map[string]*Record, len(paths))
		stale    []*Record
		done     int
		failed   int
	)

	// Phase one: cheap header reads, in parallel.
	forEach(ctx, paths, func(f demoFile) {
		rec, fresh, err := ix.loadRecord(f, cached)
		mu.Lock()
		defer mu.Unlock()
		done++
		if err != nil {
			failed++
			log.Printf("skipping %s: %v", filepath.Base(f.path), err)
		} else {
			records[rec.Replay.ID] = rec
			if !fresh {
				stale = append(stale, rec)
			}
		}
		ix.setProgress(Progress{Phase: PhaseScanning, Total: len(paths), Done: done, Failed: failed})
	})

	if ctx.Err() != nil {
		return ctx.Err()
	}

	ix.replace(records)
	ix.setProgress(Progress{Phase: PhaseEnriching, Total: len(paths), Done: len(paths) - len(stale), Failed: failed})

	// Phase two: full parses for anything not served from cache.
	go ix.enrich(ctx, stale, failed, len(paths))
	return nil
}

// loadRecord returns a record for f, preferring a valid cache entry. The
// boolean reports whether the record is already enriched.
func (ix *Index) loadRecord(f demoFile, cached map[string]*cacheRecord) (*Record, bool, error) {
	abs, err := filepath.Abs(f.path)
	if err != nil {
		abs = f.path
	}

	if c, ok := cached[abs]; ok && c.Size == f.size && c.ModTime.Equal(f.modTime) {
		return &Record{Replay: c.Replay, Enriched: true, size: c.Size, modTime: c.ModTime}, true, nil
	}

	replay, err := demo.ParseSummary(f.path)
	if err != nil {
		return nil, false, err
	}
	return &Record{Replay: replay, size: f.size, modTime: f.modTime}, false, nil
}

// enrich full-parses the given records, updating the index as it goes and
// persisting the result.
func (ix *Index) enrich(ctx context.Context, stale []*Record, failed, total int) {
	if len(stale) == 0 {
		ix.setProgress(Progress{Phase: PhaseReady, Total: total, Done: total, Failed: failed})
		ix.persist()
		return
	}

	var (
		mu   sync.Mutex
		done = total - len(stale)
	)
	forEach(ctx, stale, func(rec *Record) {
		full, err := demo.Parse(rec.Replay.Path)
		mu.Lock()
		defer mu.Unlock()
		done++
		if err != nil {
			failed++
			log.Printf("statistics unavailable for %s: %v", rec.Replay.FileName, err)
		} else {
			full.StripSamples()
			ix.mu.Lock()
			rec.Replay = full
			rec.Enriched = true
			ix.mu.Unlock()
		}
		ix.setProgress(Progress{Phase: PhaseEnriching, Total: total, Done: done, Failed: failed})
	})

	if ctx.Err() != nil {
		return
	}
	ix.mu.Lock()
	ix.revision++
	ix.mu.Unlock()
	ix.setProgress(Progress{Phase: PhaseReady, Total: total, Done: total, Failed: failed})
	ix.persist()
}

// persist writes the current index to the cache.
func (ix *Index) persist() {
	if ix.cache == nil {
		return
	}
	ix.mu.RLock()
	out := make([]*cacheRecord, 0, len(ix.records))
	for _, r := range ix.records {
		if !r.Enriched {
			continue
		}
		out = append(out, &cacheRecord{
			Path: r.Replay.Path, Size: r.size, ModTime: r.modTime, Replay: r.Replay,
		})
	}
	ix.mu.RUnlock()
	_ = ix.cache.save(out)
}

// replace swaps in a new record set and recomputes the display order.
//
// The swap is atomic and happens only once the whole header pass is done, so a
// rescan never leaves the list empty in the meantime.
func (ix *Index) replace(records map[string]*Record) {
	order := make([]string, 0, len(records))
	for id := range records {
		order = append(order, id)
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := records[order[i]].Replay, records[order[j]].Replay
		if a.Played.Equal(b.Played) {
			return a.FileName > b.FileName
		}
		return a.Played.After(b.Played)
	})

	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.records = records
	ix.order = order
	ix.revision++
}

func (ix *Index) setProgress(p Progress) {
	ix.mu.Lock()
	ix.progress = p
	ix.mu.Unlock()
}

// fingerprint summarises the folder's eligible replays. Any change to the set,
// or to a file's size or timestamp, changes the result. The duration is how
// long until the folder is worth fingerprinting again even if nothing else
// happens; see [listDemos].
func (ix *Index) fingerprint() (uint64, time.Duration) {
	files, settling, err := listDemos(ix.dir)
	if err != nil {
		return 0, 0
	}
	h := fnv.New64a()
	for _, f := range files {
		fmt.Fprintf(h, "%s|%d|%d\n", f.path, f.size, f.modTime.UnixNano())
	}
	return h.Sum64(), settling
}

// settleTime is how long a replay file must go untouched before it is read.
//
// The game writes to the demo throughout the match, so a file modified moments
// ago may still be being appended to: its statistics chunks are not there yet
// and its gzip stream is incomplete. Waiting for it to go quiet avoids parsing
// a half-written match — and keeps the watcher from re-reading a live match on
// every tick.
//
// It is kept short because it is the floor on how soon a finished match can
// appear, and the watcher now waits it out precisely rather than rediscovering
// the file on some later tick. A file caught mid-write anyway is not a
// correctness problem: it fails to parse or parses thin, and the write that
// completes it changes the fingerprint and triggers another read.
const settleTime = 10 * time.Second

// demoFile is a replay file and the metadata the index needs about it.
//
// The size and timestamp come from the directory read, so neither the scan nor
// the watcher has to stat each file again — at ~600 replays that removed two
// further stat passes per scan and per watch tick.
type demoFile struct {
	path    string
	size    int64
	modTime time.Time
}

// listDemos returns the replay files in dir that are ready to read,
// non-recursively.
//
// Empty and still-being-written files are skipped rather than reported as
// failures. The returned duration is how long until the earliest skipped file
// clears [settleTime], or zero if none was skipped: a file written as a match
// ends is invisible here for a few seconds, and the write that produced it is
// the last event the folder will ever emit, so without this the caller would
// have nothing left to wake it up and the replay would sit unlisted until some
// unrelated tick.
func listDemos(dir string) ([]demoFile, time.Duration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	var (
		out      []demoFile
		settling time.Duration
	)
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), demo.Ext) {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Size() == 0 {
			continue
		}
		if left := settleTime - time.Since(info.ModTime()); left > 0 {
			if settling == 0 || left < settling {
				settling = left
			}
			continue
		}
		out = append(out, demoFile{
			path:    filepath.Join(dir, e.Name()),
			size:    info.Size(),
			modTime: info.ModTime(),
		})
	}
	return out, settling, nil
}

// forEach runs fn over items on a bounded worker pool. Decompression is
// CPU-bound, so the pool is sized to the machine rather than to the work.
func forEach[T any](ctx context.Context, items []T, fn func(T)) {
	workers := min(runtime.NumCPU(), len(items))
	if workers < 1 {
		return
	}

	ch := make(chan T)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for item := range ch {
				if ctx.Err() != nil {
					return
				}
				fn(item)
			}
		}()
	}
	for _, item := range items {
		select {
		case ch <- item:
		case <-ctx.Done():
			close(ch)
			wg.Wait()
			return
		}
	}
	close(ch)
	wg.Wait()
}
