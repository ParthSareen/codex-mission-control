# Codex Control for iOS

Small SwiftUI companion app for choosing an existing Codex thread, monitoring
its latest rollout events, and sending prompts through the Mac-side bridge.

## Run the services

From the repo root:

```sh
go run ./cmd/cmc serve
```

For an installed binary:

```sh
cmc serve
```

This starts the Codex bridge and Zuko approval server in the background. By
default both bind only to your Mac's Tailscale IPv4 address. The command prints
the two URLs to enter in the app:

- Codex bridge URL, usually `http://100.x.y.z:8765`
- Zuko approvals URL, usually `http://100.x.y.z:9777`

For simulator-only local testing:

```sh
go run ./cmd/cmc serve --local
```

This uses `http://127.0.0.1:8765` for the bridge and `http://127.0.0.1:9777`
for Zuko approvals.

Check or stop the background services with:

```sh
cmc serve --status
cmc serve --stop
```

## Open the app

Open `CodexSessionControl.xcodeproj` in Xcode, select the
`CodexSessionControl` scheme, and run it on an iOS simulator or device.

The app lists recent Codex threads from the bridge. Pick a thread to monitor
recent rollout events; the detail screen refreshes every 5 seconds while it is
open. From that screen you can send a typed prompt or command into the selected
thread, override the model, and override reasoning speed.

Normal Codex app-server exec and file-change prompts are sent to the app for
Face ID/passcode approval by default. Matching Codex and Zuko requests appear
inside the active thread while you are viewing it. Tap the shield button for the
full approvals sheet, where you can disable Codex approvals or enter the Zuko
approvals URL from `cmc serve` and the token from `zuko pair`.

Tap New Chat to choose a project under `~/Documents/repos` and start a fresh
headless Codex thread there. The bridge only accepts directories that resolve
inside that project root. If needed, run the bridge with:

```sh
go run ./cmd/cmc serve --projects-root /path/to/repos
```

The bridge owns one persistent headless `codex app-server` process. Prompt
sends call `thread/resume` and `turn/start` on that headless server instead of
opening a new Terminal window.
