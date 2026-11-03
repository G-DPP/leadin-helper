# Native Messaging protocol

Protocol version: **2**.

The browser extension talks to the helper with Chrome Native Messaging. The
helper never writes anything else to stdout.

## Framing

- Each message is a 4-byte **little-endian** length followed by that many
  bytes of UTF-8 JSON. The JSON must be an object.
- Incoming messages (extension → helper): at most **4 MB**. Outgoing
  messages (helper → extension): at most **1 MB**, Chrome's limit.
- **One request per connection.** `get_info` gets one reply and the helper
  exits. `start_run` gets a sequence of events and the helper exits when the
  run ends.
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
  "subscription_type": "pro"
}
```

| Field | Meaning |
|---|---|
| `helper_version` | the helper version; `0.0.0-dev` for a local build |
| `protocol_version` | the version of this document |
| `os`, `arch` | Go's `GOOS` and `GOARCH` (`windows`, `darwin`, `linux`; `amd64`, `arm64`) |
| `claude_found` | whether `claude` was found in `PATH` or in the usual install folders |
| `claude_version` | the first word of `claude --version`, or `null` |
| `logged_in` | `loggedIn` of `claude auth status`; `false` on any error |
| `subscription_type` | `subscriptionType` of `claude auth status`, or `null` |

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
