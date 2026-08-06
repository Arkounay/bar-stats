package app

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
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
