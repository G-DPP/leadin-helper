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

{"run_id": 7, "ticket": "…"}
```

Redirects are not followed. A 404 means the ticket is expired, already used
or wrong (`ticket_rejected`).

### The run spec

The response is the **run spec**. The helper accepts it only if it narrows
what the helper can do, never if it widens it:

| Field | Constraint |
|---|---|
| `run_id` | equal to the requested one |
| `mcp_url` | same origin as `server_url` |
| `mcp_token` | 1–512 visible ASCII characters |
| `model` | `sonnet`, `opus` or `haiku` |
| `prompt` | not blank, at most 250 000 characters |
| `tools` | a subset of `WebSearch`, `WebFetch`, `Agent`, without duplicates |
| `agents` | object, at most 10 subagents; present if and only if `tools` has `Agent`. Name in `[A-Za-z0-9_-]{1,64}`; fields `description` and `prompt` (not blank, at most 10 000 characters), `tools` (not empty, within `WebSearch`, `WebFetch` and `mcp__leadin__*`; never `Agent`) and optionally `model` |
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
identifies. If that line is not valid, `outcome` is `failed` and `message`
is the spec's `failure_message`.

`run_failed`, when the run could not start or did not end normally:

```json
{"type": "run_failed", "code": "claude_not_logged_in", "helper_version": "1.0.0", "claude_version": "2.1.300"}
```

| `code` | When |
|---|---|
| `invalid_request` | the `start_run` request is not valid |
| `ticket_rejected` | the server rejected the ticket (404): expired, already used or wrong |
| `server_unreachable` | the server does not answer (network, timeout, TLS) |
| `server_error` | the server answered with another error, a redirect or invalid JSON |
| `invalid_spec` | the run spec breaks the constraints above |
| `claude_not_found` | `claude` is not installed |
| `claude_too_old` | `claude --version` is older than 2.1.248, which has no `--restricted` |
| `claude_not_logged_in` | `claude` is not logged in |
| `mcp_unauthorized` | the MCP server did not accept the token |
| `timeout` | over 3 hours |
| `usage_limit` | Claude stopped at the subscription's usage limit |
| `api_error` | Claude stopped on an API error (network, server, model) |
| `unknown` | no `result` event, or any other error |

The versions are present when known (not in the four redeem errors, which
happen before looking for `claude`). When `claude` ended with an error
`result`, the event also carries `duration_ms`, `num_turns`, `cost_usd` and
`usage`: an interrupted run has still consumed usage. Claude's error text
never leaves the helper.

### Cancelling

The extension cancels a run by closing the port. The helper sees stdin
close, stops `claude` and all its child processes, cleans up and exits
without replying.

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
