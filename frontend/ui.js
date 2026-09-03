// ============================================================================
// UI RENDERING
// ============================================================================
// All DOM painting lives here. No NATS logic.
//
// Layout contract: every pane owns exactly one scroll region (.list-scroll).
// Rendering helpers write into those regions and nothing else, so no renderer
// needs to know about the surrounding grid.

import { els } from "./dom.js";
import * as utils from "./utils.js";

// ============================================================================
// CONFIGURATION CONSTANTS
// ============================================================================

// Maximum characters to syntax highlight before truncation
// Above this size, browser slows down significantly during JSON.parse + highlighting
const MAX_PRETTY_SIZE = 20000;

// Maximum log entries to keep in memory for export
const MAX_LOG_HISTORY = 1000;

// Maximum messages to display in DOM at once
const MAX_VISIBLE_MESSAGES = 200;

// Cap on rendered tail entries so a busy stream doesn't grow the DOM forever
const MAX_TAIL_MESSAGES = 200;

// How long toast notifications stay visible (ms)
const TOAST_DURATION_MS = 3500;

// ============================================================================
// SMALL DOM HELPERS
// ============================================================================

/** Replace a container's contents with a centred empty/error message. */
export function setEmpty(container, message, isError = false) {
  container.replaceChildren();
  // A <ul> may only contain <li>, so match the tag to the container
  const el = document.createElement(container.tagName === "UL" ? "li" : "div");
  el.className = "empty" + (isError ? " is-error" : "");
  el.textContent = message;
  container.append(el);
}

/** Drop an empty-state placeholder before inserting real content. */
function clearEmpty(container) {
  container.querySelector(":scope > .empty")?.remove();
}

/** Mark one row in a container active, clearing the rest. */
function activateRow(container, rowEl) {
  container.querySelectorAll(".list-row.active").forEach((e) => e.classList.remove("active"));
  if (rowEl) rowEl.classList.add("active");
}

/** Build a clickable list row shared by buckets / keys / streams. */
function listRow(text, onClick) {
  const div = document.createElement("div");
  div.className = "list-row";
  div.textContent = text;
  div.title = text;
  div.addEventListener("click", () => onClick(text, div));
  return div;
}

// ============================================================================
// MODULE INITIALIZATION
// ============================================================================

/**
 * Delegate copy-button clicks for both message containers.
 * Call once on app initialization.
 */
export function initializeEventDelegation() {
  const onCopyClick = async (e) => {
    const btn = e.target.closest(".copy-btn");
    if (!btn) return;
    const el = document.getElementById(btn.dataset.msgId);
    if (!el) return;
    if (await utils.copyToClipboard(el.innerText)) {
      const original = btn.textContent;
      btn.textContent = "Copied!";
      setTimeout(() => (btn.textContent = original), 1000);
    }
  };

  els.messages.addEventListener("click", onCopyClick);
  els.streamMsgContainer.addEventListener("click", onCopyClick);
}

// ============================================================================
// TOASTS
// ============================================================================

export function showToast(msg, type = "info") {
  const div = document.createElement("div");
  div.className = `toast ${type}`;
  div.textContent = msg;
  els.toastContainer.append(div);

  setTimeout(() => {
    div.classList.add("hiding");
    div.addEventListener("animationend", () => div.remove());
  }, TOAST_DURATION_MS);
}

// ============================================================================
// DROPDOWNS
// ============================================================================

export function renderHistoryDatalist(elementId, items) {
  const el = document.getElementById(elementId);
  if (!el) return;
  el.replaceChildren();
  items.forEach((s) => {
    const opt = document.createElement("option");
    opt.value = s;
    el.append(opt);
  });
}

/**
 * Re-render a select from a list of named items, preserving the
 * placeholder option and reselecting the previous value when possible.
 */
export function renderNamedOptions(selectEl, items, placeholder) {
  const prev = selectEl.value;
  selectEl.replaceChildren();

  const ph = document.createElement("option");
  ph.value = "";
  ph.textContent = placeholder;
  selectEl.append(ph);

  items.forEach((item) => {
    const opt = document.createElement("option");
    opt.value = item.name;
    opt.textContent = item.name;
    selectEl.append(opt);
  });

  if (items.some((i) => i.name === prev)) selectEl.value = prev;
}

