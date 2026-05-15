# Codex Mission Control

<p align="center">
  <img src="assets/cmc.png" alt="Codex Mission Control startup splash" width="900">
</p>

A command-center TUI for watching local Codex Desktop/CLI threads.

It reads the same local artifacts as `codex-live`:

- `~/.codex/state_5.sqlite`
- `~/.codex/sessions/**/rollout-*.jsonl`

The TUI is file-backed, so it does not depend on the experimental app-server
control socket being present. Local control actions use the public Codex CLI,
starting with `codex resume <thread-id>`. The iOS bridge uses a persistent
headless `codex app-server` process for prompts sent from the phone.

## Run

Install:

```sh
go install github.com/parthsareen/codex-mission-control/cmd/cmc@v0.1.4
```

```sh
go run ./cmd/cmc
```

Build:

```sh
go build -o ./cmc ./cmd/cmc
./cmc
```

Static render for quick checks:

```sh
go run ./cmd/cmc --snapshot
```

## Keys

```text
j/k, up/down   move through threads
tab            switch between thread list and fleet/comms pane
1-9, 0         jump to fleet callsign, with 0 selecting the tenth row
c              open comms for selected thread
d              open nvim DiffviewOpen in the selected thread cwd
n              create a new mission in CMC: pick/create cwd, branch/review optional
/              search chats/folders/worktrees
enter          open comms for selected thread
o              fleet overview
pgup/pgdn      scroll comms history
ctrl+u/d       scroll comms history
[/]            scroll comms history
l              snap back to live/latest
v              visual-select comms lines
y              copy selected/current comms lines
r              resume selected thread through CMC
R or a         ask, then send that prompt through CMC
A              approve selected pending Codex request
S              approve selected pending Codex request for the session
D              deny selected pending Codex request
p              cycle pending Codex requests
t              cycle theme
space          pause/resume live updates
esc            leave focus or cancel prompt
q              quit
```

Themes: green, cyan, amber, blue, purple, red, white.

Mission Control remembers the last theme, selected thread, pane, comms
position, intro splash preference, and seen final timestamps in
`~/.codex/mission-control/state.json`. Set `"intro_splash": false` there to
skip the startup splash. When enabled, the splash stays up until you press a
key.

The startup splash runs quick pre-flight checks for Codex data, SQLite, recent
rollout files, the Codex CLI, detached terminal launch support, Git, nvim
Diffview, and Mission Control state persistence. Green is ready, yellow is
non-blocking degraded mode, and red means a required system failed.

Escalation requests, such as tool calls with
`sandbox_permissions: "require_escalated"`, render as `ALERT`. CMC-initiated
turns run through a persistent headless `codex app-server`, so pending Codex
exec and file-change approvals appear in the approvals pane and can be handled
with `A`, `S`, or `D` without leaving Mission Control. Threads with a fresh
final answer that you have not selected render as red `REVIEW` until opened.

## iOS companion

The `ios/CodexSessionControl` project is a small SwiftUI app for selecting an
existing thread, watching its latest rollout events, and sending a prompt from
an iPhone or simulator. For a physical iPhone with Tailscale, start both the
Codex bridge and the Zuko approval server in the background with:

```sh
cmc serve
```

`cmc serve` binds both services only to this Mac's Tailscale IPv4 address by
default and prints the two URLs to enter in the iOS app:

- Codex bridge URL, usually `http://100.x.y.z:8765`
- Zuko approvals URL, usually `http://100.x.y.z:9777`

For local simulator testing:

```sh
cmc serve --local
```

Check or stop the background services with:

```sh
cmc serve --status
cmc serve --stop
```

`cmc-bridge` still exists as the bridge-only worker for debugging, but the
normal phone workflow should use `cmc serve`.

On the thread detail screen, the app polls the bridge every 5 seconds for the
latest messages/events. You can send a prompt or command into the selected
thread through the bridge's persistent headless `codex app-server`, and
optionally override model plus reasoning speed for that turn.

Codex exec and file-change approval prompts are routed to the phone by default;
pending requests can be approved or denied with Face ID/passcode confirmation.
When you are viewing a thread, matching Codex and Zuko approvals appear inside
that thread. The shield button still opens the full approvals sheet, where you
can disable Codex approvals or enter the Zuko URL from `cmc serve` and the
token from `zuko pair`.

The app can also start a new chat. The bridge exposes a project picker rooted
at `~/Documents/repos`, validates that the chosen directory stays under that
root, then starts the new thread in the selected project. For git projects, the
new-chat flow can use the selected folder, switch to an existing worktree, or
create a new worktree from a selected branch before starting Codex. Use
`--projects-root <path>` if your repo directory lives somewhere else.
