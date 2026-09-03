# CLAUDE.MD - nats-desk Project Guide

## Project Overview

A local NATS client: a Go binary that serves its own web UI on loopback and
talks to NATS over the **native protocol**, so it works against any server
without the `websocket {}` listener a browser-only client needs.

This is the successor to `stone-age-io/nats-client`, which connects with
`wsconnect` from the browser. That client still exists and still works; this
one exists because requiring server-side websocket configuration ruled out most
real deployments.

**Key Design Principles:**

- **Boring transport.** REST for request/response, one WebSocket for push. No
  RPC bridge, no codegen. NUI - the leading OSS NATS GUI - ships a Wails app
  whose `BindApi()` returns an empty struct and routes everything over HTTP to
  its own local server; and Wails issue #4418 (open 14 months, P1) has WebView2
  silently dropping promises under sustained bridge load, with the reporter's
  fix being a move to HTTP RPC. Two independent routes to the same conclusion.
- **No webview.** `CGO_ENABLED=0` everywhere, so every platform cross-compiles
  from one machine. No per-platform CI runner, no cgo toolchain, and - because
  Gatekeeper only fires on the `com.apple.quarantine` bit that `curl`, `brew`
  and `scoop` do not set - no code signing or notarization.
- **App feel via PWA install.** `frontend/public/manifest.json` plus a
  deliberately empty `sw.js`. `localhost` is a secure context, so Chrome and
  Edge offer Install with no extra Go code.
- **Loopback only, and authenticated.** The backend opens NATS connections with
  the user's stored credentials, so an open port here would be a credential
  exfiltration primitive.
- **Bounded work reaching the browser.** A firehose must degrade honestly, not
  freeze the tab.

## Build & Test Commands

```bash
# Build frontend + binary for this platform
make build

# Every supported target, from this one machine
make build-all VERSION=1.0.0

# Frontend iteration with HMR: run this, then `npm run dev` in frontend/
make dev

# Tests. make test has no -race on purpose (see Testing below)
make test
make test-race       # needs cgo + a C compiler
make test-coverage

make fmt
make lint
make deps
make clean
make install-tools
```

## Project Structure

```
nats-desk/
├── cmd/nats-desk/main.go       # flags, single-instance, browser open, wiring
├── frontend/
│   ├── embed.go                # //go:embed all:dist  (must live beside dist)
│   ├── api.js                  # replaces the old nats-client.js
│   ├── main.js ui.js dom.js dialogs.js splitters.js utils.js storage.js
│   ├── index.html style.css sw.js
│   └── public/                 # manifest.json, icons
├── internal/
│   ├── server/                 # HTTP: embed + SPA fallback, token, Host allowlist
│   │   ├── server.go           # fixed port, single instance, idle shutdown
│   │   ├── security.go         # token -> cookie, Host allowlist
│   │   ├── spa.go              # asset caching, SPA fallback
│   │   └── addrinuse_*.go      # EADDRINUSE detection, per platform
│   ├── api/                    # REST handlers
│   ├── ws/                     # push channel: batching, drop accounting
│   ├── natsconn/               # the NATS connection, pub/sub, subject rules
│   ├── browse/                 # open a URL in the default browser
│   ├── contexts/               # (phase 3) jsm.go/natscontext
│   ├── monitor/                # (phase 4) three monitoring sources
│   └── store/                  # (phase 3) profiles on disk
└── Makefile
```

## Things that are the way they are for a reason

**The port is fixed (4111) and must stay fixed.** An installed PWA's identity -
and therefore its stored preferences - is its origin, and the origin includes
the port. A port that moved between runs would strand the user's settings every
time. The fixed port is also the entire single-instance mechanism: if `bind`
returns `EADDRINUSE`, another copy is running, so open a browser at it and exit.
No lockfile, no stale pid, and the OS reclaims the port on a crash.

**`syscall.EADDRINUSE` does not match on Windows.** Winsock reports 10048
(`WSAEADDRINUSE`) and stdlib `syscall` exports no name for it on Windows.
Verified against a real double-bind. See `internal/server/addrinuse_windows.go`.

**Two security guards, and both are needed.** The token stops another local
process from driving the API just by knowing the port. The `Host` allowlist
stops a hostile web page reaching us by resolving its own domain to 127.0.0.1 -
DNS rebinding, which the browser performs happily and which `SameSite` does not
prevent. Vite shipped a CVE for omitting exactly this check.

