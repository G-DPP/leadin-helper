// Command leadin-helper is the native messaging host of the LeadIn browser
// extension. See README.md and PROTOCOL.md.
//
// Chrome passes the caller's origin (chrome-extension://…/) as the first
// argument and, on Windows, --parent-window: both are ignored, because the
// native host manifest already allows only the LeadIn extension.
//
// The only flag is --version, alone. Chrome never passes it, so it cannot
// clash with Native Messaging. --install and --uninstall will come later.
package main

import (
	"fmt"
	"os"

	"leadin-helper/internal/host"
	"leadin-helper/internal/logs"
	"leadin-helper/internal/version"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		// Before the log is opened and without reading stdin.
		fmt.Println(version.String())
		os.Exit(0)
	}
	os.Exit(host.Serve(os.Stdin, os.Stdout, logs.Open()))
}
