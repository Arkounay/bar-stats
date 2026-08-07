// Package gamefiles reads assets out of an installed Beyond All Reason game,
// so the UI can show the same map previews the game's own lobby does.
//
// Content is stored in the "rapid" pool format, which splits an archive into
// an index and a content-addressed blob store:
//
//	<data>/packages/<hash>.sdp   gzip-compressed index of one archive
//	<data>/pool/ab/cdef… .gz     one gzip-compressed file, named by its MD5
//
// An index entry is a length-prefixed path followed by the MD5 that locates
// the content in the pool. Nothing here writes to the game directory.
package gamefiles

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The game keeps two per-map previews, and they are not interchangeable.
//
// The thumbnail is a 128px square: the map's minimap texture, which the engine
// stores squashed to a square whatever the map's real shape. The override is a
// 1024px JPEG at the map's true aspect ratio. The override is preferred
// wherever it exists — it is both sharper and correctly shaped — and the
// thumbnail is the fallback for maps that lack one.
const (
	thumbnailPrefix = "minimapthumbnail/"
	overridePrefix  = "minimapoverride/"
)

// maxAssetSize caps how much is read out of the pool for one asset. Previews
// are tens of kilobytes; this stops a corrupt index entry from being read into
// memory unbounded.
const maxAssetSize = 8 << 20

// ErrNotFound reports an asset the installed game does not contain.
var ErrNotFound = errors.New("asset not found in game files")

// Library provides read-only access to an installed game's assets.
//
// The index is built on first use rather than at construction: an install can
// hold a hundred archives, and a user who never opens a replay should not pay
// to scan them.
type Library struct {
	dataDir string

	once sync.Once
	err  error
	// Both are keyed by the map's normalised base name, without extension.
	thumbs    map[string]string // -> MD5 hex of the 128px square PNG
	overrides map[string]string // -> MD5 hex of the full-size JPEG
}

// Preview is a map image and the content type to serve it as.
type Preview struct {
	Data        []byte
	ContentType string
}

// New returns a Library rooted at a game data directory — the folder holding
// `packages` and `pool`.
func New(dataDir string) *Library { return &Library{dataDir: dataDir} }

// DataDirForDemos infers the game data directory from a replay folder, which
// normally sits directly inside it. It returns "" when the expected pool
// layout is not there, in which case previews are simply unavailable.
func DataDirForDemos(demosPath string) string {
	parent := filepath.Dir(strings.TrimRight(demosPath, `\/`))
	for _, sub := range []string{"packages", "pool"} {
		if info, err := os.Stat(filepath.Join(parent, sub)); err != nil || !info.IsDir() {
			return ""
		}
	}
	return parent
}

// Available reports whether previews can be served at all.
func (l *Library) Available() bool { return l != nil && l.dataDir != "" }

// MapPreview returns a preview for a map, as named in a replay's start script
// (for example "Rosetta 1.4.4").
//
// full selects the large, correctly-proportioned image. It is two hundred
// times the size of the thumbnail, so the replay list — which draws these at
// the size of a postage stamp, hundreds at a time — asks for the small one.
func (l *Library) MapPreview(mapName string, full bool) (*Preview, error) {
	if !l.Available() {
		return nil, ErrNotFound
	}
	l.once.Do(l.build)
	if l.err != nil {
		return nil, l.err
	}

	key := previewKey(mapName)
	if full {
		if md5hex, ok := l.overrides[key]; ok {
			if data, err := l.readPool(md5hex); err == nil {
				return &Preview{Data: data, ContentType: "image/jpeg"}, nil
			}
			// An override the install never downloaded falls through to the
			// thumbnail rather than failing the request.
		}
	}
	md5hex, ok := l.thumbs[key]
	if !ok {
		return nil, ErrNotFound
	}
	data, err := l.readPool(md5hex)
	if err != nil {
		return nil, err
	}
	return &Preview{Data: data, ContentType: "image/png"}, nil
}

// previewKey converts a map's display name to the base name the game stores
// its previews under: lower-cased with spaces replaced by underscores, and no
// extension, since the two preview kinds use different ones.
func previewKey(mapName string) string {
	name := strings.ToLower(strings.TrimSpace(mapName))
	return strings.ReplaceAll(name, " ", "_")
}

// build indexes every archive in the install, keeping only map previews.
//
// Archives from different game versions overlap heavily, so later entries are
// allowed to win: scanning in name order gives a stable result regardless of
// directory iteration order.
func (l *Library) build() {
	l.thumbs = map[string]string{}
	l.overrides = map[string]string{}

	entries, err := os.ReadDir(filepath.Join(l.dataDir, "packages"))
	if err != nil {
		l.err = fmt.Errorf("read packages: %w", err)
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".sdp") {
			names = append(names, e.Name())
		}
	}
	// os.ReadDir already returns entries sorted by name.

	for _, name := range names {
		// A single unreadable archive should not cost the whole library.
		_ = l.indexArchive(filepath.Join(l.dataDir, "packages", name))
	}
}

// indexArchive reads one .sdp index and records its map previews.
func (l *Library) indexArchive(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	zr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer zr.Close()

	data, err := io.ReadAll(io.LimitReader(zr, 64<<20))
	if err != nil {
		return err
	}

	// Each record: 1-byte name length, name, 16-byte MD5, 4-byte CRC,
	// 4-byte size.
	for off := 0; off < len(data); {
		nameLen := int(data[off])
		off++
		if off+nameLen+24 > len(data) {
			break // truncated tail; keep whatever parsed cleanly
		}
		name := string(data[off : off+nameLen])
		off += nameLen
		md5hex := hex.EncodeToString(data[off : off+16])
		off += 16 + 4 + 4 // MD5, CRC32, then the big-endian size we do not need

		if idx := strings.LastIndex(name, thumbnailPrefix); idx >= 0 {
			l.thumbs[baseName(name[idx+len(thumbnailPrefix):])] = md5hex
		}
		if idx := strings.LastIndex(name, overridePrefix); idx >= 0 {
			l.overrides[baseName(name[idx+len(overridePrefix):])] = md5hex
		}
	}
	return nil
}

// baseName normalises an archive entry's file name to the key both preview
// maps are indexed by.
func baseName(fileName string) string {
	return strings.ToLower(strings.TrimSuffix(fileName, filepath.Ext(fileName)))
}

// readPool loads and decompresses one content-addressed file.
func (l *Library) readPool(md5hex string) ([]byte, error) {
	if len(md5hex) != 32 {
		return nil, ErrNotFound
	}
	path := filepath.Join(l.dataDir, "pool", md5hex[:2], md5hex[2:]+".gz")
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		// The index lists content the install has not downloaded.
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, io.LimitReader(zr, maxAssetSize)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
