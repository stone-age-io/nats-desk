// ============================================================================
// CLUSTER TOPOLOGY
// ============================================================================
// The Monitor tab's map: servers, the routes between them, the gateways
// between clusters, and the leaf nodes hanging off their hubs.
//
// A NATS cluster is a full mesh by design, so a healthy one always draws as
// every server joined to every other, and the picture says something only
// where that breaks. So the map makes the breaks loud and the healthy mesh
// quiet: a missing route is a red dashed line, a working one a faint line
// whose weight is its measured traffic.
//
// Three steps, kept apart so the model can be checked without a browser:
//   buildTopology   rows -> what is connected to what, and what is missing
//   layoutTopology  where each thing goes
//   renderTopology  SVG
// Only utils.js is imported, for the same reason.

import * as utils from "./utils.js";

const SVG_NS = "http://www.w3.org/2000/svg";

// Above this many leaf nodes on one hub they become one node with a count. An
// edge fleet can hang hundreds off a server, and a hundred circles say less
// than "312 leaf nodes".
export const MAX_LEAFS_DRAWN = 6;

// Bytes queued on a route before it is drawn as struggling. A snapshot of a
// busy route often holds something in flight; a megabyte not yet written is a
// backlog.
export const PENDING_WARN_BYTES = 1 << 20;

// Room between neighbouring servers on a cluster's ring, and how far out leaf
// nodes sit beyond it. In the same units as the SVG, which is drawn 1:1 and
// only ever scaled down to fit.
const SPACING = 120;
const LEAF_GAP = 78;

// ----------------------------------------------------------------------------
// Converting the :8222 answers
// ----------------------------------------------------------------------------
// STATSZ carries routes and gateways with every heartbeat, and the backend
// folds them into the grid rows. A row from :8222 has neither, so for that
// source the map asks routez and gatewayz and converts them to the same shape.

/**
 * One server's routez answer as route links, one per peer. `known` is false
 * when a route names no peer the map can identify, since a list with an
 * anonymous entry cannot say which peer is missing.
 */
export function routezLinks(body, rows) {
  const nameOf = new Map(rows.map((r) => [r.id, r.name]));
  const byPeer = new Map();
  let known = true;
  for (const r of (body && body.routes) || []) {
    const peer = r.remote_name || nameOf.get(r.remote_id);
    if (!peer) {
      known = false;
      continue;
    }
    const l = byPeer.get(peer) || {
      peer, conns: 0, in_msgs: 0, out_msgs: 0, in_bytes: 0, out_bytes: 0, pending: 0,
    };
    l.conns++;
    l.in_msgs += r.in_msgs || 0;
    l.out_msgs += r.out_msgs || 0;
    l.in_bytes += r.in_bytes || 0;
    l.out_bytes += r.out_bytes || 0;
    l.pending += r.pending_size || 0;
    byPeer.set(peer, l);
  }
  return { links: [...byPeer.values()], known };
}

/** One server's gatewayz answer as gateway links. Only connected gateways count. */
export function gatewayzLinks(body) {
  const out = [];
  const inbound = (body && body.inbound_gateways) || {};
  for (const [name, gw] of Object.entries((body && body.outbound_gateways) || {})) {
    const c = gw && gw.connection;
    if (!c) continue;
    out.push({
      gateway: name,
      in_msgs: c.in_msgs || 0,
      out_msgs: c.out_msgs || 0,
      in_bytes: c.in_bytes || 0,
      out_bytes: c.out_bytes || 0,
      inbound: (inbound[name] || []).length,
    });
  }
  return out;
}

// ----------------------------------------------------------------------------
// The model
// ----------------------------------------------------------------------------

/** Undefined, not zero, when nothing was measured: a rate is a pointer. */
function addRates(...rs) {
  const known = rs.filter((r) => r != null);
  return known.length ? known.reduce((a, b) => a + b, 0) : null;
}

/**
 * Work out what is connected to what.
 *
 * @param {object[]} rows  the grid rows, as the backend pushes them
 * @param {object} [extras]
 * @param {Map<string, object[]>} [extras.leafs]     server id -> leafz `leafs`
 * @param {Map<string, {links, known}>} [extras.routes]  server id -> routezLinks
 * @param {Map<string, object[]>} [extras.gateways]  server id -> gatewayzLinks
 * @param {number} [now]
 */
