package spec

// The run spec can only narrow what the helper can do. Unknown fields are
// ignored, so the server can add new ones without breaking older helpers:
// none of them can change what the helper does, because every behaviour is
// driven by the fields checked here.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/G-DPP/leadin-helper/internal/claude"
)

// The model goes on the command line (`claude --model`), so only safe
// characters. The helper does not know which models exist: it checks the
// shape. An alias, or a full id `claude-…` (what the run's configuration
// picks on the server), lowercase, without `--`.
var ModelAliases = []string{"sonnet", "opus", "haiku"}

// MaxModel bounds a model id.
const MaxModel = 64

// Efforts are the levels of `claude --effort` and of a subagent's `effort`.
// Empty (or missing) = not passed, and Claude Code's own applies.
var Efforts = []string{"low", "medium", "high", "xhigh", "max"}

// BuiltinTools are the built-in tools a run can have: the two web tools and
// the subagent tool.
var BuiltinTools = []string{"WebSearch", "WebFetch", claude.SubagentTool}

// AgentTools are the built-in tools a subagent can have, besides the tools of
// the LeadIn MCP server (mcpTool). No Agent: a subagent never starts other
// subagents.
var AgentTools = []string{"WebSearch", "WebFetch"}

// AgentFields are the fields of a subagent in the `claude --agents` format.
// The others (hooks, MCP servers, permissions…) would widen the run.
var AgentFields = []string{"description", "prompt", "tools", "model", "effort"}

const (
	MaxAgents    = 10
	MaxAgentText = 10_000
	// MaxAgentsJSON bounds the subagents' JSON, which goes on the command
	// line (claude.BuildCommand): on Windows the whole line holds at most
	// 32,767 characters, counting the backslashes Windows adds before the
	// quotes. The contacts run uses fewer than 400.
	MaxAgentsJSON      = 8_000
	MaxPrompt          = 250_000
	MaxProgressEntries = 200
	MaxOutcomes        = 20
	// MaxMessage bounds the message of Claude's last line and failure_message.
	MaxMessage = 500
	// FailedOutcome must be among the outcomes: it is the only one the
	// helper uses by itself (package stream).
	FailedOutcome = "failed"
)

var (
	mcpTool   = regexp.MustCompile(`^mcp__` + claude.MCPServerName + `__(\*|[A-Za-z0-9_]+)$`)
	agentName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	modelID   = regexp.MustCompile(`^claude-[a-z0-9]+(-[a-z0-9]+)*$`)
	// word is a key or value of progress_map, an outcome or result_id_key:
	// tool names and neutral keys, never text.
	word = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
	// token goes into an HTTP header of mcp.json: visible characters only.
	token = regexp.MustCompile(`^[\x21-\x7e]{1,512}$`)
)

// Agent is one subagent, in the `claude --agents` format.
type Agent struct {
	Description string   `json:"description"`
	Prompt      string   `json:"prompt"`
	Tools       []string `json:"tools"`
	Model       string   `json:"model,omitempty"`
	Effort      string   `json:"effort,omitempty"`
}

// RunSpec is a checked run spec.
type RunSpec struct {
	RunID          int64
	MCPURL         string
	MCPToken       string
	Model          string
	Effort         string
	Prompt         string
	Tools          []string
	Agents         map[string]Agent
	ProgressMap    map[string]string
	Outcomes       []string
	ResultIDKey    string
	FailureMessage string
}

// String never shows the token or the prompt, not even in a panic.
func (s RunSpec) String() string {
	return fmt.Sprintf("RunSpec{RunID: %d, Model: %s, Tools: %v}", s.RunID, s.Model, s.Tools)
}

// Validate returns the run spec if data narrows what the helper can do.
func Validate(data map[string]any, serverURL string, runID int64) (RunSpec, bool) {
	if data == nil {
		return RunSpec{}, false
	}
	if id, ok := Int(data["run_id"]); !ok || id != runID {
		return RunSpec{}, false
	}

	mcpURL, ok := data["mcp_url"].(string)
	if !ok {
		return RunSpec{}, false
	}
	// The MCP server is on the origin the ticket was redeemed from.
	mcpOrigin, ok := parseOrigin(mcpURL)
	serverOrigin, serverOK := parseOrigin(serverURL)
	if !ok || !serverOK || mcpOrigin != serverOrigin {
		return RunSpec{}, false
	}

	mcpToken, ok := data["mcp_token"].(string)
	if !ok || !token.MatchString(mcpToken) {
		return RunSpec{}, false
	}
	model, ok := data["model"].(string)
	if !ok || !ValidModel(model) {
		return RunSpec{}, false
	}
	effort := ""
	if raw, present := data["effort"]; present {
		if effort, ok = raw.(string); !ok || (effort != "" && !slices.Contains(Efforts, effort)) {
			return RunSpec{}, false
		}
	}
	prompt, ok := data["prompt"].(string)
	if !ok || !validText(prompt, MaxPrompt) {
		return RunSpec{}, false
	}

	tools, ok := stringList(data["tools"])
	if !ok || !unique(tools) {
		return RunSpec{}, false
	}
	for _, tool := range tools {
		if !slices.Contains(BuiltinTools, tool) {
			return RunSpec{}, false
		}
	}

	agents, ok := validAgents(data)
	if !ok {
		return RunSpec{}, false
	}
	// Subagents only with their tool, and the tool only with subagents:
	// without --agents, Agent would start Claude Code's built-in ones.
	if (len(agents) > 0) != slices.Contains(tools, claude.SubagentTool) {
		return RunSpec{}, false
	}

	progressMap, ok := validProgressMap(data)
	if !ok {
		return RunSpec{}, false
	}

	outcomes, ok := stringList(data["outcomes"])
	if !ok || !validOutcomes(outcomes) {
		return RunSpec{}, false
	}
	resultIDKey, ok := data["result_id_key"].(string)
	if !ok || !word.MatchString(resultIDKey) {
		return RunSpec{}, false
	}
	failureMessage, ok := data["failure_message"].(string)
	if !ok || !validText(failureMessage, MaxMessage) {
		return RunSpec{}, false
	}

	return RunSpec{
		RunID:          runID,
		MCPURL:         mcpURL,
		MCPToken:       mcpToken,
		Model:          model,
		Effort:         effort,
		Prompt:         prompt,
		Tools:          tools,
		Agents:         agents,
		ProgressMap:    progressMap,
		Outcomes:       outcomes,
		ResultIDKey:    resultIDKey,
		FailureMessage: failureMessage,
	}, true
}

