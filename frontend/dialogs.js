// ============================================================================
// DIALOGS
// ============================================================================
// Every modal in the app, built on the native <dialog> element.
//
// Why native: showModal() gives us focus trapping, Escape-to-close, an inert
// background and focus restoration for free - none of which the old hand-rolled
// .modal-overlay did. It also replaces window.confirm/prompt, which broke the
// dark theme every time they appeared.
//
// Each dialog builds its own element and removes it on close, so there is no
// stale markup in index.html and no shared "which action is this modal for
// right now" state to keep in sync.
//
// Resolution note: dialogs settle synchronously in finish(), not from the
// 'close' event. The close listener only exists to catch Escape and the
// backdrop, so a promise never depends on when that event is delivered.

import * as utils from "./utils.js";

// ============================================================================
// SHELL
// ============================================================================

/**
 * Build a <dialog> with head / body / foot, wired for deterministic settling.
 *
 * @param {string} title
 * @param {object} opts - { narrow, onSettle, cancelValue }
 * @returns {{dialog, form, body, foot, finish}}
 */
function shell(title, { narrow = false, onSettle = () => {}, cancelValue } = {}) {
  const dialog = document.createElement("dialog");
  dialog.className = "app-dialog" + (narrow ? " narrow" : "");

  // Not method="dialog" - we close explicitly so nothing waits on form
  // submission semantics.
  const form = document.createElement("form");
  form.className = "dialog-form";

  const head = document.createElement("div");
  head.className = "dialog-head";
  const heading = document.createElement("h3");
  heading.textContent = title;
  const btnX = document.createElement("button");
  btnX.type = "button";
  btnX.className = "sm-btn";
  btnX.setAttribute("aria-label", "Close");
  btnX.textContent = "✕";
  head.append(heading, btnX);

  const body = document.createElement("div");
  body.className = "dialog-body";

  const foot = document.createElement("div");
  foot.className = "dialog-foot";

  form.append(head, body, foot);
  dialog.append(form);
  document.body.append(dialog);

  let settled = false;

  /** Settle once, then tear the dialog down. Safe to call repeatedly. */
  function finish(value) {
    if (settled) return;
    settled = true;
    onSettle(value);
    try { dialog.close(); } catch { /* already closed */ }
    dialog.remove();
  }

  btnX.addEventListener("click", () => finish(cancelValue));

  // Handle Escape ourselves rather than letting the browser close the dialog:
  // the native path settles via a queued 'close' event, this settles inline.
  dialog.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      e.preventDefault();
      finish(cancelValue);
    }
  });

  // Backstop for any close we did not initiate (backdrop, programmatic close)
  dialog.addEventListener("close", () => finish(cancelValue));

  return { dialog, form, body, foot, finish };
}

/**
 * Add a footer button. Submit buttons also fire on Enter inside the form.
 *
 * Footer buttons keep the base button size; only the colour separates Cancel
 * from the confirm action. Cancel used to be a .sm-btn, which sat visibly
 * shorter than the primary button beside it.
 */
function footButton(foot, label, { className = "", type = "button" } = {}) {
  const btn = document.createElement("button");
  btn.type = type;
  if (className) btn.className = className;
  btn.textContent = label;
  foot.append(btn);
  return btn;
}

// ============================================================================
// MESSAGE + CONFIRM
// ============================================================================

/** Read-only message dialog. Resolves when dismissed. */
export function alertDialog({ title, message }) {
  return new Promise((resolve) => {
    const { dialog, form, body, foot, finish } =
      shell(title, { narrow: true, onSettle: resolve, cancelValue: undefined });

    const p = document.createElement("div");
    p.className = "dialog-message";
    p.textContent = message;
    body.append(p);

    const ok = footButton(foot, "OK", { className: "primary", type: "submit" });
    form.addEventListener("submit", (e) => { e.preventDefault(); finish(undefined); });

    dialog.showModal();
    ok.focus();
  });
}

/**
 * Yes/no confirmation. Resolves true only when the confirm button is used.
 * Replaces window.confirm().
 */
export function confirmDialog({ title, message, confirmLabel = "Confirm", danger = false }) {
  return new Promise((resolve) => {
    const { dialog, form, body, foot, finish } =
      shell(title, { narrow: true, onSettle: resolve, cancelValue: false });

    const p = document.createElement("div");
    p.className = "dialog-message";
    p.textContent = message;
    body.append(p);

    const cancel = footButton(foot, "Cancel");
    const ok = footButton(foot, confirmLabel, {
      className: danger ? "danger" : "primary",
      type: "submit",
    });

    cancel.addEventListener("click", () => finish(false));
    form.addEventListener("submit", (e) => { e.preventDefault(); finish(true); });

    dialog.showModal();
    ok.focus();
  });
}

