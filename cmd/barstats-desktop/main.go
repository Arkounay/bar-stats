// Command barstats-desktop runs BAR Stats as a standalone desktop
// application: the same local server as the barreplays command, presented in a
// chromeless application window instead of a browser tab, and shut down when
// that window is closed.
//
// On Windows it is linked as a GUI binary, so double-clicking it opens the
// application and nothing else — no console window behind it. That leaves the
// log stream and any startup failure with nowhere to go, so both are given
// somewhere; see diagnostics_windows.go.
package main

import (
	"flag"

	"barreplays/internal/app"
)

func main() {
	port := flag.Int("port", app.DefaultPort, "port to listen on (0 picks a free one)")
	browser := flag.Bool("browser", false, "open in the default browser instead of an application window")
	flag.Parse()

	startLogging()

	ui := app.Window
	if *browser {
		ui = app.Browser
	}
	if err := app.Run(*port, ui); err != nil {
		fatal(err)
	}
}
