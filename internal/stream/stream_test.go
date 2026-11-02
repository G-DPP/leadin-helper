package stream

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/G-DPP/leadin-helper/internal/version"
)

// The progress map, outcomes, result id key and failure message of a made-up
// run spec: the helper receives them, it does not know them.
var progressMap = map[string]string{
	"start_example":  "started",
	"store_item":     "item_saved",
	"start_group":    "group_started",
	"close_group":    "group_done",
	"store_summary":  "summary_saved",
	"finish_example": "completing",
	"WebSearch":      "searching_web",
	"WebFetch":       "reading_page",
}

var outcomes = []string{"done", "skipped", "failed"}

const (
	resultIDKey    = "item_id"
	failureMessage = "The run did not finish correctly."
)

func ptr(s string) *string { return &s }

func newReader() *Reader {
	return NewReader(progressMap, outcomes, resultIDKey, failureMessage)
}

func readFixture(t *testing.T, name string) [][]byte {
	t.Helper()
	file, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var lines [][]byte
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		lines = append(lines, append([]byte(nil), scanner.Bytes()...))
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

func feedAll(reader *Reader, lines [][]byte) []map[string]any {
	var events []map[string]any
	for _, line := range lines {
		events = append(events, reader.Feed(line)...)
	}
	return events
}

func decode(t *testing.T, line string) map[string]any {
	t.Helper()
	data, ok := decodeObject([]byte(line))
	if !ok {
		t.Fatalf("not an object: %s", line)
	}
	return data
}

func resultLine(t *testing.T, text string, fields map[string]any) []byte {
	t.Helper()
	event := map[string]any{
		"type":           "result",
		"subtype":        "success",
		"is_error":       false,
		"result":         text,
		"total_cost_usd": 0.5,
		"usage":          map[string]any{"input_tokens": 10, "server_tool_use": map[string]any{"web_search_requests": 1}},
		"modelUsage":     map[string]any{"claude-sonnet": map[string]any{"costUSD": 0.5}},
		"duration_ms":    1234,
		"num_turns":      3,
	}
	for key, value := range fields {
		event[key] = value
	}
	line, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return line
}

func assistantLine(t *testing.T, text string, names ...string) []byte {
	t.Helper()
	content := []any{}
	for _, name := range names {
		content = append(content, map[string]any{"type": "tool_use", "id": "t", "name": name, "input": map[string]any{"secret": "SECRET INPUT"}})
	}
	if text != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	line, err := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": content}})
	if err != nil {
		t.Fatal(err)
	}
	return line
}

func finalFor(t *testing.T, text string) map[string]any {
	t.Helper()
	reader := newReader()
	reader.Feed(resultLine(t, text, nil))
	return reader.FinalEvent(ptr("2.1.300"), ptr("pro"))
}

func serialize(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func keysOf(event map[string]any) []string {
	keys := []string{}
	for key := range event {
		keys = append(keys, key)
	}
	return keys
}

func assertKeys(t *testing.T, event map[string]any, want ...string) {
	t.Helper()
	if len(event) != len(want) {
		t.Fatalf("keys = %v, want %v", keysOf(event), want)
	}
	for _, key := range want {
		if _, ok := event[key]; !ok {
			t.Fatalf("keys = %v, want %v", keysOf(event), want)
		}
	}
}

func asString(value any) string {
	if number, ok := value.(json.Number); ok {
		return number.String()
	}
	if value == nil {
		return "<nil>"
	}
	data, _ := json.Marshal(value)
	return strings.Trim(string(data), `"`)
}

func TestProgressKeysFromFixture(t *testing.T) {
	events := feedAll(newReader(), readFixture(t, "run_basic.jsonl"))
	want := []map[string]any{
		{"type": "run_progress", "key": "started", "count": 1},
		{"type": "run_progress", "key": "searching_web", "count": 1},
		{"type": "run_progress", "key": "item_saved", "count": 1},
		{"type": "run_progress", "key": "item_saved", "count": 2},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestProgressKeyComesFromProgressMap(t *testing.T) {
	reader := NewReader(map[string]string{"tool_a": "item_saved", "WebFetch": "reading_page"}, outcomes, resultIDKey, failureMessage)
	cases := map[string]string{
		"mcp__leadin__tool_a":        "item_saved",
		"WebFetch":                   "reading_page",
		"WebSearch":                  "working",
		"mcp__leadin__something_new": "working",
		// Only the tools of our MCP server: the same name on another server is unknown.
		"mcp__other__tool_a": "working",
	}
	for name, want := range cases {
		if got := reader.ProgressKey(name); got != want {
			t.Errorf("ProgressKey(%q) = %q, want %q", name, got, want)
		}
	}
	if got := NewReader(nil, outcomes, resultIDKey, failureMessage).ProgressKey("mcp__leadin__tool_a"); got != "working" {
		t.Errorf("empty map: %q", got)
	}
}

func TestProgressNeverContainsToolNamesInputsOrText(t *testing.T) {
	reader := newReader()
	events := reader.Feed(assistantLine(t, "Claude's own text", "mcp__leadin__store_item", "mcp__evil__x"))
	want := []map[string]any{
		{"type": "run_progress", "key": "item_saved", "count": 1},
		{"type": "run_progress", "key": "working", "count": 1},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v", events)
	}
	// The log gets the same neutral keys, never the names.
	if !reflect.DeepEqual(reader.LastProgress, []string{"item_saved", "working"}) {
		t.Fatalf("LastProgress = %v", reader.LastProgress)
	}
	all := feedAll(newReader(), readFixture(t, "run_basic.jsonl"))
	all = append(all, events...)
	text := serialize(t, all)
	for _, leak := range []string{"store_item", "start_example", "Stored.", "SECRET", "Claude's own", "TEXT", "WebSearch"} {
		if strings.Contains(text, leak) {
			t.Errorf("%q leaked: %s", leak, text)
		}
	}
}

func TestCountsGrowByOnePerKey(t *testing.T) {
	events := feedAll(newReader(), readFixture(t, "run_subagents.jsonl"))
	seen := map[string]int{}
	for _, event := range events {
		key := event["key"].(string)
		seen[key]++
		if event["count"] != seen[key] {
			t.Fatalf("event %v, want count %d", event, seen[key])
		}
	}
}

func TestFinalLineJSONIsParsed(t *testing.T) {
	event := finalFor(t, "All stored.\n\n{\"outcome\": \"done\", \"message\": \"All done.\", \"item_id\": 12}")
	if event["type"] != "run_finished" || event["outcome"] != "done" || event["message"] != "All done." || asString(event["result_id"]) != "12" {
		t.Fatalf("event = %v", event)
	}
	if _, ok := event["item_id"]; ok {
		t.Fatal("item_id in the event")
	}
}

func TestFinalLineInCodeFenceIsParsed(t *testing.T) {
	fenced := "```json\n{\"outcome\": \"skipped\", \"message\": \"Already there.\", \"item_id\": 3}\n```\n"
	final, ok := ParseFinalLine(fenced, outcomes, resultIDKey)
	if !ok || final["outcome"] != "skipped" || final["message"] != "Already there." || asString(final["result_id"]) != "3" {
		t.Fatalf("final = %v, %v", final, ok)
	}
	inline := "```{\"outcome\": \"skipped\", \"message\": \"Paste the text.\", \"item_id\": null}```"
	final, ok = ParseFinalLine(inline, outcomes, resultIDKey)
	if !ok || final["outcome"] != "skipped" || final["result_id"] != nil {
		t.Fatalf("inline final = %v, %v", final, ok)
	}
	crlf := "Done.\r\n{\"outcome\": \"done\", \"message\": \"ok\"}\r\n"
	if final, ok := ParseFinalLine(crlf, outcomes, resultIDKey); !ok || final["outcome"] != "done" {
		t.Fatalf("CRLF final = %v, %v", final, ok)
	}
}

func TestInvalidFinalLineFallsBackToFailureMessage(t *testing.T) {
	for _, text := range []string{
		"Tell me which time to use.",
		"{\"outcome\": \"done\", \"message\": \"ok\", \"item_id\": 1}\nAnd then a question?",
		"{\"outcome\": \"done\", \"message\": \"ok\", \"item_id\": \"12\"}",
		"{\"outcome\": \"done\", \"message\": \"ok\", \"item_id\": true}",
		"{\"outcome\": \"done\", \"message\": \"ok\", \"item_id\": 1.5}",
		"{\"outcome\": \"done\", \"message\": 5, \"item_id\": 1}",
		"{\"outcome\": [\"done\"], \"message\": \"ok\"}",
		"{\"outcome\": \"done\", \"message\": \"ok\"} trailing",
		"[1, 2]",
		"",
	} {
		event := finalFor(t, text)
		if event["type"] != "run_finished" || event["outcome"] != "failed" || event["message"] != failureMessage || event["result_id"] != nil {
			t.Errorf("%q: event = %v", text, event)
		}
		if event["error_code"] != InvalidFinalLine || event["final_message"] != strings.TrimSpace(text) {
			t.Errorf("%q: error_code %q, final_message %q", text, event["error_code"], event["final_message"])
		}
	}
	long, _ := json.Marshal(map[string]any{"outcome": "done", "message": strings.Repeat("x", 501), "item_id": 1})
	if _, ok := ParseFinalLine(string(long), outcomes, resultIDKey); ok {
		t.Error("a message of 501 characters is valid")
	}
	// Characters, not bytes: 500 accented letters are fine.
	accented, _ := json.Marshal(map[string]any{"outcome": "done", "message": strings.Repeat("è", 500)})
	if _, ok := ParseFinalLine(string(accented), outcomes, resultIDKey); !ok {
		t.Error("a message of 500 accented letters is not valid")
	}
}

func TestOutcomeOutsideSpecFallsBackToFailureMessage(t *testing.T) {
	for _, outcome := range []string{"cancelled", "completed", "DONE"} {
		line, _ := json.Marshal(map[string]any{"outcome": outcome, "message": "ok", "item_id": 1})
		event := finalFor(t, string(line))
		if event["outcome"] != "failed" || event["message"] != failureMessage || event["result_id"] != nil {
			t.Errorf("%s: event = %v", outcome, event)
		}
	}
}

func TestResultIDComesFromSpecKey(t *testing.T) {
	// A last line with another key, but the spec says item_id: no id.
	event := finalFor(t, `{"outcome": "done", "message": "ok", "other_id": 5}`)
	if event["outcome"] != "done" || event["result_id"] != nil {
		t.Fatalf("event = %v", event)
	}
	if _, ok := event["other_id"]; ok {
		t.Fatal("other_id in the event")
	}
	reader := NewReader(progressMap, outcomes, "other_id", failureMessage)
	reader.Feed(resultLine(t, `{"outcome": "done", "message": "ok", "other_id": 5}`, nil))
	if got := asString(reader.FinalEvent(nil, nil)["result_id"]); got != "5" {
		t.Fatalf("result_id = %s", got)
	}
}

func TestMCPFailedInitReportsUnauthorized(t *testing.T) {
	reader := newReader()
	events := feedAll(reader, readFixture(t, "run_mcp_failed.jsonl"))
	if !reader.MCPUnauthorized || len(events) != 0 {
		t.Fatalf("unauthorized %v, events %v", reader.MCPUnauthorized, events)
	}
	event := reader.FinalEvent(ptr("2.1.300"), ptr("pro"))
	if event["type"] != "run_failed" || event["code"] != "mcp_unauthorized" || event["claude_version"] != "2.1.300" {
		t.Fatalf("event = %v", event)
	}
	// Claude's text («I cannot…») does not go out.
	assertKeys(t, event, "type", "code", "helper_version", "claude_version", "subscription_type")
}

func TestNeedsAuthAndMissingServerAreUnauthorized(t *testing.T) {
	for _, servers := range []string{
		`[{"name": "leadin", "status": "needs-auth"}]`,
		`[]`,
		`[{"name": "x", "status": "connected"}]`,
	} {
		reader := newReader()
		reader.Feed([]byte(`{"type": "system", "subtype": "init", "mcp_servers": ` + servers + `}`))
		if !reader.MCPUnauthorized {
			t.Errorf("%s: authorized", servers)
		}
	}
	reader := newReader()
	reader.Feed([]byte(`{"type": "system", "subtype": "init", "mcp_servers": [{"name": "leadin", "status": "connected"}]}`))
	if reader.MCPUnauthorized {
		t.Error("connected: unauthorized")
	}
}

func TestMissingResultReportsUnknown(t *testing.T) {
	reader := newReader()
	feedAll(reader, [][]byte{assistantLine(t, "", "WebSearch"), []byte("not json"), []byte(""), []byte("[1]")})
	event := reader.FinalEvent(ptr("2.1.300"), nil)
	if event["type"] != "run_failed" || event["code"] != "unknown" || event["subscription_type"] != nil {
		t.Fatalf("event = %v", event)
	}
}

func TestUsageLimitResultBecomesRunFailedWithMetrics(t *testing.T) {
	reader := newReader()
	feedAll(reader, readFixture(t, "run_usage_limit.jsonl"))
	result := reader.Result
	event := reader.FinalEvent(ptr("2.1.300"), ptr("max"))
	if event["type"] != "run_failed" || event["code"] != "usage_limit" {
		t.Fatalf("event = %v", event)
	}
	if event["cost_usd"] != result["total_cost_usd"] || asString(event["cost_usd"]) == "0" {
		t.Errorf("cost_usd = %v", event["cost_usd"])
	}
	if event["duration_ms"] != result["duration_ms"] || event["num_turns"] != result["num_turns"] {
		t.Errorf("duration %v, turns %v", event["duration_ms"], event["num_turns"])
	}
	if !reflect.DeepEqual(event["usage"], map[string]any{"usage": result["usage"], "modelUsage": result["modelUsage"]}) {
		t.Errorf("usage = %v", event["usage"])
	}
	if event["claude_version"] != "2.1.300" || event["subscription_type"] != "max" {
		t.Errorf("versions = %v, %v", event["claude_version"], event["subscription_type"])
	}
	assertKeys(t, event, "type", "code", "duration_ms", "num_turns", "cost_usd", "usage", "final_message", "helper_version", "claude_version", "subscription_type")
}

func TestUsageLimitTextsAreRecognized(t *testing.T) {
	for _, text := range []string{
		"You've hit your session limit · resets 3pm (Europe/Rome)",
		"You’ve hit your weekly limit · resets Oct 9, 10am",
		"You're out of usage credits. Switch to another model to continue.",
		"Claude AI usage limit reached|1791200400",
		"API Error: Rate limit exceeded",
	} {
		if got := ClassifyError(map[string]any{"is_error": true, "result": text}); got != "usage_limit" {
			t.Errorf("%q: %s", text, got)
		}
	}
	if got := ClassifyError(map[string]any{"is_error": true, "result": "x", "api_error_status": json.Number("429")}); got != "usage_limit" {
		t.Errorf("status 429: %s", got)
	}
}

func TestAPIErrorResultBecomesRunFailed(t *testing.T) {
	reader := newReader()
	feedAll(reader, readFixture(t, "run_api_error.jsonl"))
	event := reader.FinalEvent(ptr("2.1.300"), ptr("pro"))
	if event["type"] != "run_failed" || event["code"] != "api_error" || event["num_turns"] != reader.Result["num_turns"] {
		t.Fatalf("event = %v", event)
	}
	for _, result := range []map[string]any{
		{"result": "API Error: 500 Internal server error"},
		{"result": "Overloaded, try again later"},
		// Verified on 2.1.291: a model that does not exist, without «API Error».
		{"result": "There's an issue with the selected model (x).", "terminal_reason": "api_error", "api_error_status": json.Number("404")},
		{"result": "x", "api_error_status": json.Number("500")},
	} {
		result["is_error"] = true
		if got := ClassifyError(result); got != "api_error" {
			t.Errorf("%v: %s", result, got)
		}
	}
}

func TestUnknownErrorResultBecomesRunFailedUnknown(t *testing.T) {
	for _, fields := range []map[string]any{
		{"is_error": true, "result": "Something went wrong."},
		{"is_error": true, "result": nil},
		{"is_error": false, "subtype": "error_max_turns", "result": "Half done."},
		{"subtype": "error_during_execution"},
		{"api_error_status": 4.5, "is_error": true},
	} {
		reader := newReader()
		reader.Feed(resultLine(t, "", fields))
		event := reader.FinalEvent(ptr("2.1.300"), ptr("pro"))
		if event["type"] != "run_failed" || event["code"] != "unknown" || asString(event["cost_usd"]) != "0.5" {
			t.Errorf("%v: event = %v", fields, event)
		}
	}
	// is_error wins, even with a valid last line.
	reader := newReader()
	reader.Feed(resultLine(t, `{"outcome": "done", "message": "ok", "item_id": 1}`, map[string]any{"is_error": true}))
	if got := reader.FinalEvent(nil, nil)["code"]; got != "unknown" {
		t.Fatalf("code = %v", got)
	}
}

func TestErrorTextLeavesTheHelperOnlyAsFinalMessage(t *testing.T) {
	for _, name := range []string{"run_usage_limit.jsonl", "run_api_error.jsonl"} {
		reader := newReader()
		events := feedAll(reader, readFixture(t, name))
		text := reader.Result["result"].(string)
		final := reader.FinalEvent(ptr("2.1.300"), ptr("pro"))
		if final["final_message"] != strings.TrimSpace(text) {
			t.Errorf("%s: final_message = %q", name, final["final_message"])
		}
		others := map[string]any{}
		for key, value := range final {
			if key != "final_message" {
				others[key] = value
			}
		}
		serialized := serialize(t, append(events, others))
		if strings.Contains(serialized, text[:15]) {
			t.Errorf("%s: error text leaked: %s", name, serialized)
		}
	}
}

func TestFinalMessageKeepsOnlyTheEndOfTheText(t *testing.T) {
	reader := newReader()
	reader.Feed(resultLine(t, strings.Repeat("è", 600)+" end  ", map[string]any{"is_error": true}))
	final := reader.FinalEvent(nil, nil)["final_message"].(string)
	// Characters, not bytes.
	if utf8.RuneCountInString(final) != 500 || !strings.HasSuffix(final, "è end") {
		t.Fatalf("final_message = %q (%d characters)", final, utf8.RuneCountInString(final))
	}
}

func TestResultCostAndUsageAreForwarded(t *testing.T) {
	reader := newReader()
	feedAll(reader, readFixture(t, "run_basic.jsonl"))
	result := reader.Result
	event := reader.FinalEvent(ptr("2.1.300"), ptr("pro"))
	if event["type"] != "run_finished" || event["outcome"] != "done" || event["message"] != "Stored." || asString(event["result_id"]) != "42" {
		t.Fatalf("event = %v", event)
	}
	if event["cost_usd"] != result["total_cost_usd"] || event["duration_ms"] != result["duration_ms"] || event["num_turns"] != result["num_turns"] {
		t.Fatalf("metrics = %v", event)
	}
	usage := event["usage"].(map[string]any)
	// No subagents: subagent_stats (spawned 0) stays out.
	if !reflect.DeepEqual(usage, map[string]any{"usage": result["usage"], "modelUsage": result["modelUsage"]}) {
		t.Fatalf("usage = %v", usage)
	}
	if _, ok := usage["usage"].(map[string]any)["server_tool_use"]; !ok {
		t.Fatal("server_tool_use missing")
	}
	if event["helper_version"] != version.Version || event["subscription_type"] != "pro" {
		t.Fatalf("event = %v", event)
	}
	assertKeys(t, event, "type", "outcome", "message", "result_id", "error_code", "final_message", "duration_ms", "num_turns", "cost_usd", "usage", "helper_version", "claude_version", "subscription_type")
	if event["error_code"] != "" || event["final_message"] != "Stored." {
		t.Fatalf("error_code %q, final_message %q", event["error_code"], event["final_message"])
	}
	// Numbers keep their JSON form.
	if !strings.Contains(serialize(t, event), `"cost_usd":0.0842`) {
		t.Fatalf("serialized = %s", serialize(t, event))
	}
}

func TestLongLinesAreRead(t *testing.T) {
	// A stream line can be several MB: Feed takes whole lines of any size.
	reader := newReader()
	reader.Feed(assistantLine(t, strings.Repeat("x", 3<<20), "mcp__leadin__store_item"))
	if reader.Counts["item_saved"] != 1 {
		t.Fatalf("counts = %v", reader.Counts)
	}
}

// Subagents: the stream of run_subagents.jsonl, with two background workers.

func subagentReader(t *testing.T) (*Reader, []map[string]any, [][]byte) {
	t.Helper()
	lines := readFixture(t, "run_subagents.jsonl")
	reader := newReader()
	return reader, feedAll(reader, lines), lines
}

func subagentToolCount(t *testing.T, lines [][]byte, name string) int {
	t.Helper()
	count := 0
	for _, line := range lines {
		data := decode(t, string(line))
		if data["type"] != "assistant" || data["parent_tool_use_id"] == nil {
			continue
		}
		for _, item := range data["message"].(map[string]any)["content"].([]any) {
			if block := item.(map[string]any); block["type"] == "tool_use" && block["name"] == name {
				count++
			}
		}
	}
	return count
}

func TestSubagentToolCallsAreCounted(t *testing.T) {
	reader, events, lines := subagentReader(t)
	all := 0
	for _, line := range lines {
		data := decode(t, string(line))
		if data["type"] != "assistant" {
			continue
		}
		for _, item := range data["message"].(map[string]any)["content"].([]any) {
			if item.(map[string]any)["type"] == "tool_use" {
				all++
			}
		}
	}
	// Every tool_use of the stream, subagents or not, gives one event.
	if len(events) != all {
		t.Fatalf("%d events for %d tool calls", len(events), all)
	}
	if reader.Counts["group_started"] != 2 || reader.Counts["group_started"] != subagentToolCount(t, lines, "mcp__leadin__start_group") {
		t.Fatalf("group_started = %d", reader.Counts["group_started"])
	}
	if reader.Counts["item_saved"] != subagentToolCount(t, lines, "mcp__leadin__store_item") {
		t.Fatalf("item_saved = %d", reader.Counts["item_saved"])
	}
}

func TestGroupDoneCountsEveryGroup(t *testing.T) {
	_, events, _ := subagentReader(t)
	var done []any
	keys := []string{}
	for _, event := range events {
		keys = append(keys, event["key"].(string))
		if event["key"] == "group_done" {
			done = append(done, event["count"])
		}
	}
	// Three groups in the first worker, two in the second.
	if !reflect.DeepEqual(done, []any{1, 2, 3, 4, 5}) {
		t.Fatalf("group_done counts = %v", done)
	}
	if keys[0] != "started" || keys[len(keys)-2] != "summary_saved" || keys[len(keys)-1] != "completing" {
		t.Fatalf("keys = %v", keys)
	}
}

func TestAgentToolWithoutKeyIsWorking(t *testing.T) {
	reader := newReader()
	for _, name := range []string{"Agent", "Task"} {
		if got := reader.ProgressKey(name); got != "working" {
			t.Errorf("%s: %s", name, got)
		}
	}
	events := reader.Feed(assistantLine(t, "", "Agent"))
	if !reflect.DeepEqual(events, []map[string]any{{"type": "run_progress", "key": "working", "count": 1}}) {
		t.Fatalf("events = %v", events)
	}
	subagents, _, _ := subagentReader(t)
	if subagents.Counts["working"] != 2 {
		t.Fatalf("working = %d", subagents.Counts["working"])
	}
}

func TestFinalResultIsMainAgentResult(t *testing.T) {
	reader, _, lines := subagentReader(t)
	var results []map[string]any
	for _, line := range lines {
		if data := decode(t, string(line)); data["type"] == "result" {
			results = append(results, data)
		}
	}
	if len(results) != 3 || reader.Results != 3 {
		t.Fatalf("%d results, reader %d", len(results), reader.Results)
	}
	last := results[2]
	event := reader.FinalEvent(ptr("2.1.300"), ptr("pro"))
	if event["type"] != "run_finished" || event["outcome"] != "done" || asString(event["result_id"]) != "42" {
		t.Fatalf("event = %v", event)
	}
	usage := event["usage"].(map[string]any)
	if event["cost_usd"] != last["total_cost_usd"] || !reflect.DeepEqual(usage["modelUsage"], last["modelUsage"]) || !reflect.DeepEqual(usage["subagent_stats"], last["subagent_stats"]) {
		t.Fatalf("event = %v", event)
	}
	// A subagent's result, should one arrive, does not count.
	reader = newReader()
	feedAll(reader, append(lines, resultLine(t, "Subagent text", map[string]any{"parent_tool_use_id": "toolu_x"})))
	if reader.Results != 3 || asString(reader.FinalEvent(nil, nil)["result_id"]) != "42" {
		t.Fatalf("results = %d", reader.Results)
	}
}

func TestMetricsSumOverAllMainAgentResults(t *testing.T) {
	reader, _, lines := subagentReader(t)
	event := reader.FinalEvent(ptr("2.1.300"), ptr("pro"))
	// 6458 + 3537 + 2233 ms, 4 + 2 + 1 turns.
	if event["duration_ms"] != int64(12228) || event["num_turns"] != int64(7) {
		t.Fatalf("duration %v, turns %v", event["duration_ms"], event["num_turns"])
	}
	// Even when the last turn ends with an error.
	reader = newReader()
	feedAll(reader, append(lines[:len(lines)-1:len(lines)-1], resultLine(t, "API Error: 500", map[string]any{"is_error": true, "duration_ms": 100, "num_turns": 1})))
	failed := reader.FinalEvent(nil, nil)
	if failed["type"] != "run_failed" || failed["code"] != "api_error" || failed["num_turns"] != int64(7) || failed["duration_ms"] != int64(10095) {
		t.Fatalf("failed = %v", failed)
	}
	// Without subagents usage stays {usage, modelUsage}.
	reader = newReader()
	reader.Feed(resultLine(t, "x", map[string]any{"subagent_stats": map[string]any{"spawned": 0}}))
	if usage := reader.FinalEvent(nil, nil)["usage"].(map[string]any); len(usage) != 2 {
		t.Fatalf("usage = %v", usage)
	}
}

func TestSubagentTextNeverReachesEvents(t *testing.T) {
	reader, events, lines := subagentReader(t)
	serialized := serialize(t, append(events, reader.FinalEvent(nil, nil)))
	for _, marker := range []string{"TEXT", "Group 1", "/tmp/", "Async agent", "example-worker", "start_group"} {
		if strings.Contains(serialized, marker) {
			t.Errorf("%q leaked", marker)
		}
	}
	for _, event := range events {
		assertKeys(t, event, "type", "key", "count")
	}
	// The log gets only the neutral keys of the events.
	logged := []string{}
	reader = newReader()
	for _, line := range lines {
		reader.Feed(line)
		logged = append(logged, reader.LastProgress...)
	}
	keys := []string{}
	for _, event := range events {
		keys = append(keys, event["key"].(string))
	}
	if !reflect.DeepEqual(logged, keys) {
		t.Fatalf("logged %v, events %v", logged, keys)
	}
}
