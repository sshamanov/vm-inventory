"use strict";

let currentData = null;
let searchTerm = "";
let geoFilter = "";
let hostFilter = "";
let vmSort = { col: "name", asc: true };
let authToken = "";
let windowName = "now";

const THEME_KEY = "inv-theme";
const WINDOW_KEY = "inv-window";
const WINDOWS = ["now", "month", "all"];

// --- Init ---

document.addEventListener("DOMContentLoaded", () => {
  initTheme();
  initScroll();
  initWindowSwitch();
  authToken = localStorage.getItem("inv-auth") || "";
  if (!authToken) {
    showLogin();
  } else {
    loadInventory();
    setInterval(loadInventory, 60000);
  }
  document.getElementById("search").addEventListener("input", debounce(onSearch, 200));
  document.getElementById("geos").addEventListener("click", onFoldClick);

  // The card floor is measured from rendered text, so it has to be taken again
  // once the real font replaces the fallback and whenever lines re-wrap.
  if (document.fonts && document.fonts.ready) document.fonts.ready.then(levelHostHeights);
  window.addEventListener("resize", debounce(levelHostHeights, 150));
});

// --- Theme ---

function initTheme() {
  const btn = document.getElementById("theme-toggle");
  const isDark = () => document.documentElement.getAttribute("data-theme") === "dark";

  const sync = () => {
    const dark = isDark();
    btn.setAttribute("aria-pressed", dark ? "true" : "false");
    btn.title = dark ? "Switch to light theme" : "Switch to dark theme";
    btn.setAttribute("aria-label", btn.title);
  };

  btn.addEventListener("click", () => {
    const next = isDark() ? "light" : "dark";
    document.documentElement.setAttribute("data-theme", next);
    try { localStorage.setItem(THEME_KEY, next); } catch (e) { /* private mode */ }
    sync();
  });

  sync();

  // Follow the OS while the user has expressed no explicit preference.
  const mq = window.matchMedia("(prefers-color-scheme: dark)");
  const onScheme = (e) => {
    let stored = null;
    try { stored = localStorage.getItem(THEME_KEY); } catch (err) { /* ignore */ }
    if (stored === "light" || stored === "dark") return;
    document.documentElement.setAttribute("data-theme", e.matches ? "dark" : "light");
    sync();
  };
  if (mq.addEventListener) mq.addEventListener("change", onScheme);
}

function initScroll() {
  const header = document.getElementById("header");
  const root = document.documentElement;
  let ticking = false;
  const apply = () => {
    header.classList.toggle("is-scrolled", window.scrollY > 6);
    // The sticky table header parks under the masthead, which changes height
    // when it compacts, so the offset is measured rather than hard-coded.
    root.style.setProperty("--masthead-h", `${header.offsetHeight}px`);
    ticking = false;
  };
  const queue = () => {
    if (ticking) return;
    ticking = true;
    requestAnimationFrame(apply);
  };
  window.addEventListener("scroll", queue, { passive: true });
  window.addEventListener("resize", queue);
  apply();
}

// --- View window ---

// The switch chooses how far back the inventory looks. Each window is a
// separate snapshot on the backend — "now" does not contain what "month" does —
// so switching refetches rather than filtering what is already on the page.
function initWindowSwitch() {
  let stored = null;
  try { stored = localStorage.getItem(WINDOW_KEY); } catch (e) { /* private mode */ }
  if (WINDOWS.indexOf(stored) >= 0) windowName = stored;
  syncWindowSwitch();

  document.getElementById("window-switch").addEventListener("click", (e) => {
    const btn = e.target.closest("button[data-window]");
    if (!btn || btn.dataset.window === windowName) return;
    windowName = btn.dataset.window;
    try { localStorage.setItem(WINDOW_KEY, windowName); } catch (err) { /* ignore */ }
    syncWindowSwitch();
    loadInventory();
  });
}

function syncWindowSwitch() {
  for (const btn of document.querySelectorAll("#window-switch button[data-window]")) {
    const on = btn.dataset.window === windowName;
    btn.classList.toggle("is-on", on);
    btn.setAttribute("aria-pressed", on ? "true" : "false");
  }
}

// --- Auth ---

function showLogin() {
  const overlay = document.createElement("div");
  overlay.id = "login-overlay";
  overlay.innerHTML = `<form id="login-form" autocomplete="off">
    <h2>VM Inventory</h2>
    <p class="login-note">Authentication required</p>
    <input type="password" id="login-password" placeholder="Password" aria-label="Password" autofocus>
    <button type="submit">Unlock</button>
    <p id="login-error" style="display:none">Wrong password</p>
  </form>`;
  document.body.appendChild(overlay);
  document.getElementById("login-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const pw = document.getElementById("login-password").value;
    try {
      const resp = await fetch("/api/auth", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password: pw })
      });
      if (resp.ok) {
        const data = await resp.json();
        authToken = data.token || "";
        localStorage.setItem("inv-auth", authToken);
        overlay.remove();
        loadInventory();
        setInterval(loadInventory, 60000);
      } else {
        document.getElementById("login-error").style.display = "";
      }
    } catch { document.getElementById("login-error").style.display = ""; }
  });
}

function authHeaders() {
  return authToken ? { "X-Auth": authToken, "Accept": "application/json" } : { "Accept": "application/json" };
}

// --- API ---

