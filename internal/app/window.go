package app

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"barreplays/internal/config"
)

// initialWindowSize is applied the first time a window is opened. Chromium
// remembers the size and position in its profile from then on, so this is a
// starting point rather than a policy.
const initialWindowSize = "1400,900"

// windowStartupGrace is how long a window must survive before its exit is
// taken to mean the user closed it. Below this it is read as a failure to
// launch; a user cannot realistically open and dismiss the window inside it,
// and if they somehow do, the cost is a browser tab they did not ask for.
const windowStartupGrace = 5 * time.Second

// Window opens the UI as a chromeless application window.
//
// An installed Chromium browser does the rendering, in its "app" mode: no
// address bar, no tabs, its own taskbar entry. That buys a standalone app for
// the price of a process launch, with no second rendering engine to ship and
// no change to what the UI is — it is the same page the browser build serves.
//
// The profile directory is the load-bearing part. Chromium hands a launch off
// to an already-running instance and exits immediately when the profile is
// shared, which would both put the window inside the user's ordinary browser
// session and destroy the exit signal this depends on. A private profile keeps
// the window in a process we own, so closing it ends the process, which is
// what makes the window's lifetime the application's lifetime.
//
// Falls back to the default browser when no Chromium is installed, so the
// desktop build always ends up showing something.
func Window(ctx context.Context, url string) <-chan struct{} {
	browser := findBrowser()
	if browser == "" {
		log.Print("no Chromium-based browser found; opening the default browser instead")
		return Browser(ctx, url)
	}
	profile, err := windowProfileDir()
	if err != nil {
		log.Printf("could not prepare the window profile (%v); opening the default browser instead", err)
		return Browser(ctx, url)
	}

	cmd := exec.Command(browser,
		"--app="+url,
		"--user-data-dir="+profile,
		"--window-size="+initialWindowSize,
		// A private profile is a first run every time it is created, and the
		// prompts that come with one have no place in an application window.
		"--no-first-run",
		"--no-default-browser-check",
	)
	if err := cmd.Start(); err != nil {
		log.Printf("could not open the application window (%v); opening the default browser instead", err)
		return Browser(ctx, url)
	}

	started := time.Now()
	dismissed := make(chan struct{})
	go func() {
		err := cmd.Wait()
		// Starting the process only means it was forked; the browser can still
		// fail once it is running — no display to open on, a sandbox that
		// forbids the profile directory, a missing library. Treating that as
		// the user closing the window would take the server down with it, and
		// the application would appear to flash and vanish. A window that dies
		// this fast never opened, so fall back as if it had never started.
		if lifetime := time.Since(started); lifetime < windowStartupGrace {
			log.Printf("the application window closed after %v (%v); opening the default browser instead", lifetime.Round(time.Millisecond), err)
			launchBrowser(url)
			return
		}
		if err != nil {
			log.Printf("application window exited: %v", err)
		}
		close(dismissed)
	}()
	// Ctrl-C at the terminal should take the window with it, rather than
	// leaving an orphan pointing at a server that is going away.
	go func() {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
		case <-dismissed:
		}
	}()
	return dismissed
}

// windowProfileDir is where the window's browser profile lives. It is kept
// between runs so the window reopens at the size and position the user left
// it, and sits under the cache directory because nothing in it is worth
// preserving if it is lost.
func windowProfileDir() (string, error) {
	cacheDir, err := config.CacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cacheDir, "window")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// browserLookup and launchBrowser are indirected so tests can supply a
// stand-in browser and observe the fallback without opening a real one.
var (
	browserLookup = browserCandidates
	launchBrowser = openBrowser
)

// findBrowser returns the first installed Chromium-based browser, or "" if
// there is none.
func findBrowser() string { return firstInstalled(browserLookup()) }

// firstInstalled returns the first candidate that is really on this machine.
// A bare name is looked up on PATH; anything else is a full path that only
// counts if a file is actually there.
func firstInstalled(candidates []string) string {
	for _, path := range candidates {
		if path == "" {
			continue
		}
		if filepath.Base(path) == path {
			if found, err := exec.LookPath(path); err == nil {
				return found
			}
			continue
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}