// Int is the value of a JSON integer decoded as json.Number: 7.0, 7e0, "7"
// and true are not integers.
func Int(value any) (int64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(string(number), 10, 64)
	return n, err == nil
}

// validText is true for a string that is not blank and has at most max
// characters (not bytes).
func validText(text string, max int) bool {
	return strings.TrimSpace(text) != "" && utf8.RuneCountInString(text) <= max
}

// stringList converts a JSON array of strings.
func stringList(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, text)
	}
	return out, true
}

func validAgents(data map[string]any) (map[string]Agent, bool) {
	agents := map[string]Agent{}
	raw, present := data["agents"]
	if !present {
		return agents, true
	}
	items, ok := raw.(map[string]any)
	if !ok || len(items) > MaxAgents {
		return nil, false
	}
	for name, value := range items {
		if !agentName.MatchString(name) {
			return nil, false
		}
		agent, ok := validAgent(value)
		if !ok {
			return nil, false
		}
		agents[name] = agent
	}
	encoded, err := claude.AgentsJSON(agents)
	if err != nil || utf8.RuneCountInString(encoded) > MaxAgentsJSON {
		return nil, false
	}
	return agents, true
}

// ValidModel is true for an alias (sonnet) or a `claude-…` id of at most
// MaxModel characters.
func ValidModel(value string) bool {
	return slices.Contains(ModelAliases, value) || (len(value) <= MaxModel && modelID.MatchString(value))
}

func validAgent(value any) (Agent, bool) {
	fields, ok := value.(map[string]any)
	if !ok {
		return Agent{}, false
	}
	for field := range fields {
		if !slices.Contains(AgentFields, field) {
			return Agent{}, false
		}
	}
	description, ok := fields["description"].(string)
	if !ok || !validText(description, MaxAgentText) {
		return Agent{}, false
	}
	prompt, ok := fields["prompt"].(string)
	if !ok || !validText(prompt, MaxAgentText) {
		return Agent{}, false
	}
	model := ""
	if raw, present := fields["model"]; present {
		if model, ok = raw.(string); !ok || !ValidModel(model) {
			return Agent{}, false
		}
	}
	effort := ""
	if raw, present := fields["effort"]; present {
		if effort, ok = raw.(string); !ok || !slices.Contains(Efforts, effort) {
			return Agent{}, false
		}
	}
	tools, ok := stringList(fields["tools"])
	if !ok || len(tools) == 0 {
		return Agent{}, false
	}
	for _, tool := range tools {
		if !slices.Contains(AgentTools, tool) && !mcpTool.MatchString(tool) {
			return Agent{}, false
		}
	}
	return Agent{Description: description, Prompt: prompt, Tools: tools, Model: model, Effort: effort}, true
}

func validProgressMap(data map[string]any) (map[string]string, bool) {
	progressMap := map[string]string{}
	raw, present := data["progress_map"]
	if !present {
		return progressMap, true
	}
	items, ok := raw.(map[string]any)
	if !ok || len(items) > MaxProgressEntries {
		return nil, false
	}
	for name, value := range items {
		key, ok := value.(string)
		if !ok || !word.MatchString(name) || !word.MatchString(key) {
			return nil, false
		}
		progressMap[name] = key
	}
	return progressMap, true
}

func validOutcomes(outcomes []string) bool {
	if len(outcomes) == 0 || len(outcomes) > MaxOutcomes || !unique(outcomes) {
		return false
	}
	for _, outcome := range outcomes {
		if !word.MatchString(outcome) {
			return false
		}
	}
	return slices.Contains(outcomes, FailedOutcome)
}

func unique(items []string) bool {
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if seen[item] {
			return false
		}
		seen[item] = true
	}
	return true
}