export function buildTopology(rows, extras = {}, now = Date.now()) {
  const leafsBy = extras.leafs || new Map();
  const routesBy = extras.routes || new Map();
  const gatewaysBy = extras.gateways || new Map();

  const nodes = [];
  // Routes name their peer, not its ID, and a name is only unique within its
  // cluster - so peers are looked up by cluster and name together.
  const byClusterName = new Map();
  const byName = new Map();
  const ck = (cluster, name) => `${cluster}\u0000${name}`;

  for (const r of rows) {
    const st = utils.serverState(r, now);
    let routes = null;
    if (r.route_links_known) routes = r.route_links || [];
    else if (routesBy.get(r.id)?.known) routes = routesBy.get(r.id).links;

    const node = {
      key: `s:${r.id}`,
      id: r.id,
      name: r.name,
      cluster: r.cluster || "",
      state: st.cls,
      stateText: st.text,
      row: r,
      routes, // null: this server's routes cannot be known
      gateways: r.gateway_links || gatewaysBy.get(r.id) || null,
      leafs: leafsBy.get(r.id) || [],
      ghost: false,
    };
    nodes.push(node);
    byClusterName.set(ck(node.cluster, node.name), node);
    byName.set(node.name, node);
  }

  // A route to a server that does not report is drawn too, as a ghost: "n1
  // and n2 route to n4, which says nothing" is worth seeing.
  const ghosts = [];
  const ghostFor = (cluster, name) => {
    const k = ck(cluster, name);
    let g = byClusterName.get(k);
    if (!g) {
      g = {
        key: `g:${cluster}:${name}`, id: null, name, cluster, state: "ghost",
        stateText: "Routed to, but not reporting", row: null,
        routes: null, gateways: null, leafs: [], ghost: true,
      };
      byClusterName.set(k, g);
      ghosts.push(g);
    }
    return g;
  };

  // Routes, as reported. Each server reports its own side of each route.
  const edges = new Map();
  const edgeFor = (a, b) => {
    const [x, y] = a.key < b.key ? [a, b] : [b, a];
    const k = `${x.key}|${y.key}`;
    let e = edges.get(k);
    if (!e) {
      e = { a: x, b: y, reports: new Map(), state: "up" };
      edges.set(k, e);
    }
    return e;
  };
  // A server that has stopped reporting is not believed about its routes:
  // its last report is from before it went quiet, and lists routes its peers
  // have long since dropped. Its peers' fresh reports still count.
  const quiet = (n) => n.state === "stale" || n.state === "shutdown";
  for (const n of nodes) {
    if (quiet(n)) continue;
    for (const l of n.routes || []) {
      const peer = byClusterName.get(ck(n.cluster, l.peer)) || ghostFor(n.cluster, l.peer);
      if (peer === n) continue;
      edgeFor(n, peer).reports.set(n.key, l);
    }
  }

  // Routes that should exist and do not. Only within a cluster, only between
  // servers whose route lists are both known, and only when both are up: a
  // server that has stopped reporting has lost its routes for a reason
  // already on screen, so that line is grey, not red.
  // "N of M routes" counts the same pairs on both sides: servers that are up
  // and whose route lists are both known.
  const clusterNames = [...new Set(nodes.map((n) => n.cluster))];
  let expected = 0;
  let up = 0;
  for (const c of clusterNames) {
    const members = nodes.filter((n) => n.cluster === c);
    for (let i = 0; i < members.length; i++) {
      for (let j = i + 1; j < members.length; j++) {
        const a = members[i];
        const b = members[j];
        const down = quiet(a) || quiet(b);
        const [x, y] = a.key < b.key ? [a, b] : [b, a];
        const e = edges.get(`${x.key}|${y.key}`);
        const counted = a.routes && b.routes && !down;
        if (counted) expected++;
        if (counted && e) up++;
        if (e) continue;
        if (down) edges.set(`${x.key}|${y.key}`, { a: x, b: y, reports: new Map(), state: "down" });
        else if (a.routes && b.routes) edges.set(`${x.key}|${y.key}`, { a: x, b: y, reports: new Map(), state: "missing" });
      }
    }
  }

  // Traffic per direction. A's report of its own side is the better source
  // for A -> B; B's "received from A" stands in when A did not report.
  for (const e of edges.values()) {
    const ra = e.reports.get(e.a.key);
    const rb = e.reports.get(e.b.key);
    e.aToB = addRates(ra ? ra.out_msgs_rate : rb && rb.in_msgs_rate);
    e.bToA = addRates(rb ? rb.out_msgs_rate : ra && ra.in_msgs_rate);
    e.aToBBytes = addRates(ra ? ra.out_bytes_rate : rb && rb.in_bytes_rate);
    e.bToABytes = addRates(rb ? rb.out_bytes_rate : ra && ra.in_bytes_rate);
    e.total = addRates(e.aToB, e.bToA);
    e.pending = (ra ? ra.pending : 0) + (rb ? rb.pending : 0);
    e.conns = Math.max(ra ? ra.conns : 0, rb ? rb.conns : 0);
    e.oneSided = e.state === "up" && (!ra || !rb) && !e.a.ghost && !e.b.ghost;
    e.struggling = e.pending >= PENDING_WARN_BYTES;
  }

  // Clusters, with any gateway target the map cannot see as a ghost cluster.
  const clusters = new Map();
  const clusterOf = (name) => {
    let c = clusters.get(name);
    if (!c) {
      c = { name, nodes: [], ghost: false };
      clusters.set(name, c);
    }
    return c;
  };
  for (const n of [...nodes, ...ghosts]) clusterOf(n.cluster).nodes.push(n);

  // Gateways, per pair of clusters. Every server in a cluster keeps its own
  // outbound connection to every other cluster, so one that lacks it while
  // its neighbours have it is the thing to point at.
  const gws = new Map();
  for (const n of nodes) {
    if (quiet(n)) continue;
    for (const l of n.gateways || []) {
      if (!l.gateway || l.gateway === n.cluster) continue;
      if (!clusters.has(l.gateway)) {
        const ghost = clusterOf(l.gateway);
        ghost.ghost = true;
      }
      const [x, y] = n.cluster < l.gateway ? [n.cluster, l.gateway] : [l.gateway, n.cluster];
      const k = `${x}|${y}`;
      let g = gws.get(k);
      if (!g) {
        g = { a: x, b: y, have: new Map([[x, new Set()], [y, new Set()]]), out: new Map([[x, []], [y, []]]) };
        gws.set(k, g);
      }
      g.have.get(n.cluster).add(n.name);
      g.out.get(n.cluster).push(l.out_msgs_rate);
    }
  }
  const gateways = [...gws.values()].map((g) => {
    const lack = {};
    for (const side of [g.a, g.b]) {
      const c = clusters.get(side);
      lack[side] = c.ghost
        ? []
        : c.nodes
            .filter((n) => !n.ghost && n.gateways && n.state !== "stale" && n.state !== "shutdown")
            .filter((n) => !g.have.get(side).has(n.name))
            .map((n) => n.name);
    }
    return {
      a: g.a,
      b: g.b,
      lack,
      aToB: addRates(...g.out.get(g.a)),
      bToA: addRates(...g.out.get(g.b)),
      partial: lack[g.a].length > 0 || lack[g.b].length > 0,
    };
  });

  // Leaf nodes. One whose remote server is already on the map is drawn as a
  // link to it rather than as a second circle for the same server.
  const leafs = [];
  const leafLinks = [];
  let leafCount = 0;
  for (const n of nodes) {
    const loose = [];
    for (const l of n.leafs) {
      leafCount++;
      const known = l.name && byName.get(l.name);
      if (known && known !== n) leafLinks.push({ hub: n, to: known, leaf: l });
      else loose.push(l);
    }
    if (loose.length > MAX_LEAFS_DRAWN) {
      leafs.push({ key: `l:${n.key}:all`, hub: n, count: loose.length, items: loose });
    } else {
      loose.forEach((l, i) => leafs.push({ key: `l:${n.key}:${i}`, hub: n, count: 1, items: [l] }));
    }
  }

  // The JetStream meta leader, as the servers themselves say. During an
  // election, or in a split, they may not agree, and that is worth saying.
  const votes = new Map();
  for (const n of nodes) {
    const leader = n.row.meta_leader;
    if (leader && n.state !== "stale" && n.state !== "shutdown") votes.set(leader, (votes.get(leader) || 0) + 1);
  }
  const ranked = [...votes.entries()].sort((a, b) => b[1] - a[1]);
  const metaLeader = ranked.length ? ranked[0][0] : "";
  const leaderRow = nodes.find((n) => n.name === metaLeader && n.row.meta_peers);
  const peers = new Map(((leaderRow && leaderRow.row.meta_peers) || []).map((p) => [p.name, p]));
  for (const n of [...nodes, ...ghosts]) {
    n.metaLeader = !!metaLeader && n.name === metaLeader;
    n.meta = peers.get(n.name) || null;
  }

  const edgeList = [...edges.values()];
  const missing = edgeList.filter((e) => e.state === "missing");
  const downNames = new Set(nodes.filter(quiet).map((n) => n.name));
  return {
    clusters: [...clusters.values()].sort((a, b) => a.name.localeCompare(b.name)),
    nodes,
    ghosts,
    edges: edgeList,
    gateways,
    leafs,
    leafLinks,
    summary: {
      servers: nodes.length,
      clusters: [...clusters.values()].filter((c) => !c.ghost).length,
      routesExpected: expected,
      routesUp: up,
      missing: missing.map((e) => [e.a.name, e.b.name]),
      struggling: edgeList.filter((e) => e.struggling).map((e) => [e.a.name, e.b.name]),
      ghosts: ghosts.map((g) => g.name),
      down: nodes.filter(quiet).map((n) => n.name),
      unknown: nodes.filter((n) => !n.routes).map((n) => n.name),
      metaLeader,
      metaDisagree: ranked.length > 1 ? ranked.map(([name, count]) => `${name} (${count})`) : null,
      // A server that has stopped reporting is already listed as such; its
      // JetStream standing follows from that and is not repeated.
      metaOffline: [...peers.values()].filter((p) => p.offline && !downNames.has(p.name)).map((p) => p.name),
      metaBehind: [...peers.values()].filter((p) => !p.offline && !p.current && !downNames.has(p.name)).map((p) => p.name),
      leafs: leafCount,
      gateways: gateways.length,
      gatewaysPartial: gateways.filter((g) => g.partial).length,
    },
  };
}