// ============================================================================
// SUBSCRIPTIONS
// ============================================================================

export function addSubscription(id, subject, excludeSystem = false) {
  clearEmpty(els.subList);

  const li = document.createElement("li");
  li.id = `sub-li-${id}`;

  const span = document.createElement("span");
  span.title = "Click to copy into the Publish subject";
  span.textContent = subject;
  // The badge below lives inside this span, so the click handler reads the
  // subject from here rather than from the rendered text
  span.dataset.subject = subject;

  // The subject alone no longer says what the subscription delivers, so a
  // filtered one is labelled - otherwise missing $JS traffic looks like a bug
  if (excludeSystem) {
    const tag = document.createElement("em");
    tag.className = "sub-tag";
    tag.title = "System subjects ($JS, $SYS, $KV, _INBOX...) are hidden";
    tag.textContent = "no sys";
    span.append(" ", tag);
  }

  const btn = document.createElement("button");
  btn.className = "danger";
  btn.title = `Unsubscribe from ${subject}`;
  btn.textContent = "✕";
  btn.dataset.subId = id;

  li.append(span, btn);
  els.subList.prepend(li);
}

export function removeSubscription(id) {
  document.getElementById(`sub-li-${id}`)?.remove();
  if (!els.subList.querySelector("li:not(.empty)")) {
    setEmpty(els.subList, "No subscriptions yet");
  }
}

export function updateSubCount(count) {
  els.subCount.textContent = `(${count})`;
}

export function clearSubscriptions() {
  setEmpty(els.subList, "No subscriptions yet");
  updateSubCount(0);
}

// ============================================================================
// PUBLISH HEADERS EDITOR
// ============================================================================
// Key/value rows rather than raw JSON: easier to read, and far easier to edit
// on a phone. Serialization keeps the same JSON string shape that
// nats-client.parseHeaders and saved templates already expect.

function headerRow(key = "", value = "") {
  const row = document.createElement("div");
  row.className = "header-row";

  const k = document.createElement("input");
  k.type = "text";
  k.className = "header-key";
  k.placeholder = "Header";
  k.value = key;
  k.autocomplete = "off";
  k.spellcheck = false;

  const v = document.createElement("input");
  v.type = "text";
  v.className = "header-value";
  v.placeholder = "Value";
  v.value = value;
  v.autocomplete = "off";
  v.spellcheck = false;

  const del = document.createElement("button");
  del.type = "button";
  del.className = "sm-btn danger header-remove";
  del.title = "Remove header";
  del.textContent = "✕";

  row.append(k, v, del);
  return row;
}

export function addHeaderRow(key = "", value = "") {
  const row = headerRow(key, value);
  els.headerRows.append(row);
  updateHeaderCount();
  return row;
}

/** Replace all rows from a plain object. Always leaves one blank row to type in. */
export function renderHeaderRows(obj) {
  els.headerRows.replaceChildren();
  const entries = Object.entries(obj || {});
  entries.forEach(([k, v]) => {
    els.headerRows.append(headerRow(k, Array.isArray(v) ? v.join(", ") : String(v)));
  });
  if (entries.length === 0) els.headerRows.append(headerRow());
  updateHeaderCount();
}

/** Collect the filled rows into a plain object. */
export function readHeaders() {
  const out = {};
  els.headerRows.querySelectorAll(".header-row").forEach((row) => {
    const k = row.querySelector(".header-key").value.trim();
    const v = row.querySelector(".header-value").value;
    if (k) out[k] = v;
  });
  return out;
}

/** JSON string for nats-client, or "" when no headers are set. */
export function readHeadersJson() {
  const obj = readHeaders();
  return Object.keys(obj).length ? JSON.stringify(obj) : "";
}

export function updateHeaderCount() {
  const n = Object.keys(readHeaders()).length;
  els.headerCount.textContent = n;
  els.headerCount.hidden = n === 0;
}

