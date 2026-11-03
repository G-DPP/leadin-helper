// Command leadin-helper is the native messaging host of the LeadIn browser
// extension. See README.md and PROTOCOL.md.
//
// How it runs depends on the arguments:
//
//   - chrome-extension://…/ (always the first argument when a Chromium
//     browser starts it; on Windows --parent-window follows): native
//     messaging. Both are ignored otherwise: the host manifest already allows
//     only the LeadIn extension.
//   - none (a double click): it installs itself, and with a terminal waits
//     for Enter so the window does not close at once.
//   - --install, --uninstall: the same from a terminal or a script, without
//     waiting.
//   - --version, alone: prints the version.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/G-DPP/leadin-helper/internal/host"
	"github.com/G-DPP/leadin-helper/internal/install"
	"github.com/G-DPP/leadin-helper/internal/logs"
	"github.com/G-DPP/leadin-helper/internal/version"
)

const usage = "usage: leadin-helper [--install | --uninstall | --version]\n"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin *os.File, stdout, stderr io.Writer) int {
	switch {
	case len(args) > 0 && strings.HasPrefix(args[0], "chrome-extension://"):
		return host.Serve(stdin, stdout, logs.Open())
	case len(args) == 0:
		return finish(install.Install(stdout), "install", stdout, waitForEnter(stdin))
	case len(args) == 1 && args[0] == "--install":
		return finish(install.Install(stdout), "install", stdout, nil)
	case len(args) == 1 && args[0] == "--uninstall":
		return finish(install.Uninstall(stdout), "uninstall", stdout, nil)
	case len(args) == 1 && args[0] == "--version":
		// Before the log is opened and without reading stdin.
		fmt.Fprintln(stdout, version.String())
		return 0
	}
	fmt.Fprintf(stderr, "unknown arguments %q\n%s", args, usage)
	return 2
}

// finish prints the error, if any, logs the outcome and, after a double
// click, waits for Enter.
func finish(err error, action string, out io.Writer, wait func()) int {
	code := 0
	fields := map[string]any{"action": action, "outcome": "ok"}
	if err != nil {
		fmt.Fprintf(out, "\nERROR: %v\n", err)
		// Only that it failed: the message can hold paths of the home.
		fields["outcome"] = "error"
		fields["error"] = action + "_failed"
		code = 1
	}
	logs.Open().Event(map[bool]string{true: "ERROR", false: "INFO"}[err != nil], fields)
	if wait != nil {
		wait()
	}
	return code
}

// waitForEnter returns a function that waits for Enter, or nil if stdin is
// not a terminal (a double click on Linux may have none: nothing to wait for).
func waitForEnter(stdin *os.File) func() {
	if !term.IsTerminal(int(stdin.Fd())) {
		return nil
	}
	return func() {
		fmt.Print("\nPress Enter to close")
		var line string
		fmt.Fscanln(stdin, &line)
	}
}
