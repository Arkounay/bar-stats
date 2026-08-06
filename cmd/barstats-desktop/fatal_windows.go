//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// fatal reports a startup failure and exits.
//
// A GUI binary has no console, so a printed message would go nowhere and the
// application would appear to do nothing at all when double-clicked. A dialog
// is the only way the user finds out why.
func fatal(err error) {
	text, terr := windows.UTF16PtrFromString(err.Error())
	title, cerr := windows.UTF16PtrFromString("BAR Stats could not start")
	if terr == nil && cerr == nil {
		_, _ = windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONERROR)
	}
	os.Exit(1)
}
