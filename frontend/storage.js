// ============================================================================
// CENTRALIZED STORAGE MANAGEMENT
// ============================================================================
// All localStorage operations in one place
// Makes it easy to change storage backend or add encryption later

// ============================================================================
// STORAGE KEYS
// ============================================================================
// All keys defined as constants - easier to find/change/avoid typos

const KEYS = {
  CONNECTION_URL: "nats_url",
  SUBJECT_HISTORY: "nats_subject_history",
  URL_HISTORY: "nats_url_history",
  PROFILES: "nats_profiles",
  TEMPLATES: "nats_msg_templates",
  SAVED_SUBS: "nats_saved_subs",
  EXCLUDE_SYSTEM: "nats_exclude_system",
  LOG_NEWEST_FIRST: "nats_log_newest_first",
  PANE_SIZES: "nats_pane_sizes",
};

// ============================================================================
// CONFIGURATION
// ============================================================================

const MAX_SUBJECT_HISTORY = 50;
const MAX_URL_HISTORY = 10;
const MAX_SAVED_SUBS_PER_SERVER = 30;

// ============================================================================
// GENERIC JSON HELPERS
// ============================================================================

function loadJson(key, fallback) {
  try {
    const raw = localStorage.getItem(key);
    return raw ? JSON.parse(raw) : fallback;
  } catch (e) {
    console.error(`Failed to load ${key}:`, e);
    return fallback;
  }
}

function saveJson(key, value) {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch (e) {
    console.error(`Failed to save ${key}:`, e);
  }
}

// ============================================================================
// CONNECTION URL
// ============================================================================

export function getLastUrl() {
  return localStorage.getItem(KEYS.CONNECTION_URL) || "";
}

export function saveUrl(url) {
  if (!url) return;
  localStorage.setItem(KEYS.CONNECTION_URL, url);
}

// ============================================================================
// SUBJECT HISTORY
// ============================================================================

export function getSubjectHistory() {
  try {
    const raw = localStorage.getItem(KEYS.SUBJECT_HISTORY);
    return raw ? JSON.parse(raw) : [];
  } catch (e) {
    console.error("Failed to load subject history:", e);
    return [];
  }
}

export function addSubjectToHistory(subject) {
  if (!subject) return;
  
  const history = getSubjectHistory();
  
  // Remove duplicates
  const filtered = history.filter(s => s !== subject);
  
  // Add to front
  filtered.unshift(subject);
  
  // Trim to max size
  const trimmed = filtered.slice(0, MAX_SUBJECT_HISTORY);
  
  try {
    localStorage.setItem(KEYS.SUBJECT_HISTORY, JSON.stringify(trimmed));
  } catch (e) {
    console.error("Failed to save subject history:", e);
  }
}

// ============================================================================
// URL HISTORY
// ============================================================================

export function getUrlHistory() {
  try {
    const raw = localStorage.getItem(KEYS.URL_HISTORY);
    return raw ? JSON.parse(raw) : [];
  } catch (e) {
    console.error("Failed to load URL history:", e);
    return [];
  }
}

export function addUrlToHistory(url) {
  if (!url) return;
  
  const history = getUrlHistory();
  
  // Remove duplicates
  const filtered = history.filter(u => u !== url);
  
  // Add to front
  filtered.unshift(url);
  
  // Trim to max size
  const trimmed = filtered.slice(0, MAX_URL_HISTORY);
  
  try {
    localStorage.setItem(KEYS.URL_HISTORY, JSON.stringify(trimmed));
  } catch (e) {
    console.error("Failed to save URL history:", e);
  }
}

// ============================================================================
// CONNECTION PROFILES
// ============================================================================
// A profile is { name, url, user, pass, token, credsText }
// Credentials are stored in plaintext localStorage - only saved when the user
// explicitly checks "Remember credentials"

export function getProfiles() {
  return loadJson(KEYS.PROFILES, []);
}

export function saveProfile(profile) {
  if (!profile || !profile.name) return;
  const profiles = getProfiles().filter(p => p.name !== profile.name);
  profiles.push(profile);
  profiles.sort((a, b) => a.name.localeCompare(b.name));
  saveJson(KEYS.PROFILES, profiles);
}

export function getProfile(name) {
  return getProfiles().find(p => p.name === name) || null;
}

export function deleteProfile(name) {
  saveJson(KEYS.PROFILES, getProfiles().filter(p => p.name !== name));
}

// ============================================================================
// MESSAGE TEMPLATES
// ============================================================================
// A template is { name, subject, payload, headers }

