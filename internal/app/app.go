// Package app wires the configuration, the HTTP server and the user interface
// together.
//
// It exists so the browser and desktop builds share one startup path and
// differ only in how they put the running server in front of the user.
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"barreplays/internal/config"
	"barreplays/internal/server"
)

// DefaultPort is tried first so the UI keeps a stable, bookmarkable address.
const DefaultPort = 8730

// UI presents a running server to the user.
//
// The returned channel closes when the user has finished with the interface,
// which ends the process. A nil channel means the interface's lifetime is not
// the process's business: a tab in the user's own browser can be closed and
// reopened freely, and quitting because it went away would be wrong.
type UI func(ctx context.Context, url string) <-chan struct{}

// Run serves the UI and blocks until it is dismissed, the process is
// interrupted, or serving fails.
func Run(port int, ui UI) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	store, err := config.NewStore()
	if err != nil {
		return err
	}
	cfg, err := store.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cacheDir, err := config.CacheDir()
	if err != nil {
		// The cache is an optimisation; losing it costs startup time, not
		// correctness.
		log.Printf("cache unavailable, index will rebuild each run: %v", err)
		cacheDir = ""
	}

	ln, err := listen(port)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Handler:           server.New(ctx, store, cfg, cacheDir).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	url := fmt.Sprintf("http://%s", ln.Addr().String())
	log.Printf("Beyond All Replays — %s", url)
	if cfg.Configured() {
		log.Printf("replay folder: %s", cfg.DemosPath)
	} else {
		log.Printf("first run: choose a replay folder in the browser")
	}

	var dismissed <-chan struct{}
	if ui != nil {
		dismissed = ui(ctx, url)
	}

	errc := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errc <- err
	}()

	select {
	case err := <-errc:
		return err
	case <-dismissed:
		log.Print("window closed, shutting down")
		return shutdown(srv)
	case <-ctx.Done():
		log.Print("shutting down")
		return shutdown(srv)
	}
}

func shutdown(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

// listen binds the loopback interface only — this serves local game files and
// has no authentication, so it must not be reachable from the network.
//
// If the preferred port is taken (a second copy already running, or an
// unrelated service), it falls back to any free port rather than refusing to
// start.
func listen(port int) (net.Listener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err == nil {
		return ln, nil
	}
	if port == 0 {
		return nil, err
	}
	log.Printf("port %d unavailable (%v), picking a free port", port, err)
	return net.Listen("tcp", "127.0.0.1:0")
}
