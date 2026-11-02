package run

import (
	"fmt"
	"regexp"

	"github.com/G-DPP/leadin-helper/internal/spec"
)

// ticketPattern is the server's token_urlsafe: the same check as the
// extension's.
var ticketPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{20,128}$`)

// startFields are the only fields of a start_run request.
var startFields = []string{"action", "run_id", "ticket", "server_url"}

// StartRequest is a checked start_run request.
type StartRequest struct {
	RunID     int64
	Ticket    string
	ServerURL string
}

// String never shows the ticket, not even in a panic.
func (r StartRequest) String() string {
	return fmt.Sprintf("StartRequest{RunID: %d, ServerURL: %s}", r.RunID, r.ServerURL)
}

// ParseStart returns the request if msg is a valid start_run: exactly
// {action, run_id, ticket, server_url}. An extra field (the token, the model
// or the input of an older extension) makes it invalid. Numbers must be
// json.Number, as protocol.ReadMessage decodes them.
func ParseStart(msg map[string]any) (StartRequest, bool) {
	if len(msg) != len(startFields) {
		return StartRequest{}, false
	}
	for _, field := range startFields {
		if _, ok := msg[field]; !ok {
			return StartRequest{}, false
		}
	}
	runID, ok := spec.Int(msg["run_id"])
	if !ok || runID <= 0 {
		return StartRequest{}, false
	}
	ticket, ok := msg["ticket"].(string)
	if !ok || !ticketPattern.MatchString(ticket) {
		return StartRequest{}, false
	}
	serverURL, ok := msg["server_url"].(string)
	if !ok || !spec.IsValidServerURL(serverURL) {
		return StartRequest{}, false
	}
	return StartRequest{RunID: runID, Ticket: ticket, ServerURL: serverURL}, true
}
