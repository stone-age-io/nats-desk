// ============================================================================
// NATS WEB CLIENT - MAIN APPLICATION LOGIC
// ============================================================================
// This is the "brain" - wires UI events to NATS operations
// Architecture: UI events -> main.js handlers -> nats-client.js API -> UI updates

// ============================================================================
// IMPORTS
// ============================================================================

import { els } from "./dom.js";
import * as utils from "./utils.js";
import * as ui from "./ui.js";
import * as nats from "./api.js";
import * as storage from "./storage.js";
import * as dlg from "./dialogs.js";
import { initPaneSplitters } from "./splitters.js";

// ============================================================================
// CONFIGURATION CONSTANTS
// ============================================================================

// Maximum messages to fetch from stream in one request
// Fetched as a single ordered-consumer batch; bounded to keep the DOM snappy
const MAX_STREAM_MSG_FETCH = 200;

// How long to batch up dropped-message reports before showing one toast
const DROP_REPORT_THROTTLE_MS = 3000;

// Default RPC timeout in milliseconds
const DEFAULT_RPC_TIMEOUT_MS = 2000;

// ============================================================================
// APPLICATION STATE
// ============================================================================
// All mutable state in one place so grug can find it.
// Selection lives here rather than being read back out of the DOM.

const appState = {
  // Name of the currently selected stream (string) or null
  currentStream: null,

  // Set of key names in the currently watched bucket
  kvKeys: new Set(),

  // true = KV value textarea is visible; false = syntax-highlighted <pre>
  kvEditMode: false,

  // Name of the currently selected KV bucket (string) or null
  currentKvBucket: null,

  // AsyncIterable streaming key change events. Must be stopped before
  // opening another bucket or the watcher leaks.
  currentKvWatcher: null,

  // Creds file text loaded from the selected profile (null if none)
  profileCredsText: null,

  // Every NATS CLI context the backend can see, as listed
  contexts: [],

  // Name of the context the connection form is using, or null for manual
  contextName: null,

  // The URL typed by hand, parked while a context owns the field
  manualUrl: "",

  // URL we are currently connected to - used to key saved subscriptions
  connectedUrl: null,

  // Whether stream live-tail is running
  isTailing: false,

  // Monitoring: last status payload, the cluster grid, and the selected row
  monitorStatus: null,
  monitorServers: [],
  monitorServer: null,

  // Whether this platform can start the app at sign-in. Remembered because
  // the toggle is re-enabled after every write and must not become usable on
  // a platform that cannot honour it.
  autostartSupported: false,
};

// Native popover support - Chrome 114+, Safari 17+, Firefox 125+.
// Older browsers fall back to a class toggle (see toggleConnPopover).
const SUPPORTS_POPOVER = Object.prototype.hasOwnProperty.call(HTMLElement.prototype, "popover");

// ============================================================================
// INITIALIZATION
// ============================================================================

/**
 * Surface backend message drops.
 *
 * The backend caps how many messages it forwards per flush so a firehose
 * cannot outrun the renderer. That is the right trade, but a silent gap in a
 * message log is a lie, so the count is reported - throttled, because at a
 * rate high enough to drop, the reports would themselves become a flood.
 */
function setupDropReporting() {
  let pending = 0;
  let timer = null;

  nats.setDroppedHandler((count) => {
    pending += count;
    if (timer) return;
    timer = setTimeout(() => {
      ui.showToast(`${pending.toLocaleString()} messages dropped - arriving faster than the log can render`, "info");
      pending = 0;
      timer = null;
    }, DROP_REPORT_THROTTLE_MS);
  });
}

function initializeApp() {
  ui.initializeEventDelegation();
  ui.initializeLogScrollTracking();
  setupTabNavigation();
  setupSubTabNavigation();
  setupSubscriptionEventDelegation();
  setupConsumerEventDelegation();
  setupHeaderEditor();
  setupFallbackPopover(els.btnConnStatus, els.connPopover);
  setupFallbackPopover(els.btnSettings, els.settingsPopover);
  setupSettings();
  registerServiceWorker();
  initPaneSplitters();
  setupDropReporting();

  refreshHistoryUi();
  refreshProfileUi();
  refreshTemplateUi();
  refreshContextUi();

  nats.setMonitorHandlers({
    onServers: handleMonitorServers,
    onEvent: ui.appendMonitorEvent,
    onStatus: applyMonitorStatus,
  });
  // Order matters: restoring the URLs re-registers them with the backend, and
  // loadMonitor then reports a status that includes them.
  restoreMonitorHttp().then(loadMonitor);

  // Restore log direction before anything can render into the log
  ui.setNewestFirst(storage.getLogNewestFirst(), { reflow: false });
  ui.clearLogs();
  ui.renderHeaderRows({});

  const savedUrl = storage.getLastUrl();
  if (savedUrl) els.url.value = savedUrl;

  // Start in the disconnected state so [data-requires-conn] controls disable
  ui.setConnectionState("disconnected");

  handleUrlParameters();
}

/**
 * Top-level tabs are data-driven: a tab button and a matching
 * [data-tab-panel] section is all a new section needs.
 */
function setupTabNavigation() {
  els.tabBar.addEventListener("click", (e) => {
    const btn = e.target.closest(".tab");
    if (!btn) return;
    const name = btn.dataset.tab;
    ui.switchTab(name);

    // Monitoring works with no NATS connection at all: $SYS and :8222 are
    // their own connections. So it reloads before the guard, not after it.
    if (name === "monitor") loadMonitor();

    if (!nats.isConnected()) return;
    if (name === "kv") loadKvBucketsWrapper();
    else if (name === "stream") loadStreamsWrapper();
  });
}

/** Sub-tabs inside detail panes, delegated the same way. */
function setupSubTabNavigation() {
  document.querySelectorAll(".subtabs").forEach((nav) => {
    nav.addEventListener("click", (e) => {
      const btn = e.target.closest(".subtab");
      if (!btn) return;
      ui.switchSubTab(nav.dataset.subtabs, btn.dataset.subtab);
    });
  });
}

function setupSubscriptionEventDelegation() {
  els.subList.addEventListener("click", (e) => {
    // Unsubscribe
    if (e.target.classList.contains("danger")) {
      const subId = parseInt(e.target.dataset.subId, 10);
      if (!isNaN(subId)) handleUnsubscribe(subId);
      return;
    }
    // Click the subject to target it for publishing. closest() rather than a
    // tag check so the filter badge nested in the span is not a dead zone
    const label = e.target.closest("span[data-subject]");
    if (label) {
      els.pubSubject.value = label.dataset.subject;
      ui.switchTab("msg");
      els.pubSubject.focus();
    }
  });
}

/**
 * Header key/value rows: add, remove, and keep the count badge in sync.
 * Rows are delegated because they are created and destroyed freely.
 */
function setupHeaderEditor() {
  els.btnHeaderAdd.addEventListener("click", () => {
    const row = ui.addHeaderRow();
    row.querySelector(".header-key").focus();
  });

  els.headerRows.addEventListener("click", (e) => {
    if (!e.target.classList.contains("header-remove")) return;
    e.target.closest(".header-row").remove();
    // Never leave the editor with nothing to type into
    if (!els.headerRows.querySelector(".header-row")) ui.addHeaderRow();
    ui.updateHeaderCount();
  });

  els.headerRows.addEventListener("input", () => ui.updateHeaderCount());
}

function refreshHistoryUi() {
  ui.renderHistoryDatalist("subHistory", storage.getSubjectHistory());
  ui.renderHistoryDatalist("urlHistory", storage.getUrlHistory());
}

/**
 * Handle URL parameters for deep linking
 * Supports auto-connection with pre-filled credentials
 */
function handleUrlParameters() {
  const urlParams = new URLSearchParams(window.location.search);
  const paramUrl = urlParams.get("url");
  const paramToken = urlParams.get("token");
  const paramUser = urlParams.get("user");
  const paramPass = urlParams.get("pass");
  const autoConnect = urlParams.has("connect");

  if (paramUrl) els.url.value = paramUrl;
  if (paramToken) els.authToken.value = paramToken;
  if (paramUser) els.authUser.value = paramUser;
  if (paramPass) els.authPass.value = paramPass;

  // Strip credentials from the address bar so they don't persist
  // in browser history / bookmarks / screen shares
  if (paramToken || paramUser || paramPass) {
    history.replaceState(null, "", window.location.pathname);
  }

  if (autoConnect) setTimeout(() => handleConnect(), 100);
}

initializeApp();

// ============================================================================
// POPOVERS
// ============================================================================