// ----------------------------------------------------------------------------
// Layout
// ----------------------------------------------------------------------------
// Deterministic, so a map does not rearrange itself between heartbeats: each
// cluster's servers sit on a ring in name order, clusters sit on a ring of
// their own, and leaf nodes fan out beyond their hub.

function ringRadius(k) {
  if (k <= 1) return 0;
  if (k === 2) return SPACING / 2;
  return SPACING / 2 / Math.sin(Math.PI / k);
}

/**
 * @param {object} model   from buildTopology
 * @param {number} [aspect]  the pane's width over its height. Two clusters sit
 *   side by side in a wide pane and one above the other in a tall one, so the
 *   map is not shrunk to fit a shape it does not have.
 */
export function layoutTopology(model, aspect = 1.5) {
  const pos = new Map();
  const leafCountOf = (n) => model.leafs.filter((l) => l.hub === n).length;

  const rings = model.clusters.map((c) => {
    const members = [...c.nodes].sort((a, b) => a.name.localeCompare(b.name));
    const r = ringRadius(members.length);
    const hasLeafs = members.some((n) => leafCountOf(n) > 0);
    return { c, members, r, outer: r + (hasLeafs ? LEAF_GAP + 40 : 52) };
  });

  // Cluster centres. Neighbouring clusters must not overlap: two are spaced
  // by their own footprints along the pane's long side, more go on a ring
  // sized by the largest.
  const m = rings.length;
  const maxOuter = Math.max(0, ...rings.map((g) => g.outer));
  if (m === 2) {
    const d = rings[0].outer + rings[1].outer + 30;
    const wide = aspect >= 1;
    rings[0].x = 0;
    rings[0].y = 0;
    rings[1].x = wide ? d : 0;
    rings[1].y = wide ? 0 : d;
  } else {
    const R = m <= 1 ? 0 : (maxOuter + 30) / Math.sin(Math.PI / m);
    rings.forEach((g, i) => {
      const a = -Math.PI / 2 + (2 * Math.PI * i) / m;
      g.x = R * Math.cos(a);
      g.y = R * Math.sin(a);
    });
  }

  for (const g of rings) {
    const k = g.members.length;
    g.members.forEach((n, j) => {
      const a = k === 2 ? Math.PI * (1 - j) : -Math.PI / 2 + (2 * Math.PI * j) / k;
      n.angle = a;
      pos.set(n.key, { x: g.x + g.r * Math.cos(a), y: g.y + g.r * Math.sin(a) });
    });

    // Leaf nodes: all the way round a lone server, otherwise fanned outwards
    // around the hub's own direction so they stay clear of the mesh.
    for (const n of g.members) {
      const mine = model.leafs.filter((l) => l.hub === n);
      if (!mine.length) continue;
      const lone = k === 1;
      const step = lone ? (2 * Math.PI) / mine.length : Math.min(0.42, (1.6 * Math.PI) / k / mine.length);
      const rl = lone ? LEAF_GAP : g.r + LEAF_GAP;
      mine.forEach((l, i) => {
        const a = lone ? -Math.PI / 2 + step * i : n.angle + (i - (mine.length - 1) / 2) * step;
        pos.set(l.key, { x: g.x + rl * Math.cos(a), y: g.y + rl * Math.sin(a) });
      });
    }
  }

  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
  for (const p of pos.values()) {
    minX = Math.min(minX, p.x); maxX = Math.max(maxX, p.x);
    minY = Math.min(minY, p.y); maxY = Math.max(maxY, p.y);
  }
  for (const g of rings) {
    const r = g.r + 46;
    minX = Math.min(minX, g.x - r); maxX = Math.max(maxX, g.x + r);
    minY = Math.min(minY, g.y - r - 14); maxY = Math.max(maxY, g.y + r);
  }
  if (!isFinite(minX)) { minX = minY = -100; maxX = maxY = 100; }
  // Room for the labels under the outermost nodes.
  const pad = 56;
  return {
    pos,
    rings,
    bounds: { x: minX - pad, y: minY - pad, w: maxX - minX + 2 * pad, h: maxY - minY + 2 * pad },
  };
}

