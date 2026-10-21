// Command leadin-helper is the native messaging host of the LeadIn browser
// extension. See README.md and PROTOCOL.md.
//
// Chrome passes the caller's origin (chrome-extension://…/) as the first
// argument and, on Windows, --parent-window: both are ignored, because the
// native host manifest already allows only the LeadIn extension.
package main

import (
	"os"

	"leadin-helper/internal/host"
	"leadin-helper/internal/logs"
)

func main() {
	os.Exit(host.Serve(os.Stdin, os.Stdout, logs.Open()))
}