/** Open/close a popover on a browser too old for the popover API. */
function setupFallbackPopover(btn, pop) {
  if (SUPPORTS_POPOVER) return; // popovertarget in the markup does the work

  btn.addEventListener("click", () => pop.classList.toggle("fallback-open"));
  document.addEventListener("click", (e) => {
    if (!pop.contains(e.target) && !btn.contains(e.target)) {
      pop.classList.remove("fallback-open");
    }
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") pop.classList.remove("fallback-open");
  });
}

function closePopover(pop) {
  if (SUPPORTS_POPOVER) {
    try { pop.hidePopover(); } catch { /* already closed */ }
  } else {
    pop.classList.remove("fallback-open");
  }
}

const closeConnPopover = () => closePopover(els.connPopover);

// ============================================================================
// APP SETTINGS
// ============================================================================

/**
 * Register the service worker.
 *
 * One job: the offline page. When this window is an installed app and the
 * backend is not running, a navigation lands on the browser's own connection
 * error, which has nothing in it anyone can press - see public/sw.js.
 *
 * Unawaited on purpose. Nothing in the app depends on it, and a browser that
 * declines simply keeps behaving the way it did before.
 */
function registerServiceWorker() {
  if (!("serviceWorker" in navigator)) return;
  navigator.serviceWorker.register("/sw.js").catch((err) => {
    console.warn("service worker registration failed:", err);
  });
}

function setupSettings() {
  els.setAutostart.addEventListener("change", handleAutostartToggle);
  loadDesktopSettings();
}

async function loadDesktopSettings() {
  let info;
  try {
    info = await nats.getDesktop();
  } catch (err) {
    // Deliberately not a toast. This runs on every load, and a panel nobody
    // has opened yet is not worth interrupting anyone over; the message is
    // there when they do open it.
    setAutostartHint(err.message);
    return;
  }

  appState.autostartSupported = !!info.autostartSupported;

  els.setVersion.textContent = info.version || "";
  els.setExe.textContent = info.executable || "unknown";
  els.setExe.title = info.executable || "";

  // No log path means the log is going to a terminal, where whoever started
  // the process can already read it.
  els.setLogRow.hidden = !info.logPath;
  els.setLog.textContent = info.logPath || "";
  els.setLog.title = info.logPath || "";

  els.setAutostart.checked = !!info.autostart;
  els.setAutostart.disabled = !appState.autostartSupported;
  setAutostartHint(
    appState.autostartSupported ? info.autostartError : "Not available on this platform",
  );
}

function setAutostartHint(msg) {
  els.setAutostartHint.textContent = msg || "";
  els.setAutostartHint.hidden = !msg;
}

async function handleAutostartToggle() {
  const wanted = els.setAutostart.checked;
  els.setAutostart.disabled = true;
  try {
    await nats.setAutostart(wanted);
    setAutostartHint("");
    ui.showToast(
      wanted ? "nats-desk will start when you sign in" : "Autostart turned off",
      "success",
    );
  } catch (err) {
    // Put the checkbox back. It reports what the machine is set to, and a
    // write that failed did not change that.
    els.setAutostart.checked = !wanted;
    setAutostartHint(err.message);
    ui.showToast(err.message, "error");
  } finally {
    els.setAutostart.disabled = !appState.autostartSupported;
  }
}

// ============================================================================
// CONNECTION HANDLERS
// ============================================================================

/**
 * Reset every data pane to its disconnected state.
 * Called on disconnect and on connection loss.
 */
function resetDataPanels() {
  ui.setEmpty(els.kvBucketList, "Not connected");
  ui.setEmpty(els.streamList, "Not connected");
  cleanupKvUi();

  appState.currentStream = null;
  els.streamDetailView.hidden = true;
  els.streamEmptyState.hidden = false;
}

async function handleConnect() {
  if (nats.isConnected()) {
    try {
      stopTailUi();
      await nats.disconnect();
      ui.setConnectionState("disconnected");

      appState.currentKvBucket = null;
      appState.currentKvWatcher = null;
      appState.connectedUrl = null;
      resetDataPanels();

      ui.showToast("Disconnected", "info");
      refreshMonitorStatus();
    } catch (err) {
      console.error("Error during disconnect:", err);
      ui.showToast(`Disconnect error: ${err.message}`, "error");
    }
    return;
  }

  const contextName = appState.contextName;
  const url = els.url.value.trim();
  if (!url) {
    ui.showToast("Please enter a server URL", "error");
    return;
  }

  // What the connection pill and the saved-subscription list are keyed on. A
  // context's URL is filled into the field when it is picked, so this starts
  // correct either way; the backend's answer replaces it once it arrives.
  let activeUrl = url;

  try {
    // Typed URLs go in the history. A context's does not: it was not typed,
    // and the context picker already lists it.
    if (!contextName) {
      storage.saveUrl(url);
      storage.addUrlToHistory(url);
      refreshHistoryUi();
    }

    els.statusText.textContent = "Connecting…";
    els.btnConnect.disabled = true;

    // A freshly picked .creds file wins over creds stored in the profile
    let credsText = null;
    if (els.creds.files.length > 0) {
      credsText = await els.creds.files[0].text();
    } else if (appState.profileCredsText) {
      credsText = appState.profileCredsText;
    }

    const authOptions = {
      context: contextName,
      credsText,
      user: els.authUser.value.trim(),
      pass: els.authPass.value.trim(),
      token: els.authToken.value.trim(),
    };

    const res = await nats.connectToNats(
      url,
      authOptions,
      (status, err) => {
        ui.setConnectionState(status, activeUrl);
        if (status === "disconnected") {
          // Connection lost - clear per-connection state so the UI
          // doesn't act on stale handles after a manual reconnect
          appState.currentKvBucket = null;
          appState.currentKvWatcher = null;
          appState.connectedUrl = null;
          resetDataPanels();
          stopTailUi();
          refreshMonitorStatus();
          if (err) ui.showToast(`Connection lost: ${err.message}`, "error");
        } else if (status === "connected") {
          ui.showToast("Reconnected", "success");
        }
      },
      (stats) => ui.setRtt(stats.rtt)
    );

    activeUrl = res.url || url;
    appState.connectedUrl = activeUrl;
    ui.setConnectionState("connected", activeUrl);
    ui.showToast(
      contextName ? `Connected using context '${contextName}'` : "Connected to NATS",
      "success"
    );
    closeConnPopover();
    refreshMonitorStatus();

    await restoreSubscriptions(activeUrl);

    const tab = ui.getActiveTab();
    if (tab === "kv") loadKvBucketsWrapper();
    else if (tab === "stream") loadStreamsWrapper();
  } catch (err) {
    console.error("Connection error:", err);
    ui.setConnectionState("disconnected");
    ui.showToast(err.message, "error");
  } finally {
    els.btnConnect.disabled = false;
  }
}

function handleShowServerInfo() {
  const info = nats.getServerInfo();
  dlg.infoDialog({
    title: "Server information",
    text: info ? JSON.stringify(info, null, 2) : "Not connected.",
  });
}

els.btnConnect.addEventListener("click", handleConnect);
els.btnInfo.addEventListener("click", handleShowServerInfo);

// ============================================================================
// CONNECTION PROFILE HANDLERS
// ============================================================================

function refreshProfileUi() {
  ui.renderNamedOptions(els.profileSelect, storage.getProfiles(), "-- No Profile --");
}

function handleProfileChange() {
  const profile = storage.getProfile(els.profileSelect.value);
  appState.profileCredsText = null;
  els.credsHint.hidden = true;
  ui.setActiveProfile(els.profileSelect.value);

  if (!profile) return;

  // A profile and a context both own the URL and the credentials, so picking
  // one drops the other rather than leaving it unclear which is in effect.
  if (appState.contextName) {
    els.contextSelect.value = "";
    applyContextSelection();
  }

  els.url.value = profile.url || "";
  els.authUser.value = profile.user || "";
  els.authPass.value = profile.pass || "";
  els.authToken.value = profile.token || "";
  els.creds.value = "";
  els.saveCredsChk.checked = !!(profile.pass || profile.token || profile.credsText);

  if (profile.credsText) {
    appState.profileCredsText = profile.credsText;
    els.credsHint.hidden = false;
  }
}

