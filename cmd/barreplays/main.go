// Command barreplays indexes a Beyond All Reason replay folder and serves a
// local web UI for browsing per-match statistics.
//
// It opens the UI in the default browser and keeps serving after the tab is
// closed. For a standalone application window instead, see the sibling command
// barstats-desktop.
package main

import (
	"flag"
	"log"

	"barreplays/internal/app"
)

func main() {
	port := flag.Int("port", app.DefaultPort, "port to listen on (0 picks a free one)")
	noOpen := flag.Bool("no-open", false, "do not open a browser on start")
	flag.Parse()

	ui := app.Browser
	if *noOpen {
		ui = app.None
	}
	if err := app.Run(*port, ui); err != nil {
		log.Fatal(err)
	}
}
