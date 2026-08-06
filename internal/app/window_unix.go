//go:build !windows

package app

import "runtime"

// browserCandidates lists where a Chromium browser may be installed, in
// preference order. Bare names are resolved against PATH.
//
// Edge comes last for the same reason as on Windows: it is the fallback, not
// the choice. Firefox is absent because it is not Chromium and has no --app
// mode.
func browserCandidates() []string {
	if runtime.GOOS == "darwin" {
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			"/Applications/Vivaldi.app/Contents/MacOS/Vivaldi",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	}
	return []string{
		"google-chrome",
		"google-chrome-stable",
		"brave-browser",
		"vivaldi",
		"chromium",
		"chromium-browser",
		"microsoft-edge",
	}
}
