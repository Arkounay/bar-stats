//go:build !windows

package app

import "runtime"

// browserCandidates lists where a Chromium browser may be installed, in
// preference order. Bare names are resolved against PATH.
//
// Edge comes last for the same reason as on Windows: it is the fallback, not
// the choice. Firefox is absent because it is not Chromium and has no --app
// mode.
//
// Linux gets several names per browser because packagers disagree about them —
// Chromium is `chromium` on Arch and `chromium-browser` on Debian, Brave ships
// as `brave-browser` from its own repository and `brave` from the AUR — and
// then the vendor's own .deb and .rpm install under /opt, which is only on
// PATH by way of a symlink the package may or may not have created.
//
// Browsers installed through Flatpak or Snap are deliberately not hunted for.
// Finding them is easy; making them work is not, because both sandbox the
// browser away from the profile directory this needs to hand it, and a Snap in
// particular cannot read anything under a dotted directory in $HOME. They
// would be detected only to fail, so they are left to the fallback, which puts
// the UI in an ordinary browser tab instead.
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
		"chrome",
		"/opt/google/chrome/chrome",

		"brave-browser",
		"brave-browser-stable",
		"brave",
		"/opt/brave.com/brave/brave",

		"vivaldi",
		"vivaldi-stable",
		"/opt/vivaldi/vivaldi",

		"chromium",
		"chromium-browser",

		"microsoft-edge",
		"microsoft-edge-stable",
		"/opt/microsoft/msedge/msedge",
	}
}
