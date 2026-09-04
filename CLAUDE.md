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
- **App feel from the binary, not from the PWA.** The exe opens a Chromium
  `--app=` window: chromeless, its own taskbar button, nothing to install. The
  PWA install still works and is still offered - `localhost` is a secure
  context - but it is no longer the way in, because a PWA shortcut only
  navigates and cannot start the process it needs. See "The PWA cannot be the
  entry point" below.
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

# Regenerate the Windows icon and version resources; commit the .syso output.
# Not part of build - see the Makefile comment for why.
make winres VERSION=1.0.0

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
│   ├── index.html style.css
│   └── public/                 # manifest.json, icons, sw.js, offline.html
├── internal/
│   ├── server/                 # HTTP: embed + SPA fallback, token, Host allowlist
│   │   ├── server.go           # fixed port, single instance, idle shutdown
│   │   ├── security.go         # token -> cookie, Host allowlist
│   │   ├── spa.go              # asset caching, SPA fallback
│   │   └── addrinuse_*.go      # EADDRINUSE detection, per platform
│   ├── api/                    # REST handlers
│   ├── ws/                     # push channel: batching, drop accounting
│   ├── natsconn/               # the NATS connection, pub/sub, subject rules
│   ├── browse/                 # open a URL: a tab, or a chromeless app window
│   ├── contexts/               # the nats CLI's own context store
│   ├── monitor/                # three monitoring sources, a grid and a feed
│   ├── appdir/                 # the two per-user directories we write to
│   ├── applog/                 # stderr, or a file when there is no console
│   ├── autostart/              # start at sign-in: Run key, plist, .desktop
│   ├── scheme/                 # the natsdesk:// handler
│   └── buildinfo/              # the version, stamped in by the linker
└── Makefile
```

## Things that are the way they are for a reason

**The PWA cannot be the entry point, and that is not fixable from the browser
side.** An installed PWA's shortcut only *navigates* to
`http://127.0.0.1:4111`. With nothing listening it lands on the browser's own
connection error, inside a chromeless window with no address bar - a dead end
with nothing in it to press. A page cannot start a process. So the binary owns
the lifecycle and the binary is what you click; three separate mechanisms
patch the remaining gap, and each is needed for a different reason:

- an **app window** (`--app=`), so clicking the exe already gives the
  chromeless window the PWA was wanted for, with nothing installed
- a **persisted token**, so a window that outlives the process still
  authenticates when the process comes back
- **autostart** and a **`natsdesk://` handler**, so the PWA shortcut has
  something running to reach, or a way to start it

**The Windows binary is linked `-H=windowsgui`, so it has no standard handles
at all.** That is what stops a console flashing up on every launch, and it
means `fmt.Print` and a stderr logger both write into a void. `internal/applog`
picks the destination by calling `os.Stderr.Stat()` and falling back to a file
- not by a build tag, because `make dev` runs `go run` and produces a console
binary from the same source, which must keep printing to the terminal. The
same check does the right thing for a shell that redirects our output: a pipe
stats fine, so the log follows the pipe.

**The log is appended to, not truncated per run, because runs overlap.** Every
double-click of an already-running copy starts a second process that lives
just long enough to find the port taken and exit. Truncating on open would
mean each of those wiped the log of the instance actually doing the work.
Growth is bounded by a size check at open instead.

**The startup URL is printed to stdout and never logged.** It carries the
token. The log is now a file that outlives the run.

**The default browser is read out of the registry, not scanned for.** Edge is
installed on every Windows machine, so any fixed-order path scan hands a
Chrome user an Edge window - a different profile, none of their extensions,
and any installed PWA living somewhere else entirely. `browse` follows the
same two hops Explorer does, `UserChoice` then the ProgId's open command, and
only falls back to a scan when that resolves to something with no `--app`
flag, which in practice means Firefox.

**The session token is persisted, and the cookie is persistent too.** Both
halves are required and neither is sufficient. A token that changed per
process would 401 every open window on restart; a session cookie would be
discarded when the browser closed, so the installed app would still come back
unauthenticated every morning. This is a real if narrow weakening of the token
guard, and `internal/server/security.go` carries the argument for why it is
worth it - in short, the credentials the token protects are already files in
the same user profile. Deleting the token file is the revoke.

**A token file is length-checked, not just decoded.** Go's base64 decoder
*ignores* embedded newlines rather than rejecting them, so a token with a
newline in it decodes to exactly 32 bytes and passes a decode check - and then
never matches anything, because `http.SetCookie` strips the newline back out
of the cookie it writes. The symptom is an app that is permanently
unauthenticated for no visible reason. A *trailing* newline is trimmed rather
than rejected, because any editor opening that file adds one.

**Autostart is a registry Run value, and deliberately not a service.** A
service runs as another account, and every credential this app opens NATS with
- `.creds` files, `nats` CLI contexts - lives in the user's own profile. It
would also mean an installer, administrator rights, and one shared port for
every user signed in to the machine, which is a credential leak across
accounts in a program whose whole security model is "loopback only, and
authenticated". The Run key needs none of that. A Startup folder shortcut
would have done equally well but requires building a `.lnk` through COM; this
is a string.

**Autostart and the scheme handler are both re-checked on every start.** Both
record an absolute path to the executable. A binary that is moved, renamed or
re-downloaded silently stops being the one that gets launched, and the failure
is invisible - nothing happens at sign-in, and nothing says why. `Sync` only
rewrites when the stored string differs, so the entry must be byte-stable
between calls or it would rewrite on every start forever.