async function loadInventory() {
  showLoading(true);
  try {
    const resp = await fetch(`/api/inventory?window=${encodeURIComponent(windowName)}`, { headers: authHeaders() });
    if (!resp.ok) {
      if (resp.status === 401) { localStorage.removeItem("inv-auth"); showLogin(); return; }
      throw new Error(`HTTP ${resp.status}`);
    }
    currentData = await resp.json();
    render(currentData);
    updateStatus();
  } catch (err) {
    const el = document.getElementById("loading");
    el.textContent = "Failed to load inventory — is the backend running?";
    el.hidden = false;
  } finally {
    showLoading(false);
  }
}

async function updateStatus() {
  try {
    const resp = await fetch("/api/status", { headers: authHeaders() });
    if (!resp.ok) return;
    const status = await resp.json();

    const el = document.getElementById("cache-status");
    const syncedAt = status.last_prometheus_refresh || status.cache_generated_at;
    const parts = [];
    if (syncedAt) parts.push(`Synced ${timeAgo(syncedAt)}`);
    if (status.version || status.commit) {
      parts.push(`build ${[status.version, status.commit].filter(Boolean).join("@")}`);
    }
    el.textContent = parts.join(" · ") || "Status unavailable";

    // Distinguish "the cache is old" from "Prometheus has not been read lately".
    if (syncedAt) {
      const age = (Date.now() - new Date(syncedAt).getTime()) / 1000;
      el.dataset.stale = age > 600 ? "1" : "0";
    }

    if (status.confluence_url) {
      const cl = document.getElementById("confluence-link");
      cl.href = status.confluence_url + "/display/" +
        (status.confluence_url.includes("atlassian.net") ? "" : "admin/") + "VM+Directory";
      cl.hidden = false;
    }
  } catch (e) {
    // status fetch is best-effort
  }
}

async function doRefresh() {
  const btn = document.getElementById("refresh-btn");
  btn.disabled = true;
  btn.textContent = "Refreshing";
  try {
    const resp = await fetch("/api/refresh", {
      method: "POST",
      headers: Object.assign(authHeaders(), {
        "Content-Type": "application/json",
        "X-Inventory-Action": "refresh"
      })
    });
    if (resp.ok) {
      await loadInventory();
    } else if (resp.status === 409) {
      alert("Refresh already in progress.");
    } else {
      alert("Refresh failed.");
    }
  } catch (err) {
    alert(`Refresh failed: ${err.message}`);
  } finally {
    btn.disabled = false;
    btn.textContent = "Refresh";
  }
}

async function doPublish() {
  const btn = document.getElementById("publish-btn");
  btn.disabled = true;
  btn.textContent = "Publishing";
  try {
    const resp = await fetch("/api/confluence/publish", {
      method: "POST",
      headers: Object.assign(authHeaders(), {
        "Content-Type": "application/json",
        "X-Inventory-Action": "publish"
      })
    });
    const result = await resp.json();
    const statusEl = document.getElementById("cache-status");
    statusEl.dataset.stale = "0";
    if (result.error) {
      statusEl.textContent = `Publish failed: ${result.error}`;
      statusEl.dataset.stale = "1";
    } else if (result.status === "published") {
      statusEl.textContent = "Published to Confluence";
    } else {
      statusEl.textContent = `Publish: ${result.status}`;
    }
  } catch (err) {
    alert(`Publish failed: ${err.message}`);
  } finally {
    btn.disabled = false;
    btn.textContent = "Publish";
  }
}

// --- Render ---

// The page is updated in place. Cards and rows are created once and reused, and
// a filter change only toggles `hidden`. Swapping innerHTML on every keystroke
// restarted the meter animation, dropped the reading position and flashed the
// whole board — and it left every gauge empty, because the replacement meters
// never received the class that fills them.
const cardEls = new Map();   // host id -> { el, host, geo, sig }
const geoEls = new Map();    // geo name -> section element
const tableEls = { vm: null, ct: null };
const collapsed = new Set(); // folded blocks: "geo:<name>", "table:vm", "table:ct"

let hostsCache = [];
let vmsCache = [];
let ctsCache = [];
let geoOrder = [];
const resById = new Map();   // inventory id -> resource of the current snapshot
let vmRows = [];             // [{ el, res }] — the VM table body
let ctRows = [];
let vmTableSig = "";
let ctTableSig = "";
let statsSig = "";
let geoOptSig = "";
let hostOptSig = "";
let geoSeq = 0;

