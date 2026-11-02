package spec

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

const server = "http://127.0.0.1:8000"

const testToken = "tok_" + "example" + "0123456789abcdef"

// makeSpec is a valid spec for serverURL, made of the made-up values of the
// tests (the helper receives them, it does not know them), with the changes
// of edit applied to the decoded JSON. With agents it has a subagent too.
func makeSpec(t *testing.T, serverURL string, agents bool, edit func(map[string]any)) map[string]any {
	t.Helper()
	data := map[string]any{
		"run_id":    7,
		"mcp_url":   serverURL + "/mcp",
		"mcp_token": testToken,
		"model":     "sonnet",
		"prompt":    "Call `start_example` and follow the instructions it returns.\n\nhttps://example.com/item",
		"tools":     []string{"WebSearch", "WebFetch"},
		"progress_map": map[string]string{
			"start_example": "started",
			"store_item":    "item_saved",
			"WebSearch":     "searching_web",
		},
		"outcomes":        []string{"done", "skipped", "failed"},
		"result_id_key":   "item_id",
		"failure_message": "The run did not finish correctly.",
	}
	if agents {
		data["tools"] = []string{"WebSearch", "WebFetch", "Agent"}
		data["agents"] = map[string]any{
			"example-worker": map[string]any{
				"description": "A made-up subagent.",
				"prompt":      "Call `start_group` first and follow the instructions it returns.",
				"tools":       []string{"WebSearch", "WebFetch", "mcp__leadin__*"},
			},
		}
	}
	// Through JSON, so values are what Redeem would return.
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded map[string]any
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(decoded)
	}
	return decoded
}

func set(key string, value any) func(map[string]any) {
	return func(data map[string]any) { data[key] = value }
}

func worker(data map[string]any) map[string]any {
	return data["agents"].(map[string]any)["example-worker"].(map[string]any)
}

func validate(t *testing.T, data map[string]any) (RunSpec, bool) {
	t.Helper()
	return Validate(data, server, 7)
}

func assertRejected(t *testing.T, name string, data map[string]any) {
	t.Helper()
	if _, ok := validate(t, data); ok {
		t.Errorf("%s: accepted", name)
	}
}

