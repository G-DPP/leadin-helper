# Native Messaging protocol

Protocol version: **2**.

The browser extension talks to the helper with Chrome Native Messaging. The
helper never writes anything else to stdout.

## Framing

- Each message is a 4-byte **little-endian** length followed by that many
  bytes of UTF-8 JSON. The JSON must be an object.
- Incoming messages (extension → helper): at most **4 MB**. Outgoing
  messages (helper → extension): at most **1 MB**, Chrome's limit.
- **One request per connection.** `get_info` and `diagnostics` get one reply
  and the helper exits. `start_run` and `run_setup_step` get a sequence of
  events and the helper exits when the run or the step ends.
- A message that is truncated, too large, not JSON or not an object gets
  `{"type": "error", "code": "invalid_request"}`.

## `get_info`

Request:

```json
{"action": "get_info"}
```

Reply:

```json
{
  "type": "info",
  "helper_version": "1.0.0",
  "protocol_version": 2,
  "os": "linux",
  "arch": "amd64",
  "claude_found": true,
  "claude_version": "2.1.300",
  "logged_in": true,
  "subscription_type": "pro",
  "install_path": "/home/someone/.local/share/leadin/bin/leadin-helper",
  "registered_browsers": ["Brave", "Chrome"],
  "account": "current",
  "api_key_set": false,
  "claude_path": "/home/someone/.local/bin/claude"
}
```