function render(data) {
  const container = document.getElementById("geos");
  const geos = (data && data.geos) || [];

  if (!geos.length) {
    container.innerHTML = "";
    cardEls.clear();
    geoEls.clear();
    tableEls.vm = tableEls.ct = null;
    hostsCache = []; vmsCache = []; ctsCache = []; geoOrder = [];
    vmRows = []; ctRows = [];
    vmTableSig = ctTableSig = statsSig = geoOptSig = hostOptSig = "";
    geoSeq = 0;
    document.getElementById("empty").hidden = false;
    document.getElementById("no-results").hidden = true;
    document.getElementById("stats").hidden = true;
    return;
  }
  document.getElementById("empty").hidden = true;

  // Flatten the snapshot. The API emits null rather than [] for an empty
  // collection (§17.2), so every list needs a default.
  hostsCache = []; vmsCache = []; ctsCache = []; geoOrder = [];
  resById.clear();

  for (const geo of geos) {
    geoOrder.push(geo.name);
    for (const h of (geo.hosts || [])) {
      h._geo = geo.name;
      h._search = searchText([h.id, h.description, h.hostname, h.os_name, (h.ips || []).join(" ")]);
      hostsCache.push(h);
    }
    for (const vm of (geo.virtual_machines || [])) {
      vm.geo = vm.geo || geo.name;
      vm._search = searchText([vm.name, vm.title, vm.description, vm.guest_os, (vm.ips || []).join(" "), vm.host_id]);
      vmsCache.push(vm);
      resById.set(vm.inventory_id, vm);
    }
    for (const ct of (geo.lxd_containers || [])) {
      ct.geo = ct.geo || geo.name;
      ct._search = searchText([ct.name, ct.title, ct.description, ct.guest_os, (ct.ips || []).join(" "), ct.host_id]);
      ctsCache.push(ct);
      resById.set(ct.inventory_id, ct);
    }
  }

  const perHost = {};
  for (const vm of vmsCache) perHost[vm.host_id] = (perHost[vm.host_id] || 0) + 1;
  const ctPerHost = {};
  for (const ct of ctsCache) ctPerHost[ct.host_id] = (ctPerHost[ct.host_id] || 0) + 1;

  // One storage area height for the whole board, sized by the hungriest host.
  let poolSlots = 0;
  for (const h of hostsCache) poolSlots = Math.max(poolSlots, poolRowCount(h));

  renderStats(geos, hostsCache, vmsCache, ctsCache);
  populateFilters(geoOrder, hostsCache);

  const live = new Set();

  for (const geoName of geoOrder) {
    const section = ensureGeo(geoName);
    const grid = section.querySelector(".hosts");
    // Busiest machine first: the board answers "where is everything". Confluence
    // keeps the normalizer's alphabetical order, which is the API's own (§17.2),
    // so the two orderings differ on purpose.
    const hosts = hostsCache
      .filter(h => h._geo === geoName)
      .sort((a, b) => hostCompare(a, b, perHost, ctPerHost));

    let order = 0;
    for (const host of hosts) {
      live.add(host.id);
      const sig = hostSig(host) + "|" + (perHost[host.id] || 0) + "|" + (ctPerHost[host.id] || 0) + "|" + poolSlots;
      let entry = cardEls.get(host.id);

      if (!entry || entry.sig !== sig) {
        const el = entry ? entry.el : document.createElement("article");
        el.className = "host";
        el.dataset.role = hostRole(host);
        el.dataset.state = host.observation_state === "retained" ? "retained" : "current";
        el.innerHTML = hostCardBody(host, perHost, ctPerHost, poolSlots);
        el._search = host._search;
        entry = { el, host, geo: geoName, sig };
        cardEls.set(host.id, entry);
        if (el.parentNode !== grid) grid.appendChild(el);
        // Fresh values, so let the segments fill from empty. Only rebuilt cards
        // animate; an update touches just the hosts that actually changed.
        // The reflow pins the empty state before the class flips it: a frame
        // callback would do the same, but it never runs while the tab is not
        // being painted, and the bars would then stay empty until something
        // else forced a repaint.
        void el.offsetHeight;
        for (const m of el.querySelectorAll(".meter")) m.classList.add("is-on");
      }

      entry.host = host;
      if (entry.geo !== geoName) { entry.geo = geoName; grid.appendChild(entry.el); }
      else if (entry.el.parentNode !== grid) grid.appendChild(entry.el);

      // Ordered with `order` rather than by moving nodes: re-inserting a card
      // replays its entrance animation.
      entry.el.style.order = String(order);
      entry.el.style.setProperty("--i", String(order));
      order++;
    }
  }

  // Drop what is gone: hosts no longer observed, and geos no longer reported.
  for (const [id, entry] of cardEls) {
    if (!live.has(id)) { entry.el.remove(); cardEls.delete(id); }
  }
  for (const [name, section] of geoEls) {
    if (!geoOrder.includes(name)) { section.remove(); geoEls.delete(name); }
  }

  // Resource tables are fleet-wide; the sections above are the per-host view.
  const sortKey = vmSort.col + (vmSort.asc ? "a" : "d");
  const vms = vmsCache.slice().sort(vmCompare);
  const cts = ctsCache.slice().sort(vmCompare);

  const vSig = sortKey + tableSig(vms);
  if (vSig !== vmTableSig) { vmTableSig = vSig; vmRows = buildTable("vm", vms, vmsCache.length); }
  const cSig = sortKey + tableSig(cts);
  if (cSig !== ctTableSig) { ctTableSig = cSig; ctRows = buildTable("ct", cts, ctsCache.length); }

  // Keep the tables last, after every geo section.
  if (tableEls.ct && container.lastElementChild !== tableEls.ct) {
    container.appendChild(tableEls.vm);
    container.appendChild(tableEls.ct);
  }

  applyFold();
  applyFilter();
}

// hostSig covers everything a card draws. Render-time scratch fields (`_geo`,
// `_search`, `_vis`) are left out: they are added to the payload object after
// the signature is taken, so including them would make the next render of
// unchanged data look new and rebuild every card — replaying the gauge fill and
// flashing the board on every sort click.
function hostSig(host) {
  const drawn = {};
  for (const key of Object.keys(host)) {
    if (key[0] !== "_") drawn[key] = host[key];
  }
  return JSON.stringify(drawn);
}

// tableSig changes when a row's content or membership changes, so the body is
// only rebuilt when there is something new to draw.
function tableSig(rows) {
  return rows.map(r => [
    r.inventory_id, r.name, r.title, r.host_id, r.geo, r.observation_state, r.last_seen || "",
    r.platform || "", (r.ips || []).join(","), r.description, r.guest_os,
    r.cpu_count, r.memory_bytes, r.disk_total_bytes, r.root_disk_bytes
  ].join("|")).join("\n");
}