// ----------------------------------------------------------------------------
// Rendering
// ----------------------------------------------------------------------------

function el(name, attrs = {}, text) {
  const e = document.createElementNS(SVG_NS, name);
  for (const [k, v] of Object.entries(attrs)) if (v != null && v !== false) e.setAttribute(k, v);
  if (text != null) e.textContent = text;
  return e;
}

function titled(node, text) {
  node.append(el("title", {}, text));
  return node;
}

function clip(s, n) {
  return s.length > n ? `${s.slice(0, n - 1)}…` : s;
}

// What an absent rate means, set per render: over $SYS it is a first sample
// still waiting for its second, over :8222 there will never be one.
let unmeasured = "not measured yet";

function rateText(msgs, bytes) {
  if (msgs == null) return unmeasured;
  const b = bytes == null ? "" : ` · ${utils.formatBytes(bytes, 0)}/s`;
  return `${utils.formatRate(msgs)} msg/s${b}`;
}

/** Traffic sets a working route's weight: faint and thin when quiet, heavier as it carries more. */
function weight(total) {
  if (total == null) return 1.25;
  return 1.25 + Math.min(3, Math.log10(1 + total));
}

/** Pull a line's ends in by `ra` and `rb`, so it stops at circles rather than their centres. */
function trimmed(p, q, ra, rb) {
  const dx = q.x - p.x;
  const dy = q.y - p.y;
  const d = Math.hypot(dx, dy) || 1;
  return {
    x1: p.x + (dx * ra) / d, y1: p.y + (dy * ra) / d,
    x2: q.x - (dx * rb) / d, y2: q.y - (dy * rb) / d,
  };
}

