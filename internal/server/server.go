// Package server exposes the replay index over HTTP and serves the web UI.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strings"
	"sync"

	"barreplays/internal/config"
	"barreplays/internal/gamefiles"
	"barreplays/internal/index"
)

//go:embed web
var webFS embed.FS

// Server wires the configuration store, the replay index and the UI together.
//
// The index is replaced wholesale when the user points at a different folder,
// so access goes through a mutex rather than being captured at construction.
type Server struct {
	mu       sync.RWMutex
	cfg      *config.Config
	store    *config.Store
	idx      *index.Index
	games    *gamefiles.Library
	cacheDir string

	// watchCancel stops the watcher belonging to the current index, so
	// changing folder or watch mode does not leave one running.
	watchCancel context.CancelFunc

	// ctx bounds background indexing; cancelled when the process shuts down.
	ctx context.Context
}

// New creates a server. If the configuration already names a replay folder,
// indexing starts immediately.
func New(ctx context.Context, store *config.Store, cfg *config.Config, cacheDir string) *Server {
	s := &Server{cfg: cfg, store: store, cacheDir: cacheDir, ctx: ctx}
	if cfg.Configured() {
		s.startIndex(cfg.DemosPath, index.ParseWatchMode(cfg.WatchMode), false)
	}
	return s
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /api/settings", s.handleSettings)
	mux.HandleFunc("POST /api/config", s.handleSetConfig)
	mux.HandleFunc("POST /api/rescan", s.handleRescan)
	mux.HandleFunc("GET /api/maps/{name}/preview", s.handleMapPreview)
	mux.HandleFunc("GET /api/metrics", s.handleMetrics)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/replays", s.handleReplays)
	mux.HandleFunc("GET /api/replays/{id}", s.handleReplayDetail)

	ui, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err) // embedded tree is fixed at build time
	}
	mux.Handle("/", spaFallback(ui))

	return withNoStore(mux)
}

// spaFallback serves the embedded UI, handing back the app shell for paths
// that are not files.
//
// The UI puts its routes in the address bar (/replays/<id>, /dashboard) so the
// back button and deep links work; those paths have to reach index.html rather
// than 404 on a refresh.
func spaFallback(fsys fs.FS) http.Handler {
	files := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// An unmatched API path is a client error, not an app route — serving
		// HTML there would turn a typo into a confusing parse failure.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "unknown endpoint")
			return
		}
		if name := strings.TrimPrefix(path.Clean(r.URL.Path), "/"); name != "" {
			if f, err := fsys.Open(name); err == nil {
				f.Close()
				files.ServeHTTP(w, r)
				return
			}
		}
		shell := r.Clone(r.Context())
		shell.URL.Path = "/"
		files.ServeHTTP(w, shell)
	})
}

// startIndex scans dir, reusing the existing index when the folder has not
// changed.
//
// Reuse is what makes a rescan non-destructive: the index keeps serving its
// current records until the new pass has a complete set to swap in, so the
// list never blanks out. A different folder is genuinely different content, so
// that case does start empty.
func (s *Server) startIndex(dir string, mode index.WatchMode, restartWatch bool) {
	s.mu.Lock()
	idx := s.idx
	fresh := idx == nil || idx.Dir() != dir
	if fresh {
		idx = index.New(dir, s.cacheDir)
		s.idx = idx
		// Map previews come from the game install the replays sit inside.
		s.games = gamefiles.New(gamefiles.DataDirForDemos(dir))
	}
	startWatch := fresh || restartWatch
	if startWatch && s.watchCancel != nil {
		s.watchCancel()
		s.watchCancel = nil
	}
	var watchCtx context.Context
	if startWatch {
		watchCtx, s.watchCancel = context.WithCancel(s.ctx)
	}
	s.mu.Unlock()

	// Report the queued work before returning, so a client polling straight
	// after this call sees a scan in progress rather than an idle index.
	idx.Pending()

	go func() {
		if err := idx.Scan(s.ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("index scan failed: %v", err)
		}
	}()
	if startWatch {
		go idx.Watch(watchCtx, mode)
	}
}

// watchMode reads the configured mode.
func (s *Server) watchMode() index.WatchMode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return index.ParseWatchMode(s.cfg.WatchMode)
}

func (s *Server) index() *index.Index {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.idx
}

// handleState reports whether setup is done, and how indexing is progressing.
// The setup screen and the header both render from this one endpoint.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := *s.cfg
	games := s.games
	s.mu.RUnlock()

	resp := map[string]any{
		"configured": cfg.Configured(),
		"demosPath":  cfg.DemosPath,
		"configFile": s.store.Path(),
		"playerName":    cfg.PlayerName,
		"highlightSelf": cfg.HighlightSelf,
		// Lets the UI skip requesting previews entirely when the replays do
		// not sit inside a game install.
		"previews": games != nil && games.Available(),
	}
	if !cfg.Configured() {
		// Only pay for the filesystem probe when it is actually needed.
		resp["suggestions"] = config.Detect()
	}
	if idx := s.index(); idx != nil {
		resp["progress"] = idx.Progress()
		// The client polls this endpoint and refetches the (much larger)
		// replay list only when the revision moves.
		resp["revision"] = idx.Revision()
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleSettings returns everything the settings dialog needs, including the
// detected replay folders so the user can switch between installs.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := *s.cfg
	games := s.games
	s.mu.RUnlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"demosPath":     cfg.DemosPath,
		"watchMode":     string(index.ParseWatchMode(cfg.WatchMode)),
		"playerName":    cfg.PlayerName,
		"highlightSelf": cfg.HighlightSelf,
		"configFile":    s.store.Path(),
		"cacheDir":      s.cacheDir,
		"previews":      games != nil && games.Available(),
		"suggestions":   config.Detect(),
	})
}