function ensureGeo(name) {
  let section = geoEls.get(name);
  if (section) return section;
  const id = "geohosts-" + (geoSeq++);
  section = document.createElement("section");
  section.className = "geo";
  section.dataset.geo = name;
  section.innerHTML =
    `<header class="geo-head">` +
      foldButton("geo:" + name, id) +
      `<h2 class="geo-name">${esc(name)}</h2>` +
      `<span class="geo-rule" aria-hidden="true"></span>` +
      `<span class="geo-count"></span>` +
    `</header>` +
    `<div class="hosts" id="${escAttr(id)}" data-empty="0"></div>`;
  geoEls.set(name, section);
  document.getElementById("geos").appendChild(section);
  return section;
}

function foldButton(key, controls) {
  return `<button class="fold" type="button" data-fold="${escAttr(key)}" aria-expanded="true" ` +
    `aria-controls="${escAttr(controls)}" title="Collapse">` +
    `<svg class="fold-ico" viewBox="0 0 12 12" aria-hidden="true" focusable="false">` +
    `<path d="M2.6 4.4 6 7.8l3.4-3.4" fill="none" stroke="currentColor" stroke-width="1.5" ` +
    `stroke-linecap="round" stroke-linejoin="round"/></svg></button>`;
}

function onFoldClick(e) {
  const btn = e.target.closest("[data-fold]");
  if (!btn) return;
  const key = btn.dataset.fold;
  if (collapsed.has(key)) collapsed.delete(key); else collapsed.add(key);
  applyFold();
}

function applyFold() {
  for (const btn of document.querySelectorAll("[data-fold]")) {
    const open = !collapsed.has(btn.dataset.fold);
    btn.setAttribute("aria-expanded", open ? "true" : "false");
    btn.title = open ? "Collapse" : "Expand";
    const target = document.getElementById(btn.getAttribute("aria-controls"));
    if (target) target.hidden = !open;
  }
}

// applyFilter only shows and hides what is already on the page.
function applyFilter() {
  const filterActive = Boolean(searchTerm || geoFilter || hostFilter);
  let shown = 0;

  for (const h of hostsCache) {
    h._vis = (!geoFilter || h._geo === geoFilter) && matchesSearch(h) &&
      !(hostFilter && h.id !== hostFilter);
    if (h._vis) shown++;
  }
  for (const r of vmsCache) {
    r._vis = (!geoFilter || r.geo === geoFilter) && matchesSearch(r) &&
      !(hostFilter && r.host_id !== hostFilter);
    if (r._vis) shown++;
  }
  for (const r of ctsCache) {
    r._vis = (!geoFilter || r.geo === geoFilter) && matchesSearch(r) &&
      !(hostFilter && r.host_id !== hostFilter);
    if (r._vis) shown++;
  }

  for (const entry of cardEls.values()) entry.el.hidden = !entry.host._vis;
  for (const r of vmRows) r.el.hidden = !visible(r.id);
  for (const r of ctRows) r.el.hidden = !visible(r.id);

  for (const [name, section] of geoEls) {
    const nH = hostsCache.filter(h => h._geo === name && h._vis).length;
    const nV = vmsCache.filter(v => v.geo === name && v._vis).length;
    const nC = ctsCache.filter(c => c.geo === name && c._vis).length;
    section.hidden = nH + nV + nC === 0;
    section.querySelector(".geo-count").textContent = geoCount(nH, nV, nC);
    section.querySelector(".hosts").dataset.empty = nH === 0 ? "1" : "0";
  }

  if (tableEls.vm) setCaption(tableEls.vm, vmsCache.filter(r => r._vis).length, vmsCache.length);
  if (tableEls.ct) setCaption(tableEls.ct, ctsCache.filter(r => r._vis).length, ctsCache.length);

  document.getElementById("no-results").hidden = !(filterActive && shown === 0);

  // Only level while the whole board is visible: hidden cards measure as zero,
  // so a filtered board would set the floor from a subset.
  if (!filterActive) levelHostHeights();
}

// Each geo is its own grid, so a geo whose tallest card is short would draw
// shorter cards than its neighbour. The floor is measured from the tallest card
// on the board instead of hard-coded, so it follows the content.
function levelHostHeights() {
  const root = document.getElementById("geos");
  root.style.removeProperty("--host-h");
  let tallest = 0;
  for (const card of root.querySelectorAll(".geo .host")) {
    tallest = Math.max(tallest, card.getBoundingClientRect().height);
  }
  if (tallest) root.style.setProperty("--host-h", `${Math.ceil(tallest)}px`);
}

function matchesSearch(r) {
  return !searchTerm || (r._search || "").includes(searchTerm);
}

function visible(id) {
  const res = resById.get(id);
  return Boolean(res && res._vis);
}

function geoCount(nH, nV, nC) {
  const parts = [`${nH} ${nH === 1 ? "host" : "hosts"}`];
  if (nV) parts.push(`${nV} vms`);
  if (nC) parts.push(`${nC} ct`);
  return parts.join(" · ");
}

function setCaption(section, shown, total) {
  const cap = section.querySelector(".geo-count");
  if (cap) cap.textContent = countLabel(shown, total);
}

// silent counts the entries a wider view carries that are no longer being
// collected. They are part of the view, so they are counted, but each tile says
// how many of its number is history rather than reading as a live fleet size.
function silent(items) {
  const n = items.filter(i => i.observation_state === "retained").length;
  return n ? ` · ${n} not collected` : "";
}