async function handleProfileSave() {
  const name = await dlg.promptDialog({
    title: "Save connection profile",
    label: "Profile name",
    value: els.profileSelect.value || "",
    placeholder: "dev / staging / prod",
  });
  if (!name) return;

  const saveCreds = els.saveCredsChk.checked;
  const profile = {
    name,
    url: els.url.value.trim(),
    user: els.authUser.value.trim(),
    pass: saveCreds ? els.authPass.value : "",
    token: saveCreds ? els.authToken.value.trim() : "",
    credsText: null,
  };

  if (saveCreds) {
    if (els.creds.files.length > 0) {
      profile.credsText = await els.creds.files[0].text();
    } else if (appState.profileCredsText) {
      // Keep creds already loaded from the profile being edited
      profile.credsText = appState.profileCredsText;
    }
  }

  storage.saveProfile(profile);
  refreshProfileUi();
  els.profileSelect.value = profile.name;
  ui.setActiveProfile(profile.name);
  ui.showToast(`Profile '${profile.name}' saved${saveCreds ? " (with credentials)" : ""}`, "success");
}

async function handleProfileDelete() {
  const name = els.profileSelect.value;
  if (!name) {
    ui.showToast("Select a profile first", "info");
    return;
  }

  const ok = await dlg.confirmDialog({
    title: "Delete profile",
    message: `Delete the profile '${name}'? This does not affect the server.`,
    confirmLabel: "Delete",
    danger: true,
  });
  if (!ok) return;

  storage.deleteProfile(name);
  appState.profileCredsText = null;
  els.credsHint.hidden = true;
  refreshProfileUi();
  els.profileSelect.value = "";
  ui.setActiveProfile("");
  ui.showToast(`Profile '${name}' deleted`, "info");
}

els.profileSelect.addEventListener("change", handleProfileChange);
els.btnProfileSave.addEventListener("click", handleProfileSave);
els.btnProfileDelete.addEventListener("click", handleProfileDelete);

// ============================================================================
// NATS CLI CONTEXT HANDLERS
// ============================================================================
// Contexts are the `nats` CLI's own files on this machine, and we read and
// write exactly those - a context created here works from the command line,
// and one created there shows up here.
//
// Picking a context to connect with does NOT change which context the CLI
// defaults to. That is shared state every NATS tool on the machine reads, so
// changing it takes its own deliberate button.

// A new context starts from the fields anyone actually fills in. Everything
// omitted takes its zero value, and the saved file carries the full set - the
// same shape `nats context add` writes.
const NEW_CONTEXT_TEMPLATE = {
  description: "",
  url: "nats://localhost:4222",
  user: "",
  password: "",
  token: "",
  creds: "",
};

/**
 * Reload the context list and re-apply the picker.
 *
 * @param {string} select - context to leave selected; "" for manual settings.
 *                          A name that no longer exists falls back to manual.
 */
async function refreshContextUi(select = appState.contextName) {
  try {
    appState.contexts = await nats.getContexts();
  } catch (err) {
    // We are served by the backend, so it is up; this means the context
    // directory could not be read. Stay on manual settings and say so.
    console.error("Failed to list NATS contexts:", err);
    appState.contexts = [];
    ui.showToast(`Could not read NATS contexts: ${err.message}`, "error");
  }

  const items = appState.contexts.map((c) => ({
    name: c.name,
    label: c.selected ? `${c.name}  (CLI default)` : c.name,
  }));
  ui.renderNamedOptions(els.contextSelect, items, "-- Manual settings --");

  // The monitor's system-account picker offers the same contexts. A system
  // user is usually its own context anyway, which is the tidiest way to give
  // the second connection credentials without nats-desk storing any.
  const monSelected = els.monSysContext.value;
  ui.renderNamedOptions(els.monSysContext, items, "-- Manual settings --");
  els.monSysContext.value = appState.contexts.some((c) => c.name === monSelected) ? monSelected : "";
  els.monSysManual.hidden = !!els.monSysContext.value;

  els.contextSelect.value = appState.contexts.some((c) => c.name === select) ? select : "";
  applyContextSelection();
}

/** The context currently picked in the popover, or null in manual mode. */
function currentContext() {
  return appState.contexts.find((c) => c.name === els.contextSelect.value) || null;
}

/**
 * Make the form reflect the picker.
 *
 * A context owns the URL and the credentials, so the fields it supersedes show
 * its values and go disabled - disabled rather than hidden, because "this is
 * what you are about to connect with" is the useful thing to see.
 */
function applyContextSelection() {
  const ctx = currentContext();
  const wasManual = appState.contextName === null;
  appState.contextName = ctx ? ctx.name : null;

  if (ctx) {
    if (wasManual) appState.manualUrl = els.url.value;
    els.url.value = ctx.url;
    els.profileSelect.value = "";
    ui.setActiveProfile("");
    appState.profileCredsText = null;
    els.credsHint.hidden = true;
  } else if (!wasManual) {
    els.url.value = appState.manualUrl;
  }

  for (const el of [els.url, els.creds, els.authUser, els.authPass, els.authToken, els.saveCredsChk]) {
    el.disabled = !!ctx;
  }
  els.btnProfileSave.disabled = !!ctx;
  els.btnContextEdit.disabled = !ctx;
  els.btnContextDelete.disabled = !ctx;

  els.contextHint.hidden = !ctx;
  els.btnContextDefault.hidden = !ctx || ctx.selected;

  if (ctx) {
    const bits = [ctx.auth, ctx.url];
    if (ctx.selected) bits.push("already the nats CLI default");
    els.contextHint.textContent = bits.join(" · ");
    els.contextHint.title = ctx.description || "";
  }
}

async function handleContextNew() {
  const name = await dlg.promptDialog({
    title: "New NATS context",
    label: "Context name",
    placeholder: "dev / staging / prod",
  });
  if (!name) return;

  dlg.jsonDialog({
    title: `New context '${name}'`,
    hint: "Written where the nats CLI keeps its contexts, so the CLI sees it too.",
    value: NEW_CONTEXT_TEMPLATE,
    saveLabel: "Create",
    onSave: async (config) => {
      await nats.saveContext(name, config);
      await refreshContextUi(name);
      ui.showToast(`Context '${name}' created`, "success");
    },
  });
}

async function handleContextEdit() {
  const ctx = currentContext();
  if (!ctx) {
    ui.showToast("Select a context first", "info");
    return;
  }

  let detail;
  try {
    detail = await nats.getContext(ctx.name);
  } catch (err) {
    ui.showToast(err.message, "error");
    return;
  }

  // The file's own JSON, unexpanded - so a portable "~/x.creds" stays portable
  // instead of being rewritten to an absolute path belonging to this machine.
  dlg.jsonDialog({
    title: `Edit context '${ctx.name}'`,
    hint: detail.path,
    value: detail.config,
    onSave: async (config) => {
      await nats.saveContext(ctx.name, config);
      await refreshContextUi(ctx.name);
      ui.showToast(`Context '${ctx.name}' saved`, "success");
    },
  });
}

async function handleContextDelete() {
  const ctx = currentContext();
  if (!ctx) {
    ui.showToast("Select a context first", "info");
    return;
  }

  const ok = await dlg.confirmDialog({
    title: "Delete context",
    message: `Delete the context '${ctx.name}'? This removes the file the nats CLI reads. Any creds file or certificate it points at is left alone.`,
    confirmLabel: "Delete",
    danger: true,
  });
  if (!ok) return;

  try {
    await nats.deleteContext(ctx.name);
    await refreshContextUi("");
    ui.showToast(`Context '${ctx.name}' deleted`, "info");
  } catch (err) {
    ui.showToast(err.message, "error");
  }
}

async function handleContextDefault() {
  const ctx = currentContext();
  if (!ctx) return;

  try {
    await nats.selectContext(ctx.name);
    await refreshContextUi(ctx.name);
    ui.showToast(`'${ctx.name}' is now the default context for the nats CLI`, "success");
  } catch (err) {
    ui.showToast(err.message, "error");
  }
}


// ============================================================================
// MONITOR HANDLERS
// ============================================================================
// Three sources, configured separately because they are not interchangeable:
// the data connection sees only its own account, $SYS sees the whole cluster
// and pushes events, and :8222 works where no system user exists. See
// internal/monitor for the full reasoning.

/** Reflect a status payload from the backend across the sources panel. */
function applyMonitorStatus(status) {
  appState.monitorStatus = status;
  ui.setMonitorSources(status);

  const sys = (status && status.sys) || {};
  els.btnMonSysConnect.hidden = !!sys.connected;
  els.btnMonSysDisconnect.hidden = !sys.connected;

  els.monSysHint.hidden = !sys.configured;
  els.monSysHint.classList.toggle("bad", sys.configured && !sys.connected);
  if (sys.configured) {
    els.monSysHint.textContent = sys.connected
      ? `${sys.url} \u00b7 ${sys.servers || 0} server${sys.servers === 1 ? "" : "s"}`
      : `${sys.url} \u00b7 not connected`;
  }

  const http = (status && status.http) || {};
  els.monHttpHint.hidden = !http.configured;
  if (http.configured) {
    const n = (http.bases || []).length;
    els.monHttpHint.textContent = `${n} monitoring URL${n === 1 ? "" : "s"}`;
  }
}