// ============================================================================
// KV STORE
// ============================================================================

export function renderKvBuckets(buckets, onSelect) {
  els.kvBucketList.replaceChildren();
  if (buckets.length === 0) {
    setEmpty(els.kvBucketList, "No buckets");
    return;
  }
  [...buckets].sort().forEach((b) => els.kvBucketList.append(listRow(b, onSelect)));
}

export function highlightBucket(bucket) {
  const rows = [...els.kvBucketList.querySelectorAll(".list-row")];
  activateRow(els.kvBucketList, rows.find((r) => r.textContent === bucket));
}

export function addKvKey(key, onSelect) {
  if (els.kvKeyList.querySelector(`[data-key="${CSS.escape(key)}"]`)) return;
  // First key replaces the empty-state placeholder
  els.kvKeyList.querySelector(".empty")?.remove();
  const row = listRow(key, onSelect);
  row.dataset.key = key;
  els.kvKeyList.append(row);
}

export function removeKvKey(key) {
  els.kvKeyList.querySelector(`[data-key="${CSS.escape(key)}"]`)?.remove();
  if (!els.kvKeyList.querySelector(".list-row")) {
    setEmpty(els.kvKeyList, "No keys in this bucket");
  }
}

export function highlightKvKey(key) {
  const row = els.kvKeyList.querySelector(`[data-key="${CSS.escape(key)}"]`);
  activateRow(els.kvKeyList, row);
}

export function renderKvHistory(hist, onSelect) {
  els.kvHistoryCount.textContent = hist.length;

  if (hist.length === 0) {
    setEmpty(els.kvHistoryList, "No history for this key");
    return;
  }

  els.kvHistoryList.replaceChildren();
  hist.forEach((h) => {
    const row = document.createElement("div");
    row.className = "kv-history-row";

    const left = document.createElement("div");
    const rev = document.createElement("span");
    rev.className = "rev";
    rev.textContent = `Rev ${h.revision}`;
    const op = document.createElement("span");
    op.className = "badge badge-hdr";
    op.textContent = h.operation;
    left.append(rev, " ", op);

    const when = document.createElement("div");
    when.className = "when";
    when.textContent = new Date(h.created).toLocaleString(undefined, {
      month: "numeric", day: "numeric",
      hour: "2-digit", minute: "2-digit", second: "2-digit",
    });

    const isDelete = h.operation === "DEL" || h.operation === "PURGE";
    row.title = isDelete
      ? "Deleted revision"
      : typeof h.value === "string" ? h.value : JSON.stringify(h.value);

    row.append(left, when);
    row.addEventListener("click", () => onSelect(h));
    els.kvHistoryList.append(row);
  });
}

export function setKvStatus(msg, isErr = false) {
  els.kvStatus.textContent = msg;
  els.kvStatus.classList.toggle("is-error", isErr);
}

// ============================================================================
// STREAMS
// ============================================================================

export function renderStreamList(list, onSelect) {
  els.streamList.replaceChildren();
  if (list.length === 0) {
    setEmpty(els.streamList, "No streams found");
    return;
  }
  list.forEach((s) => els.streamList.append(listRow(s.config.name, onSelect)));
}

export function highlightStream(name) {
  const rows = [...els.streamList.querySelectorAll(".list-row")];
  activateRow(els.streamList, rows.find((r) => r.textContent === name));
}

