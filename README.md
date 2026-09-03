# nats-desk 🪨

A local [NATS](https://nats.io/) client. One binary: it serves its own UI and
connects to NATS over the **native protocol**.

No `websocket {}` listener to configure. No Electron. No install ceremony.

```bash
nats-desk
```

That's it — your browser opens on the app.

## Why this exists

Browsers cannot open a raw TCP connection, so a browser-only NATS client can
only reach servers whose operator has enabled a websocket listener. Most
haven't. [stone-age-io/nats-client](https://github.com/stone-age-io/nats-client)
is that browser client, and its README has to open with a "Crucial Step"
explaining the server config you must change before it will work at all.

`nats-desk` moves the connection into a Go process using `nats.go`, so it talks
to **any** NATS server as-is. That also unlocks the things a browser structurally
cannot do: mutual TLS and custom CAs, importing your `nats` CLI contexts, the
`:8222` monitoring endpoints, and `$SYS` cluster monitoring.

## Install

Download a binary from the releases page, or:

```bash
go install github.com/stone-age-io/nats-desk/cmd/nats-desk@latest
```

Installing this way means no Gatekeeper or SmartScreen prompt — those are
triggered by the quarantine mark that browser downloads apply, and `go install`,
`brew`, `scoop` and `curl` do not set it.

## Use

```bash
nats-desk                  # start and open a browser
nats-desk --no-browser     # print the URL instead of opening one
nats-desk --port 4200      # listen elsewhere (see the note below)
nats-desk --idle-timeout 5m  # exit 5 minutes after the last tab closes
```

Running it twice does not start a second copy — the second invocation notices
the first and just opens a browser at it.

### Install it as an app

In Chrome or Edge, use **Install** from the address bar or the ⋮ menu. You get a
windowed app with its own icon and taskbar entry, and no browser chrome.

A caveat worth knowing: an installed app's identity is its origin, and the
origin includes the port. If you change `--port`, the installed app and its
saved settings stay on the old one. Pick a port once.

## Features

### Connection
- **Native NATS** over `nats://` and `tls://`
- **Auth:** user/password, token, and `.creds` (JWT + NKey) files
- **Named profiles** with an explicit opt-in before any credential is stored
- **Live RTT** in the connection pill

### Messaging
- **Publish** with payloads and headers; `Ctrl+Enter` to send
- **Subscribe** with real-time logging, JSON formatting and syntax highlighting
- **Request/Reply** with a configurable timeout
- **Templates** — save subject/payload/header sets, like a Postman collection
- **Hide system subjects** — subscribing to `>` also picks up every `$JS`,
  `$SYS`, `$KV` and `_INBOX` message, which is rarely what "everything" means.
  The filter runs in the backend, so the traffic never reaches your browser.
- **Binary-safe** — payloads are carried as bytes end to end, so non-UTF-8
  messages show a real hex view rather than a lossy stub
- **Honest under load** — a firehose is batched and capped so the UI stays
  responsive, and anything dropped is *counted and reported*, never silently
  skipped
- **QoL:** persistent subscriptions per server, click-to-fill, subject history,
  pause/resume, and a log direction toggle that only auto-follows when you are
  already at the live edge

### Coming
KV browsing, JetStream stream and consumer management, `nats` CLI context
import, and server monitoring via both `:8222` and the `$SYS` account. The UI
tells you plainly when you reach something that isn't wired up yet.

## Security

The backend connects to NATS with your saved credentials, so its port is
treated as sensitive:

- It listens on **127.0.0.1 only**, never on all interfaces.
- Every request needs a session cookie, obtained once from a random token that
  is passed in the URL the app opens and then stripped from the address bar.
- The `Host` header is checked against an exact allowlist, which is what stops a
  malicious page from reaching the port by pointing its own domain at
  `127.0.0.1` (DNS rebinding).
- No CORS headers are sent.

If you use `--no-browser`, the printed URL contains that one-time token. Treat
it like a password.

## Building

Requires Go 1.25+ and Node 18+.

```bash
make build        # frontend + binary for this platform
make build-all    # every supported target, from this one machine
make test
```

`CGO_ENABLED=0` throughout, so a single machine cross-compiles Linux
(amd64/arm64), macOS (amd64/arm64), Windows (amd64/arm64) and FreeBSD without a
CI matrix.

For frontend work with hot reload, run `make dev` and then `npm run dev` in
`frontend/` — Vite proxies the API and WebSocket to the Go process.

## License

See [LICENSE](LICENSE).