function handleMonitorServers(rows) {
  appState.monitorServers = rows;
  ui.renderMonitorServers(rows, selectMonitorServer);
  if (appState.monitorServer) ui.highlightMonitorServer(appState.monitorServer);

  // The grid arrives on its own - a server heartbeats, a row appears - so the
  // count in the sources panel has to follow it rather than sitting on
  // whatever the last status payload happened to say.
  const st = appState.monitorStatus;
  if (st && st.sys && st.sys.configured && st.sys.servers !== rows.length) {
    applyMonitorStatus({ ...st, sys: { ...st.sys, servers: rows.length } });
  }
}

/**
 * Re-read which sources are live.
 *
 * The data source is the app's own connection, and the backend does not push
 * monitoring status when that changes - so the one dot the connection form
 * owns is refreshed from here instead.
 */
async function refreshMonitorStatus() {
  try {
    applyMonitorStatus(await nats.getMonitorStatus());
  } catch {
    // Purely cosmetic; leave the panel showing what it last knew.
  }
}

function selectMonitorServer(row) {
  appState.monitorServer = row.id;
  ui.highlightMonitorServer(row.id);
  ui.renderMonitorDetail(row);
  ui.switchSubTab("monitor", "server");
}

/**
 * Load the Monitor tab.
 *
 * Everything here is safe without a NATS connection: the $SYS and :8222
 * sources have nothing to do with the app's own connection, which is the
 * whole point of keeping them separate.
 */
async function loadMonitor() {
  try {
    applyMonitorStatus(await nats.getMonitorStatus());
    handleMonitorServers(await nats.getMonitorServers());
  } catch (err) {
    ui.showToast(err.message, "error");
  }
}

async function handleMonitorRefresh() {
  try {
    handleMonitorServers(await nats.refreshMonitorServers());
    applyMonitorStatus(await nats.getMonitorStatus());
  } catch (err) {
    ui.showToast(err.message, "error");
  }
}

async function handleMonSysConnect() {
  const ctx = els.monSysContext.value;

  // A .creds file wins over user/password, which is the order the backend
  // resolves them in and the same rule the main connection form follows. A
  // system account is the likeliest place to need one: operator mode is
  // exactly where a $SYS user exists at all.
  //
  // Read only on the manual branch - a context carries its own credentials.
  const body = ctx
    ? { context: ctx }
    : {
        url: els.monSysUrl.value.trim(),
        credsText: els.monSysCreds.files.length ? await els.monSysCreds.files[0].text() : "",
        user: els.monSysUser.value.trim(),
        pass: els.monSysPass.value,
      };

  if (!ctx && !body.url) {
    ui.showToast("A system account URL or a context is required", "error");
    return;
  }

  els.btnMonSysConnect.disabled = true;
  try {
    applyMonitorStatus(await nats.connectMonitorSys(body));
    handleMonitorServers(await nats.getMonitorServers());
    ui.showToast("System account connected", "success");
  } catch (err) {
    ui.showToast(err.message, "error");
  } finally {
    els.btnMonSysConnect.disabled = false;
  }
}

async function handleMonSysDisconnect() {
  try {
    applyMonitorStatus(await nats.disconnectMonitorSys());
    handleMonitorServers([]);
    ui.showToast("System account disconnected", "info");
  } catch (err) {
    ui.showToast(err.message, "error");
  }
}

async function handleMonHttpSave() {
  const bases = els.monHttpBases.value
    .split(/[\n,]/)
    .map((s) => s.trim())
    .filter(Boolean);

  try {
    applyMonitorStatus(
      await nats.setMonitorHttp({ bases, insecure: els.monHttpInsecure.checked })
    );
    storage.setMonitorHttp({ bases, insecure: els.monHttpInsecure.checked });

    // Fetching varz is also what populates the grid from this source, so a
    // "Use" that showed nothing would look like it had not worked.
    await nats.getMonitorHttpEndpoint("varz");
    handleMonitorServers(await nats.getMonitorServers());
    ui.showToast(`Monitoring ${bases.length} URL${bases.length === 1 ? "" : "s"}`, "success");
  } catch (err) {
    ui.showToast(err.message, "error");
  }
}

async function handleMonHttpClear() {
  try {
    applyMonitorStatus(await nats.clearMonitorHttp());
    storage.setMonitorHttp(null);
    ui.showToast("Monitoring URLs cleared", "info");
  } catch (err) {
    ui.showToast(err.message, "error");
  }
}

async function handleMonEndpoint() {
  const name = els.monEndpoint.value;
  const via = els.monEndpointVia.value;

  ui.setEmpty(els.monDetail, `Asking every server for ${name}\u2026`);
  try {
    const res =
      via === "http"
        ? await nats.getMonitorHttpEndpoint(name)
        : await nats.getMonitorEndpoint(name);
    ui.renderJsonInto(els.monDetail, res);
  } catch (err) {
    ui.setEmpty(els.monDetail, err.message, true);
  }
}

async function handleMonAccountLoad() {
  ui.setEmpty(els.monAccount, "Asking\u2026");
  try {
    ui.renderJsonInto(els.monAccount, await nats.getMonitorAccount());
  } catch (err) {
    ui.setEmpty(els.monAccount, err.message, true);
  }
}

/**
 * Put the remembered monitoring URLs back, in the form and in the backend.
 *
 * The backend holds them only for the life of the process, so a restart would
 * otherwise leave the form showing URLs that are not actually in use. These
 * are addresses, not credentials - re-applying them costs nothing and asks
 * nobody anything until the grid is refreshed.
 */
async function restoreMonitorHttp() {
  const saved = storage.getMonitorHttp();
  if (!saved || !saved.bases || !saved.bases.length) return;

  els.monHttpBases.value = saved.bases.join("\n");
  els.monHttpInsecure.checked = !!saved.insecure;

  try {
    applyMonitorStatus(await nats.setMonitorHttp({ bases: saved.bases, insecure: saved.insecure }));
  } catch (err) {
    // A saved URL that no longer parses should not stop the app loading.
    console.error("Could not restore monitoring URLs:", err);
  }
}

els.btnMonRefresh.addEventListener("click", handleMonitorRefresh);
els.btnMonSysConnect.addEventListener("click", handleMonSysConnect);
els.btnMonSysDisconnect.addEventListener("click", handleMonSysDisconnect);
els.btnMonHttpSave.addEventListener("click", handleMonHttpSave);
els.btnMonHttpClear.addEventListener("click", handleMonHttpClear);
els.btnMonEndpoint.addEventListener("click", handleMonEndpoint);
els.btnMonAccountLoad.addEventListener("click", handleMonAccountLoad);
els.btnMonEventsClear.addEventListener("click", ui.clearMonitorEvents);
els.monEventFilter.addEventListener("input", () =>
  ui.filterList(els.monEventFilter, els.monEvents, ".mon-event")
);

// A context supplies the URL and the credentials, so the manual fields it
// replaces go away rather than sitting there doing nothing.
els.monSysContext.addEventListener("change", () => {
  els.monSysManual.hidden = !!els.monSysContext.value;
});

els.contextSelect.addEventListener("change", applyContextSelection);
els.btnContextNew.addEventListener("click", handleContextNew);
els.btnContextEdit.addEventListener("click", handleContextEdit);
els.btnContextDelete.addEventListener("click", handleContextDelete);
els.btnContextDefault.addEventListener("click", handleContextDefault);

// Picking a real .creds file overrides profile creds - hide the hint
els.creds.addEventListener("change", () => {
  els.credsHint.hidden = els.creds.files.length > 0 || !appState.profileCredsText;
});

// ============================================================================
// SUBSCRIPTION HANDLERS
// ============================================================================

/**
 * Subscribe to a subject and add it to the UI
 * @param {string} subj - Subject to subscribe to
 * @param {boolean} excludeSystem - Drop $SYS/$JS/$KV/_INBOX traffic
 * @param {boolean} quiet - Suppress the success toast (used during bulk restore)
 * @returns {boolean} - Whether the exclusion actually applies to this pattern
 */