/**
 * Single-line text input. Resolves the trimmed string, or null if cancelled.
 * Replaces window.prompt().
 */
export function promptDialog({ title, label, value = "", placeholder = "", confirmLabel = "Save" }) {
  return new Promise((resolve) => {
    const { dialog, form, body, foot, finish } =
      shell(title, { narrow: true, onSettle: resolve, cancelValue: null });

    if (label) {
      const lbl = document.createElement("label");
      lbl.textContent = label;
      lbl.htmlFor = "dlg-prompt-input";
      body.append(lbl);
    }

    const input = document.createElement("input");
    input.type = "text";
    input.id = "dlg-prompt-input";
    input.value = value;
    input.placeholder = placeholder;
    input.autocomplete = "off";
    input.spellcheck = false;
    body.append(input);

    const cancel = footButton(foot, "Cancel");
    footButton(foot, confirmLabel, { className: "primary", type: "submit" });

    cancel.addEventListener("click", () => finish(null));
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      const v = input.value.trim();
      finish(v === "" ? null : v);
    });

    dialog.showModal();
    input.focus();
    input.select();
  });
}

/**
 * Destructive confirmation that requires typing an exact phrase.
 * The confirm button stays disabled until the text matches.
 */
export function typeToConfirmDialog({ title, message, phrase, confirmLabel = "Delete" }) {
  return new Promise((resolve) => {
    const { dialog, form, body, foot, finish } =
      shell(title, { narrow: true, onSettle: resolve, cancelValue: false });

    const p = document.createElement("div");
    p.className = "dialog-message";
    p.textContent = message;

    const hint = document.createElement("div");
    hint.className = "dialog-hint";
    hint.textContent = `Type "${phrase}" to confirm:`;

    const input = document.createElement("input");
    input.type = "text";
    input.autocomplete = "off";
    input.spellcheck = false;
    input.placeholder = phrase;

    body.append(p, hint, input);

    const cancel = footButton(foot, "Cancel");
    const ok = footButton(foot, confirmLabel, { className: "danger", type: "submit" });
    ok.disabled = true;

    const matches = () => input.value.trim() === phrase;
    input.addEventListener("input", () => { ok.disabled = !matches(); });

    cancel.addEventListener("click", () => finish(false));
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      if (matches()) finish(true);
    });

    dialog.showModal();
    input.focus();
  });
}

// ============================================================================
// CONTENT DIALOGS
// ============================================================================

/** Scrollable read-only text block - used for server info. */
export function infoDialog({ title, text }) {
  const { dialog, form, body, foot, finish } = shell(title);

  const pre = document.createElement("pre");
  pre.textContent = text;
  body.append(pre);

  const close = footButton(foot, "Close", { type: "submit" });
  form.addEventListener("submit", (e) => { e.preventDefault(); finish(undefined); });

  dialog.showModal();
  close.focus();
  return dialog;
}

/**
 * JSON configuration editor, shared by streams, KV buckets and consumers.
 *
 * `onSave` receives the parsed object. If it throws, the dialog stays open and
 * shows the error inline, so a rejected config can be corrected without
 * retyping it.
 *
 * @param {object} opts - { title, hint, value, saveLabel, onSave }
 */
export function jsonDialog({ title, hint, value, saveLabel = "Save", onSave }) {
  const { dialog, form, body, foot, finish } = shell(title);

  if (hint) {
    const h = document.createElement("div");
    h.className = "dialog-hint";
    h.textContent = hint;
    body.append(h);
  }

  const ta = document.createElement("textarea");
  ta.className = "json-editor";
  ta.value = JSON.stringify(value, null, 2);
  ta.autocomplete = "off";
  ta.spellcheck = false;
  body.append(ta);

  const err = document.createElement("div");
  err.className = "dialog-error";
  err.hidden = true;
  body.append(err);

  ta.addEventListener("input", () => {
    utils.validateJsonInput(ta);
    err.hidden = true;
  });

  const cancel = footButton(foot, "Cancel");
  const save = footButton(foot, saveLabel, { className: "primary" });

  const showError = (msg) => {
    err.textContent = msg;
    err.hidden = false;
  };

  cancel.addEventListener("click", () => finish(undefined));

  save.addEventListener("click", async () => {
    if (!utils.validateJsonInput(ta)) {
      showError("Invalid JSON - check for trailing commas or unquoted keys.");
      return;
    }

    let parsed;
    try {
      parsed = JSON.parse(ta.value);
    } catch (e) {
      showError(e.message);
      return;
    }

    save.disabled = true;
    save.textContent = "Saving…";
    try {
      await onSave(parsed);
      finish(undefined);
    } catch (e) {
      showError(e.message || String(e));
    } finally {
      save.disabled = false;
      save.textContent = saveLabel;
    }
  });

  dialog.showModal();
  ta.focus();
  return dialog;
}
