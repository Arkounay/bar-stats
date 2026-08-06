//go:build windows

package main

import (
	"log"
	"os"
	"path/filepath"

	"barreplays/internal/config"
	"golang.org/x/sys/windows"
)

// logFileName is where the GUI build's log stream ends up. It is truncated on
// every start: its use is explaining the run that just went wrong, not keeping
// a history.
const logFileName = "barstats-desktop.log"

// startLogging points the log stream at a file.
//
// A GUI binary has no console, so everything the application logs — which
// browser it could not find, why it fell back to a tab, which port it settled
// on — is otherwise written to a handle that does not exist. Those messages
// answer the likeliest question this flavour will ever be asked, so they are
// put somewhere a user can be pointed at rather than dropped.
//
// Failing to open the file is not worth reporting: there would be nowhere to
// report it to, and it does not stop the application working.
func startLogging() {
	dir, err := config.CacheDir()
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, logFileName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(f)
}

// fatal reports a startup failure and exits.
//
// This is the one message that cannot wait for someone to find the log file:
// without it the application would appear to do nothing at all when
// double-clicked.
func fatal(err error) {
	log.Printf("could not start: %v", err)

	text, terr := windows.UTF16PtrFromString(err.Error())
	title, cerr := windows.UTF16PtrFromString("BAR Stats could not start")
	if terr == nil && cerr == nil {
		_, _ = windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONERROR)
	}
	os.Exit(1)
}