async function subscribeTo(subj, excludeSystem = false, quiet = false) {
  const res = await nats.subscribe(subj, (s, data, isRpc, headers) => {
    ui.renderMessage(s, data, isRpc, headers);
  }, { excludeSystem });

  ui.addSubscription(res.id, res.subject, res.excludeSystem);
  ui.updateSubCount(res.size);
  if (!quiet) {
    ui.showToast(`Subscribed to ${res.subject}${res.excludeSystem ? " (system subjects hidden)" : ""}`, "success");
  }
  return res.excludeSystem;
}

async function restoreSubscriptions(url) {
  const saved = storage.getSavedSubscriptions(url);
  if (saved.length === 0) return;

  let count = 0;
  for (const { subject, excludeSystem } of saved) {
    try {
      await subscribeTo(subject, excludeSystem, true);
      count++;
    } catch (err) {
      console.error(`Failed to restore subscription '${subject}':`, err);
    }
  }
  if (count > 0) ui.showToast(`Restored ${count} subscription${count > 1 ? "s" : ""}`, "info");
}

async function handleSubscribe() {
  const subj = els.subSubject.value.trim();
  if (!subj) return;

  try {
    storage.addSubjectToHistory(subj);
    refreshHistoryUi();

    // Save what the subscription is really doing, not what was ticked: on a
    // pattern like $JS.> the box is inert and restoring it must stay inert
    const applied = await subscribeTo(subj, els.excludeSystemChk.checked);
    if (appState.connectedUrl) {
      storage.addSavedSubscription(appState.connectedUrl, subj, applied);
    }
    els.subSubject.value = "";
    syncExcludeSystemRow();
  } catch (err) {
    console.error("Subscribe error:", err);
    ui.showToast(err.message, "error");
  }
}

/**
 * Grey the option out on patterns it cannot affect, so an inert checkbox never
 * looks like a promise the subscription is not keeping.
 */
function syncExcludeSystemRow() {
  const subj = els.subSubject.value.trim();
  const usable = !subj || nats.canExcludeSystem(subj);

  els.excludeSystemChk.disabled = !usable;
  els.excludeSystemRow.classList.toggle("is-disabled", !usable);
  els.excludeSystemRow.title = usable
    ? ""
    : "Only applies to wildcard subjects like > or *.foo";
}

async function handleUnsubscribe(id) {
  try {
    const { size, subject } = await nats.unsubscribe(id);
    ui.removeSubscription(id);
    ui.updateSubCount(size);
    if (subject && appState.connectedUrl) {
      storage.removeSavedSubscription(appState.connectedUrl, subject);
    }
  } catch (error) {
    console.error("Unsubscribe error:", error);
    ui.showToast(error.message, "error");
  }
}

els.btnSub.addEventListener("click", handleSubscribe);
els.subSubject.addEventListener("keyup", (e) => {
  if (e.key === "Enter") handleSubscribe();
});
els.subSubject.addEventListener("input", syncExcludeSystemRow);

els.excludeSystemChk.checked = storage.getExcludeSystem();
els.excludeSystemChk.addEventListener("change", () => {
  storage.setExcludeSystem(els.excludeSystemChk.checked);
});
syncExcludeSystemRow();

// ============================================================================
// PUBLISH / REQUEST HANDLERS
// ============================================================================

async function handlePublish() {
  const subj = els.pubSubject.value.trim();
  if (!subj) {
    ui.showToast("Enter a subject", "error");
    return;
  }

  try {
    storage.addSubjectToHistory(subj);
    refreshHistoryUi();
    await nats.publish(subj, els.pubPayload.value, ui.readHeadersJson());

    els.btnPub.textContent = "✓";
    setTimeout(() => (els.btnPub.textContent = "Pub"), 1000);
  } catch (err) {
    console.error("Publish error:", err);
    ui.showToast(err.message, "error");
  }
}

async function handleRequest() {
  const subj = els.pubSubject.value.trim();
  if (!subj) {
    ui.showToast("Enter a subject", "error");
    return;
  }

  const timeout = parseInt(els.reqTimeout.value, 10) || DEFAULT_RPC_TIMEOUT_MS;

  try {
    storage.addSubjectToHistory(subj);
    refreshHistoryUi();
    els.btnReq.disabled = true;

    const msg = await nats.request(subj, els.pubPayload.value, ui.readHeadersJson(), timeout);
    ui.renderMessage(msg.subject, msg.data, true, msg.headers);
  } catch (err) {
    console.error("Request error:", err);
    ui.showToast(err.message, "error");
  } finally {
    els.btnReq.disabled = false;
  }
}

/**
 * Collapse the composer to its title bar so the log gets the space.
 *
 * Everything below the head goes, template controls included - they load into
 * fields that are no longer on screen, and leaving half the card behind reads
 * as broken rather than collapsed.
 */
function handleComposerToggle() {
  const expanded = els.btnComposerToggle.getAttribute("aria-expanded") === "true";
  els.btnComposerToggle.setAttribute("aria-expanded", String(!expanded));
  els.composer.classList.toggle("is-collapsed", expanded);
  els.btnComposerToggle.querySelector(".chev").textContent = expanded ? "▸" : "▾";
}

function setHeadersVisible(visible) {
  els.headerContainer.hidden = !visible;
  els.btnHeaderToggle.setAttribute("aria-expanded", String(visible));
  els.btnHeaderToggle.querySelector(".chev").textContent = visible ? "▾" : "▸";
}

function handleHeaderToggle() {
  setHeadersVisible(els.headerContainer.hidden);
}

els.btnPub.addEventListener("click", handlePublish);
els.btnReq.addEventListener("click", handleRequest);
els.btnHeaderToggle.addEventListener("click", handleHeaderToggle);
els.btnComposerToggle.addEventListener("click", handleComposerToggle);

// Ctrl/Cmd+Enter to publish
els.pubPayload.addEventListener("keydown", (e) => {
  if ((e.ctrlKey || e.metaKey) && e.key === "Enter") handlePublish();
});

// ============================================================================
// MESSAGE TEMPLATE HANDLERS
// ============================================================================
// Saved subject/payload/headers combos - like Postman collections for NATS

function refreshTemplateUi() {
  ui.renderNamedOptions(els.templateSelect, storage.getTemplates(), "-- Templates --");
}

function handleTemplateChange() {
  const template = storage.getTemplate(els.templateSelect.value);
  if (!template) return;

  els.pubSubject.value = template.subject || "";
  els.pubPayload.value = template.payload || "";
  // Templates still store headers as a JSON string; fan it back out into rows
  let headerObj = {};
  if (template.headers) {
    try {
      headerObj = JSON.parse(template.headers);
    } catch {
      console.warn(`Template '${template.name}' has unparseable headers; ignoring them.`);
    }
  }
  ui.renderHeaderRows(headerObj);
  if (Object.keys(headerObj).length > 0) setHeadersVisible(true);
  utils.validateJsonInput(els.pubPayload);
}

async function handleTemplateSave() {
  const name = await dlg.promptDialog({
    title: "Save message template",
    label: "Template name",
    value: els.templateSelect.value || els.pubSubject.value.trim(),
    placeholder: "e.g. reset-device",
  });
  if (!name) return;

  storage.saveTemplate({
    name,
    subject: els.pubSubject.value.trim(),
    payload: els.pubPayload.value,
    headers: ui.readHeadersJson(),
  });
  refreshTemplateUi();
  els.templateSelect.value = name;
  ui.showToast(`Template '${name}' saved`, "success");
}

async function handleTemplateDelete() {
  const name = els.templateSelect.value;
  if (!name) {
    ui.showToast("Select a template first", "info");
    return;
  }

  const ok = await dlg.confirmDialog({
    title: "Delete template",
    message: `Delete the template '${name}'?`,
    confirmLabel: "Delete",
    danger: true,
  });
  if (!ok) return;

  storage.deleteTemplate(name);
  refreshTemplateUi();
  els.templateSelect.value = "";
  ui.showToast(`Template '${name}' deleted`, "info");
}

els.templateSelect.addEventListener("change", handleTemplateChange);
els.btnTemplateSave.addEventListener("click", handleTemplateSave);
els.btnTemplateDelete.addEventListener("click", handleTemplateDelete);

// ============================================================================
// MESSAGE LOG HANDLERS
// ============================================================================

els.btnClear.addEventListener("click", () => ui.clearLogs());
els.logFilter.addEventListener("keyup", (e) => ui.filterLogs(e.target.value));
els.btnPause.addEventListener("click", ui.toggleLogPause);
els.btnLogOrder.addEventListener("click", () => {
  const next = !ui.isNewestFirst();
  ui.setNewestFirst(next);
  storage.setLogNewestFirst(next);
});
els.btnDownloadLogs.addEventListener("click", ui.downloadLogs);

