//go:build !windows

package main

import "log"

// startLogging leaves the log stream on standard error. Outside Windows the
// binary is linked normally and keeps its console, so there is nothing to
// rescue.
func startLogging() {}

// fatal reports a startup failure and exits.
func fatal(err error) {
	log.Fatal(err)
}
