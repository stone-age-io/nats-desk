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
