//go:build !windows

package main

import "log"

// fatal reports a startup failure and exits. Outside Windows the binary is
// linked normally and keeps its standard error, so the message has somewhere
// to go.
func fatal(err error) {
	log.Fatal(err)
}
