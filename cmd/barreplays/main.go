// Command barreplays indexes a Beyond All Reason replay folder and serves a
// local web UI for browsing per-match statistics.
package main

import (
	"context"
	"errors"
	"flag"
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

// defaultPort is tried first so the UI keeps a stable, bookmarkable address.
const defaultPort = 8730

func main() {
	port := flag.Int("port", defaultPort, "port to listen on (0 picks a free one)")
	noOpen := flag.Bool("no-open", false, "do not open a browser on start")
	flag.Parse()

	if err := run(*port, !*noOpen); err != nil {
		log.Fatal(err)
	}
}

func run(port int, openBrowser bool) error {
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
	if openBrowser {
		go browse(url)
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
	case <-ctx.Done():
		log.Print("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
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

// browse is implemented per platform; see browse_windows.go and browse_unix.go.
