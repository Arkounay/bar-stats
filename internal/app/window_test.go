package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// touchExecutable creates a file that exec.LookPath will accept, so the PATH
// branch can be exercised on platforms that check the executable bit.
func touchExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFirstInstalledPrefersEarlierCandidates(t *testing.T) {
	dir := t.TempDir()
	first := touchExecutable(t, dir, "first.exe")
	second := touchExecutable(t, dir, "second.exe")

	got := firstInstalled([]string{
		"",
		filepath.Join(dir, "missing.exe"),
		first,
		second,
	})
	if got != first {
		t.Errorf("firstInstalled = %q, want %q", got, first)
	}
}

func TestFirstInstalledSkipsDirectoriesAndMisses(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "chrome.exe"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := firstInstalled([]string{
		filepath.Join(dir, "nope.exe"),
		filepath.Join(dir, "chrome.exe"), // a directory, not a browser
	})
	if got != "" {
		t.Errorf("firstInstalled = %q, want \"\" when nothing is installed", got)
	}
}

func TestFirstInstalledResolvesBareNamesOnPath(t *testing.T) {
	dir := t.TempDir()
	name := "fake-chromium"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	want := touchExecutable(t, dir, name)
	t.Setenv("PATH", dir)

	if got := firstInstalled([]string{name}); got != want {
		t.Errorf("firstInstalled = %q, want %q", got, want)
	}
}

// TestWindowFallsBackWhenTheBrowserDiesOnStartup covers the failure this is
// most likely to hit on Linux, where a sandboxed browser can be launched
// successfully and then refuse the profile directory. The window must not
// report itself dismissed, because that would take the server down with it and
// the application would appear to flash and vanish.
func TestWindowFallsBackWhenTheBrowserDiesOnStartup(t *testing.T) {
	// A "browser" that exits the instant it starts, the way a confined one does.
	useBrowserCandidates(t, []string{buildQuitter(t, t.TempDir())})
	fellBack := make(chan string, 1)
	useLaunchBrowser(t, func(url string) { fellBack <- url })

	const url = "http://127.0.0.1:1/"
	dismissed := Window(t.Context(), url)

	select {
	case got := <-fellBack:
		if got != url {
			t.Errorf("fell back to %q, want %q", got, url)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("never fell back after the window failed to open")
	}

	select {
	case <-dismissed:
		t.Fatal("reported dismissal for a window that never opened; the server would have shut down")
	default:
	}
}

// useBrowserCandidates substitutes the installed-browser list for one test.
func useBrowserCandidates(t *testing.T, candidates []string) {
	t.Helper()
	prev := browserLookup
	browserLookup = func() []string { return candidates }
	t.Cleanup(func() { browserLookup = prev })
}

// useLaunchBrowser substitutes the fallback for one test, so exercising it does
// not open a real browser on the machine running the tests.
func useLaunchBrowser(t *testing.T, fn func(string)) {
	t.Helper()
	prev := launchBrowser
	launchBrowser = fn
	t.Cleanup(func() { launchBrowser = prev })
}

// buildQuitter compiles a stand-in browser that exits the moment it starts,
// which is what a sandboxed one does when it cannot use the profile directory
// it was handed.
func buildQuitter(t *testing.T, dir string) string {
	t.Helper()
	src := filepath.Join(dir, "quitter.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "quitter")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	if output, err := exec.Command("go", "build", "-o", out, src).CombinedOutput(); err != nil {
		t.Skipf("cannot build the stand-in browser: %v\n%s", err, output)
	}
	return out
}

// TestBrowserCandidatesAreOrdered guards the fallback intent: a deliberately
// installed Chrome should win over the Edge that every Windows install has.
func TestBrowserCandidatesAreOrdered(t *testing.T) {
	candidates := browserCandidates()
	if len(candidates) == 0 {
		t.Fatal("no browser candidates for this platform")
	}
	chrome, edge := -1, -1
	for i, c := range candidates {
		base := filepath.Base(c)
		if chrome < 0 && (base == "chrome.exe" || base == "Google Chrome" || base == "google-chrome") {
			chrome = i
		}
		if edge < 0 && (base == "msedge.exe" || base == "Microsoft Edge" || base == "microsoft-edge") {
			edge = i
		}
	}
	if chrome < 0 || edge < 0 {
		t.Fatalf("expected both Chrome and Edge among %v", candidates)
	}
	if chrome > edge {
		t.Errorf("Edge (%d) is preferred over Chrome (%d)", edge, chrome)
	}
}
