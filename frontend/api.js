// ============================================================================
// BACKEND CLIENT
// ============================================================================
// Drop-in replacement for the old nats-client.js. Every exported name and
// signature is the same, so main.js only changed its import line - the NATS
// connection now lives in the Go process instead of in this tab.
//
// Two channels:
//   - fetch, for anything request/response
//   - one WebSocket, for everything the server pushes (messages, KV watch,
//     stream tail, connection status, RTT)
//
// Functions that used to be synchronous are async now. main.js already
// awaited most of them; the few that did not are noted at their call sites.

// ============================================================================
// PAYLOAD ENCODING
// ============================================================================
// Payloads cross the wire base64-encoded, so a message is real bytes rather
// than a lossy string. The old client decoded on arrival and could only ever
// show a hex stub for binary; now the bytes survive and only the *display*
// falls back to hex.

const strictDecoder = new TextDecoder("utf-8", { fatal: true });
const encoder = new TextEncoder();

function b64ToBytes(b64) {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function bytesToB64(bytes) {
  let bin = "";
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin);
}

/**
 * Decode a payload for display, detecting binary data.
 * Same contract as the old decodePayload: a string, or a hex preview.
 */
function decodePayload(b64) {
  if (b64 == null || b64 === "") return "";
  const bytes = b64ToBytes(b64);
  try {
    return strictDecoder.decode(bytes);
  } catch {
    const preview = Array.from(bytes.slice(0, 64))
      .map((b) => b.toString(16).padStart(2, "0"))
      .join(" ");
    return `[Binary: ${bytes.length} bytes]\n${preview}${bytes.length > 64 ? " …" : ""}`;
  }
}

// ============================================================================
// HTTP
// ============================================================================