export function renderStreamConsumers(consumers) {
  els.consumerTabCount.textContent = consumers.length;

  if (consumers.length === 0) {
    setEmpty(els.consumerList, "No consumers on this stream");
    return;
  }

  els.consumerList.replaceChildren();
  consumers.forEach((c) => {
    const row = document.createElement("div");
    row.className = "consumer-row";

    const isDurable = !!c.config.durable_name;
    const name = document.createElement("div");
    name.className = `consumer-name ${isDurable ? "durable" : "ephemeral"}`;
    name.textContent = c.name;
    name.title = c.name;
    if (!isDurable) {
      const badge = document.createElement("span");
      badge.className = "badge badge-hdr";
      badge.textContent = "Ephemeral";
      name.append(" ", badge);
    }

    const stats = document.createElement("div");
    stats.className = "consumer-stats";
    const pending = c.num_pending || 0;
    const pendingEl = document.createElement("span");
    pendingEl.className = pending > 0 ? "hot" : "";
    pendingEl.textContent = `Pending ${pending}`;
    const waiting = document.createElement("span");
    waiting.textContent = `Waiting ${c.num_waiting || 0}`;
    const ack = document.createElement("span");
    ack.textContent = `Ack ${c.num_ack_pending || 0}`;
    stats.append(pendingEl, waiting, ack);

    const actions = document.createElement("div");
    actions.className = "consumer-actions";
    const edit = document.createElement("button");
    edit.className = "sm-btn consumer-edit";
    edit.dataset.consumer = c.name;
    edit.title = "View / edit config";
    edit.textContent = "Edit";
    const del = document.createElement("button");
    del.className = "sm-btn danger consumer-delete";
    del.dataset.consumer = c.name;
    del.title = "Delete consumer";
    del.textContent = "✕";
    actions.append(edit, del);

    const right = document.createElement("div");
    right.className = "consumer-right";
    right.append(stats, actions);

    row.append(name, right);
    els.consumerList.append(row);
  });
}

/**
 * Render message headers as a name/value grid.
 * Accepts anything iterable as [key, value] pairs - callers pass
 * Object.entries() of the plain {name: [values]} object the backend sends.
 *
 * @returns {HTMLElement|null} null when there are no headers to show
 */
function buildHeaderBlock(pairs) {
  const entries = [...pairs];
  if (entries.length === 0) return null;

  const wrap = document.createElement("div");
  wrap.className = "msg-headers";

  const badge = document.createElement("span");
  badge.className = "badge badge-hdr";
  badge.textContent = `HEAD ${entries.length}`;
  wrap.append(badge);

  const grid = document.createElement("div");
  grid.className = "hdr-grid";
  entries.forEach(([key, value]) => {
    const k = document.createElement("span");
    k.className = "hdr-key";
    k.textContent = key;
    const v = document.createElement("span");
    v.className = "hdr-value";
    v.textContent = Array.isArray(value) ? value.join(", ") : value;
    grid.append(k, v);
  });

  wrap.append(grid);
  return wrap;
}

// Counter for unique stream message DOM ids (tail can redeliver the same seq
// after a restart, so seq alone is not unique enough)
let streamMsgCounter = 0;

/** Build one stream message entry. Shared by range loading and live tail. */
function createStreamMsgDiv(m) {
  const div = document.createElement("div");
  div.className = "stream-msg-entry";

  const msgId = `stream-msg-${streamMsgCounter++}`;

  const head = document.createElement("div");
  head.className = "stream-msg-head";
  const seq = document.createElement("span");
  seq.textContent = `#${m.seq}`;
  const when = document.createElement("span");
  when.className = "when";
  when.textContent = new Date(m.time).toLocaleString();
  head.append(seq, when);

  const subject = document.createElement("div");
  subject.className = "stream-msg-subject";
  subject.textContent = m.subject;

  div.append(head, subject);

  if (m.headers) {
    const hdr = buildHeaderBlock(Object.entries(m.headers));
    if (hdr) div.append(hdr);
  }

  const body = document.createElement("div");
  body.className = "stream-msg-body";

  const copy = document.createElement("button");
  copy.className = "copy-btn";
  copy.dataset.msgId = msgId;
  copy.textContent = "Copy JSON";

  const pre = document.createElement("pre");
  pre.id = msgId;
  try {
    pre.innerHTML = utils.syntaxHighlight(JSON.parse(m.data));
  } catch {
    pre.textContent = m.data;
  }

  body.append(copy, pre);
  div.append(body);
  return div;
}

export function renderStreamMessages(msgs) {
  if (msgs.length === 0) {
    setEmpty(els.streamMsgContainer, "No messages found in range");
    return;
  }
  els.streamMsgContainer.replaceChildren();
  msgs.forEach((m) => els.streamMsgContainer.append(createStreamMsgDiv(m)));
}

