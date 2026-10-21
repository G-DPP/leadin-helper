package claude

import "strings"

// MCPServerName is the name of the MCP server in mcp.json: it gives its
// tools the mcp__leadin__ prefix.
const MCPServerName = "leadin"

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
// With agentsConfig (the file of WriteAgentsConfig) --agents adds the
// subagents' definitions. The prompt is not here: it goes through stdin
// (--input-format stream-json), because anyone can read a command line.
func BuildCommand(path, model string, tools []string, mcpConfig, agentsConfig string) []string {
	allowed := append([]string{"mcp__" + MCPServerName}, tools...)
	command := []string{
		path,
		"-p",
		"--restricted",
		"--model", model,
		"--tools", strings.Join(tools, ","),
		"--allowedTools", strings.Join(allowed, " "),
	}
	if agentsConfig != "" {
		command = append(command, "--agents", agentsConfig)
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
