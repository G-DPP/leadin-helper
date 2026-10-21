// Package version holds the helper version and the protocol version spoken
// with the browser extension.
package version

import (
	"fmt"
	"runtime"
)

// Version is the helper version. scripts/build-release.sh sets it with
// -ldflags "-X <module>/internal/version.Version=X.Y.Z".
var Version = "0.0.0-dev"

// ProtocolVersion changes whenever the shape of the messages exchanged with
// the extension changes. Version 2 sends result_id in run_finished and reads
// the allowed outcomes from the run spec.
const ProtocolVersion = 2

// String is what leadin-helper --version prints.
func String() string {
	return fmt.Sprintf("leadin-helper %s (protocol %d, %s/%s)", Version, ProtocolVersion, runtime.GOOS, runtime.GOARCH)
}