function routeTitle(e) {
  const a = e.a.name;
  const b = e.b.name;
  if (e.state === "missing") {
    return `No route between ${a} and ${b}\nBoth report their routes, and neither lists the other.`;
  }
  if (e.state === "down") {
    const gone = [e.a, e.b].find((n) => n.state === "stale" || n.state === "shutdown");
    return `No route between ${a} and ${b}\n${gone.name}: ${gone.stateText}`;
  }
  const lines = [
    `${a} ⇄ ${b}`,
    `${a} → ${b}: ${rateText(e.aToB, e.aToBBytes)}`,
    `${b} → ${a}: ${rateText(e.bToA, e.bToABytes)}`,
    `pending: ${utils.formatBytes(e.pending, 0)}`,
  ];
  if (e.conns) lines.push(`${e.conns} route connection${e.conns === 1 ? "" : "s"}`);
  const ghost = [e.a, e.b].find((n) => n.ghost);
  if (ghost) lines.push(`${ghost.name} is routed to but does not report`);
  else if (e.oneSided) lines.push(`Only ${e.reports.has(e.a.key) ? a : b} reports this route`);
  return lines.join("\n");
}

function nodeTitle(n) {
  if (n.ghost) return `${n.name}\n${n.stateText}`;
  const r = n.row;
  const lines = [n.name, [n.cluster, `v${r.version}`, r.host].filter(Boolean).join(" · "), n.stateText];
  if (n.metaLeader) lines.push("JetStream meta leader");
  if (n.meta && n.meta.offline) lines.push("JetStream: offline to the meta leader");
  else if (n.meta && !n.meta.current) lines.push(`JetStream: behind the meta leader${n.meta.lag ? ` by ${n.meta.lag}` : ""}`);
  if (!n.routes) lines.push("Routes unknown from this source");
  return lines.join("\n");
}

