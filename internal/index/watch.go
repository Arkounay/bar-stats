package index

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/fsnotify/fsnotify"
)

// WatchMode selects how the replay folder is monitored.
type WatchMode string

const (
	// WatchEvents subscribes to filesystem notifications, falling back to
	// polling if the platform or filesystem will not deliver them.
	WatchEvents WatchMode = "events"
	// WatchPoll re-lists the folder on a timer.
	WatchPoll WatchMode = "poll"
	// WatchOff disables automatic refresh; only the Rescan button updates.
	WatchOff WatchMode = "off"
)

// ParseWatchMode maps a stored setting onto a mode, defaulting to events.
func ParseWatchMode(s string) WatchMode {
	switch WatchMode(s) {
	case WatchPoll:
		return WatchPoll
	case WatchOff:
		return WatchOff
	default:
		return WatchEvents
	}
}

const (
	// pollInterval is how often the folder is re-listed in polling mode.
	pollInterval = 15 * time.Second
	// eventPollInterval is the slow safety net kept running alongside events,
	// to catch anything the notification layer silently drops.
	eventPollInterval = 2 * time.Minute
	// quietPeriod is how long events must stop arriving before acting on
	// them. The game writes to the demo continuously during a match, so
	// without this a single match would trigger a rescan per flush.
	quietPeriod = 3 * time.Second
)

// Watch keeps the index current until ctx is cancelled.
//
// Both modes converge on the same check — compare a fingerprint of the folder,
// rescan when it differs — so filesystem events act only as a trigger. That
// keeps the correctness of the refresh independent of how reliable the
// platform's notifications turn out to be.
func (ix *Index) Watch(ctx context.Context, mode WatchMode) {
	switch mode {
	case WatchOff:
		<-ctx.Done()
	case WatchPoll:
		ix.watchByPolling(ctx, pollInterval)
	default:
		if err := ix.watchByEvents(ctx); err != nil {
			log.Printf("filesystem events unavailable (%v); falling back to polling", err)
			ix.watchByPolling(ctx, pollInterval)
		}
	}
}

// watchByPolling re-lists the folder on a timer.
func (ix *Index) watchByPolling(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	last := ix.fingerprint()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			last = ix.rescanIfChanged(ctx, last)
		}
	}
}

// watchByEvents subscribes to filesystem notifications for the replay folder.
//
// It returns an error only if the watch cannot be established, letting the
// caller fall back. A slow poll runs alongside regardless: notifications are
// best-effort on network filesystems and in virtualised mounts, and a missed
// event would otherwise mean a replay never appears.
func (ix *Index) watchByEvents(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	if err := watcher.Add(ix.dir); err != nil {
		return err
	}

	safetyNet := time.NewTicker(eventPollInterval)
	defer safetyNet.Stop()

	// A nil channel blocks forever, so the timer is only selected on once an
	// event has actually scheduled it.
	var settle <-chan time.Time
	last := ix.fingerprint()

	for {
		select {
		case <-ctx.Done():
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return errors.New("watcher closed")
			}
			// Chmod alone never changes what we would read.
			if event.Op == fsnotify.Chmod {
				continue
			}
			// Restart the quiet period on every event, so a match being
			// written produces one rescan after it finishes rather than
			// hundreds while it runs.
			settle = time.After(quietPeriod)

		case err, ok := <-watcher.Errors:
			if !ok {
				return errors.New("watcher closed")
			}
			log.Printf("watch error: %v", err)

		case <-settle:
			settle = nil
			last = ix.rescanIfChanged(ctx, last)

		case <-safetyNet.C:
			last = ix.rescanIfChanged(ctx, last)
		}
	}
}

// rescanIfChanged rescans when the folder's fingerprint has moved, returning
// the fingerprint to compare against next time.
func (ix *Index) rescanIfChanged(ctx context.Context, last uint64) uint64 {
	current := ix.fingerprint()
	if current == last {
		return last
	}
	ix.Pending()
	if err := ix.Scan(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("automatic rescan failed: %v", err)
		return last // retry on the next tick
	}
	return current
}