/**
 * Add a live-tail message, pruning old entries.
 * Follows the same direction as the message log so both read the same way.
 */
export function appendStreamTailMessage(m) {
  const box = els.streamMsgContainer;
  clearEmpty(box);

  const wasAtEdge = newestFirst
    ? box.scrollTop <= 4
    : box.scrollHeight - box.scrollTop - box.clientHeight <= 8;

  if (newestFirst) box.prepend(createStreamMsgDiv(m));
  else box.append(createStreamMsgDiv(m));

  while (box.children.length > MAX_TAIL_MESSAGES) {
    (newestFirst ? box.lastElementChild : box.firstElementChild).remove();
  }

  if (wasAtEdge) box.scrollTop = newestFirst ? 0 : box.scrollHeight;
}

export function showTailPlaceholder() {
  setEmpty(els.streamMsgContainer, "Tailing… waiting for new messages");
}

// ============================================================================
// MESSAGE LOG
// ============================================================================

const logHistory = [];
let isPaused = false;
let msgCounter = 0;

function tryParsePayload(rawData) {
  if (typeof rawData !== "string") return rawData;
  const trimmed = rawData.trim();
  if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
    try { return JSON.parse(rawData); } catch { return rawData; }
  }
  return rawData;
}

function addToLogHistory(subject, rawData, isRpc, headers) {
  let headerObj = null;
  if (headers) {
    headerObj = {};
    for (const [key, value] of Object.entries(headers)) headerObj[key] = value;
  }

  logHistory.push({
    timestamp: new Date().toISOString(),
    type: isRpc ? "RPC" : "MSG",
    subject,
    headers: headerObj,
    payload: tryParsePayload(rawData),
  });

  if (logHistory.length > MAX_LOG_HISTORY) logHistory.shift();
}

function createMessageDiv(subject, data, isRpc, msgHeaders) {
  const div = document.createElement("div");
  div.className = "msg-entry";

  // Respect an active filter so incoming messages don't jump the list
  const filterText = els.logFilter.value.toLowerCase();
  if (filterText && !(subject + data).toLowerCase().includes(filterText)) {
    div.hidden = true;
  }

  const msgId = `msg-${msgCounter++}`;

  const meta = document.createElement("div");
  meta.className = "msg-meta";

  const badge = document.createElement("span");
  badge.className = `badge ${isRpc ? "badge-rpc" : "badge-sub"}`;
  badge.textContent = isRpc ? "RPC" : "MSG";

  const time = document.createElement("span");
  time.textContent = new Date().toLocaleTimeString();

  const subjectEl = document.createElement("span");
  subjectEl.className = "msg-subject";
  subjectEl.textContent = subject;

  const copy = document.createElement("button");
  copy.className = "copy-btn push-right";
  copy.dataset.msgId = msgId;
  copy.textContent = "Copy JSON";

  meta.append(badge, time, subjectEl, copy);
  div.append(meta);

  if (msgHeaders) {
    const hdr = buildHeaderBlock(Object.entries(msgHeaders));
    if (hdr) div.append(hdr);
  }

  const pre = document.createElement("pre");
  pre.id = msgId;
  if (data.length < MAX_PRETTY_SIZE) {
    try {
      pre.innerHTML = utils.syntaxHighlight(JSON.parse(data));
    } catch {
      pre.textContent = data;
    }
  } else {
    // Truncate large payloads to keep the UI responsive
    pre.textContent = data.substring(0, MAX_PRETTY_SIZE) +
      `\n... [Truncated ${utils.formatBytes(data.length)}]`;
  }
  div.append(pre);

  return div;
}

// Log order. Default is newest-at-the-bottom, matching `nats sub` and other
// tail-style tools; the header badge toggles it and the choice is persisted.
let newestFirst = false;

// Messages that arrived while the user was scrolled away from the live edge
let pendingNew = 0;

/** Is the log scrolled to the end where new messages land? */
function atLiveEdge() {
  const el = els.messages;
  if (newestFirst) return el.scrollTop <= 4;
  return el.scrollHeight - el.scrollTop - el.clientHeight <= 8;
}