| Field | Meaning |
|---|---|
| `helper_version` | the helper version; `0.0.0-dev` for a local build |
| `protocol_version` | the version of this document |
| `os`, `arch` | Go's `GOOS` and `GOARCH` (`windows`, `darwin`, `linux`; `amd64`, `arm64`) |
| `claude_found` | whether `claude` was found in `PATH` or in the usual install folders |
| `claude_version` | the first word of `claude --version`, or `null` |
| `logged_in` | `loggedIn` of `claude auth status` with the account of the runs; `false` on any error, or with the `api_key` account and no key saved |
| `subscription_type` | `subscriptionType` of `claude auth status`, or `null` |
| `install_path` | the installed copy (see README, «Install»), or `""` if it is not there |
| `account` | the account of the runs: `current`, `profile` or `api_key` (see `run_setup_step`) |
| `api_key_set` | whether an API key is saved; the key itself never leaves the helper |
| `claude_path` | the `claude` the runs will use, or `""` |
| `registered_browsers` | the browsers where the host is registered, read when asked (on Windows Brave and Vivaldi follow Chrome's key) |

`claude --version` and `claude auth status` run with the same environment as
a run (see `internal/claude/env.go`), so the login shown is the one a run
will use. The email address printed by `claude auth status` never leaves the
helper.

## `start_run`

Request, with exactly these fields (an extra field makes it invalid):

```json
{"action": "start_run", "run_id": 7, "ticket": "…", "server_url": "https://leadin.example"}
```

| Field | Constraint |
|---|---|
| `run_id` | positive integer |
| `ticket` | 20–128 characters in `[A-Za-z0-9_-]` |
| `server_url` | a bare origin: `https`, or `http` only for `localhost`, `127.0.0.1` and `::1`; no path, query or fragment |

The extension adds `server_url` itself, from its own configuration: the web
page cannot choose it.

### Redeeming the ticket

First of all, because the ticket expires quickly:

```
POST {server_url}/api/v1/claude-runs/redeem/
Content-Type: application/json

{"run_id": 7, "ticket": "…", "helper_version": "1.0.0", "protocol_version": 2}
```

Redirects are not followed. A 404 means the ticket is expired, already used
or wrong (`ticket_rejected`): the server always gives the same answer, and
checks the ticket before anything else.

With a good ticket, the server also checks the helper. If `helper_version` is
below the server's minimum, or `protocol_version` is not the one it expects
(older or newer), it answers:

```
409 Conflict

{"code": "helper_outdated", "min_version": "1.2.0"}
```

`min_version` is `null` when the server has no minimum and only the protocol
did not match. The run is already closed on the server and the ticket is
spent: the helper reports `helper_outdated` and does not look for `claude`.
Any other 409 is `server_error`.

### The run spec

The response is the **run spec**. The helper accepts it only if it narrows
what the helper can do, never if it widens it:

| Field | Constraint |
|---|---|
| `run_id` | equal to the requested one |
| `mcp_url` | same origin as `server_url` |
| `mcp_token` | 1–512 visible ASCII characters |
| `model` | an alias (`sonnet`, `opus`, `haiku`) or a full id matching `claude-[a-z0-9]+(-[a-z0-9]+)*`, at most 64 characters |
| `effort` | optional: empty, or one of `low`, `medium`, `high`, `xhigh`, `max`, passed as `--effort` |
| `prompt` | not blank, at most 250 000 characters |
| `tools` | a subset of `WebSearch`, `WebFetch`, `Agent`, without duplicates |
| `agents` | object, at most 10 subagents; present if and only if `tools` has `Agent`. Name in `[A-Za-z0-9_-]{1,64}`; fields `description` and `prompt` (not blank, at most 10 000 characters), `tools` (not empty, within `WebSearch`, `WebFetch` and `mcp__leadin__*`; never `Agent`) and optionally `model` and `effort`, with the same rules as the run's. Their JSON goes on Claude Code's command line, so it holds at most 8 000 characters |
| `progress_map` | object, at most 200 entries: tool name → neutral progress key, both in `[A-Za-z0-9_]{1,64}` |
| `outcomes` | the outcomes allowed in Claude's last line: 1–20 words in `[A-Za-z0-9_]{1,64}`, without duplicates, including `failed` |
| `result_id_key` | the key of Claude's last line holding the result id, a word in `[A-Za-z0-9_]{1,64}` |
| `failure_message` | the message used when the last line is not valid: not blank, at most 500 characters |

### Events

Events carry no `run_id`: the extension adds it.

`run_progress`, once per tool call:

```json
{"type": "run_progress", "key": "item_saved", "count": 2}
```

`key` is the tool's value in `progress_map`, or `working` for tools it does
not list; `count` counts the calls per key. Never tool names, inputs, results
or Claude's text.

`run_finished`, when Claude ends normally:

```json
{
  "type": "run_finished",
  "outcome": "done",
  "message": "…",
  "result_id": 42,
  "error_code": "",
  "final_message": "…",
  "duration_ms": 61000,
  "num_turns": 12,
  "cost_usd": 0.31,
  "usage": {"usage": {}, "modelUsage": {}},
  "claude_version": "2.1.300",
  "subscription_type": "pro",
  "helper_version": "1.0.0"
}
```

`outcome` and `message` come from Claude's last JSON line, with `outcome`
among the spec's `outcomes`; `result_id` is the integer (or `null`) under
the spec's `result_id_key` in the same line. The helper does not know what it
identifies. If that line is not valid, `outcome` is `failed`, `message`
is the spec's `failure_message` and `error_code` is `invalid_final_line`
(otherwise `error_code` is empty).

`final_message` is what the server keeps on the run, to tell how it ended:
the `message` of a valid last line, otherwise the last 500 characters of
Claude's final text. It is the only text of Claude that leaves the helper.

`run_failed`, when the run could not start or did not end normally:

```json
{"type": "run_failed", "code": "claude_not_logged_in", "helper_version": "1.0.0", "claude_version": "2.1.300"}
```

| `code` | When |
|---|---|
| `invalid_request` | the `start_run` request is not valid |
| `ticket_rejected` | the server rejected the ticket (404): expired, already used or wrong |
| `helper_outdated` | the server turned down this helper (409): too old or another protocol; it must be updated |
| `server_unreachable` | the server does not answer (network, timeout, TLS) |
| `server_error` | the server answered with another error (a 409 without `helper_outdated` too), a redirect or invalid JSON |
| `invalid_spec` | the run spec breaks the constraints above |
| `claude_not_found` | `claude` is not installed |
| `claude_too_old` | `claude --version` is older than 2.1.248, which has no `--restricted` |
| `claude_not_logged_in` | `claude` is not logged in |
| `mcp_unauthorized` | the MCP server did not accept the token |
| `timeout` | over 3 hours |
| `usage_limit` | Claude stopped at the subscription's usage limit |
| `api_error` | Claude stopped on an API error (network, server, model) |
| `unknown` | no `result` event, or any other error |

The versions are present when known (not in the five redeem errors, which
happen before looking for `claude`). When `claude` ended with an error
`result`, the event also carries `duration_ms`, `num_turns`, `cost_usd` and
`usage`: an interrupted run has still consumed usage, and `final_message`
with the last 500 characters of Claude's error text.

### Cancelling

The extension cancels a run by closing the port. The helper sees stdin
close, stops `claude` and all its child processes (SIGTERM to the process
group, SIGKILL after 3 seconds), cleans up and exits without replying. After
the cancellation no more `run_progress` is sent.

### Timeout

A run lasts at most 3 hours, whatever the spec says. Beyond that the helper
stops `claude` as for a cancellation and sends `run_failed` with `timeout`
and the versions.

### Example

A run with two tool calls, as the extension receives it:

```json
{"type": "run_progress", "key": "started", "count": 1}
{"type": "run_progress", "key": "item_saved", "count": 1}
{"type": "run_finished", "outcome": "done", "message": "Stored.", "result_id": 42, "error_code": "", "final_message": "Stored.", "duration_ms": 18250, "num_turns": 5, "cost_usd": 0.08, "usage": {"usage": {}, "modelUsage": {}}, "claude_version": "2.1.300", "subscription_type": "pro", "helper_version": "1.0.0"}
```

With subagents, `usage` also carries Claude Code's `subagent_stats`
(how many were spawned, and how): counters only. `duration_ms` and
`num_turns` are summed over every turn of the main agent.

## `run_setup_step`

The installation steps of the LeadIn setup page. Like `start_run`, one port
per step: the end of stdin (the port closed) cancels it and stops every
process it started.

Request:

```json
{"action": "run_setup_step", "step": "login_claude", "options": {"method": "subscription"}}
```

The steps are a fixed list in the helper; the page only picks one. Options
are strings, and a step accepts only its own:

| `step` | `options` | What it runs |
|---|---|---|
| `install_claude` | — | Claude Code's native installer: `curl -fsSL https://claude.ai/install.sh \| bash`, on Windows `powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://claude.ai/install.ps1 \| iex"`. 10 minutes at most |
| `login_claude` | `method`: `subscription` or `console` | `claude auth login --claudeai` / `--console`, stdin closed: it opens the default browser and exits when the person has logged in. 5 minutes at most. With the `profile` account, inside the profile's folder; not with `api_key` |
| `logout_claude` | — | `claude auth logout` (to change account) |
| `set_claude_config` | `account`: `current`, `profile` or `api_key`; `api_key`; `claude_path` (absolute, or empty to find it) | saves the choices in the helper's data folder (`config.json`, the key in `api_key`, readable by the user only). `api_key` needs a key, given now or before; another account deletes the saved key |
| `check_claude` | — | `claude -p --restricted --tools "" --strict-mcp-config --max-turns 1 --no-session-persistence --output-format text "Reply with OK."` with the account of the runs: one minimal request. With an API key it is the only way to know the key works |

Events, in order:

```json
{"type": "setup_started", "step": "login_claude", "command": "claude auth login --claudeai"}
{"type": "setup_output", "line": "Opening your browser to sign in as <email>"}
{"type": "setup_finished", "step": "login_claude", "exit_code": 0, "error_code": ""}
```

- `command` is the command shown in the page's Details.
- `setup_output` carries stdout and stderr, one line each (at most 2 000
  characters, 5 000 lines). Email addresses become `<email>`. The key is never
  printed.
- `exit_code` is `null` if no command started. `error_code` is `""` on
  success, otherwise one of `invalid_request` (unknown step or option),
  `claude_not_found`, `network_error`, `install_failed`, `login_failed`,
  `claude_not_logged_in`, `invalid_api_key`, `usage_limit`, `check_failed`,
  `config_failed`, `timeout`, `cancelled`.

The account chosen with `set_claude_config` is used by every later run and by
`get_info`: `profile` sets `CLAUDE_CONFIG_DIR` to the profile's folder,
`api_key` sets `ANTHROPIC_API_KEY`. Inherited `ANTHROPIC_*` and `CLAUDE_*`
variables are never passed.

## `diagnostics`

```json
{"action": "diagnostics"}
```

Reply: `{"type": "diagnostics", "text": "…"}`, a block for the person to copy
into a message: the helper version, the OS, every `get_info` field and the
last 50 lines of the local log. Never the API key or the email address.

## `error`

```json
{"type": "error", "code": "invalid_request"}
```

| `code` | When |
|---|---|
| `invalid_request` | unknown action, or a message that is not valid |
| `unknown` | an unexpected error inside the helper |

## Changes

- **2**: `run_finished` carries `result_id`, read from the spec's
  `result_id_key`; the allowed outcomes and the failure message come from the
  spec (`outcomes`, `failure_message`).
- **2, added fields**: `run_finished` carries `error_code` and
  `final_message`, `run_failed` after an error `result` carries
  `final_message`. Added fields only: the protocol version does not change.
- **2, added fields (helper 0.7.0)**: the spec's `model` may be a full id
  (`claude-…`), the spec and each subagent may have `effort`. An older helper
  rejects a full id with `invalid_spec`; the server asks for 0.7.0 at least.
- **2, added fields (helper 0.8.0)**: `get_info` carries `install_path`,
  `registered_browsers`, `account`, `api_key_set` and `claude_path`; new
  actions `run_setup_step` and `diagnostics`.
