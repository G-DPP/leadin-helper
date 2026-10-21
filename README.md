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

**Status:** in development. `get_info` works; runs do not yet: `start_run`
answers `run_failed` with `unknown`.

The browser starts the helper through Chrome's
[Native Messaging](https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging).
The messages it accepts and sends are described in [PROTOCOL.md](PROTOCOL.md).

## What it cannot do

These limits are written in the code. Even if the LeadIn server asked for
something else, the helper would refuse: it never touches your files or your
terminal.

| Limit | Where in the code |
|---|---|
| Claude Code starts only in a new, empty temporary folder, deleted at the end in every case. | to be added with the runs |
| At most these tools: `WebSearch`, `WebFetch`, `Agent` and the LeadIn MCP server alone. Never files or a terminal (`Bash`, `Read`, `Write`, `Edit`…). The server can only narrow this list. | to be added with the runs |
| The MCP server must be on the same address as the LeadIn server, over `https` (`http` only on this computer). | to be added with the runs |
| The prompt and the token are never in the command line, which any program can see: the prompt goes through stdin, the token into a file only you can read. | to be added with the runs |
| None of your personal Claude Code configuration (hooks, `CLAUDE.md`, plugins, skills, agents, other MCP servers, `env` settings) applies to a run. | to be added with the runs |
| Claude Code gets only a short list of environment variables: never `ANTHROPIC_*` or `CLAUDE_*`, so an API key left in your shell is not used. | [`internal/claude/env.go`](internal/claude/env.go) |
| A run stops after at most 3 hours. | to be added with the runs |
| The local log holds only times, actions, outcomes and identifiers: never prompts, Claude's text, tokens or your email address. | [`internal/logs/logs.go`](internal/logs/logs.go) |

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
