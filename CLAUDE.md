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
│   ├── contexts/               # the nats CLI's own context store
│   └── monitor/                # three monitoring sources, a grid and a feed
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

**The three monitoring sources are not interchangeable, and one of them cannot
be merged with the data connection even in principle.** A `nats.Conn` is bound
to one account for its lifetime, so a system-account view is *mandatorily* a
second connection. Only `$SYS` fans out across a cluster in one request and
pushes events as they happen; only `:8222` works where nobody has provisioned a
system user; only the data connection needs no configuration at all, because
the server auto-imports `$SYS.REQ.ACCOUNT.PING.{STATZ,CONNZ}` and
`$SYS.REQ.USER.INFO` into every account, scoped to that account.

**The cluster grid is pushed, not polled.** `$SYS.SERVER.*.STATSZ` arrives from
every server every ten seconds by default, which is a live grid for free. The
scatter-gather (`$SYS.REQ.SERVER.PING`) runs only on connect - so the grid is
populated at once instead of after a ten second wait - and when someone presses
refresh.

**A rate is a pointer.** "We have not seen a second sample yet" and "the rate is
zero" are different facts, and showing 0 msg/s for a busy server we have only
just met is a made-up number presented as a measurement.

**Rates are measured on the server's own clock**, from `ServerInfo.Time` or
`Varz.Now`, never ours. The interval that matters is the one the counters
accumulated over, and in a cluster the samples come from several machines whose
clocks may not agree with ours or with each other. A counter that goes backwards
means the process restarted, so that interval is skipped rather than reported as
a large negative rate.

**A scatter-gather is a census, so `RefreshServers` prunes.** Two things make
this necessary: a server that is killed rather than shut down never sends
`SHUTDOWN`, and a server that restarts comes back with a **new ID** - NATS
generates one per process - so its old row would sit in the grid forever. Pruning
only touches rows from the same family of sources; a `$SYS` census knows nothing
about servers reached over `:8222`. Between censuses the UI dims a row whose
`seen` is older than three heartbeats rather than dropping it, because a grid
that silently loses a line does not tell you anything went away.

**A fresh inbox per scatter-gather, deliberately.** nats-surveyor keeps one
long-lived inbox with a per-poll key, and the original plan called for the same.
It is not worth it here: the grid is pushed, so a scatter happens on connect and
on an explicit refresh, and one subscribe/unsubscribe at that rate costs nothing
against the bookkeeping. Revisit only if something ever polls faster than about
once a second.

**`:8222` has no authentication of any kind**, and setting `https_port` forces
the server to `ClientAuth = NoClientCert`, so client certificates cannot gate it
either. Anything it returns is readable by whoever can reach the port. Its TLS
settings are its own in the UI, because the monitoring port routinely has a
different certificate - often a private CA - from the client port.

**The system-account panel takes a `.creds` file, and needs to.** Operator
mode is exactly where a `$SYS` user exists at all, so JWT is the *likeliest*
way to authenticate that connection, not an edge case. It reuses
`natsconn.AuthOptions.Option` - which is why that method is exported - so the
resolution order is identical to the main connection form: creds, then token,
then user/password.

**Only the monitoring URLs are remembered; the system-account credentials are
not.** URLs are addresses. Credentials belong in the NATS CLI context that
already holds them, which is why the system-account panel offers the context
picker first. The backend holds the system connection for the life of the
process only, so the saved URLs are re-registered on load - otherwise the form
would show URLs that are not actually in use.

**Monitoring events take the batched WebSocket path; the grid and status take
the control path.** On a cluster with connection churn,
`$SYS.ACCOUNT.*.{CONNECT,DISCONNECT}` arrives as fast as clients come and go,
which is a firehose by any other name. A grid update or a source going down is
rare and must not be dropped.

**A context is edited as its own file, not through a form.** The settings
struct has two dozen fields and grows with the CLI; a form would silently drop
whatever it had not been taught about, so the dialog round-trips the file's raw
JSON and `internal/contexts.Save` hands it back through natscontext for
validation. Two consequences to keep: `Get` reads the **raw backend payload**
rather than a loaded Context, because a load has already expanded `~` and
`$VARS` and saving that back would rewrite a portable `~/x.creds` into an
absolute path belonging to this machine; and `Save` goes through
`NewFromBytesRaw`, the library's own "faithful roundtrip" entry point.

**`With*` options cannot clear a field.** Every one of them is
`if v != "" { s.X = v }`, so a load-then-apply-options editor can set a
password but never remove one. That is the other reason the editor is
JSON-shaped.

**Selecting a context to connect with is not the same as selecting it for the
CLI.** `context.txt` is read by every NATS tool on the machine, so changing it
is its own endpoint and its own button. Picking one in the popover only affects
this connection.

