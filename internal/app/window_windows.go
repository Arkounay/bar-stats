//go:build windows

package app

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// chromiumExecutables lists the browsers that can host an application window,
// in preference order.
//
// Edge comes last on purpose. It is on every Windows install whether or not
// anyone wanted it, which makes it the right thing to fall back to and the
// wrong thing to reach for first: a machine carrying any other entry here
// carries it because someone chose it. Firefox is absent because it is not
// Chromium and has no --app mode.
var chromiumExecutables = []string{
	"chrome.exe",
	"brave.exe",
	"vivaldi.exe",
	"opera.exe",
	"chromium.exe",
	"msedge.exe",
}

// vendorDirs maps an executable to the folders its vendor installs into,
// relative to a program-files or local-app-data root.
var vendorDirs = map[string][]string{
	"chrome.exe":   {`Google\Chrome\Application`},
	"brave.exe":    {`BraveSoftware\Brave-Browser\Application`},
	"vivaldi.exe":  {`Vivaldi\Application`},
	"opera.exe":    {`Programs\Opera`, `Opera`},
	"chromium.exe": {`Chromium\Application`},
	"msedge.exe":   {`Microsoft\Edge\Application`},
}

// browserCandidates lists where a Chromium browser may be installed, in
// preference order.
func browserCandidates() []string {
	roots := []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("LOCALAPPDATA"),
	}

	var out []string
	for _, exe := range chromiumExecutables {
		// What the browser recorded at install time is tried first: it is
		// authoritative, and it finds installs in places no list would guess.
		if path := registeredPath(exe); path != "" {
			out = append(out, path)
		}
		for _, root := range roots {
			if root == "" {
				continue
			}
			for _, dir := range vendorDirs[exe] {
				out = append(out, filepath.Join(root, dir, exe))
			}
		}
	}
	return out
}

// registeredPath returns the location a browser recorded under the App Paths
// key, which is the mechanism Windows itself uses to resolve a bare executable
// name typed into Run or passed to ShellExecute.
func registeredPath(exe string) string {
	const appPaths = `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\`
	// Per-user installs shadow machine-wide ones, so HKCU is consulted first.
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		key, err := registry.OpenKey(root, appPaths+exe, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		path, _, err := key.GetStringValue("")
		key.Close()
		if err == nil && path != "" {
			return path
		}
	}
	return ""
}
