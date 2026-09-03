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

/**
 * Called when the backend had to drop messages to keep the UI responsive.
 * Set by main.js so the count surfaces as a toast rather than a silent gap.
 */
export function setDroppedHandler(fn) {
  droppedHandler = fn;
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

export async function connectToNats(url, authOptions, onStatusChange, onStats) {
  statusHandler = onStatusChange;
  statsHandler = onStats;

  await connectWs();
  const res = await post("/api/connect", {
    url,
    credsText: authOptions.credsText || "",
    user: authOptions.user || "",
    pass: authOptions.pass || "",
    token: authOptions.token || "",
  });
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
// NOT YET PORTED
// ============================================================================
// Phase 2 lands KV and JetStream. These throw rather than silently doing
// nothing so a gap shows up as a toast instead of a dead button.

const later = (what) => async () => {
  throw new Error(`${what} is not available yet in nats-desk`);
};

export const getKvBuckets = later("KV");
export const createKvBucket = later("KV");
export const openKvBucket = later("KV");
export const getKvStatus = later("KV");
export const updateKvBucket = later("KV");
export const watchKvBucket = later("KV");
export const getKvValue = later("KV");
export const getKvHistory = later("KV");
export const putKvValue = later("KV");
export const deleteKvValue = later("KV");
export const purgeKvValue = later("KV");
export const destroyKvBucket = later("KV");

export const getStreams = later("Stream management");
export const createStream = later("Stream management");
export const updateStream = later("Stream management");
export const getStreamInfo = later("Stream management");
export const purgeStream = later("Stream management");
export const deleteStream = later("Stream management");
export const getConsumers = later("Consumer management");
export const getConsumerInfo = later("Consumer management");
export const createConsumer = later("Consumer management");
export const updateConsumer = later("Consumer management");
export const deleteConsumer = later("Consumer management");
export const getStreamMessageRange = later("Stream messages");
export const startStreamTail = later("Stream tail");

export function stopStreamTail() {
  tailHandler = null;
}

export function isTailing() {
  return tailHandler !== null;
}