function leafTitle(l) {
  if (l.count > 1) {
    const names = l.items.slice(0, 12).map((x) => x.name || x.ip).join(", ");
    return `${l.count} leaf nodes on ${l.hub.name}\n${names}${l.count > 12 ? ", …" : ""}`;
  }
  const x = l.items[0];
  return [
    x.name || "leaf node",
    [x.account && `account ${x.account}`, x.ip && `${x.ip}:${x.port}`, x.rtt && `rtt ${x.rtt}`].filter(Boolean).join(" · "),
    x.is_spoke ? "spoke (this server is its hub)" : "",
    `${Number(x.in_msgs || 0).toLocaleString()} msgs in · ${Number(x.out_msgs || 0).toLocaleString()} out, since it connected`,
  ].filter(Boolean).join("\n");
}

/** The line above the map: counts, and anything wrong, in words. */
function statusItems(model, note) {
  const s = model.summary;
  const items = [];
  items.push({ text: `${s.servers} server${s.servers === 1 ? "" : "s"} · ${s.clusters} cluster${s.clusters === 1 ? "" : "s"}` });
  if (s.routesExpected) {
    items.push({ text: `${s.routesUp} of ${s.routesExpected} routes`, cls: s.missing.length ? "" : "good" });
  }
  if (s.missing.length) {
    items.push({
      text: `${s.missing.length} missing: ${s.missing.map(([a, b]) => `${a}–${b}`).join(", ")}`,
      cls: "bad",
    });
  }
  if (s.down.length) items.push({ text: `stopped reporting: ${s.down.join(", ")}`, cls: "bad" });
  if (s.struggling.length) items.push({ text: `${s.struggling.length} route backlogged`, cls: "warn" });
  if (s.ghosts.length) items.push({ text: `not reporting: ${s.ghosts.join(", ")}`, cls: "warn" });
  if (s.metaDisagree) items.push({ text: `meta leader disputed: ${s.metaDisagree.join(", ")}`, cls: "warn" });
  else if (s.metaLeader) items.push({ text: `meta leader ${s.metaLeader}` });
  if (s.metaOffline.length) items.push({ text: `JetStream offline: ${s.metaOffline.join(", ")}`, cls: "bad" });
  if (s.metaBehind.length) items.push({ text: `JetStream behind: ${s.metaBehind.join(", ")}`, cls: "warn" });
  if (s.gateways) {
    items.push({
      text: `${s.gateways} gateway${s.gateways === 1 ? "" : "s"}${s.gatewaysPartial ? `, ${s.gatewaysPartial} incomplete` : ""}`,
      cls: s.gatewaysPartial ? "warn" : "",
    });
  }
  if (s.leafs) items.push({ text: `${s.leafs} leaf node${s.leafs === 1 ? "" : "s"}` });
  if (s.unknown.length) items.push({ text: `routes unknown: ${s.unknown.join(", ")}`, cls: "muted" });
  if (note) items.push({ text: note.text, cls: note.cls || "muted" });
  return items;
}

/**
 * Draw the map into `container`, replacing what was there.
 *
 * Drawn 1:1 and only ever scaled down to fit: scaling a three-server cluster
 * up to fill a wide pane would blow its labels up to headline size.
 *
 * @param {HTMLElement} container
 * @param {object} model   from buildTopology
 * @param {object} opts
 * @param {string|null} opts.selectedId  the selected server's grid row id
 * @param {(id: string) => void} opts.onSelect
 * @param {{text: string, cls?: string}} [opts.note]  an extra line, e.g. where the data came from
 * @param {string} [opts.unmeasured]  what to say where a rate is absent
 */
