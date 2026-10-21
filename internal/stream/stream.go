// Package stream reads the stream-json output of `claude -p`: progress,
// final outcome, cost.
//
// Only this goes out to the browser:
//
//   - run_progress with a neutral key (item_saved…) and a counter per key,
//     derived from the tool name alone with the spec's progress_map. Never
//     inputs, results or text;
//   - run_finished with the outcome read from Claude's last JSON line, among
//     the spec's outcomes, and the cost and usage of the result event. The
//     result id is read under the spec's result_id_key and goes out as
//     result_id: the helper does not know what it is. If the last line is not
//     valid, the outcome is failed with the spec's failure_message: Claude's
//     text never goes out;
//   - run_failed if the MCP server did not accept the token, if the result
//     is missing or if it is an error (is_error). In that last case
//     ClassifyError picks the code (usage_limit, api_error, unknown), and the
//     event carries cost, usage, duration and turns, never Claude's text.
//
// The local log gets the same neutral keys, never the tool names: with the
// names the log would reconstruct the steps of a run.
//
// Subagents (Claude Code 2.1.291). Their calls arrive as ordinary assistant
// events, with parent_tool_use_id, and their tool_use blocks carry the full
// name (mcp__leadin__<tool>): they count like those of the main agent, and
// only the tool name is read for them too. The tool that starts them (Agent,
// Task in the init event) is usually not in the map and counts as working.
// The subagents' system events (task_started, task_progress,
// task_notification, with subagent text inside) carry no tools and are
// ignored; repeated init events mid-stream only matter for the MCP check.
//
// With background subagents the main agent closes several turns, and each
// turn has its own result, without parent_tool_use_id. The last one counts:
// it has the final text, and cost, usage and modelUsage (subagents included)
// are cumulative. duration_ms and num_turns belong to that turn only, and
// are summed over every result. A result with parent_tool_use_id (not seen
// so far) is dropped.
package stream

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"leadin-helper/internal/claude"
	"leadin-helper/internal/protocol"
	"leadin-helper/internal/spec"
	"leadin-helper/internal/version"
)

// MCPPrefix is the prefix of the LeadIn MCP server's tools.
const MCPPrefix = "mcp__" + claude.MCPServerName + "__"

// DefaultProgressKey is the key of the tools the run's progress_map does not
// name.
const DefaultProgressKey = "working"

// Reader reads claude's stream line by line and derives the protocol events.
type Reader struct {
	progressMap    map[string]string
	outcomes       []string
	resultIDKey    string
	failureMessage string

	// Counts are the calls per progress key so far.
	Counts map[string]int
	// MCPUnauthorized is set when an init event shows the LeadIn MCP server
	// not connected, or missing.
	MCPUnauthorized bool
	// Result is the last result of the main agent, Results how many arrived.
	Result     map[string]any
	Results    int
	durationMS int64
	numTurns   int64
	// LastProgress are the keys (for the log) of the tools seen in the last
	// line passed to Feed.
	LastProgress []string
}

// NewReader returns a reader for the spec's progress map, outcomes, result
// id key and failure message.
func NewReader(progressMap map[string]string, outcomes []string, resultIDKey, failureMessage string) *Reader {
	return &Reader{
		progressMap:    progressMap,
		outcomes:       outcomes,
		resultIDKey:    resultIDKey,
		failureMessage: failureMessage,
		Counts:         map[string]int{},
	}
}

// Feed returns the run_progress events of one stream line. Lines that are
// not JSON objects are ignored.
func (r *Reader) Feed(line []byte) []map[string]any {
	r.LastProgress = nil
	data, ok := decodeObject(line)
	if !ok {
		return nil
	}
	switch data["type"] {
	case "system":
		if data["subtype"] == "init" {
			r.checkInit(data)
		}
	case "assistant":
		return r.progress(data)
	case "result":
		if data["parent_tool_use_id"] == nil {
			r.addResult(data)
		}
	}
	return nil
}

// ProgressKey is the neutral key of the tool name, or working if the map
// does not name it. Only tools of the LeadIn MCP server lose their prefix:
// the same name on another server stays unknown.
func (r *Reader) ProgressKey(name string) string {
	if key, ok := r.progressMap[strings.TrimPrefix(name, MCPPrefix)]; ok {
		return key
	}
	return DefaultProgressKey
}

func (r *Reader) addResult(data map[string]any) {
	r.Result = data
	r.Results++
	if n, ok := intValue(data["duration_ms"]); ok {
		r.durationMS += n
	}
	if n, ok := intValue(data["num_turns"]); ok {
		r.numTurns += n
	}
}

func (r *Reader) checkInit(data map[string]any) {
	servers, ok := data["mcp_servers"].([]any)
	if !ok {
		return
	}
	for _, item := range servers {
		server, ok := item.(map[string]any)
		if ok && server["name"] == claude.MCPServerName {
			// failed or needs-auth: the token was not accepted.
			if server["status"] != "connected" {
				r.MCPUnauthorized = true
			}
			return
		}
	}
	// The server is not there at all: claude did not load it.
	r.MCPUnauthorized = true
}