// ============================================================================
// INPUT VALIDATION
// ============================================================================

// Payloads and KV values are arbitrary bytes - `hello` and `42` are both
// valid messages - so neither is validated as JSON while it is being typed,
// and neither is silently reformatted on blur. Formatting is a button now:
// ask for it and you get the parse error if it is not JSON after all.
function formatInto(el) {
  const err = utils.formatJson(el);
  if (err) ui.showToast(`Not valid JSON - ${err}`, "error");
}

els.btnFormatPayload.addEventListener("click", () => formatInto(els.pubPayload));
els.btnKvFormat.addEventListener("click", () => formatInto(els.kvValueInput));

// ============================================================================
// KV STORE - BUCKETS
// ============================================================================

function handleKvCreate() {
  dlg.jsonDialog({
    title: "Create KV bucket",
    hint: "Bucket configuration. Only `bucket` is required.",
    saveLabel: "Create",
    value: {
      bucket: "new-bucket",
      description: "My KV Bucket",
      history: 5,
      storage: "file",
      num_replicas: 1,
    },
    onSave: async (config) => {
      await nats.createKvBucket(config);
      ui.showToast(`Bucket ${config.bucket} created`, "success");
      loadKvBucketsWrapper();
    },
  });
}

async function handleKvEdit() {
  const bucket = appState.currentKvBucket;
  if (!bucket) {
    ui.showToast("Select a bucket first", "info");
    return;
  }

  try {
    const status = await nats.getKvStatus();
    dlg.jsonDialog({
      title: `Edit bucket: ${bucket}`,
      hint: "Some fields cannot be changed after creation; the server will reject those.",
      value: {
        bucket: status.bucket,
        description: status.description,
        history: status.history,
        ttl: status.ttl,
        max_bytes: status.max_bytes,
        max_value_size: status.max_value_size,
        storage: status.storage,
        num_replicas: status.num_replicas,
      },
      onSave: async (config) => {
        await nats.updateKvBucket(config);
        ui.showToast(`Bucket ${bucket} updated`, "success");
      },
    });
  } catch (e) {
    console.error("KV status error:", e);
    ui.showToast("Error fetching KV status: " + e.message, "error");
  }
}

/**
 * Load the bucket list, restoring the previous selection when it still exists
 * so switching tabs doesn't lose the user's working context.
 */
async function loadKvBucketsWrapper() {
  try {
    const list = await nats.getKvBuckets();
    ui.renderKvBuckets(list, (name) => selectKvBucket(name));
    ui.setKvStatus(`${list.length} bucket${list.length === 1 ? "" : "s"}`);

    if (appState.currentKvBucket && list.includes(appState.currentKvBucket)) {
      ui.highlightBucket(appState.currentKvBucket);
      if (!appState.currentKvWatcher) {
        // No live watcher (e.g. after a reconnect) - reopen so the handle is fresh
        await selectKvBucket(appState.currentKvBucket);
      }
    } else if (appState.currentKvBucket) {
      // Bucket was deleted while the user was on another tab
      appState.currentKvBucket = null;
      cleanupKvUi();
      ui.setKvStatus("Previous bucket no longer exists", true);
    }
  } catch (e) {
    console.error("Load KV buckets error:", e);
    ui.setEmpty(els.kvBucketList, e.message, true);
    ui.setKvStatus("Error loading buckets", true);
    ui.showToast(e.message, "error");
  }
}

/**
 * Open a bucket: start watching its keys and reveal the key detail pane.
 */
async function selectKvBucket(bucket) {
  appState.currentKvBucket = bucket;
  ui.highlightBucket(bucket);

  els.kvBucketLabel.textContent = bucket || "Keys";
  els.btnKvEdit.disabled = !bucket;
  els.btnKvDeleteBucket.disabled = !bucket;

  els.kvKeyList.replaceChildren();
  appState.kvKeys.clear();

  // Stop the previous watcher or it keeps delivering into a dead bucket
  if (appState.currentKvWatcher) {
    appState.currentKvWatcher.stop();
    appState.currentKvWatcher = null;
  }

  if (!bucket) {
    cleanupKvUi();
    return;
  }

  // Reveal the detail pane so a new key can be typed straight away
  els.kvEmptyState.hidden = true;
  els.kvDetailView.hidden = false;
  ui.setEmpty(els.kvKeyList, "No keys yet");

  try {
    await nats.openKvBucket(bucket);
    ui.setKvStatus(`Watching ${bucket}`);

    appState.currentKvWatcher = await nats.watchKvBucket((key, op) => {
      if (op === "DEL" || op === "PURGE") {
        appState.kvKeys.delete(key);
        ui.removeKvKey(key);
      } else if (!appState.kvKeys.has(key)) {
        appState.kvKeys.add(key);
        ui.addKvKey(key, (k) => selectKeyWrapper(k));
        if (els.kvFilter.value) ui.filterList(els.kvFilter, els.kvKeyList);
      }
    });
  } catch (e) {
    console.error("KV bucket open error:", e);
    ui.setKvStatus(e.message, true);
    ui.showToast(e.message, "error");
  }
}

/** Clear key-level UI without touching the watcher. */
function cleanupKvUi() {
  ui.setEmpty(els.kvKeyList, "Select a bucket");
  els.kvBucketLabel.textContent = "Keys";
  els.btnKvEdit.disabled = true;
  els.btnKvDeleteBucket.disabled = true;
  els.kvKeyInput.value = "";
  els.kvValueInput.value = "";
  els.kvValueHighlighter.textContent = "";
  els.kvRevLabel.textContent = "";
  els.kvHistoryCount.textContent = "0";
  ui.setEmpty(els.kvHistoryList, "Select a key to view its revisions");
  els.kvDetailView.hidden = true;
  els.kvEmptyState.hidden = false;
  appState.kvKeys.clear();
}

async function handleKvDeleteBucket() {
  const bucket = appState.currentKvBucket;
  if (!bucket) {
    ui.showToast("Select a bucket first", "info");
    return;
  }

  const ok = await dlg.typeToConfirmDialog({
    title: "Delete KV bucket",
    message: `This permanently deletes the bucket '${bucket}', every key in it, and all revision history. It cannot be undone.`,
    phrase: bucket,
    confirmLabel: "Delete bucket",
  });
  if (!ok) return;

  try {
    // Stop watching before destroying the underlying stream
    if (appState.currentKvWatcher) {
      appState.currentKvWatcher.stop();
      appState.currentKvWatcher = null;
    }
    await nats.destroyKvBucket(bucket);
    appState.currentKvBucket = null;
    cleanupKvUi();
    ui.showToast(`Bucket '${bucket}' deleted`, "success");
    loadKvBucketsWrapper();
  } catch (e) {
    console.error("KV bucket delete error:", e);
    ui.showToast(e.message, "error");
  }
}

// ============================================================================
// KV STORE - KEYS
// ============================================================================

/**
 * Toggle between the syntax-highlighted view and the raw editor.
 */
function setKvEditMode(isEdit) {
  appState.kvEditMode = isEdit;

  els.kvValueInput.hidden = !isEdit;
  els.kvValueHighlighter.hidden = isEdit;
  els.btnKvFormat.hidden = !isEdit;
  els.btnKvToggleMode.textContent = isEdit ? "👁 View" : "✎ Edit";

  if (isEdit) {
    els.kvValueInput.focus();
  } else {
    renderKvValueView();
  }
}

/** Paint the read-only value pane from whatever is in the editor. */
function renderKvValueView() {
  try {
    els.kvValueHighlighter.innerHTML = utils.syntaxHighlight(JSON.parse(els.kvValueInput.value));
  } catch {
    els.kvValueHighlighter.textContent = els.kvValueInput.value;
  }
}

/**
 * Load a key's current value (HEAD) and its revision history.
 */
