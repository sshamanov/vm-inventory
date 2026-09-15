"use strict";

let currentData = null;
let searchTerm = "";
let geoFilter = "";
let hostFilter = "";
let vmSort = { col: "name", asc: true };
let authToken = "";

// Entrance animations replay on every render otherwise, which is distracting
// while typing in the search box. Only the first paint animates.
let firstPaint = true;

const THEME_KEY = "inv-theme";

// --- Init ---

document.addEventListener("DOMContentLoaded", () => {
  initTheme();
  initScroll();
  authToken = localStorage.getItem("inv-auth") || "";
  if (!authToken) {
    showLogin();
  } else {
    loadInventory();
    setInterval(loadInventory, 60000);
  }
  document.getElementById("search").addEventListener("input", debounce(onSearch, 200));
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
  let ticking = false;
  const apply = () => {
    header.classList.toggle("is-scrolled", window.scrollY > 6);
    ticking = false;
  };
  window.addEventListener("scroll", () => {
    if (ticking) return;
    ticking = true;
    requestAnimationFrame(apply);
  }, { passive: true });
  apply();
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
    const resp = await fetch("/api/inventory", { headers: authHeaders() });
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

function render(data) {
  const container = document.getElementById("geos");
  const geos = (data && data.geos) || [];

  if (!geos.length) {
    container.innerHTML = "";
    document.getElementById("empty").hidden = false;
    document.getElementById("no-results").hidden = true;
    document.getElementById("stats").hidden = true;
    firstPaint = false;
    return;
  }
  document.getElementById("empty").hidden = true;

  // Flatten the snapshot. The API emits null rather than [] for empty
  // collections, so every list needs a default.
  const allHosts = [];
  const allVMs = [];
  const allLXDs = [];
  const geoOrder = [];

  for (const geo of geos) {
    geoOrder.push(geo.name);
    for (const h of (geo.hosts || [])) { h._geo = geo.name; allHosts.push(h); }
    for (const vm of (geo.virtual_machines || [])) { vm.geo = vm.geo || geo.name; allVMs.push(vm); }
    for (const ct of (geo.lxd_containers || [])) { ct.geo = ct.geo || geo.name; allLXDs.push(ct); }
  }

  // Resources per host, for the card readout and the role chip.
  const perHost = {};
  for (const vm of allVMs) { perHost[vm.host_id] = (perHost[vm.host_id] || 0) + 1; }
  const ctPerHost = {};
  for (const ct of allLXDs) { ctPerHost[ct.host_id] = (ctPerHost[ct.host_id] || 0) + 1; }

  renderStats(geos, allHosts, allVMs, allLXDs);
  populateFilters(geoOrder, allHosts);

  // Sorting is shared by both resource tables.
  allVMs.sort(vmCompare);
  allLXDs.sort(vmCompare);

  const filterActive = Boolean(searchTerm || geoFilter || hostFilter);
  let shown = 0;
  let html = "";

  geoOrder.forEach((geoName, geoIdx) => {
    if (geoFilter && geoName !== geoFilter) return;
    const hosts = allHosts.filter(h => h._geo === geoName && hostVisible(h));
    const vms = allVMs.filter(v => v.geo === geoName && resourceVisible(v));
    const cts = allLXDs.filter(c => c.geo === geoName && resourceVisible(c));
    if (!hosts.length && !vms.length && !cts.length) return;
    shown += hosts.length + vms.length + cts.length;

    const counts = [];
    counts.push(`${hosts.length} ${hosts.length === 1 ? "host" : "hosts"}`);
    if (vms.length) counts.push(`${vms.length} vms`);
    if (cts.length) counts.push(`${cts.length} ct`);

    html += `<section class="geo">`;
    html += `<header class="geo-head">`;
    html += `<span class="geo-fig">Fig. ${pad2(geoIdx + 1)}</span>`;
    html += `<h2 class="geo-name">${esc(geoName)}</h2>`;
    html += `<span class="geo-rule" aria-hidden="true"></span>`;
    html += `<span class="geo-count">${esc(counts.join(" · "))}</span>`;
    html += `</header>`;

    if (hosts.length) {
      hosts.sort((a, b) => (a.id || "").localeCompare(b.id || ""));
      html += `<div class="hosts">`;
      hosts.forEach((host, i) => { html += hostCard(host, i, perHost, ctPerHost); });
      html += `</div>`;
    }
    html += `</section>`;
  });

  // Resource tables are fleet-wide; the geo sections above are the per-host view.
  const vms = allVMs.filter(resourceVisible);
  const cts = allLXDs.filter(resourceVisible);
  if (geoFilter) {
    // A geo filter narrows the tables too, matching the sections above.
    for (let i = vms.length - 1; i >= 0; i--) if (vms[i].geo !== geoFilter) vms.splice(i, 1);
    for (let i = cts.length - 1; i >= 0; i--) if (cts[i].geo !== geoFilter) cts.splice(i, 1);
  }

  html += vmTable(vms, allVMs.length);
  html += ctTable(cts, allLXDs.length);

  container.innerHTML = html;

  if (firstPaint) {
    requestAnimationFrame(() => {
      for (const m of container.querySelectorAll(".meter")) m.classList.add("is-on");
    });
    firstPaint = false;
  }

  document.getElementById("no-results").hidden = !(filterActive && shown === 0 && !vms.length && !cts.length);
}

function renderStats(geos, hosts, vms, cts) {
  const rail = document.getElementById("stats");
  const esxi = hosts.filter(h => hostRole(h) === "esxi").length;
  const kvmCount = vms.filter(v => v.platform === "KVM").length;
  const esxiVMs = vms.filter(v => v.platform === "ESXi").length;

  const stats = [
    { label: "Hosts", value: hosts.length, note: `${esxi} esxi · ${hosts.length - esxi} linux` },
    { label: "Virtual machines", value: vms.length, note: `${kvmCount} kvm · ${esxiVMs} esxi` },
    { label: "Containers", value: cts.length, note: cts.length ? "lxd" : "none observed" },
    { label: "Geos", value: geos.length, note: geos.map(g => g.name).join(" · ") },
  ];

  rail.innerHTML = stats.map(s => `
    <div class="stat">
      <div class="stat-label">${esc(s.label)}</div>
      <div class="stat-value">${s.value}</div>
      <div class="stat-note">${esc(s.note)}</div>
    </div>`).join("");
  rail.hidden = false;
}

function populateFilters(geoOrder, hosts) {
  const geoSelect = document.getElementById("geo-filter");
  const currentGeo = geoSelect.value;
  geoSelect.innerHTML = `<option value="">All geos</option>` +
    geoOrder.slice().sort().map(g =>
      `<option value="${escAttr(g)}"${g === currentGeo ? " selected" : ""}>${esc(g)}</option>`).join("");

  const hostSelect = document.getElementById("host-filter");
  const currentHost = hostSelect.value;
  const sorted = hosts.slice().sort((a, b) => (a.id || "").localeCompare(b.id || ""));
  hostSelect.innerHTML = `<option value="">All hosts</option>` +
    sorted.map(h =>
      `<option value="${escAttr(h.id)}"${h.id === currentHost ? " selected" : ""}>${esc(h.id)} (${esc(h._geo || "")})</option>`).join("");
}

function hostVisible(host) {
  if (hostFilter && host.id !== hostFilter) return false;
  if (searchTerm && !hostMatches(host, host._geo, searchTerm)) return false;
  return true;
}

function resourceVisible(res) {
  if (hostFilter && res.host_id !== hostFilter) return false;
  if (searchTerm && !resourceMatches(res, "res", searchTerm)) return false;
  return true;
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

function hostCard(host, index, perHost, ctPerHost) {
  const role = hostRole(host);
  const retained = host.observation_state === "retained";
  const nVM = perHost[host.id] || 0;
  const nCT = ctPerHost[host.id] || 0;

  let h = `<article class="host" data-role="${role}" data-state="${retained ? "retained" : "current"}" style="--i:${index}">`;

  h += `<div class="host-head">`;
  h += `<h3 class="host-name">${esc(host.id)}</h3>`;
  h += `<span class="host-chip">${ROLE_LABEL[role] || "Linux"}</span>`;
  const guests = [];
  if (nVM) guests.push(`${nVM} vm${nVM === 1 ? "" : "s"}`);
  if (nCT) guests.push(`${nCT} container${nCT === 1 ? "" : "s"}`);
  if (guests.length) h += `<span class="host-guests">${esc(guests.join(" · "))}</span>`;
  h += `<span class="host-idx">${pad2(index + 1)}</span>`;
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
    rows.push(["IP", `<span title="${escAttr(all)}">${formatIPs(host.ips)}</span>`]);
  }
  if (host.disks && host.disks.length) {
    const groups = host.disks.map(d => `${d.count} × ${formatBytes(d.size_bytes)}`);
    rows.push(["Disks", `<span class="disk-list">${esc(groups.join(" · "))}</span>`]);
  }
  if (retained) {
    rows.push(["Last seen", `<span class="host-stale">${esc(timeAgo(host.last_seen) || "unknown")}</span>`]);
  }
  if (rows.length) {
    h += `<dl class="host-meta">` +
      rows.map(([k, v]) => `<dt>${esc(k)}</dt><dd>${v}</dd>`).join("") +
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

  if (storage.length) h += `<div class="pools">${storage.join("")}</div>`;

  h += `</div></article>`;
  return h;
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

function vmTable(vms, total) {
  if (!vms.length && total === 0) return "";
  const cap = countLabel("Virtual machines", vms.length, total);
  let h = `<section class="table-block">`;
  h += `<header class="table-cap"><h2>Virtual machines</h2><span class="geo-rule" aria-hidden="true"></span><span class="geo-count">${esc(cap)}</span></header>`;
  h += `<div class="table-scroll"><table><thead><tr>`;
  h += `<th><button class="sort-btn" type="button" onclick="setSort('host_id')">Host${sortArrow("host_id")}</button></th>`;
  h += `<th><button class="sort-btn" type="button" onclick="setSort('name')">Name${sortArrow("name")}</button></th>`;
  h += `<th><button class="sort-btn" type="button" onclick="setSort('platform')">Platform${sortArrow("platform")}</button></th>`;
  h += `<th>Geo</th><th>IPs</th><th>Description</th><th>Guest OS</th>`;
  h += `<th class="num">vCPU</th><th class="num">RAM</th><th class="num">Disk</th>`;
  h += `</tr></thead><tbody>`;

  for (const vm of vms) {
    const label = vm.title || vm.name;
    const ips = (vm.ips || []).join(", ");
    h += `<tr>`;
    h += `<td class="mono">${esc(vm.host_id)}</td>`;
    h += `<td class="strong"${vm.title ? ` title="${escAttr(vm.name)}"` : ""}>${esc(label)}${staleMark(vm)}</td>`;
    h += `<td><span class="dot" data-role="${platformRole(vm.platform)}"></span>${esc(vm.platform)}</td>`;
    h += `<td>${esc(vm.geo || "—")}</td>`;
    h += `<td class="mono"${ips ? ` title="${escAttr(ips)}"` : ""}>${formatIPs(vm.ips)}</td>`;
    h += `<td>${esc(vm.description) || na()}</td>`;
    h += `<td>${esc(vm.guest_os) || na()}</td>`;
    h += `<td class="num">${vm.cpu_count || na()}</td>`;
    h += `<td class="num">${vm.memory_bytes ? formatBytes(vm.memory_bytes) : na()}</td>`;
    h += `<td class="num">${vm.disk_total_bytes ? formatBytes(vm.disk_total_bytes) : na()}</td>`;
    h += `</tr>`;
  }

  h += `</tbody></table></div></section>`;
  return h;
}

function ctTable(cts, total) {
  if (!cts.length && total === 0) return "";
  const cap = countLabel("LXD containers", cts.length, total);
  let h = `<section class="table-block">`;
  h += `<header class="table-cap"><h2>LXD containers</h2><span class="geo-rule" aria-hidden="true"></span><span class="geo-count">${esc(cap)}</span></header>`;
  h += `<div class="table-scroll"><table><thead><tr>`;
  h += `<th>Host</th><th>Name</th><th>Geo</th><th>IPs</th><th>Description</th><th>OS / Image</th>`;
  h += `<th class="num">CPU</th><th class="num">RAM</th><th class="num">Disk</th>`;
  h += `</tr></thead><tbody>`;

  for (const ct of cts) {
    const ips = (ct.ips || []).join(", ");
    h += `<tr>`;
    h += `<td class="mono">${esc(ct.host_id)}</td>`;
    h += `<td class="strong"><span class="dot" data-role="lxd"></span>${esc(ct.name)}${staleMark(ct)}</td>`;
    h += `<td>${esc(ct.geo || "—")}</td>`;
    h += `<td class="mono"${ips ? ` title="${escAttr(ips)}"` : ""}>${formatIPs(ct.ips)}</td>`;
    h += `<td>${esc(ct.description) || na()}</td>`;
    h += `<td>${esc(ct.guest_os) || na()}</td>`;
    h += `<td class="num">${ct.cpu_count || na()}</td>`;
    h += `<td class="num">${ct.memory_bytes ? formatBytes(ct.memory_bytes) : na()}</td>`;
    h += `<td class="num">${ct.root_disk_bytes ? formatBytes(ct.root_disk_bytes) : na()}</td>`;
    h += `</tr>`;
  }

  h += `</tbody></table></div></section>`;
  return h;
}

function countLabel(noun, shown, total) {
  if (shown === total) return `${total} ${total === 1 ? "entry" : "entries"}`;
  return `${shown} of ${total} shown`;
}

function staleMark(res) {
  if (res.observation_state !== "retained") return "";
  return ` <span class="host-stale">last seen ${esc(timeAgo(res.last_seen) || "unknown")}</span>`;
}

function platformRole(platform) {
  if (platform === "ESXi") return "esxi";
  if (platform === "KVM") return "kvm";
  return "host";
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
  if (currentData) render(currentData);
}

function onGeoFilter() {
  geoFilter = document.getElementById("geo-filter").value;
  if (geoFilter) resetDropdowns("geo");
  if (currentData) render(currentData);
}

function onHostFilter() {
  hostFilter = document.getElementById("host-filter").value;
  if (hostFilter) resetDropdowns("host");
  if (currentData) render(currentData);
}

function hostMatches(host, geo, term) {
  if ((host.id || "").toLowerCase().includes(term)) return true;
  if ((host.description || "").toLowerCase().includes(term)) return true;
  if ((host.hostname || "").toLowerCase().includes(term)) return true;
  if ((host.ips || []).some(ip => ip.includes(term))) return true;
  if ((host.os_name || "").toLowerCase().includes(term)) return true;
  return false;
}

function resourceMatches(res, type, term) {
  if ((res.name || "").toLowerCase().includes(term)) return true;
  if ((res.title || "").toLowerCase().includes(term)) return true;
  if ((res.description || "").toLowerCase().includes(term)) return true;
  if ((res.guest_os || "").toLowerCase().includes(term)) return true;
  if ((res.ips || []).some(ip => ip.includes(term))) return true;
  if ((res.host_id || "").toLowerCase().includes(term)) return true;
  return false;
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

function pad2(n) { return String(n).padStart(2, "0"); }

// memBands turns a memory readout into three non-overlapping bands. Hugepage
// free memory is a subset of available, so it is carved out of the free band —
// otherwise the segments would sum past 100% and overflow the meter.
function memBands(mem) {
  const total = mem.total_bytes || 0;
  if (!total) return null;
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