export function getTemplates() {
  return loadJson(KEYS.TEMPLATES, []);
}

export function saveTemplate(template) {
  if (!template || !template.name) return;
  const templates = getTemplates().filter(t => t.name !== template.name);
  templates.push(template);
  templates.sort((a, b) => a.name.localeCompare(b.name));
  saveJson(KEYS.TEMPLATES, templates);
}

export function getTemplate(name) {
  return getTemplates().find(t => t.name === name) || null;
}

export function deleteTemplate(name) {
  saveJson(KEYS.TEMPLATES, getTemplates().filter(t => t.name !== name));
}

// ============================================================================
// SAVED SUBSCRIPTIONS (per server URL)
// ============================================================================
// Map of { url: [{ subject, excludeSystem }] } - restored on connect.
// Entries written before the excludeSystem option existed are bare strings;
// they read back as "do not exclude", which is what they used to do.

function normalizeSub(entry) {
  if (typeof entry === "string") return { subject: entry, excludeSystem: false };
  return { subject: entry.subject, excludeSystem: !!entry.excludeSystem };
}

export function getSavedSubscriptions(url) {
  const all = loadJson(KEYS.SAVED_SUBS, {});
  return (all[url] || []).map(normalizeSub);
}

export function addSavedSubscription(url, subject, excludeSystem = false) {
  if (!url || !subject) return;
  const all = loadJson(KEYS.SAVED_SUBS, {});
  const subs = (all[url] || []).map(normalizeSub);

  // Same subject with a different filter setting replaces the old entry,
  // so a re-subscribe is what gets restored next time
  const existing = subs.find(s => s.subject === subject);
  if (existing) existing.excludeSystem = !!excludeSystem;
  else subs.push({ subject, excludeSystem: !!excludeSystem });

  all[url] = subs.slice(-MAX_SAVED_SUBS_PER_SERVER);
  saveJson(KEYS.SAVED_SUBS, all);
}

export function removeSavedSubscription(url, subject) {
  if (!url) return;
  const all = loadJson(KEYS.SAVED_SUBS, {});
  if (all[url]) {
    all[url] = all[url].map(normalizeSub).filter(s => s.subject !== subject);
    if (all[url].length === 0) delete all[url];
    saveJson(KEYS.SAVED_SUBS, all);
  }
}

// ============================================================================
// EXCLUDE-SYSTEM-SUBJECTS PREFERENCE
// ============================================================================
// Sticky default for the subscribe box. Defaults to on: a bare `>` picking up
// every $JS/$SYS/_INBOX message is rarely what someone means by "everything".

export function getExcludeSystem() {
  return localStorage.getItem(KEYS.EXCLUDE_SYSTEM) !== "false";
}

export function setExcludeSystem(exclude) {
  localStorage.setItem(KEYS.EXCLUDE_SYSTEM, String(!!exclude));
}

// ============================================================================
// LOG ORDER PREFERENCE
// ============================================================================
// false (default) = newest at the bottom, like `nats sub` and other tail tools

export function getLogNewestFirst() {
  return localStorage.getItem(KEYS.LOG_NEWEST_FIRST) === "true";
}

export function setLogNewestFirst(newestFirst) {
  localStorage.setItem(KEYS.LOG_NEWEST_FIRST, String(!!newestFirst));
}

// ============================================================================
// PANE SIZES
// ============================================================================
// Shape: { msg: { "0": 320 }, kv: { "0": 180, "1": 300 } }
// Keyed by the grid's data-panes group, then by column index. A column the
// user has never dragged is simply absent, so it keeps the CSS default and
// picks up any future change to that default.

export function getPaneSizes() {
  return loadJson(KEYS.PANE_SIZES, {});
}

/** Pass width = null to forget the override and fall back to the CSS default. */
export function setPaneSize(group, index, width) {
  const all = loadJson(KEYS.PANE_SIZES, {});
  const cols = all[group] || {};

  if (width == null) delete cols[index];
  else cols[index] = Math.round(width);

  if (Object.keys(cols).length === 0) delete all[group];
  else all[group] = cols;

  saveJson(KEYS.PANE_SIZES, all);
}

// ============================================================================
// UTILITY - CLEAR ALL DATA
// ============================================================================

export function clearAllData() {
  Object.values(KEYS).forEach(key => {
    try {
      localStorage.removeItem(key);
    } catch (e) {
      console.error(`Failed to clear ${key}:`, e);
    }
  });
}
