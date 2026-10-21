# LeadIn helper

A small program that runs on your computer and lets the LeadIn website use
the Claude Code you have already installed.

## What it does

1. The LeadIn browser extension hands it a **ticket**: a random, single-use
   string that expires after a few seconds.
2. The helper sends the ticket to the LeadIn server and gets back the
   **instructions for one run**: which model, which prompt, which tools.
3. It starts your Claude Code with those instructions and with a fixed set
   of limits (below), which the server cannot change.
4. It tells the extension how the run is going (only neutral step names and
   counters) and how it ended.

**Status:** in development.

- **Linux:** verified, also with the LeadIn dashboard.
- **macOS:** builds and passes the tests, not yet tried with the dashboard.
- **Windows:** builds and passes `go vet`, but it has **not been run on
  Windows yet**. There Claude Code runs in a job object, so cancelling a run
  stops it with every process it started; the test of this
  (`internal/proc/proc_windows_test.go`) will run in the release CI.
- Automatic registration with the browser and signed-off releases are on
  the way. For now the helper is built from source.

The browser starts the helper through Chrome's
[Native Messaging](https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging).
The messages it accepts and sends are described in [PROTOCOL.md](PROTOCOL.md).

## What it cannot do

These limits are written in the code. Even if the LeadIn server asked for
something else, the helper would refuse: it never touches your files or your
terminal.

Each limit has a test, named in the last column. Unless another file is
given, it is in [`internal/run/constraints_test.go`](internal/run/constraints_test.go):
those tests run the whole helper with a fake server and a fake Claude Code,
and check the limit from the outside.

| Limit | Where in the code | Test |
|---|---|---|
| Claude Code starts only in a new, empty temporary folder (`leadin-run-…`), deleted at the end in every case. | [`internal/run/run.go`](internal/run/run.go) | `TestRunsInEmptyTemporaryDirectory`, `TestTemporaryDirectoryIsRemovedAfterSuccess`, `…AfterFailure`, `…AfterCancel`, `…AfterTimeout`, `…AfterPanic` |
| At most these tools: `WebSearch`, `WebFetch`, `Agent` and the LeadIn MCP server alone. Never files or a terminal (`Bash`, `Read`, `Write`, `Edit`…). The server can only narrow this list. | [`internal/spec/spec.go`](internal/spec/spec.go), [`internal/claude/command.go`](internal/claude/command.go) | `TestOnlyAllowedBuiltinToolsReachClaude`, `TestSpecWithBashIsRejected`, `TestSpecWithReadWriteEditIsRejected` |
| Subagents get at most `WebSearch`, `WebFetch` and the LeadIn MCP server, never `Agent`, and no fields beyond description, prompt, tools and model. | [`internal/spec/spec.go`](internal/spec/spec.go) | `TestSubagentToolsAreLimited`, `TestSubagentWithAgentToolIsRejected` |
| The model is `sonnet`, `opus` or `haiku`. | [`internal/spec/spec.go`](internal/spec/spec.go) | `TestInvalidSpecNeverStartsClaude` (in `run_test.go`) |
| Only the LeadIn MCP server, on the same address as the LeadIn server, over `https` (`http` only on this computer). | [`internal/spec/redeem.go`](internal/spec/redeem.go), [`internal/spec/spec.go`](internal/spec/spec.go) | `TestOnlyLeadinMCPServerIsConfigured`, `TestMCPOnOtherOriginIsRejected`, `TestPlainHTTPRemoteMCPIsRejected` |
| The ticket goes only to the LeadIn server the extension is configured for: redirects are not followed. | [`internal/spec/redeem.go`](internal/spec/redeem.go) | `TestRedeemDoesNotFollowRedirects` (in `internal/spec/redeem_test.go`) |
| The prompt and the token are never in the command line, which any program can see: the prompt goes through stdin, the token into a file only you can read (`mcp.json`, permissions `0600`). | [`internal/run/run.go`](internal/run/run.go), [`internal/claude/files.go`](internal/claude/files.go) | `TestPromptIsNeverInArguments`, `TestTokenIsNeverInArguments`, `TestTokenFileIsOwnerOnly` |
| Claude Code keeps no transcript of the run. | [`internal/claude/command.go`](internal/claude/command.go) (`--no-session-persistence`) | `TestSessionIsNotPersisted` |
| None of your personal Claude Code configuration (hooks, `CLAUDE.md`, plugins, skills, agents, other MCP servers, `env` settings) applies to a run. Claude Code gets only a short list of environment variables: never `ANTHROPIC_*` or `CLAUDE_*`, so an API key left in your shell is not used. | [`internal/claude/command.go`](internal/claude/command.go) (`--restricted`, `--strict-mcp-config`), [`internal/claude/env.go`](internal/claude/env.go) | `TestPersonalConfigurationIsExcluded` |
| A run stops after at most 3 hours. | [`internal/run/run.go`](internal/run/run.go) (`MaxRunDuration`) | `TestRunCannotOutliveMaxDuration` |
| Cancelling a run stops Claude Code and every process it started. | [`internal/proc/`](internal/proc/) | `TestTemporaryDirectoryIsRemovedAfterCancel`; the process tree in `internal/proc/` |
| Only neutral step names and counters reach the browser: never tool names, inputs, results or Claude's text. The local log holds only times, actions, outcomes and identifiers: never prompts, Claude's text, tokens or your email address. | [`internal/stream/stream.go`](internal/stream/stream.go), [`internal/logs/logs.go`](internal/logs/logs.go) | `TestLogHasNoPromptTokenOrToolNames` |
| What Claude Code writes for the run in `~/.claude/projects/` and in `$TMPDIR/claude-<uid>/` is removed; the folders of your other projects are not touched. | [`internal/claude/cleanup.go`](internal/claude/cleanup.go) | `TestRunDirectoriesAreRemoved` |