async function selectKeyWrapper(key) {
  ui.highlightKvKey(key);
  els.kvKeyInput.value = key;
  els.kvValueInput.value = "";
  els.kvValueHighlighter.textContent = "Loading…";
  els.kvRevLabel.textContent = "";

  try {
    const res = await nats.getKvValue(key);
    if (res) {
      els.kvValueInput.value = res.value;
      utils.formatJson(els.kvValueInput);
      setKvEditMode(false);
      els.kvRevLabel.textContent = `rev ${res.revision}`;
      ui.setKvStatus(`Loaded '${key}'`);
    } else {
      els.kvValueHighlighter.textContent = "";
      ui.setKvStatus("Key not found", true);
    }

    const hist = await nats.getKvHistory(key);
    ui.renderKvHistory(hist, (entry) => {
      const isDelete = entry.operation === "DEL" || entry.operation === "PURGE";

      if (isDelete) {
        els.kvValueInput.value = "";
        els.kvValueHighlighter.textContent = "// [deleted revision]";
      } else {
        els.kvValueInput.value = entry.value;
        utils.formatJson(els.kvValueInput);
        renderKvValueView();
      }

      // Force view mode so an old revision can't be saved over HEAD by accident
      setKvEditMode(false);
      els.kvRevLabel.textContent = `rev ${entry.revision} (historical)`;
      ui.switchSubTab("kv", "value");
      ui.setKvStatus(`Viewing revision ${entry.revision}`);
    });
  } catch (e) {
    console.error("KV select key error:", e);
    els.kvValueInput.value = "";
    els.kvValueHighlighter.textContent = "";
    ui.setKvStatus(e.message, true);
    ui.showToast(e.message, "error");
  }
}

async function handleKvGet() {
  const key = els.kvKeyInput.value.trim();
  if (!key) {
    ui.showToast("Enter a key name", "error");
    return;
  }
  await selectKeyWrapper(key);
}

async function handleKvCopy() {
  const val = els.kvValueInput.value;
  if (!val) return;

  if (await utils.copyToClipboard(val)) {
    const orig = els.btnKvCopy.textContent;
    els.btnKvCopy.textContent = "Copied!";
    setTimeout(() => (els.btnKvCopy.textContent = orig), 1000);
  }
}

async function handleKvPut() {
  const key = els.kvKeyInput.value.trim();
  if (!key) {
    ui.showToast("Enter a key name", "error");
    return;
  }

  try {
    await nats.putKvValue(key, els.kvValueInput.value);
    ui.setKvStatus(`Saved '${key}'`);
    ui.showToast("Key saved", "success");
    selectKeyWrapper(key);
  } catch (e) {
    console.error("KV put error:", e);
    ui.setKvStatus(e.message, true);
    ui.showToast(e.message, "error");
  }
}

async function handleKvDelete() {
  const key = els.kvKeyInput.value.trim();
  if (!key) return;

  const ok = await dlg.confirmDialog({
    title: "Delete key",
    message: `Delete '${key}'? Its revision history is kept, so the value can still be recovered.`,
    confirmLabel: "Delete key",
    danger: true,
  });
  if (!ok) return;

  try {
    await nats.deleteKvValue(key);
    ui.setKvStatus(`Deleted '${key}'`);
    els.kvValueInput.value = "";
    els.kvValueHighlighter.textContent = "";
    ui.showToast("Key deleted", "info");
  } catch (e) {
    console.error("KV delete error:", e);
    ui.setKvStatus(e.message, true);
    ui.showToast(e.message, "error");
  }
}

async function handleKvPurge() {
  const key = els.kvKeyInput.value.trim();
  if (!key) return;

  const ok = await dlg.confirmDialog({
    title: "Purge key",
    message: `Purge '${key}'?\n\nThis removes the key AND all of its revision history. It cannot be undone.`,
    confirmLabel: "Purge key",
    danger: true,
  });
  if (!ok) return;

  try {
    await nats.purgeKvValue(key);
    ui.setKvStatus(`Purged '${key}'`);
    els.kvValueInput.value = "";
    els.kvValueHighlighter.textContent = "";
    ui.setEmpty(els.kvHistoryList, "Key purged");
    els.kvHistoryCount.textContent = "0";
    ui.showToast("Key purged (history removed)", "info");
  } catch (e) {
    console.error("KV purge error:", e);
    ui.setKvStatus(e.message, true);
    ui.showToast(e.message, "error");
  }
}

els.btnKvCreate.addEventListener("click", handleKvCreate);
els.btnKvEdit.addEventListener("click", handleKvEdit);
els.btnKvDeleteBucket.addEventListener("click", handleKvDeleteBucket);
els.btnKvRefresh.addEventListener("click", loadKvBucketsWrapper);
els.btnKvToggleMode.addEventListener("click", () => setKvEditMode(!appState.kvEditMode));
els.btnKvGet.addEventListener("click", handleKvGet);
els.btnKvCopy.addEventListener("click", handleKvCopy);
els.btnKvPut.addEventListener("click", handleKvPut);
els.btnKvDelete.addEventListener("click", handleKvDelete);
els.btnKvPurge.addEventListener("click", handleKvPurge);
els.kvKeyInput.addEventListener("keyup", (e) => {
  if (e.key === "Enter") handleKvGet();
});
els.kvFilter.addEventListener("keyup", () => ui.filterList(els.kvFilter, els.kvKeyList));
els.kvBucketFilter.addEventListener("keyup", () => ui.filterList(els.kvBucketFilter, els.kvBucketList));

// ============================================================================
// STREAM HANDLERS
// ============================================================================

function handleStreamCreate() {
  dlg.jsonDialog({
    title: "Create stream",
    hint: "Stream configuration. `name` and `subjects` are the essentials.",
    saveLabel: "Create",
    value: {
      name: "NEW_STREAM",
      description: "Stream Description",
      subjects: ["events.>"],
      retention: "limits",
      max_msgs: -1,
      max_bytes: -1,
      max_age: 0,
      discard: "old",
      storage: "file",
      num_replicas: 1,
      duplicate_window: 120000000000,
    },
    onSave: async (config) => {
      await nats.createStream(config);
      ui.showToast(`Stream ${config.name} created`, "success");
      loadStreamsWrapper();
    },
  });
}

async function handleStreamEdit() {
  if (!appState.currentStream) {
    ui.showToast("Select a stream first", "info");
    return;
  }

  try {
    const info = await nats.getStreamInfo(appState.currentStream);
    const name = appState.currentStream;
    dlg.jsonDialog({
      title: `Edit stream: ${name}`,
      hint: "Some fields are immutable after creation; the server will reject those.",
      value: info.config,
      onSave: async (config) => {
        await nats.updateStream(config);
        ui.showToast(`Stream ${name} updated`, "success");
        selectStreamWrapper(name);
      },
    });
  } catch (e) {
    console.error("Stream info error:", e);
    ui.showToast("Error fetching stream info: " + e.message, "error");
  }
}

async function loadStreamsWrapper() {
  ui.setEmpty(els.streamList, "Loading…");
  try {
    const list = await nats.getStreams();
    list.sort((a, b) => a.config.name.localeCompare(b.config.name));
    ui.renderStreamList(list, (name) => selectStreamWrapper(name));

    if (els.streamFilter.value) ui.filterList(els.streamFilter, els.streamList);
    if (appState.currentStream) ui.highlightStream(appState.currentStream);
  } catch (e) {
    console.error("Load streams error:", e);
    ui.setEmpty(els.streamList, e.message, true);
    ui.showToast(e.message, "error");
  }
}

async function selectStreamWrapper(name) {
  stopTailUi();
  ui.highlightStream(name);
  appState.currentStream = name;

  els.streamEmptyState.hidden = true;
  els.streamDetailView.hidden = true;
  ui.setEmpty(els.streamMsgContainer, "Click Load to view stream messages");
  ui.setEmpty(els.consumerList, "Loading…");

  try {
    const info = await nats.getStreamInfo(name);
    const { config: conf, state } = info;

    els.streamNameTitle.textContent = conf.name;
    els.streamCreated.textContent = `created ${new Date(info.created).toLocaleString()}`;
    els.streamSubjects.textContent = (conf.subjects || []).join(", ") || "-";
    els.streamStorage.textContent = conf.storage;
    els.streamRetention.textContent = conf.retention;
    els.streamMsgs.textContent = state.messages.toLocaleString();
    els.streamBytes.textContent = utils.formatBytes(state.bytes);
    els.streamFirstSeq.textContent = state.first_seq;
    els.streamLastSeq.textContent = state.last_seq;
    els.streamConsumerCount.textContent = state.consumer_count;
    els.consumerTabCount.textContent = state.consumer_count;

    // Default the message range to the last 50 sequences
    const start = Math.max(state.last_seq - 49, state.first_seq);
    els.msgEndSeq.value = state.last_seq;
    els.msgStartSeq.value = start > 0 ? start : 0;

    els.streamDetailView.hidden = false;

    // Consumers load automatically - no extra click needed
    handleLoadConsumers();
  } catch (e) {
    console.error("Stream select error:", e);
    els.streamEmptyState.hidden = false;
    ui.showToast(`Error loading stream info: ${e.message}`, "error");
  }
}