function renderStats(geos, hosts, vms, cts) {
  const rail = document.getElementById("stats");
  const esxi = hosts.filter(h => hostRole(h) === "esxi").length;
  const kvmCount = vms.filter(v => v.platform === "KVM").length;
  const esxiVMs = vms.filter(v => v.platform === "ESXi").length;

  const stats = [
    { label: "Hosts", value: hosts.length, note: `${esxi} esxi · ${hosts.length - esxi} linux${silent(hosts)}` },
    { label: "Virtual machines", value: vms.length, note: `${kvmCount} kvm · ${esxiVMs} esxi${silent(vms)}` },
    { label: "Containers", value: cts.length, note: (cts.length ? "lxd" : "none observed") + silent(cts) },
    { label: "Geos", value: geos.length, note: geos.map(g => g.name).join(" · ") },
  ];

  const sig = JSON.stringify(stats);
  if (sig === statsSig) { rail.hidden = false; return; }
  statsSig = sig;

  rail.innerHTML = stats.map(s => `
    <div class="stat">
      <div class="stat-label">${esc(s.label)}</div>
      <div class="stat-value">${s.value}</div>
      <div class="stat-note">${esc(s.note)}</div>
    </div>`).join("");
  rail.hidden = false;
}

function populateFilters(geoOrderNames, hosts) {
  // Rebuilding a select closes an open native dropdown, so only touch it when
  // the option list actually changed.
  const geoSelect = document.getElementById("geo-filter");
  const geoNames = geoOrderNames.slice().sort();
  const gSig = geoNames.join(" ");
  if (gSig !== geoOptSig) {
    geoOptSig = gSig;
    geoSelect.innerHTML = `<option value="">All geos</option>` +
      geoNames.map(g => `<option value="${escAttr(g)}">${esc(g)}</option>`).join("");
  }
  geoSelect.value = geoFilter;

  const hostSelect = document.getElementById("host-filter");
  const sorted = hosts.slice().sort((a, b) => (a.id || "").localeCompare(b.id || ""));
  const hSig = sorted.map(h => h.id + "" + (h._geo || "")).join(" ");
  if (hSig !== hostOptSig) {
    hostOptSig = hSig;
    hostSelect.innerHTML = `<option value="">All hosts</option>` +
      sorted.map(h => `<option value="${escAttr(h.id)}">${esc(h.id)} (${esc(h._geo || "")})</option>`).join("");
  }
  hostSelect.value = hostFilter;
}

// hostRole derives what the machine actually runs from the storage pools the
// exporter reported, which is more reliable than the generic platform label
// ("linux" covers both KVM and LXD hosts).
function hostRole(host) {
  const types = (host.storage_pools || []).map(p => p.pool_type || "");
  if (types.some(t => t.startsWith("esxi-"))) return "esxi";
  if (types.some(t => t.startsWith("lxd-"))) return "lxd";
  if (types.some(t => t.startsWith("libvirt-"))) return "kvm";
  return host.platform === "esxi" ? "esxi" : "host";
}

const ROLE_LABEL = { kvm: "KVM", lxd: "LXD", esxi: "ESXi", host: "Linux" };

