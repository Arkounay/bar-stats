//go:build windows

package main

import (
	"log"

	"golang.org/x/sys/windows"
)

// browse opens url in the default browser.
//
// This calls ShellExecute directly instead of the usual Go recipe of shelling
// out to `rundll32 url.dll,FileProtocolHandler`. Spawning rundll32 is a
// living-off-the-land pattern that antivirus heuristics weigh heavily, and it
// leaves those strings in the binary; the API call avoids both and starts no
// child process.
func browse(url string) {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		log.Printf("could not open browser (open %s manually): %v", url, err)
		return
	}
	target, err := windows.UTF16PtrFromString(url)
	if err != nil {
		log.Printf("could not open browser (open %s manually): %v", url, err)
		return
	}
	if err := windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL); err != nil {
		log.Printf("could not open browser (open %s manually): %v", url, err)
	}
}