func (r *Reader) progress(data map[string]any) []map[string]any {
	message, _ := data["message"].(map[string]any)
	content, ok := message["content"].([]any)
	if !ok {
		return nil
	}
	var events []map[string]any
	for _, item := range content {
		block, ok := item.(map[string]any)
		if !ok || block["type"] != "tool_use" {
			continue
		}
		name, ok := block["name"].(string)
		if !ok {
			continue
		}
		key := r.ProgressKey(name)
		r.Counts[key]++
		events = append(events, map[string]any{"type": "run_progress", "key": key, "count": r.Counts[key]})
		r.LastProgress = append(r.LastProgress, key)
	}
	return events
}

// FinalEvent is run_finished or run_failed, to send once claude has exited.
// A nil version is sent as null.
func (r *Reader) FinalEvent(claudeVersion, subscriptionType *string) map[string]any {
	versions := map[string]any{"claude_version": stringOrNil(claudeVersion), "subscription_type": stringOrNil(subscriptionType)}
	if r.MCPUnauthorized {
		return protocol.RunFailed("mcp_unauthorized", versions)
	}
	if r.Result == nil {
		return protocol.RunFailed("unknown", versions)
	}
	// is_error wins, even over a valid last line.
	if r.Result["is_error"] == true || r.Result["subtype"] != "success" {
		return protocol.RunFailed(ClassifyError(r.Result), merge(versions, r.metrics()))
	}

	final, ok := ParseFinalLine(r.Result["result"], r.outcomes, r.resultIDKey)
	if !ok {
		// The spec always has failed (package spec).
		final = map[string]any{"outcome": spec.FailedOutcome, "message": r.failureMessage, "result_id": nil}
	}
	event := merge(final, r.metrics())
	event["type"] = "run_finished"
	event["helper_version"] = version.Version
	return merge(event, versions)
}

// metrics are duration and turns summed over every result, cost and usage
// from the last one. With a single result they are its own. If the run
// started at least one subagent, usage also carries subagent_stats
// (spawned, finished, failed, stopped): counters only.
func (r *Reader) metrics() map[string]any {
	result := r.Result
	usage := map[string]any{"usage": result["usage"], "modelUsage": result["modelUsage"]}
	// Runs without subagents have subagent_stats too, all zero.
	if stats, ok := result["subagent_stats"].(map[string]any); ok {
		if spawned, ok := intValue(stats["spawned"]); ok && spawned != 0 {
			usage["subagent_stats"] = stats
		}
	}
	var duration, turns any = r.durationMS, r.numTurns
	if r.Results == 1 {
		duration, turns = intOrNil(result["duration_ms"]), intOrNil(result["num_turns"])
	}
	return map[string]any{
		"duration_ms": duration,
		"num_turns":   turns,
		"cost_usd":    numberOrNil(result["total_cost_usd"]),
		"usage":       usage,
	}
}

// ParseFinalLine reads the outcome from Claude's last non-empty line, if it
// is valid: an object whose outcome is among outcomes, whose message is a
// string of at most spec.MaxMessage characters, and whose resultIDKey is an
// integer or missing. Lines made only of code fences are skipped: the last
// useful line is the JSON.
func ParseFinalLine(value any, outcomes []string, resultIDKey string) (map[string]any, bool) {
	text, ok := value.(string)
	if !ok {
		return nil, false
	}
	var lines []string
	for _, line := range splitLines(text) {
		line = strings.TrimSpace(line)
		if line == "" || (strings.HasPrefix(line, "```") && stripFence(line) == "") {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return nil, false
	}
	data, ok := decodeObject([]byte(stripFence(lines[len(lines)-1])))
	if !ok {
		return nil, false
	}
	outcome, ok := data["outcome"].(string)
	if !ok || !slices.Contains(outcomes, outcome) {
		return nil, false
	}
	message, ok := data["message"].(string)
	if !ok || utf8.RuneCountInString(message) > spec.MaxMessage {
		return nil, false
	}
	resultID := data[resultIDKey]
	if resultID != nil {
		if _, ok := intValue(resultID); !ok {
			return nil, false
		}
	}
	return map[string]any{"outcome": outcome, "message": message, "result_id": resultID}, true
}

// stripFence removes a code fence on the same line (```json {…}```).
func stripFence(line string) string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "```") {
		line = strings.TrimPrefix(line[3:], "json")
	}
	line = strings.TrimSuffix(line, "```")
	return strings.TrimSpace(line)
}

// splitLines splits like Python's str.splitlines, which the earlier helper
// used: \n, \r, \r\n and the other Unicode line boundaries.
func splitLines(text string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		next := i + size
		switch r {
		case '\r':
			// \r\n is a single boundary.
			if next < len(text) && text[next] == '\n' {
				next++
			}
			fallthrough
		case '\n', '\v', '\f', '\x1c', '\x1d', '\x1e', '\u0085', '\u2028', '\u2029':
			lines = append(lines, text[start:i])
			start = next
		}
		i = next
	}
	if start < len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}

// decodeObject decodes a JSON object, numbers as json.Number.
func decodeObject(data []byte) (map[string]any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, false
	}
	// Anything after the object makes it invalid.
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	return object, true
}

// intValue is the value of a JSON integer (json.Number without fraction or
// exponent).
func intValue(value any) (int64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(string(number), 10, 64)
	return n, err == nil
}

func intOrNil(value any) any {
	if _, ok := intValue(value); ok {
		return value
	}
	return nil
}

func numberOrNil(value any) any {
	if _, ok := value.(json.Number); ok {
		return value
	}
	return nil
}

func stringOrNil(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func merge(maps ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, m := range maps {
		for key, value := range m {
			out[key] = value
		}
	}
	return out
}