// hostCardBody renders the inside of a host card. The card element itself is
// created and keyed in render(), so unchanged hosts are never rebuilt.
function hostCardBody(host, perHost, ctPerHost, poolSlots) {
  const role = hostRole(host);
  const retained = host.observation_state === "retained";
  const nVM = perHost[host.id] || 0;
  const nCT = ctPerHost[host.id] || 0;

  let h = `<div class="host-head">`;
  h += `<h3 class="host-name">${esc(host.id)}</h3>`;
  h += `<span class="host-chip">${ROLE_LABEL[role] || "Linux"}</span>`;
  const guests = [];
  if (nVM) guests.push(`${nVM} vm${nVM === 1 ? "" : "s"}`);
  if (nCT) guests.push(`${nCT} container${nCT === 1 ? "" : "s"}`);
  if (guests.length) h += `<span class="host-guests">${esc(guests.join(" · "))}</span>`;
  h += `</div>`;

  if (host.description) h += `<p class="host-desc">${esc(host.description)}</p>`;

  h += `<div class="host-body">`;

  // --- meta readout ---
  const rows = [];
  // The ESXi collector reports an os_name that already embeds the version
  // ("VMware ESXi 7.0.3 build-20036589"), so appending os_version would repeat
  // it. Same for kernel, which is just the platform name on those hosts.
  const osName = host.os_name || "";
  const osVersion = host.os_version || "";
  const os = osVersion && !containsAll(osName, osVersion) ? `${osName} ${osVersion}`.trim() : osName;
  if (os || host.architecture) {
    rows.push(["OS", [os, host.architecture].filter(Boolean).join(" · ")]);
  }
  if (host.kernel && !containsAll(os, host.kernel)) rows.push(["Kernel", host.kernel]);
  if (host.hostname && host.hostname !== host.id) rows.push(["Hostname", host.hostname]);
  if (host.ips && host.ips.length) {
    const all = host.ips.join(", ");
    rows.push(["IP", raw(`<span title="${escAttr(all)}">${formatIPs(host.ips)}</span>`)]);
  }
  // Always shown, even empty, so every card's readout stays on the same line
  // grid (§10 unknown values render —).
  const diskGroups = (host.disks || []).map(d => `${d.count} × ${formatBytes(d.size_bytes)}`);
  rows.push(["Disks", diskGroups.length
    ? raw(`<span class="disk-list">${esc(diskGroups.join(", "))}</span>`)
    : raw(na())]);
  if (retained) {
    rows.push(["Last seen", raw(`<span class="host-stale">${esc(timeAgo(host.last_seen) || "unknown")}</span>`)]);
  }
  if (rows.length) {
    h += `<dl class="host-meta">` +
      rows.map(([k, v]) => `<dt>${esc(k)}</dt><dd>${v && v.html != null ? v.html : esc(v)}</dd>`).join("") +
      `</dl>`;
  }

  // --- meters ---
  const meters = [];

  if (host.cpu && host.cpu.threads) {
    const used = host.cpu.used_thread_equivalents;
    if (used != null) {
      const p = clampPct(used / host.cpu.threads * 100);
      meters.push(meterRow("CPU", p, p, `${round(used, 1)}/${host.cpu.threads} thr · ${Math.round(p)}%`,
        `${host.cpu.model || "unknown"} — ${host.cpu.sockets}s × ${host.cpu.cores}c × ${host.cpu.threads}t`));
    } else {
      meters.push(meterNote("CPU", `${host.cpu.model || "unknown"} — ${host.cpu.sockets}s × ${host.cpu.cores}c × ${host.cpu.threads}t`));
    }
  }

  if (host.memory && host.memory.total_bytes) {
    const m = memBands(host.memory);
    if (m) {
      let label = `${formatBytes(m.used)}/${formatBytes(m.total)} · ${Math.round(m.u)}%`;
      let title = `used ${formatBytes(m.used)} · free ${formatBytes(m.avail)} · total ${formatBytes(m.total)}`;
      if (m.hp > 0) {
        title += ` · hugepage free ${formatBytes(m.hp)}`;
      }
      meters.push(meterRow("RAM", m.u, m.h, label, title));
    } else {
      meters.push(meterNote("RAM", `${formatBytes(host.memory.total_bytes)} total · usage not reported`));
    }
  }

  if (meters.length) h += `<div class="meters">${meters.join("")}</div>`;

  // --- storage ---
  const pools = host.storage_pools || [];
  const filesystems = host.filesystems || [];
  const storage = [];

  for (const pool of pools) {
    const total = pool.total_bytes || 0;
    if (!total) continue;
    const avail = pool.available_bytes || 0;
    const used = Math.max(0, total - avail);
    const p = clampPct(used / total * 100);
    storage.push(storageRow(
      pool.pool_name, pool.pool_type, p,
      `${formatBytes(used)}/${formatBytes(total)} · ${Math.round(p)}%`
    ));
  }
  for (const fs of filesystems) {
    const total = fs.total_bytes || 0;
    if (!total) continue;
    const avail = fs.available_bytes != null ? fs.available_bytes : 0;
    const used = Math.max(0, total - avail);
    const p = clampPct(used / total * 100);
    const mounts = (fs.mountpoints || []).join(", ");
    storage.push(storageRow(
      mounts || fs.filesystem_type, fs.filesystem_type, p,
      `${formatBytes(used)}/${formatBytes(total)} · ${Math.round(p)}%`
    ));
  }

  // Hosts report a different number of pools — an ESXi host one datastore, a
  // KVM host one or two pools — which would leave the row of cards with a
  // ragged bottom edge. Padding to the fleet's slot count keeps every storage
  // area the same height; the extra slots are blank, not empty gauges.
  if (storage.length || poolSlots) {
    while (storage.length < poolSlots) storage.push(blankPool());
    h += `<div class="pools">${storage.join("")}</div>`;
  }

  return h;
}

// blankPool occupies a storage slot with the same markup a real pool does, so
// the reserved height cannot drift when the pool styles change.
function blankPool() {
  return `<div class="pool pool-blank" aria-hidden="true">` +
    `<div class="pool-head">` +
    `<span class="pool-key"></span><span class="pool-type"></span><span class="pool-val"></span>` +
    `</div>` +
    `<div class="meter"></div>` +
    `</div>`;
}

// poolRowCount counts the storage rows a host will actually render, so the
// fleet-wide slot count matches what the cards draw.
function poolRowCount(host) {
  let n = 0;
  for (const pool of (host.storage_pools || [])) { if (pool.total_bytes) n++; }
  for (const fs of (host.filesystems || [])) { if (fs.total_bytes) n++; }
  return n;
}

function meterRow(key, u, h, value, title) {
  return `<div class="meter-row"${title ? ` title="${escAttr(title)}"` : ""}>` +
    `<span class="meter-key">${esc(key)}</span>` +
    `<div class="meter" aria-hidden="true" style="--u:${pctStr(u)};--h:${pctStr(h)}"><div class="meter-bands"></div></div>` +
    `<span class="meter-val">${esc(value)}</span>` +
    `</div>`;
}

function meterNote(key, value) {
  return `<div class="meter-row">` +
    `<span class="meter-key">${esc(key)}</span>` +
    `<span class="meter-note">${esc(value)}</span>` +
    `</div>`;
}

function storageRow(name, type, p, value) {
  return `<div class="pool">` +
    `<div class="pool-head">` +
    `<span class="pool-key">${esc(name)}</span>` +
    `<span class="pool-type">${esc(type)}</span>` +
    `<span class="pool-val">${esc(value)}</span>` +
    `</div>` +
    `<div class="meter" aria-hidden="true" style="--u:${pctStr(p)};--h:${pctStr(p)}"><div class="meter-bands"></div></div>` +
    `</div>`;
}

