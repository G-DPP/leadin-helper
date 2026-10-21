// Package spec redeems the run ticket on the server and checks the run spec
// it gets back.
//
// The web page hands the helper only run_id and ticket; the extension adds
// its own server_url. The helper trades the ticket for the run spec
// (POST {server_url}/api/v1/claude-runs/redeem/): URL and token of the MCP
// server, model, prompt, tools, subagents, progress map and the outcomes
// allowed in Claude's last line. The ticket is valid once and for a few
// seconds, so it is redeemed first, before even looking for claude.
//
// The helper knows nothing about what a run does: it executes the spec it
// gets. It accepts the spec only if it narrows what the helper can do, never
// if it widens it: a compromised server or a wrong address cannot make it
// start claude with Bash, with another MCP server or with any model. The
// allowed sets in spec.go are the helper's limits, not data of the runs.
package spec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"leadin-helper/internal/version"
)

const (
	// RedeemPath is appended to server_url to redeem the ticket.
	RedeemPath = "/api/v1/claude-runs/redeem/"
	// RedeemTimeout bounds the whole redeem request.
	RedeemTimeout = 10 * time.Second
	// MaxRedeemBody bounds the response: the largest spec, with every
	// character of the prompt and of the subagents escaped as \uXXXX, is
	// below 3 MB.
	MaxRedeemBody = 8 << 20
)

// localHosts are the only hosts allowed over plain http.
var localHosts = map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}

// RedeemError reports a failed redeem; Code is the run_failed code.
type RedeemError struct {
	Code string
}

func (e *RedeemError) Error() string { return "redeem failed: " + e.Code }

// origin is the (scheme, host, port) of an http or https URL.
type origin struct {
	scheme, host string
	port         int
}

// parseOrigin returns the origin of an http or https URL with a host and no
// user information.
func parseOrigin(raw string) (origin, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" || u.User != nil {
		return origin{}, false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return origin{}, false
	}
	port := 0
	if p := u.Port(); p != "" {
		port, err = strconv.Atoi(p)
		if err != nil || port > 65535 {
			return origin{}, false
		}
	}
	if port == 0 {
		port = 80
		if u.Scheme == "https" {
			port = 443
		}
	}
	return origin{u.Scheme, host, port}, true
}

// IsValidServerURL is true for a bare origin: https, or http only on this
// computer. No path, query or fragment: it is the backend's origin, and the
// helper adds the redeem path itself.
func IsValidServerURL(raw string) bool {
	o, ok := parseOrigin(raw)
	if !ok {
		return false
	}
	u, _ := url.Parse(raw)
	if u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		strings.HasSuffix(raw, "?") || strings.HasSuffix(raw, "#") {
		return false
	}
	return o.scheme == "https" || localHosts[o.host]
}

var client = &http.Client{
	Timeout: RedeemTimeout,
	// No redirects: the ticket goes only to the configured origin, and a
	// 3xx becomes server_error.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// Redeem trades the ticket for the run spec, still to be checked with
// Validate. Numbers are decoded as json.Number.
//
// Errors are *RedeemError with ticket_rejected (404: the ticket is expired,
// already used or wrong; the server does not say which), server_unreachable
// (network, TLS, timeout) or server_error (any other answer, redirects
// included, and invalid JSON).
func Redeem(ctx context.Context, serverURL string, runID int64, ticket string) (map[string]any, error) {
	body, err := json.Marshal(map[string]any{"run_id": runID, "ticket": ticket})
	if err != nil {
		return nil, &RedeemError{"server_error"}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+RedeemPath, bytes.NewReader(body))
	if err != nil {
		return nil, &RedeemError{"server_unreachable"}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "leadin-helper/"+version.Version)
	response, err := client.Do(request)
	if err != nil {
		// The error text holds the URL: only the code goes out.
		return nil, &RedeemError{"server_unreachable"}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, MaxRedeemBody+1))
	if response.StatusCode == http.StatusNotFound {
		return nil, &RedeemError{"ticket_rejected"}
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, &RedeemError{"server_error"}
	}
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) {
			return nil, &RedeemError{"server_unreachable"}
		}
		return nil, &RedeemError{"server_error"}
	}
	if len(payload) > MaxRedeemBody || !utf8.Valid(payload) {
		return nil, &RedeemError{"server_error"}
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var data map[string]any
	if err := decoder.Decode(&data); err != nil || data == nil {
		return nil, &RedeemError{"server_error"}
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, &RedeemError{"server_error"}
	}
	return data, nil
}