**`sw.js` is no longer empty, and it never actually shipped when it was.** It
sat at `frontend/` rather than `frontend/public/`, so vite never copied it and
nothing registered it; the PWA was installable on the manifest and icons
alone. It is now a real worker with exactly one job: serve `offline.html` when
a *navigation* fails. Nothing else is cached, and that restraint is the point
- an app shell served from cache while the backend is down looks like a
working app and then fails every single call. Non-navigation requests are not
intercepted at all, so a failed API call still surfaces as the transport error
`api.js` already reports.

**The offline page polls as well as offering a button.** The button navigates
to `natsdesk://start`, which is the only way a page can ask an operating
system to start a program. The poll is what recovers the window when the
backend comes up some other way - the binary double-clicked, or a sign-in
autostart finishing late - and it only runs while the document is visible, so
an abandoned tab is not left hitting the port forever.

**`natsdesk://start` must not open a window; `natsdesk://open` must.** Start is
sent by a page that is already open and polling, so a second window would be
noise. Open is the recovery path for the one case start cannot fix: a browser
that has lost its cookie, where reloading in place reaches the app shell and
then 401s. Note that the shell delivers the URL with a trailing slash added -
`natsdesk://start/` - which is the common form, not the odd one.

**macOS gets no URL scheme.** Schemes come from `CFBundleURLTypes` in an
application bundle's `Info.plist`, and a bare executable has no bundle.
Building one would mean shipping a directory instead of a file, which is the
packaging this project is arranged to avoid. The offline page's button simply
does nothing there, and the page says so.

**An `--app=` window's taskbar icon comes from the *favicon*, not from the
manifest.** That is why `index.html` points `rel="icon"` at `icon-192/512.png`
and not at `logo.svg`. The bare mark is edge to edge with a knocked-out
transparent interior, which is correct inside the UI - on a surface we control
- and wrong on a taskbar, where it rendered as a hollow ring with no padding
beside properly treated icons. The PNGs carry the launcher treatment: opaque
white behind the knockout and margin around the disc, matching the other
Stone-Age.io apps. `logo.svg` stays in `public/` as the vector source and is
deliberately referenced by nothing.

Three surfaces take an icon and they resolve differently, which is why getting
one right does not get the others right: the **exe and any pinned shortcut**
use the committed `.syso`, the **app window** uses the favicon, and an
**installed PWA** uses the manifest icons. All three now come from the same
PNG. To check the taskbar one for real rather than by eye, send `WM_GETICON`
to the window and save the handle through `Icon.FromHandle` - the padding and
the opaque corners are the tell.

**The `.syso` resources are committed.** That is what makes a plain `go build`
produce an exe with an icon, and it keeps the release build free of a
downloaded tool. `make winres` regenerates them. One trap: Windows ignores the
entire version string block unless `FileVersion` **and** `ProductVersion`
appear in `StringFileInfo` as strings - the numeric `FixedFileInfo` values are
not enough, and without them the properties dialog shows nothing at all while
the icon still works, which makes it look like the resource did not build.

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

**Service workers cannot be tested in the Browser pane at all.**
`'serviceWorker' in navigator` is true and `isSecureContext` is true, but
`register()` fails with "An unknown error occurred when fetching the script"
while the same script fetches fine with 200 and the right content type. The
worker registers normally in real Chrome. Two ways to check it from outside
the browser, both used to verify this for real:

- Chrome's own registration store, `%LOCALAPPDATA%\Google\Chrome\User
  Data\Default\Service Worker\Database` - grep the `.log`/`.ldb` files for the
  origin, and a `REG:` entry naming `/sw.js` means it registered.
- **Window titles**, which is the good one. `offline.html` is titled
  "nats-desk is not running" and the app is titled "NATS Client", and an
  `--app=` window's title has no " - Google Chrome" suffix where an ordinary
  tab's does. So `EnumWindows` proves three separate things without driving a
  browser: that the launch produced an *app* window rather than a tab, that
  the offline page rendered with nothing listening, and that it recovered
  itself once the backend came up.

To produce the cold-PWA-click case by hand, stop the backend and run
`chrome.exe --app=http://127.0.0.1:<port>/` directly. That is exactly what the
installed shortcut does.

`--app=` windows are deduplicated by Chrome: asking for one whose URL already
has a window focuses the existing window instead of opening a second. Close
the window first or a launch test will look like it did nothing.

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
batching/drop accounting, the context editor's round-trip fidelity, the
monitoring rate arithmetic, the `natsdesk://` argument parsing, and the token
file's accept/reject rules. The HTTP and NATS layers are covered by driving the
real UI against a real server, not by unit tests.

No test writes to the real Run key, the real `Software\Classes`, or the real
token path. Turning autostart on for whoever is running the suite is not a
side effect a test is entitled to, so `internal/autostart` tests only the
entry string - including that it is byte-stable, because `Sync` compares it
and an unstable one would rewrite the registry on every start. The registry
round-trip itself is verified by driving the real app.

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
- **Phase 5 (done):** desktop integration - no console window, an app-mode
  launch, a persisted session, autostart at sign-in, and a `natsdesk://`
  handler with an offline page so an installed PWA can start its own backend.