// buildTable draws one resource table and returns its body rows, paired with the
// resources they represent so a later filter pass can hide them individually.
function buildTable(kind, rows, total) {
  let section = tableEls[kind];
  if (!section) {
    section = document.createElement("section");
    section.className = "table-block";
    section.dataset.table = kind;
    document.getElementById("geos").appendChild(section);
    tableEls[kind] = section;
  }

  if (!rows.length && total === 0) {
    section.hidden = true;
    section.innerHTML = "";
    return [];
  }
  section.hidden = false;

  const isVM = kind === "vm";
  const id = isVM ? "vm-rows" : "ct-rows";
  const title = isVM ? "Virtual machines" : "LXD containers";

  let h = `<header class="table-cap">` + foldButton("table:" + kind, id) +
    `<h2>${esc(title)}</h2><span class="geo-rule" aria-hidden="true"></span><span class="geo-count"></span></header>`;
  h += `<div class="table-scroll" id="${id}"><table><thead><tr>`;
  if (isVM) {
    h += `<th><button class="sort-btn" type="button" onclick="setSort('host_id')">Host${sortArrow("host_id")}</button></th>`;
    h += `<th><button class="sort-btn" type="button" onclick="setSort('name')">Name${sortArrow("name")}</button></th>`;
    h += `<th><button class="sort-btn" type="button" onclick="setSort('platform')">Platform${sortArrow("platform")}</button></th>`;
    h += `<th>Geo</th><th>IPs</th><th>Description</th><th>Guest OS</th><th class="num">vCPU</th>`;
  } else {
    h += `<th>Host</th><th>Name</th><th>Geo</th><th>IPs</th><th>Description</th><th>OS / Image</th><th class="num">CPU</th>`;
  }
  h += `<th class="num">RAM</th><th class="num">Disk</th>`;
  h += `</tr></thead><tbody>` + (isVM ? rows.map(vmRow).join("") : rows.map(ctRow).join("")) + `</tbody></table></div>`;
  section.innerHTML = h;

  // Rows are paired by inventory id, not by object reference: a refresh replaces
  // every resource object, and a row holding the previous one would keep
  // reading a stale visibility flag.
  const trs = section.querySelectorAll("tbody tr");
  return rows.map((res, i) => ({ el: trs[i], id: res.inventory_id }));
}

function vmRow(vm) {
  const label = vm.title || vm.name;
  const ips = (vm.ips || []).join(", ");
  const readings = [
    vm.cpu_count ? `${vm.cpu_count} vCPU` : "",
    vm.memory_bytes ? formatBytes(vm.memory_bytes) : "",
    vm.disk_total_bytes ? formatBytes(vm.disk_total_bytes) : "",
  ];
  return `<tr${rowState(vm)}>` +
    `<td class="mono">${esc(vm.host_id)}</td>` +
    `<td class="strong"${vm.title ? ` title="${escAttr(vm.name)}"` : ""}>${esc(label)}</td>` +
    `<td>${esc(vm.platform)}</td>` +
    `<td>${esc(vm.geo || "—")}</td>` +
    `<td class="mono"${ips ? ` title="${escAttr(ips)}"` : ""}>${formatIPs(vm.ips)}</td>` +
    `<td>${esc(vm.description) || na()}</td>` +
    `<td>${esc(vm.guest_os) || na()}</td>` +
    readingsCell(vm, readings) +
    `</tr>`;
}

function ctRow(ct) {
  const ips = (ct.ips || []).join(", ");
  const readings = [
    ct.cpu_count ? `${ct.cpu_count} vCPU` : "",
    ct.memory_bytes ? formatBytes(ct.memory_bytes) : "",
    ct.root_disk_bytes ? formatBytes(ct.root_disk_bytes) : "",
  ];
  return `<tr${rowState(ct)}>` +
    `<td class="mono">${esc(ct.host_id)}</td>` +
    `<td class="strong">${esc(ct.name)}</td>` +
    `<td>${esc(ct.geo || "—")}</td>` +
    `<td class="mono"${ips ? ` title="${escAttr(ips)}"` : ""}>${formatIPs(ct.ips)}</td>` +
    `<td>${esc(ct.description) || na()}</td>` +
    `<td>${esc(ct.guest_os) || na()}</td>` +
    readingsCell(ct, readings) +
    `</tr>`;
}

// readingsCell renders the three trailing columns. A resource being collected
// gets one cell each: vCPU, RAM and disk. A retained one gets a single cell
// spanning them, carrying the capacity it was last seen with — when the index
// still knows it — and the stamp saying when that was.
function readingsCell(res, readings) {
  if (res.observation_state !== "retained") {
    return readings.map(r => `<td class="num">${r || na()}</td>`).join("");
  }
  const known = readings.filter(Boolean).map(esc).join(" · ");
  return `<td class="num stale-cell" colspan="${readings.length}">` +
    (known ? `<span class="stale-specs">${known}</span>` : "") +
    `<span class="stale-stamp">last seen <b>${esc(timeAgo(res.last_seen) || "unknown")}</b></span>` +
    `</td>`;
}

function countLabel(shown, total) {
  if (shown === total) return `${total} ${total === 1 ? "entry" : "entries"}`;
  return `${shown} of ${total} shown`;
}

// rowState marks a retired resource so the stylesheet can dim its row, the way
// a retired host's card is dimmed.
function rowState(res) {
  return res.observation_state === "retained" ? ` data-state="retained"` : "";
}

// --- Search ---

function resetDropdowns(except) {
  if (except !== "search") { document.getElementById("search").value = ""; searchTerm = ""; }
  if (except !== "geo") { document.getElementById("geo-filter").value = ""; geoFilter = ""; }
  if (except !== "host") { document.getElementById("host-filter").value = ""; hostFilter = ""; }
}

function onSearch(e) {
  searchTerm = e.target.value.trim().toLowerCase();
  if (searchTerm) resetDropdowns("search");
  if (currentData) applyFilter();
}