function scrollToLiveEdge() {
  els.messages.scrollTop = newestFirst ? 0 : els.messages.scrollHeight;
}

function setPendingNew(n) {
  pendingNew = n;
  els.jumpCount.textContent = n;
  els.btnJumpLatest.hidden = n === 0;
}

/** Jump back to wherever new messages are arriving. */
export function jumpToLatest() {
  scrollToLiveEdge();
  setPendingNew(0);
}

export function isNewestFirst() {
  return newestFirst;
}

/**
 * Set log direction. Existing entries are reversed in place so the visible
 * order matches immediately instead of only applying to new messages.
 */
export function setNewestFirst(value, { reflow = true } = {}) {
  newestFirst = !!value;

  els.btnLogOrder.textContent = newestFirst ? "⬆ newest first" : "⬇ newest last";
  els.btnLogOrder.title = newestFirst
    ? "New messages arrive at the top - click for newest last"
    : "New messages arrive at the bottom - click for newest first";

  // The jump pill has to follow the log direction. Left pinned to the bottom
  // pointing down, it sends you to the top in newest-first mode - the
  // opposite of what it shows.
  els.jumpArrow.textContent = newestFirst ? "⇧" : "⇩";
  els.btnJumpLatest.classList.toggle("at-top", newestFirst);
  els.btnJumpLatest.title = newestFirst
    ? "Jump to the newest messages at the top"
    : "Jump to the newest messages at the bottom";

  if (reflow) {
    const entries = [...els.messages.children];
    if (entries.length > 1) els.messages.replaceChildren(...entries.reverse());
    scrollToLiveEdge();
  }
  setPendingNew(0);
}

/** Track whether the user has scrolled away from the live edge. */
export function initializeLogScrollTracking() {
  els.messages.addEventListener("scroll", () => {
    if (atLiveEdge()) setPendingNew(0);
  });
  els.btnJumpLatest.addEventListener("click", jumpToLatest);
}

export function renderMessage(subject, data, isRpc = false, msgHeaders = null) {
  // RPC replies always render - they are a direct response to a user action
  if (isPaused && !isRpc) return;

  addToLogHistory(subject, data, isRpc, msgHeaders);
  clearEmpty(els.messages);

  const div = createMessageDiv(subject, data, isRpc, msgHeaders);
  const wasAtEdge = atLiveEdge();

  if (newestFirst) els.messages.prepend(div);
  else els.messages.append(div);

  // Prune from the far end so the newest entries always survive
  while (els.messages.children.length > MAX_VISIBLE_MESSAGES) {
    (newestFirst ? els.messages.lastElementChild : els.messages.firstElementChild).remove();
  }

  // Stay pinned only if the user was already at the live edge, so scrolling
  // back to read something is never interrupted by incoming traffic
  if (wasAtEdge) {
    scrollToLiveEdge();
    setPendingNew(0);
  } else if (!div.hidden) {
    setPendingNew(pendingNew + 1);
  }
}

export function toggleLogPause() {
  isPaused = !isPaused;
  els.btnPause.textContent = isPaused ? "Resume" : "Pause";
  els.btnPause.classList.toggle("paused", isPaused);
}

export function clearLogs() {
  logHistory.length = 0;
  setEmpty(els.messages, "No messages yet - subscribe to a subject to see traffic");
  setPendingNew(0);
}

