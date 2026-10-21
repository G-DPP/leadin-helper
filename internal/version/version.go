// Package version holds the helper version and the protocol version spoken
// with the browser extension.
package version

// Version is the helper version. Release builds set it with
// -ldflags "-X leadin-helper/internal/version.Version=1.2.3".
var Version = "0.0.0-dev"

// ProtocolVersion changes whenever the shape of the messages exchanged with
// the extension changes. Version 2 sends result_id in run_finished and reads
// the allowed outcomes from the run spec.
const ProtocolVersion = 2