function onGeoFilter() {
  geoFilter = document.getElementById("geo-filter").value;
  if (geoFilter) resetDropdowns("geo");
  if (currentData) applyFilter();
}

function onHostFilter() {
  hostFilter = document.getElementById("host-filter").value;
  if (hostFilter) resetDropdowns("host");
  if (currentData) applyFilter();
}

// searchText joins the searchable fields of a record. The newline separator
// keeps a term from spanning two unrelated fields.
function searchText(parts) {
  return parts.filter(Boolean).join("\n").toLowerCase();
}

// hostCompare orders hosts by total guests, most first. Ties fall back to the
// host name so the order is stable between refreshes.
function hostCompare(a, b, perHost, ctPerHost) {
  const ga = (perHost[a.id] || 0) + (ctPerHost[a.id] || 0);
  const gb = (perHost[b.id] || 0) + (ctPerHost[b.id] || 0);
  if (ga !== gb) return gb - ga;
  return (a.id || "").localeCompare(b.id || "");
}

// --- Utilities ---

function showLoading(show) {
  document.getElementById("loading").hidden = !show;
}

function esc(s) {
  if (!s) return "";
  const div = document.createElement("div");
  div.appendChild(document.createTextNode(String(s)));
  return div.innerHTML;
}

// escAttr is like esc but also safe inside a double-quoted HTML attribute.
function escAttr(s) {
  return esc(s).replace(/"/g, "&quot;");
}

// raw tags a value as pre-rendered HTML. Host meta rows escape every untagged
// value, so a row added without escaping cannot inject markup: those fields
// arrive from Prometheus labels that anything able to name a guest or a
// storage pool can set.
function raw(html) { return { html }; }

// na renders the missing-value placeholder (§10: unknown values render as —).
function na() { return `<span class="na">—</span>`; }

// containsAll reports whether every token of needle already appears in
// haystack. A plain substring test is not enough: the ESXi collector writes
// os_name "VMware ESXi 7.0.3 build-20036589" but os_version "7.0.3 build
// 20036589", which differ only in the separator.
function containsAll(haystack, needle) {
  if (!haystack || !needle) return false;
  const hay = normalizeTokens(haystack);
  const tokens = normalizeTokens(needle).split(" ").filter(Boolean);
  return tokens.length > 0 && tokens.every(t => hay.includes(t));
}

function normalizeTokens(s) {
  return String(s).toLowerCase().replace(/[^a-z0-9]+/g, " ").trim();
}

function clampPct(v) {
  if (!isFinite(v) || v < 0) return 0;
  return v > 100 ? 100 : v;
}

function pctStr(v) { return `${round(v, 2)}%`; }

function round(v, dp) {
  const f = Math.pow(10, dp);
  return Math.round(v * f) / f;
}

// memBands turns a memory readout into three non-overlapping bands. Hugepage
// free memory is a subset of available, so it is carved out of the free band —
// otherwise the segments would sum past 100% and overflow the meter.
function memBands(mem) {
  const total = mem.total_bytes || 0;
  if (!total) return null;
  // A host outside the freshness window keeps its capacity but loses its usage
  // reading (§15.3). With no reading there is no band to draw, and the card
  // says so rather than filling the meter from a zero it would have to invent.
  if (mem.used_bytes == null && mem.available_bytes == null) return null;
  const used = mem.used_bytes != null ? mem.used_bytes : Math.max(0, total - (mem.available_bytes || 0));
  const avail = mem.available_bytes != null ? mem.available_bytes : Math.max(0, total - used);
  const hp = mem.hugepages_free_bytes || 0;
  const u = clampPct(used / total * 100);
  const h = clampPct(u + hp / total * 100);
  return { u, h, used, avail, hp, total };
}

function timeAgo(ts) {
  if (!ts) return "";
  const diff = (Date.now() - new Date(ts).getTime()) / 1000;
  if (diff < 60) return `${Math.round(diff)}s ago`;
  if (diff < 3600) return `${Math.round(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.round(diff / 3600)}h ago`;
  return `${Math.round(diff / 86400)}d ago`;
}

// formatIPs renders the leading addresses of a resource. The API returns them in
// display order — the management address first, then the rest canonically (§11.4,
// §12.7) — so they are shown as sent. Re-sorting here would disagree with the
// tooltip, which lists the same slice to the same order.
function formatIPs(ips) {
  if (!ips || !ips.length) return na();
  if (ips.length <= 2) return ips.map(esc).join(", ");
  return ips.slice(0, 2).map(esc).join(", ") + ", …";
}

function formatBytes(bytes) {
  if (!bytes || bytes === 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  const val = bytes / Math.pow(1024, i);
  return `${val.toFixed(i > 0 ? 1 : 0)} ${units[i]}`;
}

function setSort(col) {
  if (vmSort.col === col) { vmSort.asc = !vmSort.asc; }
  else { vmSort.col = col; vmSort.asc = true; }
  if (currentData) render(currentData);
}

function sortArrow(col) {
  if (vmSort.col !== col) return "";
  return ` <span class="arw">${vmSort.asc ? "↑" : "↓"}</span>`;
}

function vmCompare(a, b) {
  const va = (a[vmSort.col] || "").toString().toLowerCase();
  const vb = (b[vmSort.col] || "").toString().toLowerCase();
  const cmp = va.localeCompare(vb);
  return vmSort.asc ? cmp : -cmp;
}

function debounce(fn, delay) {
  let timer;
  return (...args) => {
    clearTimeout(timer);
    timer = setTimeout(() => fn(...args), delay);
  };
}