func TestValidSpecWithoutSubagents(t *testing.T) {
	spec, ok := validate(t, makeSpec(t, server, false, nil))
	if !ok {
		t.Fatal("rejected")
	}
	if spec.RunID != 7 || spec.MCPURL != server+"/mcp" || spec.MCPToken != testToken || spec.Model != "sonnet" {
		t.Fatalf("spec = %+v", spec)
	}
	if strings.Join(spec.Tools, ",") != "WebSearch,WebFetch" || len(spec.Agents) != 0 || spec.ProgressMap["store_item"] != "item_saved" {
		t.Fatalf("spec = %+v", spec)
	}
	if strings.Join(spec.Outcomes, ",") != "done,skipped,failed" || spec.ResultIDKey != "item_id" || spec.FailureMessage != "The run did not finish correctly." {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestValidSpecWithSubagents(t *testing.T) {
	spec, ok := validate(t, makeSpec(t, server, true, nil))
	if !ok {
		t.Fatal("rejected")
	}
	agent, found := spec.Agents["example-worker"]
	if !found || len(spec.Agents) != 1 || strings.Join(agent.Tools, ",") != "WebSearch,WebFetch,mcp__leadin__*" || agent.Model != "" {
		t.Fatalf("agents = %+v", spec.Agents)
	}
	withModel := makeSpec(t, server, true, func(data map[string]any) { worker(data)["model"] = "haiku" })
	if spec, ok := validate(t, withModel); !ok || spec.Agents["example-worker"].Model != "haiku" {
		t.Fatalf("subagent model: %+v, %v", spec.Agents, ok)
	}
}

func TestSpecMayLeaveOutProgressMapAndHaveNoTools(t *testing.T) {
	data := makeSpec(t, server, false, set("tools", []any{}))
	delete(data, "progress_map")
	if spec, ok := validate(t, data); !ok || len(spec.ProgressMap) != 0 || len(spec.Tools) != 0 {
		t.Fatalf("spec = %+v, %v", spec, ok)
	}
}

func TestUnknownFieldsAreIgnored(t *testing.T) {
	if _, ok := validate(t, makeSpec(t, server, false, set("new_field", "x"))); !ok {
		t.Fatal("rejected")
	}
}

func TestStringNeverContainsTokenOrPrompt(t *testing.T) {
	spec, _ := validate(t, makeSpec(t, server, false, nil))
	text := spec.String()
	if strings.Contains(text, testToken) || strings.Contains(text, "example.com") || !strings.Contains(text, "RunID: 7") {
		t.Fatalf("String = %q", text)
	}
}

func TestSpecForAnotherRunIsRejected(t *testing.T) {
	assertRejected(t, "another run", makeSpec(t, server, false, set("run_id", json.Number("8"))))
	assertRejected(t, "run_id 7.0", makeSpec(t, server, false, set("run_id", json.Number("7.0"))))
	assertRejected(t, "run_id true", makeSpec(t, server, false, set("run_id", true)))
	assertRejected(t, "run_id string", makeSpec(t, server, false, set("run_id", "7")))
	assertRejected(t, "nil spec", nil)
}

func TestSpecWithUnknownModelIsRejected(t *testing.T) {
	assertRejected(t, "unknown model", makeSpec(t, server, false, set("model", "claude-opus-x")))
	assertRejected(t, "model not a string", makeSpec(t, server, false, set("model", json.Number("1"))))
}

func TestSpecWithBashIsRejected(t *testing.T) {
	assertRejected(t, "Bash", makeSpec(t, server, false, set("tools", []any{"WebSearch", "Bash"})))
	assertRejected(t, "Read", makeSpec(t, server, false, set("tools", []any{"Read"})))
	assertRejected(t, "MCP tool among built-ins", makeSpec(t, server, false, set("tools", []any{"mcp__leadin__*"})))
}

func TestSpecWithDuplicatedOrMalformedToolsIsRejected(t *testing.T) {
	assertRejected(t, "duplicated", makeSpec(t, server, false, set("tools", []any{"WebSearch", "WebSearch"})))
	assertRejected(t, "not a list", makeSpec(t, server, false, set("tools", "WebSearch")))
	assertRejected(t, "not strings", makeSpec(t, server, false, set("tools", []any{json.Number("1")})))
	data := makeSpec(t, server, false, nil)
	delete(data, "tools")
	assertRejected(t, "missing", data)
}

func TestSpecWithMCPOnOtherOriginIsRejected(t *testing.T) {
	for name, url := range map[string]string{
		"another host":   "https://evil.example/mcp",
		"another port":   "http://127.0.0.1:8001/mcp",
		"another scheme": "https://127.0.0.1:8000/mcp",
		"not http":       "file:///etc/passwd",
		"with user":      "http://user@127.0.0.1:8000/mcp",
		"no host":        "http:///mcp",
	} {
		assertRejected(t, name, makeSpec(t, server, false, set("mcp_url", url)))
	}
	// Host case and the default port do not matter.
	if _, ok := Validate(makeSpec(t, "https://LeadIn.example", false, set("mcp_url", "https://leadin.example:443/mcp")), "https://leadin.example", 7); !ok {
		t.Fatal("same origin rejected")
	}
}

func TestSpecWithPlainHTTPRemoteMCPIsRejected(t *testing.T) {
	// The server URL is checked before redeeming; the MCP URL must share its
	// origin, so a plain http MCP URL can only be on this computer.
	data := makeSpec(t, "http://leadin.example", false, nil)
	if _, ok := Validate(data, "https://leadin.example", 7); ok {
		t.Fatal("plain http MCP accepted for an https server")
	}
	if IsValidServerURL("http://leadin.example") {
		t.Fatal("plain http server URL accepted")
	}
}

func TestSpecWithBadTokenIsRejected(t *testing.T) {
	assertRejected(t, "empty token", makeSpec(t, server, false, set("mcp_token", "")))
	assertRejected(t, "token with newline", makeSpec(t, server, false, set("mcp_token", "abc\nX-Evil: 1")))
	assertRejected(t, "token with space", makeSpec(t, server, false, set("mcp_token", "abc def")))
	assertRejected(t, "long token", makeSpec(t, server, false, set("mcp_token", strings.Repeat("a", 513))))
	if _, ok := validate(t, makeSpec(t, server, false, set("mcp_token", strings.Repeat("a", 512)))); !ok {
		t.Fatal("512-character token rejected")
	}
}

func TestBlankPromptIsRejected(t *testing.T) {
	assertRejected(t, "blank prompt", makeSpec(t, server, false, set("prompt", "  \n")))
	assertRejected(t, "prompt not a string", makeSpec(t, server, false, set("prompt", nil)))
}

func TestPromptOverLimitIsRejected(t *testing.T) {
	assertRejected(t, "long prompt", makeSpec(t, server, false, set("prompt", strings.Repeat("x", MaxPrompt+1))))
	if _, ok := validate(t, makeSpec(t, server, false, set("prompt", strings.Repeat("x", MaxPrompt)))); !ok {
		t.Fatal("prompt at the limit rejected")
	}
	// Characters, not bytes.
	if _, ok := validate(t, makeSpec(t, server, false, set("prompt", strings.Repeat("è", MaxPrompt)))); !ok {
		t.Fatal("accented prompt at the limit rejected")
	}
}

func TestSubagentWithAgentToolIsRejected(t *testing.T) {
	assertRejected(t, "Agent", makeSpec(t, server, true, func(data map[string]any) {
		worker(data)["tools"] = []any{"WebSearch", "Agent"}
	}))
}

func TestSubagentWithBashOrAnotherMCPIsRejected(t *testing.T) {
	for name, tools := range map[string][]any{
		"Bash":        {"WebSearch", "Bash"},
		"another MCP": {"mcp__other__*"},
		"bad MCP":     {"mcp__leadin__a-b"},
		"empty":       {},
		"not strings": {json.Number("1")},
	} {
		assertRejected(t, name, makeSpec(t, server, true, func(data map[string]any) { worker(data)["tools"] = tools }))
	}
	assertRejected(t, "no tools", makeSpec(t, server, true, func(data map[string]any) { delete(worker(data), "tools") }))
}

func TestSubagentWithExtraFieldsIsRejected(t *testing.T) {
	for _, field := range []string{"mcpServers", "hooks", "permissionMode"} {
		assertRejected(t, field, makeSpec(t, server, true, func(data map[string]any) { worker(data)[field] = map[string]any{} }))
	}
}

func TestSubagentWithUnknownModelIsRejected(t *testing.T) {
	assertRejected(t, "unknown model", makeSpec(t, server, true, func(data map[string]any) { worker(data)["model"] = "claude-opus-x" }))
	assertRejected(t, "null model", makeSpec(t, server, true, func(data map[string]any) { worker(data)["model"] = nil }))
}

func TestSubagentWithBadTextIsRejected(t *testing.T) {
	for _, field := range []string{"description", "prompt"} {
		assertRejected(t, "blank "+field, makeSpec(t, server, true, func(data map[string]any) { worker(data)[field] = " " }))
		assertRejected(t, "long "+field, makeSpec(t, server, true, func(data map[string]any) {
			worker(data)[field] = strings.Repeat("x", MaxAgentText+1)
		}))
		assertRejected(t, "missing "+field, makeSpec(t, server, true, func(data map[string]any) { delete(worker(data), field) }))
	}
}

func TestSubagentWithBadNameIsRejected(t *testing.T) {
	for _, name := range []string{"../x", "", strings.Repeat("a", 65), "a b"} {
		assertRejected(t, name, makeSpec(t, server, true, func(data map[string]any) {
			data["agents"] = map[string]any{name: worker(data)}
		}))
	}
}

func TestTooManySubagentsAreRejected(t *testing.T) {
	assertRejected(t, "11 subagents", makeSpec(t, server, true, func(data map[string]any) {
		agents := map[string]any{}
		for i := 0; i <= MaxAgents; i++ {
			agents[strings.Repeat("a", i+1)] = worker(data)
		}
		data["agents"] = agents
	}))
	assertRejected(t, "agents not an object", makeSpec(t, server, true, set("agents", []any{"example-worker"})))
}

// The subagents go on the command line: their JSON must fit the Windows limit.
func TestSubagentsTooLongForTheCommandLineAreRejected(t *testing.T) {
	assertRejected(t, "8 long subagents", makeSpec(t, server, true, func(data map[string]any) {
		agents := map[string]any{}
		for i := 0; i < 8; i++ {
			agent := map[string]any{"description": "d", "prompt": strings.Repeat("x", 1_000), "tools": []any{"WebSearch"}}
			agents["agent_"+strconv.Itoa(i)] = agent
		}
		data["agents"] = agents
	}))
}

func TestAgentToolWithoutAgentsIsRejected(t *testing.T) {
	assertRejected(t, "Agent without agents", makeSpec(t, server, false, set("tools", []any{"WebSearch", "Agent"})))
	assertRejected(t, "Agent with empty agents", makeSpec(t, server, true, set("agents", map[string]any{})))
}

func TestAgentsWithoutAgentToolAreRejected(t *testing.T) {
	assertRejected(t, "agents without Agent", makeSpec(t, server, true, set("tools", []any{"WebSearch"})))
}

func TestProgressMapWithTextIsRejected(t *testing.T) {
	assertRejected(t, "text value", makeSpec(t, server, false, set("progress_map", map[string]any{"store_item": "Saving the item"})))
	assertRejected(t, "long key", makeSpec(t, server, false, set("progress_map", map[string]any{strings.Repeat("x", 65): "started"})))
	assertRejected(t, "value not a string", makeSpec(t, server, false, set("progress_map", map[string]any{"store_item": json.Number("1")})))
	assertRejected(t, "not an object", makeSpec(t, server, false, set("progress_map", []any{"store_item"})))
	assertRejected(t, "null", makeSpec(t, server, false, set("progress_map", nil)))
	assertRejected(t, "too many entries", makeSpec(t, server, false, func(data map[string]any) {
		entries := map[string]any{}
		for i := 0; i <= MaxProgressEntries; i++ {
			entries["tool_"+strconv.Itoa(i)] = "started"
		}
		data["progress_map"] = entries
	}))
}

func TestSpecWithInvalidOutcomesIsRejected(t *testing.T) {
	many := []any{}
	for i := 0; i < MaxOutcomes; i++ {
		many = append(many, "o"+string(rune('a'+i)))
	}
	cases := map[string]func(map[string]any){
		"empty":           set("outcomes", []any{}),
		"without failed":  set("outcomes", []any{"done", "skipped"}),
		"duplicated":      set("outcomes", []any{"done", "done", "failed"}),
		"too many":        set("outcomes", append(append([]any{}, many...), "failed")),
		"not a word":      set("outcomes", []any{"done!", "failed"}),
		"not strings":     set("outcomes", []any{json.Number("1"), "failed"}),
		"not a list":      set("outcomes", "failed"),
		"missing":         func(data map[string]any) { delete(data, "outcomes") },
		"key not a word":  set("result_id_key", "item id"),
		"key not str":     set("result_id_key", nil),
		"key missing":     func(data map[string]any) { delete(data, "result_id_key") },
		"blank message":   set("failure_message", " "),
		"long message":    set("failure_message", strings.Repeat("x", MaxMessage+1)),
		"message missing": func(data map[string]any) { delete(data, "failure_message") },
	}
	for name, edit := range cases {
		assertRejected(t, name, makeSpec(t, server, false, edit))
	}
	twenty := append(append([]any{}, many[:MaxOutcomes-1]...), "failed")
	if _, ok := validate(t, makeSpec(t, server, false, set("outcomes", twenty))); !ok {
		t.Fatal("20 outcomes rejected")
	}
	if _, ok := validate(t, makeSpec(t, server, false, set("failure_message", strings.Repeat("x", MaxMessage)))); !ok {
		t.Fatal("failure message at the limit rejected")
	}
}
