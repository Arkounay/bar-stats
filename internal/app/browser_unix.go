//go:build !windows

package app

import (
	"log"
	"os/exec"
	"runtime"
)

// openBrowser opens url in the default browser.
func openBrowser(url string) {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	if err := exec.Command(opener, url).Start(); err != nil {
		log.Printf("could not open browser (open %s manually): %v", url, err)
	}
}