## The commands it runs

To check Claude Code, with the reduced environment above:

```
claude --version
claude auth status
```

For a run, from the temporary folder (`<tools>` are those of the run spec,
`--agents` only when it has subagents):

```
claude -p --restricted --model <model> --tools <tools> \
  --allowedTools "mcp__leadin <tools>" [--agents <folder>/agents.json] \
  --mcp-config <folder>/mcp.json --strict-mcp-config --no-session-persistence \
  --output-format stream-json --verbose --input-format stream-json
```

The prompt is written to its stdin as one `stream-json` line. Its stderr is
discarded, because it contains Claude's text.

## What is left on disk after a run

Nothing of the run. In every case (finished, failed, cancelled, timed out)
the helper removes:

- the temporary folder `leadin-run-…` with `mcp.json` and `agents.json`;
- the folder Claude Code creates for it in `~/.claude/projects/` (with
  `--no-session-persistence` there is no transcript, but `WebFetch` saves
  large downloads there);
- the folder Claude Code creates for it in `$TMPDIR/claude-<uid>/` (output
  files of background subagents).

Only the local log stays (below).

## Why it is useless on its own

The code is public and anyone can build it, but the helper has no features
of its own:

- a ticket can only be created by a person signed in to LeadIn, and LeadIn
  accounts are created only by the LeadIn administrators;
- the token the ticket gives access to is valid for one run only, for a few
  hours, and can be revoked;
- the actual prompts are not here: the LeadIn server returns them only to a
  valid token.

A helper built by someone else, without an account, can do exactly what
this one does: start Claude Code with a ticket it cannot get.

## Build

You need [Go](https://go.dev/dl/) 1.27.2 or later.

```bash
go build ./cmd/leadin-helper
```

All the checks (formatting, `go vet` for Linux, macOS and Windows, tests,
and a build for each target in `dist/`):

```bash
./scripts/check.sh
```

## Where the log is

| OS | Path |
|---|---|
| Linux | `$XDG_DATA_HOME/leadin/logs/helper.log` (usually `~/.local/share/leadin/logs/helper.log`) |
| macOS | `~/Library/Application Support/LeadIn/logs/helper.log` |
| Windows | `%LOCALAPPDATA%\LeadIn\logs\helper.log` |

It rotates at 1 MB and keeps 3 old files.

## License

[MIT](LICENSE).
