package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeDemo creates a replay file aged by the given duration. Modification
// times are set explicitly rather than slept for, so the settle window can be
// exercised without the test taking as long as the window.
func writeDemo(t *testing.T, dir, name string, size int, age time.Duration) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestListDemosReportsWhenSettlingFilesAreReady(t *testing.T) {
	dir := t.TempDir()
	writeDemo(t, dir, "old.sdfz", 128, settleTime+time.Minute)
	writeDemo(t, dir, "fresh.sdfz", 128, settleTime/4)
	writeDemo(t, dir, "fresher.sdfz", 128, 0)
	writeDemo(t, dir, "empty.sdfz", 0, time.Hour)
	writeDemo(t, dir, "notademo.txt", 128, time.Hour)

	files, settling, err := listDemos(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || filepath.Base(files[0].path) != "old.sdfz" {
		t.Fatalf("listed %v, want only old.sdfz", files)
	}
	// The wait is to the *earliest* file to become ready — the caller re-arms
	// after each look, so the later ones are picked up by the next wait.
	want := settleTime - settleTime/4
	if settling > want || settling < want/2 {
		t.Errorf("settling = %v, want the wait for fresh.sdfz (near %v)", settling, want)
	}
}

func TestListDemosSettlingIsZeroWhenNothingIsWaiting(t *testing.T) {
	dir := t.TempDir()
	writeDemo(t, dir, "old.sdfz", 128, time.Hour)

	files, settling, err := listDemos(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("listed %d files, want 1", len(files))
	}
	if settling != 0 {
		t.Errorf("settling = %v, want 0 with no file in its settle window", settling)
	}
}

// TestWatchStateArmsForSettlingFile pins down the fix for a replay appearing
// late: the check triggered by a match's final write finds the file still too
// fresh to read, so it has to leave a timer behind to come back for it.
func TestWatchStateArmsForSettlingFile(t *testing.T) {
	dir := t.TempDir()
	ix := New(dir, "")

	w := ix.newWatchState()
	if w.settled != nil {
		t.Fatal("armed a settle timer for an empty folder")
	}

	writeDemo(t, dir, "just-finished.sdfz", 128, 0)
	w.check(t.Context())
	if w.settled == nil {
		t.Fatal("no settle timer after a replay appeared mid-settle; it would go unnoticed")
	}

	// Once the file ages out, the timer is dropped and the folder is acted on.
	before := w.last
	writeDemo(t, dir, "just-finished.sdfz", 128, settleTime+time.Minute)
	w.check(t.Context())
	if w.settled != nil {
		t.Error("kept a settle timer with no file waiting")
	}
	if w.last == before {
		t.Error("the settled replay did not register as a change to the folder")
	}
}
