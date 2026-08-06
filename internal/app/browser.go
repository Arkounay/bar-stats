package app

import "context"

// Browser opens the UI as a tab in the user's default browser.
//
// It reports no dismissal: the server deliberately outlives the tab, because a
// user who closes it may only be navigating away and can return to the address
// whenever they like.
func Browser(ctx context.Context, url string) <-chan struct{} {
	go launchBrowser(url)
	return nil
}