export function renderTopology(container, model, opts) {
  unmeasured = opts.unmeasured || "not measured yet";
  // Every heartbeat redraws the map, which would drop keyboard focus each
  // time; note which server had it, to give it back afterwards.
  const active = document.activeElement;
  const focused = active && container.contains(active) ? active.getAttribute("data-key") : null;

  const status = document.createElement("div");
  status.className = "topo-status";
  for (const it of statusItems(model, opts.note)) {
    const span = document.createElement("span");
    span.className = it.cls || "";
    span.textContent = it.text;
    status.append(span);
  }

  // The status line wraps in a narrow pane, so the map gets whatever height
  // is left once it is in place.
  container.replaceChildren(status);
  const W = Math.max(1, container.clientWidth);
  const H = Math.max(1, container.clientHeight - status.offsetHeight);
  const { pos, rings, bounds } = layoutTopology(model, W / H);
  const scale = Math.min(1, W / bounds.w, H / bounds.h);
  const vw = W / scale;
  const vh = H / scale;
  const cx = bounds.x + bounds.w / 2;
  const cy = bounds.y + bounds.h / 2;

  const svg = el("svg", {
    class: "topo",
    viewBox: `${cx - vw / 2} ${cy - vh / 2} ${vw} ${vh}`,
    role: "img",
    "aria-label": "Cluster topology",
  });

  const selectedKey = opts.selectedId ? `s:${opts.selectedId}` : null;
  const layer = (cls) => svg.appendChild(el("g", { class: cls }));
  const clusterLayer = layer("topo-clusters");
  const gatewayLayer = layer("topo-gateways");
  const routeLayer = layer("topo-routes");
  const leafLayer = layer("topo-leafs");
  const nodeLayer = layer("topo-nodes");

  // A cluster outline and name, once there is more than one thing to group.
  const showClusters = rings.length > 1 || (rings[0] && rings[0].c.name);
  for (const g of rings) {
    if (!showClusters) break;
    const r = g.r + 40;
    if (g.c.ghost) {
      titled(
        clusterLayer.appendChild(el("circle", { class: "topo-cluster ghost", cx: g.x, cy: g.y, r: 34 })),
        `${g.c.name}\nA gateway peer this source cannot see into`,
      );
      clusterLayer.append(el("text", { class: "topo-cluster-label", x: g.x, y: g.y + 4 }, clip(g.c.name, 16)));
      continue;
    }
    clusterLayer.append(el("circle", { class: "topo-cluster", cx: g.x, cy: g.y, r }));
    // Upper left of the outline. The top is where the first server's leaf
    // nodes fan out, and where a gateway to a cluster below arrives.
    const d = r * Math.SQRT1_2;
    clusterLayer.append(el("text", {
      class: "topo-cluster-label corner", x: g.x - d - 4, y: g.y - d - 4,
    }, clip(g.c.name || "(no cluster)", 24)));
  }

  // Gateways, between cluster outlines.
  const ringOf = new Map(rings.map((g) => [g.c.name, g]));
  for (const gw of model.gateways) {
    const ga = ringOf.get(gw.a);
    const gb = ringOf.get(gw.b);
    if (!ga || !gb) continue;
    const t = trimmed(ga, gb, ga.c.ghost ? 34 : ga.r + 40, gb.c.ghost ? 34 : gb.r + 40);
    const lines = [
      `gateway ${gw.a} ⇄ ${gw.b}`,
      `${gw.a} → ${gw.b}: ${rateText(gw.aToB)}`,
      `${gw.b} → ${gw.a}: ${rateText(gw.bToA)}`,
    ];
    for (const side of [gw.a, gw.b]) {
      if (gw.lack[side].length) lines.push(`no gateway from ${gw.lack[side].join(", ")} (${side})`);
    }
    titled(
      gatewayLayer.appendChild(el("line", { class: `topo-gateway${gw.partial ? " partial" : ""}`, ...t, "stroke-width": weight(addRates(gw.aToB, gw.bToA)) })),
      lines.join("\n"),
    );
  }

  // Routes: quiet ones first, so a missing one is drawn over them.
  const order = { up: 0, down: 1, missing: 2 };
  for (const e of [...model.edges].sort((x, y) => order[x.state] - order[y.state])) {
    const p = pos.get(e.a.key);
    const q = pos.get(e.b.key);
    if (!p || !q) continue;
    const t = trimmed(p, q, 15, 15);
    const cls = ["topo-route", e.state];
    if (e.state === "up" && (e.a.ghost || e.b.ghost)) cls.push("ghosted");
    if (e.struggling) cls.push("struggling");
    if (selectedKey && (e.a.key === selectedKey || e.b.key === selectedKey)) cls.push("focus");
    const w = e.state === "up" ? weight(e.total) : 1.6;
    titled(routeLayer.appendChild(el("line", { class: cls.join(" "), ...t, "stroke-width": w })), routeTitle(e));
  }

  // Leaf links to servers already on the map.
  for (const ll of model.leafLinks) {
    const p = pos.get(ll.hub.key);
    const q = pos.get(ll.to.key);
    if (!p || !q) continue;
    titled(leafLayer.appendChild(el("line", { class: "topo-leaf-line", ...trimmed(p, q, 15, 15) })), `leaf node ${ll.to.name} on ${ll.hub.name}`);
  }

  // Leaf nodes.
  for (const l of model.leafs) {
    const p = pos.get(l.hub.key);
    const q = pos.get(l.key);
    if (!p || !q) continue;
    const many = l.count > 1;
    leafLayer.append(el("line", { class: "topo-leaf-line", ...trimmed(p, q, 15, many ? 11 : 6) }));
    const g = leafLayer.appendChild(el("g", { class: `topo-leaf${many ? " many" : ""}` }));
    g.append(el("circle", { cx: q.x, cy: q.y, r: many ? 11 : 6 }));
    if (many) g.append(el("text", { class: "topo-leaf-count", x: q.x, y: q.y + 3.5 }, String(l.count)));
    const label = many ? "leaf nodes" : l.items[0].name || l.items[0].ip || "leaf";
    g.append(el("text", { class: "topo-leaf-label", x: q.x, y: q.y + (many ? 24 : 18) }, clip(label, 16)));
    titled(g, leafTitle(l));
  }

  // Servers last, on top of every line.
  for (const n of [...model.nodes, ...model.ghosts]) {
    const p = pos.get(n.key);
    if (!p) continue;
    // A server that has stopped reporting says so first: its JetStream
    // standing follows from that, and would only bury it.
    const down = n.state === "stale" || n.state === "shutdown";
    const cls = ["topo-node", n.state];
    if (n.key === selectedKey) cls.push("selected");
    if (!down && n.meta && n.meta.offline) cls.push("js-offline");
    else if (!down && n.meta && !n.meta.current) cls.push("js-behind");
    const g = nodeLayer.appendChild(el("g", { class: cls.join(" "), "data-key": n.key }));
    if (n.metaLeader) g.append(el("circle", { class: "topo-leader-ring", cx: p.x, cy: p.y, r: 19 }));
    g.append(el("circle", { class: "topo-dot", cx: p.x, cy: p.y, r: 13 }));
    g.append(el("text", { class: "topo-label", x: p.x, y: p.y + 30 }, clip(n.name, 16)));
    let sub = "";
    if (down) sub = n.state === "shutdown" ? "shut down" : "stale";
    else if (n.ghost) sub = "not reporting";
    else if (n.state === "lameduck") sub = "lame duck";
    else if (n.metaLeader) sub = "meta leader";
    else if (n.meta && n.meta.offline) sub = "JS offline";
    else if (n.meta && !n.meta.current) sub = "JS behind";
    if (sub) g.append(el("text", { class: "topo-sublabel", x: p.x, y: p.y + 43 }, sub));
    titled(g, nodeTitle(n));

    if (n.id) {
      g.setAttribute("tabindex", "0");
      g.setAttribute("role", "button");
      g.setAttribute("aria-label", `${n.name}, ${n.stateText}`);
      g.addEventListener("click", () => opts.onSelect(n.id));
      g.addEventListener("keydown", (ev) => {
        if (ev.key === "Enter" || ev.key === " ") {
          ev.preventDefault();
          opts.onSelect(n.id);
        }
      });
    }
  }

  container.append(svg);
  if (focused) svg.querySelector(`[data-key="${CSS.escape(focused)}"]`)?.focus();
}