export function downloadLogs() {
  if (logHistory.length === 0) {
    showToast("No logs to export", "info");
    return;
  }

  const blob = new Blob([JSON.stringify(logHistory, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `nats-logs-${new Date().toISOString()}.json`;
  document.body.append(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);

  showToast(`Exported ${logHistory.length} messages`, "success");
}

export function filterLogs(val) {
  const v = val.toLowerCase();
  els.messages.querySelectorAll(".msg-entry").forEach((entry) => {
    entry.hidden = !entry.innerText.toLowerCase().includes(v);
  });
}

// ============================================================================
// LIST FILTERING
// ============================================================================

export function filterList(inputElement, containerElement, childSelector = ".list-row") {
  const term = inputElement.value.toLowerCase();
  containerElement.querySelectorAll(childSelector).forEach((child) => {
    child.hidden = !child.innerText.toLowerCase().includes(term);
  });
}

// ============================================================================
// TAB NAVIGATION
// ============================================================================

/** Switch a top-level tab. Panels are matched by their data-tab-panel value. */
export function switchTab(name) {
  els.tabBar.querySelectorAll(".tab").forEach((btn) => {
    const on = btn.dataset.tab === name;
    btn.classList.toggle("active", on);
    btn.setAttribute("aria-selected", String(on));
  });
  document.querySelectorAll(".tab-panel").forEach((panel) => {
    panel.hidden = panel.dataset.tabPanel !== name;
  });
}

export function getActiveTab() {
  return els.tabBar.querySelector(".tab.active")?.dataset.tab ?? "msg";
}

/**
 * Switch a sub-tab inside a detail pane.
 * Sub-tab panels must be siblings of their <nav class="subtabs">.
 */
export function switchSubTab(group, name) {
  const nav = document.querySelector(`.subtabs[data-subtabs="${group}"]`);
  if (!nav) return;

  nav.querySelectorAll(".subtab").forEach((btn) => {
    const on = btn.dataset.subtab === name;
    btn.classList.toggle("active", on);
    btn.setAttribute("aria-selected", String(on));
  });

  nav.parentElement.querySelectorAll(":scope > .subtab-panel").forEach((panel) => {
    panel.hidden = panel.dataset.subtabPanel !== name;
  });
}

// ============================================================================
// CONNECTION STATE
// ============================================================================

/** Strip the scheme so the status pill shows just the host. */
function shortHost(url) {
  try {
    return new URL(url).host;
  } catch {
    return url.replace(/^wss?:\/\//, "");
  }
}

// What the pill shows next to the dot. The profile name beats the host:
// deciding whether it is safe to publish is easier from `prod` than from
// `mq.internal.example.com`. The full URL stays in the tooltip.
let activeProfile = "";
let pillUrl = null;

function renderConnLabel() {
  els.statusHost.textContent = activeProfile || (pillUrl ? shortHost(pillUrl) : "");
  // The pill shows one of the two and may ellipse it, so the tooltip carries
  // both in full.
  const detail = [activeProfile, pillUrl].filter(Boolean).join(" — ");
  els.btnConnStatus.title = detail || "Connection settings";
}

/**
 * Name of the profile selected in the connection popover, or "" for none.
 * The popover is behind the pill, so the pill has to carry this - it is the
 * only place the active profile is visible once the popover closes.
 */
export function setActiveProfile(name) {
  activeProfile = name || "";
  renderConnLabel();
}

/**
 * Reflect connection state across the whole app.
 *
 * The workspace stays visible when disconnected - only the controls that
 * actually need a connection ([data-requires-conn]) are disabled, so profiles,
 * templates and previous results remain readable offline.
 */
export function setConnectionState(state, url = null) {
  const connected = state === "connected";
  document.body.dataset.conn = state;

  els.btnConnect.textContent = connected ? "Disconnect" : "Connect";
  els.btnConnect.className = connected ? "danger block" : "primary block";
  els.btnConnect.disabled = state === "reconnecting";

  els.url.disabled = connected;
  els.creds.disabled = connected;

  els.statusText.textContent = connected
    ? "Connected"
    : state === "reconnecting" ? "Reconnecting…" : "Disconnected";

  els.statusDot.className = "status-dot" +
    (connected ? " connected" : state === "reconnecting" ? " reconnecting" : "");

  if (connected && url) pillUrl = url;

  if (state === "disconnected") {
    pillUrl = null;
    els.rttLabel.textContent = "";
    clearSubscriptions();
  }

  renderConnLabel();

  document.querySelectorAll("[data-requires-conn]").forEach((el) => {
    el.disabled = !connected;
  });
}

export function setRtt(ms) {
  els.rttLabel.textContent = `${ms}ms`;
}