**`DeleteContext` refuses to delete the active context** unless it is the only
one. That refusal is the library's, and the UI shows it verbatim rather than
pre-empting it - the message names the problem exactly.

**Contexts are listed from raw payloads, never loaded.** `Registry.Load`
eagerly resolves the deprecated `nsc` field, which shells out to the `nsc`
binary. Listing five contexts must not run five subprocesses.

**`jsm.go`, not `orbit.go`, for contexts.** `orbit.go/natscontext` exports one
function - `Connect` - and cannot enumerate contexts, which a GUI picker needs.
It also parses `user_jwt` and never uses it, resolves `nsc` lookups and discards
the result, and hard-errors on Windows cert-store contexts.
`github.com/nats-io/jsm.go/natscontext` is what the `nats` CLI itself runs, and
`jsm.go/serverdata` then comes free in the same module for `$SYS` scatter-gather.
Measured cost: `nats.go` alone links to 6.3MB stripped; adding `natscontext`
takes it to 12MB; adding `serverdata` and the `nats-server` response types costs
only **1MB more**. Measured again when phase 3 landed it for real: the binary
went from 12.1MB to 14.6MB, so `natscontext` costs about **2.5MB**.

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

Test rig config lives in the scratchpad, not the repo. Two rigs, on separate
ports so both run at once:

- a single server on 4222 with JetStream, `http_port: 8222`, an `APP` account
  and a `SYS` account with `system_account: SYS`. `APP` also carries an **nkey
  user**, which is what makes "the backend reads a credential file the browser
  could never touch" testable locally - a `.creds` file would need the server in
  operator mode.
- a three node cluster on 4322-4324 (monitoring 8322-8324, routes 6322-6324),
  same accounts, for the fan-out and the live grid.
- an **operator-mode** server on 4422 (monitoring 8422) for `.creds` auth,
  which plain accounts/users config cannot express. `nsc` is not installed on
  this machine and is not needed: `jwt/v2` and `nkeys` are already dependencies,
  and about a hundred lines generates an operator, a SYS and an APP account
  (APP with `Limits.JetStreamLimits.{Disk,Memory}Storage = -1`), a user per
  account written with `jwt.FormatUserConfig`, and a config carrying
  `operator:`, `system_account:`, `resolver: MEMORY` and `resolver_preload`.
  Generate it into the scratchpad; it must not ship in `cmd/`.

Set `server_name` in any test rig. Without it NATS reports the 56-character
server ID as the name, which is worth seeing once - it is what caught the grid's
name column pushing every other column out of the pane - but is unreadable
otherwise.

**nats-server on Windows accepts `--signal` only when installed as a service**,
so `ldm` and `stop` return "Access is denied" against a console-run server and a
*graceful* shutdown cannot be produced on this machine. `$SYS.REQ.SERVER.<id>.LDM`
is not an alternative - despite the name it is a *client* operation, and sending
it an empty body logs "Error unmarshalling kick client request". So the
SHUTDOWN and LAMEDUCK grid marking is covered by a unit test that feeds
`handleEvent` a crafted message, while the pushed-event path itself is proven
live with CONNECT, DISCONNECT and AUTH.ERR. The hard-kill case - no event at
all, row goes stale, census prunes it - is the one verified end to end, and is
also the more common one in practice.

Context tests must call `t.Setenv("XDG_CONFIG_HOME", t.TempDir())` first.
natscontext reads that on every call and caches nothing, so it is enough to
keep a test off the developer's real `nats` contexts - and running them
against those would be destructive.

## Testing

`make test` runs without the race detector on purpose - `-race` needs cgo and a
C compiler, which the global `CGO_ENABLED=0` and a typical Windows dev box do
not provide. Use `make test-race` where a toolchain exists.

Coverage is currently thin and concentrated on the logic that is easy to get
silently wrong: the subject filter rules, the KV operation mapping, the
batching/drop accounting, the context editor's round-trip fidelity, and the
monitoring rate arithmetic. The HTTP and NATS layers are covered by driving the
real UI against a real server, not by unit tests.

## Status

- **Phase 1 (done):** server, auth, connect/disconnect/status/info,
  publish/request, subscribe with the system-subject filter, message push.
- **Phase 2 (done):** KV buckets, keys, history, live watch; JetStream streams,
  consumers, message-range fetch and live tail.
- **Phase 3 (done):** NATS CLI contexts - list, create, edit, delete, set the
  CLI default, and connect through one.
- **Phase 4 (done):** monitoring - data connection, separate `$SYS` connection
  and HTTP `:8222`, each independently configurable; a live cluster grid with
  rates, and an event feed.
