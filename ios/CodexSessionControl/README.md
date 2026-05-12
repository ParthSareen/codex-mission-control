# Codex Control for iOS

Small SwiftUI companion app for choosing an existing Codex thread, monitoring
its latest rollout events, and sending prompts through the Mac-side bridge.

## Run the bridge

From the repo root:

```sh
go run ./cmd/cmc-bridge
```

The simulator uses `http://127.0.0.1:8765` by default.

For a physical iPhone with Tailscale, bind the bridge only to your Mac's
Tailscale IPv4 address:

```sh
go run ./cmd/cmc-bridge --tailscale
```

The bridge prints the exact `http://100.x.y.z:8765` URL. Set the app's bridge
URL to that value. This does not bind the bridge to public interfaces or normal
Wi-Fi/LAN addresses.

## Open the app

Open `CodexSessionControl.xcodeproj` in Xcode, select the
`CodexSessionControl` scheme, and run it on an iOS simulator or device.

The app lists recent Codex threads from the bridge. Pick a thread to monitor
recent rollout events; the detail screen refreshes every 5 seconds while it is
open. From that screen you can send a typed prompt or command into the selected
thread, override the model, and override reasoning speed.

Tap New Chat to choose a project under `~/Documents/repos` and start a fresh
headless Codex thread there. The bridge only accepts directories that resolve
inside that project root. If needed, run the bridge with:

```sh
go run ./cmd/cmc-bridge --projects-root /path/to/repos
```

The bridge owns one persistent headless `codex app-server` process. Prompt
sends call `thread/resume` and `turn/start` on that headless server instead of
opening a new Terminal window.
