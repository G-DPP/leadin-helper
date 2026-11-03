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
- **macOS:** the tests pass in CI on Apple Silicon, and the release binary
  starts there. Not yet tried with the dashboard; the warning macOS shows
  for an unsigned program is still to be checked.
- **Windows:** the tests pass in CI, including the one of the job object
  that stops a cancelled run with every process it started
  (`internal/proc/proc_windows_test.go`). Not yet tried with the dashboard.
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
| Subagents get at most `WebSearch`, `WebFetch` and the LeadIn MCP server, never `Agent`, and no fields beyond description, prompt, tools, model and effort. | [`internal/spec/spec.go`](internal/spec/spec.go) | `TestSubagentToolsAreLimited`, `TestSubagentWithAgentToolIsRejected` |
| The model is an alias (`sonnet`, `opus`, `haiku`) or a `claude-…` id of safe characters, and the effort one of Claude Code's levels. | [`internal/spec/spec.go`](internal/spec/spec.go) | `TestInvalidSpecNeverStartsClaude` (in `run_test.go`) |
| Only the LeadIn MCP server, on the same address as the LeadIn server, over `https` (`http` only on this computer). | [`internal/spec/redeem.go`](internal/spec/redeem.go), [`internal/spec/spec.go`](internal/spec/spec.go) | `TestOnlyLeadinMCPServerIsConfigured`, `TestMCPOnOtherOriginIsRejected`, `TestPlainHTTPRemoteMCPIsRejected` |
| The ticket goes only to the LeadIn server the extension is configured for: redirects are not followed. | [`internal/spec/redeem.go`](internal/spec/redeem.go) | `TestRedeemDoesNotFollowRedirects` (in `internal/spec/redeem_test.go`) |
| The prompt and the token are never in the command line, which any program can see: the prompt goes through stdin, the token into a file only you can read (`mcp.json`, permissions `0600`). | [`internal/run/run.go`](internal/run/run.go), [`internal/claude/files.go`](internal/claude/files.go) | `TestPromptIsNeverInArguments`, `TestTokenIsNeverInArguments`, `TestTokenFileIsOwnerOnly` |
| Claude Code keeps no transcript of the run. | [`internal/claude/command.go`](internal/claude/command.go) (`--no-session-persistence`) | `TestSessionIsNotPersisted` |
| None of your personal Claude Code configuration (hooks, `CLAUDE.md`, plugins, skills, agents, other MCP servers, `env` settings) applies to a run. Claude Code gets only a short list of environment variables: never `ANTHROPIC_*` or `CLAUDE_*`, so an API key left in your shell is not used. | [`internal/claude/command.go`](internal/claude/command.go) (`--restricted`, `--strict-mcp-config`), [`internal/claude/env.go`](internal/claude/env.go) | `TestPersonalConfigurationIsExcluded` |
| A run stops after at most 3 hours. | [`internal/run/run.go`](internal/run/run.go) (`MaxRunDuration`) | `TestRunCannotOutliveMaxDuration` |
| Cancelling a run stops Claude Code and every process it started. | [`internal/proc/`](internal/proc/) | `TestTemporaryDirectoryIsRemovedAfterCancel`; the process tree in `internal/proc/` |
| Only neutral step names and counters reach the browser: never tool names, inputs or results. Of Claude's text, only the end of its final answer, which LeadIn keeps on the run to tell how it ended (`final_message`). The local log holds only times, actions, outcomes and identifiers: never prompts, Claude's text, tokens or your email address. | [`internal/stream/stream.go`](internal/stream/stream.go), [`internal/logs/logs.go`](internal/logs/logs.go) | `TestLogHasNoPromptTokenOrToolNames` |
| What Claude Code writes for the run in `~/.claude/projects/` and in `$TMPDIR/claude-<uid>/` is removed; the folders of your other projects are not touched. | [`internal/claude/cleanup.go`](internal/claude/cleanup.go) | `TestRunDirectoriesAreRemoved` |

## The commands it runs

To check Claude Code, with the reduced environment above:

```
claude --version
claude auth status
```