async function handleStreamViewMessages() {
  if (!appState.currentStream) return;
  stopTailUi();

  const start = parseInt(els.msgStartSeq.value, 10) || 0;
  const end = parseInt(els.msgEndSeq.value, 10) || 0;
  const subjectFilter = els.msgSubjectFilter.value.trim();

  if (end < start) {
    ui.showToast("End sequence cannot be less than start sequence", "error");
    return;
  }

  els.btnStreamViewMsgs.disabled = true;
  ui.setEmpty(els.streamMsgContainer, "Loading…");

  try {
    const msgs = await nats.getStreamMessageRange(
      appState.currentStream, start, end, subjectFilter, MAX_STREAM_MSG_FETCH
    );
    ui.renderStreamMessages(msgs);
    if (els.streamMsgFilter.value) {
      ui.filterList(els.streamMsgFilter, els.streamMsgContainer, ".stream-msg-entry");
    }
  } catch (e) {
    console.error("Stream messages error:", e);
    ui.setEmpty(els.streamMsgContainer, e.message, true);
    ui.showToast(e.message, "error");
  } finally {
    els.btnStreamViewMsgs.disabled = false;
  }
}

/** Reset the tail button/state without touching the message container. */
function stopTailUi() {
  nats.stopStreamTail();
  appState.isTailing = false;
  els.btnStreamTail.textContent = "▶ Tail";
  els.btnStreamTail.classList.remove("paused");
}

async function handleStreamTailToggle() {
  if (!appState.currentStream) return;

  if (appState.isTailing) {
    stopTailUi();
    ui.showToast("Tail stopped", "info");
    return;
  }

  const subjectFilter = els.msgSubjectFilter.value.trim();

  try {
    await nats.startStreamTail(appState.currentStream, subjectFilter, (m) => {
      ui.appendStreamTailMessage(m);
      if (els.streamMsgFilter.value) {
        ui.filterList(els.streamMsgFilter, els.streamMsgContainer, ".stream-msg-entry");
      }
    });
    appState.isTailing = true;
    els.btnStreamTail.textContent = "⏹ Stop";
    els.btnStreamTail.classList.add("paused");
    ui.showTailPlaceholder();
    ui.showToast(`Tailing ${appState.currentStream}${subjectFilter ? ` (${subjectFilter})` : ""}…`, "success");
  } catch (e) {
    console.error("Stream tail error:", e);
    ui.showToast(e.message, "error");
  }
}

function handleStreamClearMessages() {
  stopTailUi();
  ui.setEmpty(els.streamMsgContainer, "Click Load to view stream messages");
  els.streamMsgFilter.value = "";
}

async function handleLoadConsumers() {
  if (!appState.currentStream) return;

  els.btnLoadConsumers.disabled = true;
  ui.setEmpty(els.consumerList, "Loading…");

  try {
    const consumers = await nats.getConsumers(appState.currentStream);
    ui.renderStreamConsumers(consumers);
  } catch (e) {
    console.error("Load consumers error:", e);
    ui.setEmpty(els.consumerList, e.message, true);
    ui.showToast(e.message, "error");
  } finally {
    els.btnLoadConsumers.disabled = false;
  }
}

async function handleStreamPurge() {
  if (!appState.currentStream) return;
  const name = appState.currentStream;

  const ok = await dlg.confirmDialog({
    title: "Purge stream messages",
    message: `Purge ALL messages from '${name}'?\n\nThe stream and its consumers stay, but every stored message is removed. This cannot be undone.`,
    confirmLabel: "Purge messages",
    danger: true,
  });
  if (!ok) return;

  try {
    await nats.purgeStream(name);
    ui.showToast(`Stream '${name}' purged`, "success");
    selectStreamWrapper(name);
  } catch (e) {
    console.error("Stream purge error:", e);
    ui.showToast(e.message, "error");
  }
}

async function handleStreamDelete() {
  if (!appState.currentStream) return;
  const name = appState.currentStream;

  const ok = await dlg.typeToConfirmDialog({
    title: "Delete stream",
    message: `This permanently deletes the stream '${name}', all of its messages, and all of its consumers. It cannot be undone.`,
    phrase: name,
    confirmLabel: "Delete stream",
  });
  if (!ok) return;

  try {
    stopTailUi();
    await nats.deleteStream(name);
    ui.showToast(`Stream '${name}' deleted`, "success");
    appState.currentStream = null;
    els.streamDetailView.hidden = true;
    els.streamEmptyState.hidden = false;
    loadStreamsWrapper();
  } catch (e) {
    console.error("Stream delete error:", e);
    ui.showToast(e.message, "error");
  }
}

// ============================================================================
// CONSUMER MANAGEMENT
// ============================================================================

function handleConsumerCreate() {
  if (!appState.currentStream) return;
  const stream = appState.currentStream;

  dlg.jsonDialog({
    title: `Create consumer on ${stream}`,
    hint: "Leave `durable_name` empty for an ephemeral consumer.",
    saveLabel: "Create",
    value: {
      durable_name: "my-consumer",
      description: "",
      ack_policy: "explicit",
      deliver_policy: "all",
      filter_subject: "",
      max_ack_pending: 1000,
      max_deliver: -1,
      ack_wait: 30000000000,
    },
    onSave: async (config) => {
      await nats.createConsumer(stream, config);
      ui.showToast(`Consumer ${config.durable_name || config.name} created`, "success");
      handleLoadConsumers();
    },
  });
}

async function handleConsumerEdit(consumerName) {
  if (!appState.currentStream) return;
  const stream = appState.currentStream;

  try {
    const info = await nats.getConsumerInfo(stream, consumerName);
    const isDurable = !!info.config.durable_name;

    dlg.jsonDialog({
      title: `Consumer: ${consumerName}`,
      hint: isDurable
        ? "Edit and save to update this durable consumer."
        : "This consumer is ephemeral and cannot be updated - view only.",
      value: info.config,
      onSave: async (config) => {
        if (!isDurable) throw new Error("Ephemeral consumers cannot be updated.");
        await nats.updateConsumer(stream, consumerName, config);
        ui.showToast(`Consumer ${consumerName} updated`, "success");
        handleLoadConsumers();
      },
    });
  } catch (e) {
    console.error("Consumer info error:", e);
    ui.showToast(e.message, "error");
  }
}

async function handleConsumerDelete(consumerName) {
  if (!appState.currentStream) return;

  const ok = await dlg.confirmDialog({
    title: "Delete consumer",
    message: `Delete consumer '${consumerName}' from stream '${appState.currentStream}'?`,
    confirmLabel: "Delete consumer",
    danger: true,
  });
  if (!ok) return;

  try {
    await nats.deleteConsumer(appState.currentStream, consumerName);
    ui.showToast(`Consumer '${consumerName}' deleted`, "success");
    handleLoadConsumers();
  } catch (e) {
    console.error("Consumer delete error:", e);
    ui.showToast(e.message, "error");
  }
}

function setupConsumerEventDelegation() {
  els.consumerList.addEventListener("click", (e) => {
    const name = e.target.dataset.consumer;
    if (!name) return;

    if (e.target.classList.contains("consumer-edit")) handleConsumerEdit(name);
    else if (e.target.classList.contains("consumer-delete")) handleConsumerDelete(name);
  });
}

// ============================================================================
// STREAM EVENT WIRING
// ============================================================================

els.btnStreamCreate.addEventListener("click", handleStreamCreate);
els.btnStreamEdit.addEventListener("click", handleStreamEdit);
els.btnStreamRefresh.addEventListener("click", loadStreamsWrapper);
els.btnStreamViewMsgs.addEventListener("click", handleStreamViewMessages);
els.btnStreamTail.addEventListener("click", handleStreamTailToggle);
els.btnStreamClearMsgs.addEventListener("click", handleStreamClearMessages);
els.btnLoadConsumers.addEventListener("click", handleLoadConsumers);
els.btnConsumerCreate.addEventListener("click", handleConsumerCreate);
els.btnStreamPurge.addEventListener("click", handleStreamPurge);
els.btnStreamDelete.addEventListener("click", handleStreamDelete);
els.streamFilter.addEventListener("keyup", () => ui.filterList(els.streamFilter, els.streamList));
els.streamMsgFilter.addEventListener("keyup", () =>
  ui.filterList(els.streamMsgFilter, els.streamMsgContainer, ".stream-msg-entry")
);