// handleStats aggregates the configured player's record across the folder.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	playerName := s.cfg.PlayerName
	s.mu.RUnlock()

	idx := s.index()
	if idx == nil {
		writeJSON(w, http.StatusOK, buildStats(nil, playerName))
		return
	}
	writeJSON(w, http.StatusOK, buildStats(idx.List(), playerName))
}

// handleSetConfig accepts settings from the setup screen or the settings
// dialog. Fields are optional so either caller can send just what it changed.
func (s *Server) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path          string  `json:"path"`
		WatchMode     *string `json:"watchMode"`
		PlayerName    *string `json:"playerName"`
		HighlightSelf *bool   `json:"highlightSelf"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	s.mu.RLock()
	currentPath := s.cfg.DemosPath
	currentMode := index.ParseWatchMode(s.cfg.WatchMode)
	s.mu.RUnlock()

	path := body.Path
	if path == "" {
		path = currentPath
	}
	if path == "" {
		writeError(w, http.StatusBadRequest, "no folder given")
		return
	}
	if err := config.ValidateDemosPath(path); err != nil {
		writeError(w, http.StatusBadRequest, "cannot read that folder: "+err.Error())
		return
	}

	mode := currentMode
	if body.WatchMode != nil {
		mode = index.ParseWatchMode(*body.WatchMode)
	}

	s.mu.Lock()
	s.cfg.DemosPath = path
	s.cfg.WatchMode = string(mode)
	if body.PlayerName != nil {
		s.cfg.PlayerName = strings.TrimSpace(*body.PlayerName)
	}
	if body.HighlightSelf != nil {
		s.cfg.HighlightSelf = *body.HighlightSelf
	}
	cfg := *s.cfg
	s.mu.Unlock()

	if err := s.store.Save(&cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save settings: "+err.Error())
		return
	}
	s.startIndex(path, mode, mode != currentMode)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "demosPath": path, "watchMode": string(mode),
		"playerName": cfg.PlayerName, "highlightSelf": cfg.HighlightSelf,
	})
}

// handleMapPreview serves a map's lobby preview straight out of the installed
// game's archives.
func (s *Server) handleMapPreview(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	games := s.games
	s.mu.RUnlock()

	if games == nil {
		writeError(w, http.StatusNotFound, "no game files")
		return
	}
	// ?full=1 asks for the large, correctly-proportioned image; the default
	// stays the small thumbnail the replay list draws by the hundred.
	full := r.URL.Query().Get("full") == "1"
	preview, err := games.MapPreview(r.PathValue("name"), full)
	if err != nil {
		// A missing preview is ordinary — the UI just shows a placeholder.
		writeError(w, http.StatusNotFound, "no preview for that map")
		return
	}
	w.Header().Set("Content-Type", preview.ContentType)
	// Previews only change when the game is updated, and the process is
	// restarted for that, so they are safe to hold for the session.
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(preview.Data)
}

// handleRescan re-reads the folder, picking up replays played since startup.
func (s *Server) handleRescan(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	dir := s.cfg.DemosPath
	s.mu.RUnlock()
	if dir == "" {
		writeError(w, http.StatusBadRequest, "no replay folder configured")
		return
	}
	s.startIndex(dir, s.watchMode(), false)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	metrics, groups := metricCatalogue()
	writeJSON(w, http.StatusOK, map[string]any{"metrics": metrics, "groups": groups})
}

func (s *Server) handleReplays(w http.ResponseWriter, r *http.Request) {
	idx := s.index()
	if idx == nil {
		writeJSON(w, http.StatusOK, map[string]any{"replays": []any{}, "progress": index.Progress{Phase: index.PhaseIdle}})
		return
	}
	s.mu.RLock()
	playerName := s.cfg.PlayerName
	s.mu.RUnlock()

	records := idx.List()
	items := make([]replayListItem, 0, len(records))
	for _, rec := range records {
		items = append(items, newReplayListItem(rec, playerName))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"replays":  items,
		"progress": idx.Progress(),
		"revision": idx.Revision(),
	})
}

func (s *Server) handleReplayDetail(w http.ResponseWriter, r *http.Request) {
	idx := s.index()
	if idx == nil {
		writeError(w, http.StatusServiceUnavailable, "no replay folder configured")
		return
	}
	replay, err := idx.Detail(r.PathValue("id"))
	if errors.Is(err, index.ErrNotFound) {
		writeError(w, http.StatusNotFound, "replay not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.mu.RLock()
	playerName := s.cfg.PlayerName
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, newReplayDetail(replay, playerName))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// withNoStore stops the browser caching API responses or a stale UI build,
// which otherwise makes indexing progress appear frozen.
//
// Map previews are exempt: they are immutable for the life of the process and
// the list requests dozens of them at a time.
func withNoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/maps/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