For a run, from `work/` in the temporary folder (`<tools>` are those of the
run spec, `--agents` only when it has subagents, with `Read` added to each
subagent's tools; `<run folder>` is the name Claude Code gives to `work/`):

```
MAX_MCP_OUTPUT_TOKENS=200000 \
claude -p --restricted --model <model> [--effort <effort>] --tools <tools>,Read \
  --allowedTools "mcp__leadin <tools> Read(~/.claude/projects/<run folder>/**)" \
  [--agents '<subagents JSON>'] \
  --mcp-config <folder>/config/mcp.json --strict-mcp-config --no-session-persistence \
  --output-format stream-json --verbose --input-format stream-json
```

`Read` is there because Claude Code saves a tool answer longer than about
50,000 characters to a file in the run's project folder and gives Claude only
its path. It is allowed only in that folder, never in the rest of your home.
`mcp.json` holds the run's token and sits in `config/`, outside the working
directory, so that Claude cannot read it.

The prompt is written to its stdin as one `stream-json` line. Its stderr is
discarded, because it contains Claude's text.

## What is left on disk after a run

Nothing of the run. In every case (finished, failed, cancelled, timed out)
the helper removes:

- the temporary folder `leadin-run-…`, with `work/` and `config/mcp.json`;
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

## Downloads

Download the helper from the [releases page](https://github.com/G-DPP/leadin-helper/releases/latest).
Each release has these files (`X.Y.Z` is the version):

| Your computer | Take this file |
|---|---|
| Mac with Apple chip (M1 or later) | `leadin-helper_X.Y.Z_darwin_arm64.zip` |
| Mac with Intel chip | `leadin-helper_X.Y.Z_darwin_amd64.zip` |
| Linux | `leadin-helper_X.Y.Z_linux_amd64.zip` |
| Windows | `leadin-helper_X.Y.Z_windows_amd64.exe` |

On macOS and Linux the zip holds a single file, `leadin-helper`, already
marked as a program (a browser saves a bare binary without that mark). On
Windows the `.exe` is all you need. The LeadIn tutorial says what to do
next.

The same binaries are also in the release without a zip
(`leadin-helper_X.Y.Z_<os>_<arch>`): they are for those who install from a
terminal or want to compare the hashes. `SHA256SUMS` lists the hash of every
file.

## Install

Double-click the file you downloaded (on macOS and Linux, the one inside the
zip). The helper copies itself into a folder of your user and registers with
every Chromium browser it finds (Chrome, Chromium, Edge, Brave, Vivaldi). No
administrator password. A window shows what it did; press Enter to close it.

| OS | Installed copy | Registration |
|---|---|---|
| Linux | `~/.local/share/leadin/bin/leadin-helper` | a manifest in each browser profile, e.g. `~/.config/google-chrome/NativeMessagingHosts/com.leadin.helper.json` |
| macOS | `~/Library/Application Support/LeadIn/bin/leadin-helper` | a manifest in each browser profile under `~/Library/Application Support/` |
| Windows | `%LOCALAPPDATA%\LeadIn\bin\leadin-helper.exe` | `%LOCALAPPDATA%\LeadIn\native_host\com.leadin.helper.json`, pointed to by `HKCU\Software\{Google\Chrome,Chromium,Microsoft\Edge}\NativeMessagingHosts\com.leadin.helper` |

Or, without downloading anything by hand and without the warnings of macOS
and Windows, from a terminal (the LeadIn setup page shows the same command
with its own address, which redirects here):

```bash
curl -fsSL https://github.com/G-DPP/leadin-helper/releases/latest/download/install.sh | sh
```

```powershell
irm https://github.com/G-DPP/leadin-helper/releases/latest/download/install.ps1 | iex
```

The script downloads the binary of its own release and `SHA256SUMS`, checks
the hash and runs `--install`; with a wrong hash it installs nothing.

From a terminal: `leadin-helper --install` does the same without waiting
for Enter, and `leadin-helper --uninstall` removes the copy, the manifests and
the registry keys (the log stays). Installing again overwrites the copy.
Opening the browser once before installing creates its profile: a browser
never opened is not found.

To see which version you have:

```bash
leadin-helper --version
# leadin-helper 0.6.0 (protocol 2, linux/amd64)
```

## Verify a release

Check that the files you downloaded are the ones of the release, from the
folder where they are:

```bash
sha256sum -c --ignore-missing SHA256SUMS
```

Check that the release really comes from this code: this rebuilds the tag
`vX.Y.Z` in a temporary folder and compares every hash.

```bash
git clone https://github.com/G-DPP/leadin-helper.git && cd leadin-helper
scripts/verify-release.sh X.Y.Z <URL of the release's SHA256SUMS>
```

Every file must print `OK`. You need `git`, `bash`, `zip`, `curl` and any
version of [Go](https://go.dev/dl/): the script downloads Go 1.27.2, the
one the releases are built with.

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

The files of a release, in `dist/release/`, from a commit with no local
changes:

```bash
scripts/build-release.sh X.Y.Z
```

The build is reproducible: the same commit always gives the same files.
For this the script fixes the Go version (`GOTOOLCHAIN=go1.27.2`), builds
without C code (`CGO_ENABLED=0`), drops the paths of the machine
(`-trimpath`) and the build identifier (`-buildid=`), and gives the files in
the zips the date of the commit.

The binaries are **not code-signed**. The first time you open one, macOS
and Windows warn that it comes from an unidentified developer; the LeadIn
tutorial shows how to go ahead.

## Updating

The helper tells the server its version and protocol version when it
redeems a ticket. If the server needs a newer helper, the run stops with
`helper_outdated` and the LeadIn website asks you to update: download the
version it asks for and double-click it again. Nothing updates by itself.

A build from source is version `0.0.0-dev` (release builds set the version,
see `scripts/build-release.sh`). A server that sets a minimum version turns it down;
a local development server has no minimum, so it accepts it.

## Where the log is

| OS | Path |
|---|---|
| Linux | `$XDG_DATA_HOME/leadin/logs/helper.log` (usually `~/.local/share/leadin/logs/helper.log`) |
| macOS | `~/Library/Application Support/LeadIn/logs/helper.log` |
| Windows | `%LOCALAPPDATA%\LeadIn\logs\helper.log` |

It rotates at 1 MB and keeps 3 old files.

## License

[MIT](LICENSE).
