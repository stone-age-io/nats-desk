// ============================================================================
// UTILITY FUNCTIONS
// ============================================================================
// Pure helper functions with no side effects
// No DOM manipulation, no localStorage, no globals

// ============================================================================
// JSON UTILITIES
// ============================================================================

/**
 * Pretty-print a textarea's contents as JSON, in place.
 *
 * Returns null on success, or the parse error to report. NATS payloads and KV
 * values are arbitrary bytes - a bare string or number is a perfectly good
 * message - so "not JSON" is something to answer when asked, never an error
 * state to flag while someone is typing.
 *
 * @param {HTMLTextAreaElement|HTMLInputElement} el
 * @returns {string|null} - Error message, or null if it formatted
 */
export function formatJson(el) {
  const val = el.value.trim();
  if (!val) return "Nothing to format";
  try {
    el.value = JSON.stringify(JSON.parse(val), null, 2);
    return null;
  } catch (e) {
    return e.message;
  }
}

/**
 * Flag a field red when it is not JSON. Only for inputs where JSON is the
 * required format - stream/consumer/bucket configs - never for free-form
 * message payloads. See formatJson above.
 */
export function validateJsonInput(el) {
  const val = el.value.trim();
  if (!val) {
    el.classList.remove("input-error");
    return true;
  }
  try {
    JSON.parse(val);
    el.classList.remove("input-error");
    return true;
  } catch (e) {
    el.classList.add("input-error");
    return false;
  }
}

// ============================================================================
// FORMATTING UTILITIES
// ============================================================================

export function formatBytes(bytes, decimals = 2) {
    if (!+bytes) return '0 Bytes';
    const k = 1024;
    const dm = decimals < 0 ? 0 : decimals;
    const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return `${parseFloat((bytes / Math.pow(k, i)).toFixed(dm))} ${sizes[i]}`;
}

export function escapeHtml(str) {
    if (typeof str !== 'string') return str;
    return str.replace(/&/g, '&amp;')
              .replace(/</g, '&lt;')
              .replace(/>/g, '&gt;')
              .replace(/"/g, '&quot;')
              .replace(/'/g, '&#039;');
}

/**
 * Syntax highlight JSON for display
 * Returns HTML string with color-coded spans
 */
export function syntaxHighlight(json) {
    if (typeof json !== 'string') {
      json = JSON.stringify(json, null, 2);
    }
    json = json.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    
    return json.replace(/("(\\u[a-zA-Z0-9]{4}|\\[^u]|[^\\"])*"(\s*:)?|\b(true|false|null)\b|-?\d+(?:\.\d*)?(?:[eE][+\-]?\d+)?)/g, function (match) {
      let cls = 'number';
      if (/^"/.test(match)) {
        if (/:$/.test(match)) {
          cls = 'key';
        } else {
          cls = 'string';
        }
      } else if (/true|false/.test(match)) {
        cls = 'boolean';
      } else if (/null/.test(match)) {
        cls = 'null';
      }
      return '<span class="' + cls + '">' + match + '</span>';
    });
}

/**
 * A Go time.Duration, which is how micro reports processing times: an integer
 * count of nanoseconds.
 */
export function formatDuration(ns) {
  if (ns == null) return "-";
  if (ns < 1e3) return `${ns}ns`;
  if (ns < 1e6) return `${(ns / 1e3).toFixed(ns < 1e4 ? 1 : 0)}µs`;
  if (ns < 1e9) return `${(ns / 1e6).toFixed(ns < 1e7 ? 1 : 0)}ms`;
  return `${(ns / 1e9).toFixed(1)}s`;
}

/** How long ago something started, coarsely: "45s", "12m", "3h 4m", "2d 3h". */
export function formatAge(ms) {
  if (ms == null || ms < 0) return "-";
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}

// ============================================================================
// MICRO SERVICES
// ============================================================================
// The census passes each instance's INFO and STATS replies through as the
// service sent them; these read the few fields the UI needs out of them.

/** Endpoints as INFO describes them: name, subject, queue_group, metadata. */
export function serviceEndpoints(inst) {
  return (inst && inst.info && inst.info.endpoints) || [];
}

/**
 * The STATS entry for one endpoint, or null when this instance's stats are
 * unknown. Matched on name and subject together, because a service may
 * register the same name in two groups.
 */
export function endpointStats(inst, ep) {
  const list = inst && inst.stats && inst.stats.endpoints;
  if (!list) return null;
  return list.find((s) => s.name === ep.name && s.subject === ep.subject) || null;
}

/**
 * Requests and errors summed over an instance's endpoints, or null when its
 * stats are unknown. Null, not zeros: an instance that did not answer STATS
 * has not been shown to have handled nothing.
 */
export function serviceTotals(inst) {
  const list = inst && inst.stats && inst.stats.endpoints;
  if (!list) return null;
  return list.reduce(
    (t, s) => ({ requests: t.requests + (s.num_requests || 0), errors: t.errors + (s.num_errors || 0) }),
    { requests: 0, errors: 0 },
  );
}

/**
 * One entry per service name, in census order. Versions and errors count live
 * instances only: a gone one's stats are its last words, not its state. errors
 * stays null when no live instance answered STATS, for the same reason
 * serviceTotals returns null.
 */
export function groupServices(instances) {
  const groups = new Map();
  for (const inst of instances) {
    let g = groups.get(inst.name);
    if (!g) {
      g = { name: inst.name, instances: [], live: 0, gone: 0, versions: new Set(), errors: null };
      groups.set(inst.name, g);
    }
    g.instances.push(inst);
    if (inst.gone) {
      g.gone++;
      continue;
    }
    g.live++;
    g.versions.add(inst.version);
    const t = serviceTotals(inst);
    if (t) g.errors = (g.errors || 0) + t.errors;
  }
  return [...groups.values()];
}

/** "k=v k=v", keys sorted - the same order the backend sorts instances in. */
export function metadataText(meta) {
  return Object.keys(meta || {})
    .sort()
    .map((k) => `${k}=${meta[k]}`)
    .join(" ");
}

/** Does this subject still contain a wildcard token? Such a subject cannot be published to. */
export function hasWildcardToken(subject) {
  return subject.split(".").some((t) => t === "*" || t === ">");
}

/** A header's first value, looked up case-insensitively. */
export function headerValue(headers, name) {
  if (!headers) return null;
  const want = name.toLowerCase();
  const key = Object.keys(headers).find((k) => k.toLowerCase() === want);
  if (key === undefined) return null;
  const v = headers[key];
  return Array.isArray(v) ? v[0] : v;
}

// ============================================================================
// CLIPBOARD UTILITIES
// ============================================================================

/**
 * Copy text content to clipboard
 * Returns promise that resolves on success
 */
export async function copyToClipboard(text) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch (e) {
    console.error("Failed to copy to clipboard:", e);
    return false;
  }
}