async function call(method, path, body) {
  let res;
  try {
    res = await fetch(path, {
      method,
      headers: body === undefined ? undefined : { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch (e) {
    // fetch only rejects on transport failure, which here means the Go
    // process is gone - the browser tab outlived it.
    throw new Error("nats-desk backend is not responding. Is it still running?");
  }

  if (res.status === 401) {
    throw new Error("Session expired. Relaunch nats-desk to reconnect.");
  }

  const text = await res.text();
  let payload = null;
  if (text) {
    try {
      payload = JSON.parse(text);
    } catch {
      throw new Error(text.slice(0, 200));
    }
  }

  if (!res.ok) {
    throw new Error((payload && payload.error) || `Request failed (${res.status})`);
  }
  return payload;
}

const get = (p) => call("GET", p);
const post = (p, b) => call("POST", p, b);
const put = (p, b) => call("PUT", p, b);
const del = (p) => call("DELETE", p);

// ============================================================================
// PUSH CHANNEL
// ============================================================================

let ws = null;
let wsReady = null;
const subHandlers = new Map(); // subscription id -> onMessage
let statusHandler = null;
let statsHandler = null;
let kvWatchHandler = null;
let tailHandler = null;
let droppedHandler = null;
let monitorHandlers = {};

/**
 * Called when the backend had to drop messages to keep the UI responsive.
 * Set by main.js so the count surfaces as a toast rather than a silent gap.
 */
export function setDroppedHandler(fn) {
  droppedHandler = fn;
}

/**
 * Monitoring arrives unprompted, so it needs handlers rather than a return
 * value: the cluster grid, the event feed and the source status all change
 * because a server said something, not because the UI asked.
 */
export function setMonitorHandlers({ onServers, onEvent, onStatus }) {
  monitorHandlers = { onServers, onEvent, onStatus };
}

function wsUrl() {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${location.host}/ws`;
}

/**
 * Open the push channel, or return the existing one.
 *
 * Resolves once the socket is open so callers can be sure a subscription
 * created straight afterwards will not miss its first messages.
 */
function connectWs() {
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
    return wsReady;
  }

  wsReady = new Promise((resolve, reject) => {
    ws = new WebSocket(wsUrl());
    ws.onopen = () => resolve();
    ws.onerror = () => reject(new Error("Could not open the push channel"));
    ws.onclose = () => {
      ws = null;
      wsReady = null;
    };
    ws.onmessage = (ev) => {
      let frame;
      try {
        frame = JSON.parse(ev.data);
      } catch {
        return;
      }
      dispatch(frame);
    };
  });
  return wsReady;
}

function dispatch(frame) {
  switch (frame.type) {
    case "batch":
      // A burst arrives as one frame so it costs one parse and one render
      // pass rather than hundreds. Order within the batch is preserved.
      for (const f of frame.frames) dispatch(f);
      break;
    case "dropped":
      if (droppedHandler) droppedHandler(frame.count);
      break;
    case "msg": {
      const h = subHandlers.get(frame.subId);
      if (h) h(frame.subject, decodePayload(frame.data), false, frame.headers || null);
      break;
    }
    case "status":
      // Track it here so isConnected() can stay synchronous, which is what
      // main.js expects at its call sites.
      connected = frame.state === "connected";
      if (statusHandler) statusHandler(frame.state, frame.err ? new Error(frame.err) : null);
      break;
    case "stats":
      if (statsHandler) statsHandler({ rtt: frame.rtt });
      break;
    case "kv":
      if (kvWatchHandler) kvWatchHandler(frame.key, frame.operation);
      break;
    case "monitor_servers":
      if (monitorHandlers.onServers) monitorHandlers.onServers(frame.servers || []);
      break;
    case "monitor_event":
      if (monitorHandlers.onEvent) monitorHandlers.onEvent(frame);
      break;
    case "monitor_status":
      if (monitorHandlers.onStatus) monitorHandlers.onStatus(frame.status);
      break;
    case "tail":
      if (tailHandler) {
        tailHandler({
          seq: frame.seq,
          subject: frame.subject,
          data: decodePayload(frame.data),
          time: frame.time,
          headers: frame.headers || null,
        });
      }
      break;
  }
}

// ============================================================================
// CONNECTION
// ============================================================================

/**
 * Connect. Pass `authOptions.context` to connect through a NATS CLI context
 * instead, in which case nothing else is sent: the context carries its own URL
 * and its own credentials, and the backend resolves the creds file, nkey or
 * client certificate that this tab could never read itself.
 *
 * The response carries `url` - what the backend actually dialled - which is
 * the only way the UI learns a context's server address.
 */
export async function connectToNats(url, authOptions, onStatusChange, onStats) {
  statusHandler = onStatusChange;
  statsHandler = onStats;

  await connectWs();
  const body = authOptions.context
    ? { context: authOptions.context }
    : {
        url,
        credsText: authOptions.credsText || "",
        user: authOptions.user || "",
        pass: authOptions.pass || "",
        token: authOptions.token || "",
      };
  const res = await post("/api/connect", body);
  connected = true;
  serverInfo = res.info || null;
  return res;
}

export async function disconnect() {
  subHandlers.clear();
  kvWatchHandler = null;
  tailHandler = null;
  connected = false;
  serverInfo = null;
  try {
    await post("/api/disconnect");
  } catch {
    // Disconnect is best-effort: if the backend already dropped the
    // connection there is nothing left to tear down.
  }
}

let connected = false;

export function isConnected() {
  return connected;
}

let serverInfo = null;

export function getServerInfo() {
  return serverInfo;
}

// ============================================================================
// SUBJECT CLASSIFICATION
// ============================================================================
// The backend owns the actual filtering - it drops system subjects before a
// message crosses the wire, so isSystemSubject lives in Go now. This one stays
// because the UI needs the answer before any round trip, to know whether the
// "hide system subjects" checkbox means anything for what has been typed.

/**
 * Would excluding system subjects change anything for this pattern?
 *
 * Only meaningful when the first token is a wildcard: `>` and `*.foo` sweep up
 * $/_ traffic nobody asked for, while `$JS.EVENT.>` asks for it deliberately
 * and would otherwise be filtered down to nothing.
 */
export function canExcludeSystem(subject) {
  const first = subject.split(".")[0];
  return first === ">" || first === "*";
}

// ============================================================================
// PUB / SUB
// ============================================================================

export async function subscribe(subject, onMessage, opts = {}) {
  await connectWs();
  const res = await post("/api/sub", {
    subject,
    excludeSystem: !!opts.excludeSystem,
  });
  subHandlers.set(res.id, onMessage);
  return res;
}

export async function unsubscribe(id) {
  subHandlers.delete(id);
  return await del(`/api/sub/${id}`);
}

export async function publish(subject, payload, headersJson) {
  return await post("/api/publish", {
    subject,
    data: bytesToB64(encoder.encode(payload)),
    headers: parseHeaders(headersJson),
  });
}

export async function request(subject, payload, headersJson, timeout) {
  const res = await post("/api/request", {
    subject,
    data: bytesToB64(encoder.encode(payload)),
    headers: parseHeaders(headersJson),
    timeoutMs: timeout,
  });
  return {
    subject: res.subject,
    data: decodePayload(res.data),
    headers: res.headers || null,
  };
}

/**
 * Parse the headers JSON string the composer produces into a plain object.
 * Values are always arrays so a repeated header survives the round trip.
 */
function parseHeaders(jsonStr) {
  const val = (jsonStr || "").trim();
  if (!val) return null;
  let obj;
  try {
    obj = JSON.parse(val);
  } catch {
    throw new Error("Invalid Headers JSON");
  }
  const out = {};
  for (const k in obj) {
    out[k] = Array.isArray(obj[k]) ? obj[k].map(String) : [String(obj[k])];
  }
  return out;
}

// ============================================================================
// KV STORE
// ============================================================================

/**
 * Encode a KV key for use in a URL path.
 *
 * Keys may legitimately contain "/", and the route uses a trailing wildcard so
 * that structure has to survive; everything else still needs escaping. So the
 * key is split on "/" and each segment encoded separately.
 */
function keyPath(key) {
  return String(key).split("/").map(encodeURIComponent).join("/");
}

export async function getKvBuckets() {
  return await get("/api/kv/buckets");
}

export async function createKvBucket(config) {
  return await post("/api/kv/buckets", config);
}

export async function updateKvBucket(config) {
  return await call("PUT", "/api/kv/buckets", config);
}

export async function destroyKvBucket(bucket) {
  return await del(`/api/kv/buckets/${encodeURIComponent(bucket)}`);
}

export async function openKvBucket(bucket) {
  await connectWs();
  return await post("/api/kv/open", { bucket });
}

export async function getKvStatus() {
  return await get("/api/kv/status");
}

/**
 * Watch the open bucket.
 *
 * The handler is registered before the backend is told to start, because the
 * watcher replays every existing key the instant it opens - that replay is
 * how the key list gets populated at all, so losing it means an empty list.
 */
export async function watchKvBucket(onKeyChange) {
  await connectWs();
  kvWatchHandler = onKeyChange;
  await post("/api/kv/watch", {});
  return {
    stop() {
      kvWatchHandler = null;
      // Best effort: if the backend has already dropped the watcher (bucket
      // closed, connection lost) there is nothing left to stop.
      del("/api/kv/watch").catch(() => {});
    },
  };
}

export async function getKvValue(key) {
  const res = await get(`/api/kv/keys/${keyPath(key)}`);
  if (!res) return null;
  return { value: decodePayload(res.value), revision: res.revision };
}

export async function getKvHistory(key) {
  const hist = await get(`/api/kv/history/${keyPath(key)}`);
  return (hist || []).map((e) => ({
    revision: e.revision,
    operation: e.operation,
    value: e.value ? decodePayload(e.value) : null,
    created: e.created,
  }));
}

export async function putKvValue(key, value) {
  return await call("PUT", `/api/kv/keys/${keyPath(key)}`, {
    value: bytesToB64(encoder.encode(value)),
  });
}

export async function deleteKvValue(key) {
  return await del(`/api/kv/keys/${keyPath(key)}`);
}

export async function purgeKvValue(key) {
  return await post(`/api/kv/purge/${keyPath(key)}`, {});
}

// ============================================================================
// JETSTREAM - STREAMS
// ============================================================================

const streamPath = (name) => `/api/streams/${encodeURIComponent(name)}`;

export async function getStreams() {
  return await get("/api/streams");
}

export async function getStreamInfo(name) {
  return await get(streamPath(name));
}

export async function createStream(config) {
  return await post("/api/streams", config);
}

export async function updateStream(config) {
  return await call("PUT", "/api/streams", config);
}

export async function purgeStream(name) {
  return await post(`${streamPath(name)}/purge`, {});
}

export async function deleteStream(name) {
  return await del(streamPath(name));
}

// ============================================================================
// JETSTREAM - CONSUMERS
// ============================================================================

export async function getConsumers(stream) {
  return await get(`${streamPath(stream)}/consumers`);
}

export async function getConsumerInfo(stream, consumer) {
  return await get(`${streamPath(stream)}/consumers/${encodeURIComponent(consumer)}`);
}

export async function createConsumer(stream, config) {
  return await post(`${streamPath(stream)}/consumers`, config);
}

export async function updateConsumer(stream, consumer, config) {
  return await call("PUT", `${streamPath(stream)}/consumers/${encodeURIComponent(consumer)}`, config);
}

export async function deleteConsumer(stream, consumer) {
  return await del(`${streamPath(stream)}/consumers/${encodeURIComponent(consumer)}`);
}

// ============================================================================
// JETSTREAM - MESSAGES
// ============================================================================

export async function getStreamMessageRange(name, startSeq, endSeq, subjectFilter, max) {
  const q = new URLSearchParams({
    start: String(startSeq),
    end: String(endSeq),
    max: String(max),
  });
  if (subjectFilter) q.set("filter", subjectFilter);

  const msgs = await get(`${streamPath(name)}/messages?${q}`);
  return (msgs || []).map((m) => ({
    seq: m.seq,
    subject: m.subject,
    data: decodePayload(m.data),
    time: m.time,
    headers: m.headers || null,
  }));
}

export async function startStreamTail(name, subjectFilter, onMsg) {
  await connectWs();
  tailHandler = onMsg;
  try {
    return await post(`${streamPath(name)}/tail`, { filter: subjectFilter || "" });
  } catch (e) {
    tailHandler = null;
    throw e;
  }
}

export function stopStreamTail() {
  tailHandler = null;
  del("/api/tail").catch(() => {});
}

export function isTailing() {
  return tailHandler !== null;
}

// ============================================================================
// NATS CLI CONTEXTS
// ============================================================================
// The same files `nats context` reads and writes, on this machine. Names go
// through encodeURIComponent because a context name is only barred from
// containing "/", "\\" and ".." - spaces and wildcards are legal and in the
// wild.

export async function getContexts() {
  return get("/api/contexts");
}

/** The context file's own JSON, verbatim, plus where it lives on disk. */
export async function getContext(name) {
  return get(`/api/contexts/${encodeURIComponent(name)}`);
}

export async function saveContext(name, config) {
  return put(`/api/contexts/${encodeURIComponent(name)}`, config);
}

export async function deleteContext(name) {
  return del(`/api/contexts/${encodeURIComponent(name)}`);
}

/** Make this the context every other NATS tool on the machine defaults to. */
export async function selectContext(name) {
  return post(`/api/contexts/${encodeURIComponent(name)}/select`);
}

// ============================================================================
// MONITORING
// ============================================================================
// Three sources, each configured on its own. See internal/monitor for why they
// are not interchangeable.

export async function getMonitorStatus() {
  return get("/api/monitor/status");
}

export async function getMonitorServers() {
  return get("/api/monitor/servers");
}

/** Ask every server now instead of waiting for its next heartbeat. */
export async function refreshMonitorServers() {
  return post("/api/monitor/refresh");
}

export async function getMonitorAccount() {
  return get("/api/monitor/account");
}

/** Opens the second, system-account connection. Pass { context } or { url, user, pass }. */
export async function connectMonitorSys(opts) {
  await connectWs();
  return post("/api/monitor/sys", opts);
}

export async function disconnectMonitorSys() {
  return del("/api/monitor/sys");
}

export async function setMonitorHttp({ bases, ca = "", insecure = false }) {
  return post("/api/monitor/http", { bases, ca, insecure });
}

export async function clearMonitorHttp() {
  return del("/api/monitor/http");
}

/** One endpoint, fanned out over $SYS to every server in the cluster. */
export async function getMonitorEndpoint(name) {
  return get(`/api/monitor/endpoint/${encodeURIComponent(name)}`);
}

/** The same endpoint asked of each configured :8222 URL directly. */
export async function getMonitorHttpEndpoint(name) {
  return get(`/api/monitor/http/${encodeURIComponent(name)}`);
}
