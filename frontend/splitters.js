// ============================================================================
// PANE SPLITTERS
// ============================================================================
// Drag the gutter between two panes to resize the one on its left.
//
// The default layout stays in CSS. Each `.panes` grid declares its columns as
// `var(--c0, 260px) var(--gutter) minmax(0, 1fr)`, and a splitter only ever
// writes `--c{n}` onto the grid element. So this module never needs to know a
// template, and removing an override restores whatever CSS currently says.
//
// Widths are per grid (`data-panes="msg"`) and persist in localStorage.

import * as storage from "./storage.js";

// These mirror --min-col / --min-flex in style.css. CSS enforces them on the
// layout; these keep the drag and the announced aria values in step with it.
const MIN_COL = 140;
const MIN_FLEX = 280;

const STEP = 16;      // arrow-key nudge
const STEP_BIG = 64;  // shift + arrow

// ============================================================================
// TRACK MATHS
// ============================================================================
// Computed `grid-template-columns` resolves to used pixel values, and the
// template alternates pane / gutter / pane / gutter / flex - so the track for
// column N sits at index N * 2.

function tracks(grid) {
  return getComputedStyle(grid).gridTemplateColumns.split(/\s+/).map(parseFloat);
}

function currentWidth(grid, index) {
  return tracks(grid)[index * 2] || 0;
}

/** Widest this column can get before the flexible column hits MIN_FLEX. */
function maxWidth(grid, index) {
  const cols = tracks(grid);
  const flex = cols[cols.length - 1];
  const mine = cols[index * 2];

  // everything that is neither this column nor the flexible one
  const fixed = cols.reduce((sum, w) => sum + w, 0) - flex - mine;

  return Math.max(MIN_COL, grid.clientWidth - fixed - MIN_FLEX);
}

function clamp(grid, index, width) {
  return Math.min(Math.max(width, MIN_COL), maxWidth(grid, index));
}

function applyWidth(grid, index, width) {
  grid.style.setProperty(`--c${index}`, `${Math.round(width)}px`);
}

function resetWidth(grid, index) {
  grid.style.removeProperty(`--c${index}`);
}

// A grid inside a hidden tab panel measures zero; anything that needs real
// geometry has to sit this out rather than clamp everything to MIN_COL.
function isMeasurable(grid) {
  return grid.clientWidth > 0;
}

function syncAria(grid, splitter, index) {
  if (!isMeasurable(grid)) return;
  splitter.setAttribute("aria-valuenow", String(Math.round(currentWidth(grid, index))));
  splitter.setAttribute("aria-valuemin", String(MIN_COL));
  splitter.setAttribute("aria-valuemax", String(Math.round(maxWidth(grid, index))));
}

// ============================================================================
// WIRING
// ============================================================================

export function initPaneSplitters() {
  const grids = [...document.querySelectorAll(".panes[data-panes]")];
  const saved = storage.getPaneSizes();

  grids.forEach((grid) => {
    const group = grid.dataset.panes;
    const sizes = saved[group] || {};

    grid.querySelectorAll(":scope > .splitter").forEach((splitter) => {
      const index = Number(splitter.dataset.col);

      // Restore unclamped: the grid may be in a hidden tab and unmeasurable,
      // and the value was already clamped when it was saved.
      if (sizes[index] > 0) applyWidth(grid, index, sizes[index]);

      setupSplitter(grid, group, splitter, index);
    });
  });

  // Fitting a saved width to a smaller window is CSS's job (the clamp() in
  // grid-template-columns). This only keeps the announced values honest.
  window.addEventListener("resize", () => {
    grids.forEach((grid) => {
      if (!isMeasurable(grid)) return;
      grid.querySelectorAll(":scope > .splitter").forEach((splitter) => {
        syncAria(grid, splitter, Number(splitter.dataset.col));
      });
    });
  });
}

function setupSplitter(grid, group, splitter, index) {
  // Best effort at startup - two of the three grids sit in a hidden tab and
  // cannot be measured yet, so also sync when the splitter is about to be
  // used. That covers the hidden case without watching for tab switches.
  syncAria(grid, splitter, index);
  splitter.addEventListener("focus", () => syncAria(grid, splitter, index));
  splitter.addEventListener("pointerenter", () => syncAria(grid, splitter, index));

  splitter.addEventListener("pointerdown", (e) => {
    if (e.button !== 0) return;
    e.preventDefault();

    const startX = e.clientX;
    const startWidth = currentWidth(grid, index);

    // Capture so the drag survives the pointer leaving the 4px handle.
    splitter.setPointerCapture(e.pointerId);
    splitter.classList.add("dragging");
    document.body.classList.add("resizing");

    const onMove = (ev) => {
      applyWidth(grid, index, clamp(grid, index, startWidth + (ev.clientX - startX)));
      syncAria(grid, splitter, index);
    };

    const onEnd = () => {
      splitter.removeEventListener("pointermove", onMove);
      splitter.removeEventListener("pointerup", onEnd);
      splitter.removeEventListener("pointercancel", onEnd);
      splitter.classList.remove("dragging");
      document.body.classList.remove("resizing");
      storage.setPaneSize(group, index, currentWidth(grid, index));
    };

    splitter.addEventListener("pointermove", onMove);
    splitter.addEventListener("pointerup", onEnd);
    splitter.addEventListener("pointercancel", onEnd);
  });

  splitter.addEventListener("keydown", (e) => {
    const step = e.shiftKey ? STEP_BIG : STEP;
    let next;

    if (e.key === "ArrowLeft") next = currentWidth(grid, index) - step;
    else if (e.key === "ArrowRight") next = currentWidth(grid, index) + step;
    else if (e.key === "Home" || e.key === "Enter") next = null;
    else return;

    e.preventDefault();
    commit(grid, group, splitter, index, next);
  });

  // Double-click the gutter to go back to the default width
  splitter.addEventListener("dblclick", () => commit(grid, group, splitter, index, null));
}

/** width = null resets to the CSS default and forgets the saved override. */
function commit(grid, group, splitter, index, width) {
  if (width == null) resetWidth(grid, index);
  else applyWidth(grid, index, clamp(grid, index, width));

  storage.setPaneSize(group, index, width == null ? null : currentWidth(grid, index));
  syncAria(grid, splitter, index);
}