**Messages are batched, and drops are reported.** Measured: ~1,000,000 msg/sec
published against a UI that forwarded every message as its own frame showed
**3.7 seconds** of main-thread lag. Batching per 50ms window plus a hard cap of
200 messages per window brought that to **5.7ms** under the same load. The log
only keeps a couple of hundred entries on screen, so forwarding more is work
whose result is discarded before anyone can read it. Excess is dropped
oldest-first (a firehose viewer wants the live edge) and **counted**, because a
silent gap in a message log is a lie. See `internal/ws/hub.go`.

**Payloads cross the wire base64-encoded.** The old client decoded to a string
on arrival and could only ever show a hex stub for binary. Now the bytes
survive end to end and only the *display* falls back to hex.

**`jsm.go`, not `orbit.go`, for contexts.** `orbit.go/natscontext` exports one
function - `Connect` - and cannot enumerate contexts, which a GUI picker needs.
It also parses `user_jwt` and never uses it, resolves `nsc` lookups and discards
the result, and hard-errors on Windows cert-store contexts.
`github.com/nats-io/jsm.go/natscontext` is what the `nats` CLI itself runs, and
`jsm.go/serverdata` then comes free in the same module for `$SYS` scatter-gather.
Measured cost: `nats.go` alone links to 6.3MB stripped; adding `natscontext`
takes it to 12MB; adding `serverdata` and the `nats-server` response types costs
only **1MB more**.

**`serverdata` is not in a tagged jsm.go release.** v0.4.1 does not contain the
package; it only exists on later commits, which is why `natscli` pins a
pseudo-version. We do the same.

**`serverdata.DoReqAsync` panics on a nil logger.** It calls `log.Debugf` with
no nil check. Pass `api.NewDiscardLogger()`.

**A KV watch must not start before its listener exists.** `WatchAll` replays
every existing key the instant it opens, and that replay is the *only* thing
that populates the key list. Opening the bucket and starting the watch were
originally one endpoint, so the whole replay landed before the browser had
registered its handler and the list came up empty. `POST /api/kv/open` and
`POST /api/kv/watch` are separate for that reason - do not recombine them.

**`IgnoreDeletes` would break the key list.** The UI removes rows on DEL and
PURGE, so those events have to arrive.

**Two jetstream String() methods lie about the wire format.**
`KeyValueOp.String()` gives "KeyValueDeleteOp" where the KV-Operation header
says "DEL" - the UI keys off the latter, so `kvOpName` maps them.
`StorageType.String()` gives "File", but `StorageType.UnmarshalJSON` accepts
only the lowercase `"file"`, so reporting the String() form produced a config
the edit dialog could show and the server would then reject on save. Marshal
the value itself and both directions agree.

**KV keys can contain "/".** The routes use `{key...}` trailing wildcards, and
the client encodes each slash-separated segment individually, so
`config/db/host` keeps its structure while everything else is escaped.

**`ui.js` takes headers as a plain object.** They used to arrive as a NATS
`MsgHdrs`, which iterates as `[key, value]` pairs; `ui.js` was already
inconsistent about this, using `Object.entries` for stream messages and bare
iteration for live ones. It now uses `Object.entries` throughout. The UI has no
NATS library any more and should not expect one of its types.

## Testing notes

Drive the **real UI** against a **real `nats-server`**. Do not verify through
`await import('/api.js')` in the console: that loads a separate module graph
from the app's own `<script type="module">`, so it reports on an instance the
app is not using.

`requestAnimationFrame` does not fire while the Browser pane is hidden, so it is
useless as a responsiveness probe there. Use `setTimeout` lag instead.

Test rig config lives in the scratchpad, not the repo: a single server with
JetStream, `http_port: 8222`, an `APP` account and a `SYS` account with
`system_account: SYS`, so all three monitoring sources are exercisable.

## Testing

`make test` runs without the race detector on purpose - `-race` needs cgo and a
C compiler, which the global `CGO_ENABLED=0` and a typical Windows dev box do
not provide. Use `make test-race` where a toolchain exists.

Coverage is currently thin and concentrated on the logic that is easy to get
silently wrong: the subject filter rules, the KV operation mapping, and the
batching/drop accounting. The HTTP and NATS layers are covered by driving the
real UI against a real server, not by unit tests.

## Status

- **Phase 1 (done):** server, auth, connect/disconnect/status/info,
  publish/request, subscribe with the system-subject filter, message push.
- **Phase 2 (done):** KV buckets, keys, history, live watch; JetStream streams,
  consumers, message-range fetch and live tail.
- **Phase 3:** NATS CLI contexts via `jsm.go/natscontext`.
- **Phase 4:** monitoring - data connection, separate `$SYS` connection, and
  HTTP `:8222`, each independently configurable.
