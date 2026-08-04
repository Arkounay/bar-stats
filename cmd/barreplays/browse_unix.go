//go:build !windows

package main

import (
	"log"
	"os/exec"
	"runtime"
)

// browse opens url in the default browser.
func browse(url string) {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	if err := exec.Command(opener, url).Start(); err != nil {
		log.Printf("could not open browser (open %s manually): %v", url, err)
	}
}
