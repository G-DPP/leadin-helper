package claude

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// MCPServerName is the name of the MCP server in mcp.json: it gives its
// tools the mcp__leadin__ prefix.
const MCPServerName = "leadin"

// MaxMCPOutputTokens is the token limit of an MCP tool's answer, set in
// claude's environment (RunEnv). Claude Code's own (25,000) is too small for
// the answer of a rich run's first tool; the server uses the same value.
const MaxMCPOutputTokens = 200_000

// ReadTool lets Claude read the answers Claude Code saves to a file: past
// about 50,000 characters (a fixed limit of the CLI) an MCP tool's answer
// goes to <config>/projects/<run folder>/…/tool-results/ and Claude only gets
// the path. It is allowed only by the rules of ReadRules.
const ReadTool = "Read"

// SubagentTool is the tool that starts subagents. The stream's init event
// calls it Task, but tool_use blocks and flags write Agent (checked on
// Claude Code 2.1.291).
const SubagentTool = "Agent"

// BuildCommand is the `claude -p` command of a run: only the built-in tools
// of the spec and the LeadIn MCP server.
//
//   - --restricted ignores the user, project and local settings, and so
//     hooks, CLAUDE.md (of parent folders too), plugins, skills, agents,
//     output styles and env; managed settings and our --mcp-config and
//     --agents still apply. Not --setting-sources "", which gives the same
//     result with an undocumented value, nor --safe-mode, which also turns
//     off --mcp-config and --agents. It also drops WebFetch unless --tools
//     names it;
//   - --tools limits the built-in tools to those of the spec (at most the two
//     web tools and Agent, see spec.BuiltinTools): no Bash, no files;
//   - --allowedTools avoids permission prompts nobody would answer;
//   - --strict-mcp-config ignores the MCP servers the user has configured and
//     the claude.ai connectors, which --restricted alone lets through;
//   - --no-session-persistence leaves no transcript in ~/.claude/projects/.
//
// With readRules (ReadRules) Read joins --tools, and the rules, never Read
// alone, join --allowedTools: outside those folders Read would ask for a
// permission nobody gives in -p.
//
// With agents (the JSON of AgentsJSON) --agents adds the subagents'
// definitions on the command line, not in a file: Claude Code 2.1.278
// rejects a file path ("Invalid --agents configuration: invalid JSON", F0
// results on Windows). Anyone on the PC can read a command line, so the
// prompt is not here: it goes through stdin (--input-format stream-json). A
// subagent's prompt only points to the MCP tools, which give the
// instructions; spec.MaxAgentsJSON keeps the line under the Windows limit.
func BuildCommand(path, model string, tools []string, mcpConfig, agents string, readRules []string) []string {
	allowed := append(append([]string{"mcp__" + MCPServerName}, tools...), readRules...)
	builtin := append([]string{}, tools...)
	if len(readRules) > 0 {
		builtin = append(builtin, ReadTool)
	}
	command := []string{
		path,
		"-p",
		"--restricted",
		"--model", model,
		"--tools", strings.Join(builtin, ","),
		"--allowedTools", strings.Join(allowed, " "),
	}
	if agents != "" {
		command = append(command, "--agents", agents)
	}
	return append(command,
		"--mcp-config", mcpConfig,
		"--strict-mcp-config",
		"--no-session-persistence",
		"--output-format", "stream-json",
		"--verbose",
		"--input-format", "stream-json",
	)
}

// AgentsJSON is the value of --agents: the subagents of the spec as compact
// JSON, with <, > and & left as they are.
func AgentsJSON(agents any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(agents); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buffer.String(), "\n"), nil
}

// ReadRules are the --allowedTools rules that allow Read only in the project
// folders of workdir (ProjectDirs): on the person's PC ~ is their real home.
// ~/.claude because RunEnvironment drops CLAUDE_CONFIG_DIR; one rule per name,
// since on macOS the real path differs.
func ReadRules(workdir string) []string {
	rules := []string{}
	for _, name := range pathNames(workdir) {
		rules = append(rules, ReadTool+"(~/.claude/projects/"+name+"/**)")
	}
	return rules
}

// RunEnv is claude's environment for a run: env (RunEnvironment) plus the
// MCP answer limit.
func RunEnv(env []string) []string {
	return append(append([]string{}, env...), "MAX_MCP_OUTPUT_TOKENS="+strconv.Itoa(MaxMCPOutputTokens))
}
