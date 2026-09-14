"use strict";

let currentData = null;
let searchTerm = "";
let geoFilter = "";
let hostFilter = "";
let vmSort = { col: "name", asc: true };
let authToken = "";

// --- Init ---

document.addEventListener("DOMContentLoaded", () => {
  authToken = localStorage.getItem("inv-auth") || "";
  if (!authToken) {
    showLogin();
  } else {
    loadInventory();
    setInterval(loadInventory, 60000);
  }
  document.getElementById("search").addEventListener("input", debounce(onSearch, 200));
});

// --- Auth ---

function showLogin() {
  const overlay = document.createElement("div");
  overlay.id = "login-overlay";
  overlay.innerHTML = `<form id="login-form" autocomplete="off">
    <h2>VM Inventory</h2>
    <input type="password" id="login-password" placeholder="Password" autofocus>
    <button type="submit">Login</button>
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
    document.getElementById("loading").textContent = "Failed to load inventory. Is the backend running?";
    document.getElementById("loading").hidden = false;
  } finally {
    showLoading(false);
  }
}

async function updateStatus() {
  try {
    const resp = await fetch("/api/status", { headers: authHeaders() });
    if (resp.ok) {
      const status = await resp.json();
      const el = document.getElementById("cache-status");
      const age = timeAgo(status.cache_generated_at);
      el.textContent = `Cache: ${age} | Hosts: ${status.host_count || 0} | Resources: ${status.resource_count || 0}`;
      if (status.confluence_url) {
        const cl = document.getElementById("confluence-link");
        cl.href = status.confluence_url + "/display/" + (status.confluence_url.includes("atlassian.net") ? "" : "admin/") + "VM+Directory";
        cl.style.display = "";
      }
    }
  } catch (e) {
    // status fetch is best-effort
  }
}

async function doRefresh() {
  const btn = document.getElementById("refresh-btn");
  btn.disabled = true;
  btn.textContent = "Refreshing...";
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
  btn.textContent = "Publishing...";
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
    if (result.error) {
      statusEl.textContent = `Publish failed: ${result.error}`;
    } else if (result.status === "published") {
      statusEl.textContent = "Published to Confluence";
    } else {
      statusEl.textContent = `Publish: ${result.status}`;
    }
  } catch (err) {
    alert(`Publish failed: ${err.message}`);
  } finally {
    btn.disabled = false;
    btn.textContent = "Publish to Confluence";
  }
}

// --- Render ---

function render(data) {
  if (!data || !data.geos || data.geos.length === 0) {
    document.getElementById("empty").hidden = false;
    document.getElementById("geos").innerHTML = "";
    return;
  }
  document.getElementById("empty").hidden = true;
  document.getElementById("no-results").hidden = true;

  const container = document.getElementById("geos");

  let html = "";

  // Flatten: collect all hosts, VMs, and LXD across geos.
  const allHosts = [];
  const allVMs = [];
  const allLXDs = [];
  const geoNames = new Set();
  for (const geo of data.geos) {
    geoNames.add(geo.name);
    if (geo.hosts) {
      for (const h of geo.hosts) { h._geo = geo.name; allHosts.push(h); }
    }
    if (geo.virtual_machines) {
      for (const vm of geo.virtual_machines) { vm.geo = vm.geo || geo.name; allVMs.push(vm); }
    }
    if (geo.lxd_containers) {
      for (const ct of geo.lxd_containers) { ct.geo = ct.geo || geo.name; allLXDs.push(ct); }
    }
  }

  // Populate geo filter dropdown.
  const geoSelect = document.getElementById("geo-filter");
  const currentGeo = geoSelect.value;
  geoSelect.innerHTML = '<option value="">All Geos</option>';
  for (const g of [...geoNames].sort()) {
    geoSelect.innerHTML += `<option value="${esc(g)}"${g === currentGeo ? " selected" : ""}>${esc(g)}</option>`;
  }

  // Populate host filter dropdown.
  const hostSelect = document.getElementById("host-filter");
  const currentHost = hostSelect.value;
  hostSelect.innerHTML = '<option value="">All Hosts</option>';
  for (const h of allHosts) {
    const label = `${h.id} (${h._geo || h.geo || "—"})`;
    hostSelect.innerHTML += `<option value="${esc(h.id)}"${h.id === currentHost ? " selected" : ""}>${esc(label)}</option>`;
  }

  // Sort.
  allHosts.sort((a, b) => (a._geo || "").localeCompare(b._geo || "") || (a.id || "").localeCompare(b.id || ""));
  allVMs.sort(vmCompare);
  allLXDs.sort(vmCompare);

  // Hosts.
  for (const host of allHosts) {
    if (geoFilter && host._geo !== geoFilter) continue;
    if (hostFilter && host.id !== hostFilter) continue;
    if (searchTerm && !hostMatches(host, host._geo, searchTerm)) continue;

    const retainedClass = host.observation_state === "retained" ? " retained" : "";
    html += `<div class="host-card${retainedClass}" data-host="${esc(host.id)}">`;
    html += `<h3>${esc(host.id)}</h3>`;
    html += `<div class="host-meta">`;
    html += `<span>Geo: ${esc(host._geo || host.geo || "—")}</span>`;
    html += `<span>Platform: ${esc(host.platform)}</span>`;
    html += `<span>OS: ${esc(host.os_name)} ${esc(host.os_version)}</span>`;
    if (host.hostname) html += `<span>Hostname: ${esc(host.hostname)}</span>`;
    html += `</div>`;
    if (host.ips && host.ips.length) {
      html += `<div class="host-ips">IPs: ${formatIPs(host.ips)}</div>`;
    }
    if (host.last_seen) {
      html += `<div class="last-seen">Last seen ${timeAgo(host.last_seen)}</div>`;
    }

    // CPU bar.
    if (host.cpu && host.cpu.threads) {
      const used = host.cpu.used_thread_equivalents;
      const free = host.cpu.free_thread_equivalents;
      if (used != null && free != null) {
        const pctUsed = Math.round((used / host.cpu.threads) * 100);
        const pctFree = 100 - pctUsed;
        html += `<div class="bar-container">`;
        html += `<div class="bar-label">CPU: ${used.toFixed(1)} of ${host.cpu.threads} threads used (${host.cpu.model || "unknown"})</div>`;
        html += `<div class="bar">`;
        html += `<div class="bar-segment used" style="width:${pctUsed}%" aria-label="${pctUsed}% used">${pctUsed}%</div>`;
        html += `<div class="bar-segment available" style="width:${pctFree}%" aria-label="${pctFree}% free">${pctFree}%</div>`;
        html += `</div></div>`;
      } else {
        html += `<div class="bar-label">CPU: ${host.cpu.model || "unknown"} (${host.cpu.sockets}s × ${host.cpu.cores}c × ${host.cpu.threads}t)</div>`;
      }
    }

    // RAM bar.
    if (host.memory && host.memory.total_bytes) {
      const used = host.memory.used_bytes;
      const hpFree = host.memory.hugepages_free_bytes;
      const normalFree = host.memory.available_bytes;
      if (used != null && normalFree != null) {
        const total = host.memory.total_bytes;
        const usedPct = Math.round((used / total) * 100);
        const hpPct = hpFree ? Math.round((hpFree / total) * 100) : 0;
        const freePct = Math.round((normalFree / total) * 100);
        html += `<div class="bar-container">`;
        html += `<div class="bar-label">RAM: ${formatBytes(used)} used / ${formatBytes(normalFree)} free / ${formatBytes(total)} total</div>`;
        html += `<div class="bar">`;
        html += `<div class="bar-segment used" style="width:${usedPct}%" aria-label="Used ${usedPct}%">${usedPct}%</div>`;
        if (hpPct > 0) html += `<div class="bar-segment hugepage-free" style="width:${hpPct}%" aria-label="Hugepage free ${hpPct}%">${hpPct}%</div>`;
        html += `<div class="bar-segment normal-free" style="width:${freePct}%" aria-label="Free ${freePct}%">${freePct}%</div>`;
        html += `</div></div>`;
      } else {
        html += `<div class="bar-label">RAM: ${formatBytes(host.memory.total_bytes)} total</div>`;
      }
    }

    // Disks.
    if (host.disks && host.disks.length) {
      html += `<div class="disk-group">Disks: ${host.disks.map(d => formatBytes(d.size_bytes) + " × " + d.count).join(", ")}</div>`;
    }

    // Filesystems.
    if (host.filesystems && host.filesystems.length) {
      html += `<div class="storage-section"><h4>Filesystems</h4>`;
      for (const fs of host.filesystems) {
        const mnts = fs.mountpoints ? fs.mountpoints.join(", ") : "—";
        html += `<div>${esc(fs.filesystem_type)} (${mnts}): ${formatBytes(fs.total_bytes)} total`;
        if (fs.available_bytes != null) html += `, ${formatBytes(fs.available_bytes)} free`;
        html += `</div>`;
      }
      html += `</div>`;
    }

    // Storage pools.
    if (host.storage_pools && host.storage_pools.length) {
      for (const pool of host.storage_pools) {
        const used = pool.total_bytes - (pool.available_bytes || 0);
        const pct = pool.total_bytes > 0 ? Math.round((used / pool.total_bytes) * 100) : 0;
        const typeLabel = pool.pool_type.startsWith('lxd-') ? `LXD (${esc(pool.pool_type)})` : esc(pool.pool_type);
        html += `<div class="storage-section"><h4>${typeLabel} Pool</h4>`;
        html += `<div class="bar-label">${esc(pool.pool_name)}: ${formatBytes(used)} used / ${formatBytes(pool.total_bytes)} total</div>`;
        html += `<div class="bar"><div class="bar-segment used" style="width:${pct}%">${pct}%</div>`;
        html += `<div class="bar-segment normal-free" style="width:${100-pct}%">${100-pct}%</div></div></div>`;
      }
    }

    html += `</div>`; // host-card
  }

  // VM table.
  if (allVMs.length) {
    html += `<table><caption>Virtual Machines</caption><thead><tr>`;
    html += `<th><button class="sort-btn" onclick="setSort('host_id')">Host ${sortArrow('host_id')}</button></th>`;
    html += `<th><button class="sort-btn" onclick="setSort('name')">Name ${sortArrow('name')}</button></th>`;
    html += `<th><button class="sort-btn" onclick="setSort('platform')">Platform ${sortArrow('platform')}</button></th>`;
    html += `<th>Geo</th><th>IPs</th><th>Description</th><th>Guest OS</th><th>vCPU</th><th>RAM</th><th>Disk</th>`;
    html += `</tr></thead><tbody>`;
    for (const vm of allVMs) {
      if (geoFilter && vm.geo !== geoFilter) continue;
      if (hostFilter && vm.host_id !== hostFilter) continue;
      if (searchTerm && !resourceMatches(vm, "vm", searchTerm)) continue;
      html += `<tr>`;
      html += `<td>${esc(vm.host_id)}</td>`;
      const vmLabel = vm.title || vm.name;
      html += `<td${vm.title ? ` title="${escAttr(vm.name)}"` : ""}>${esc(vmLabel)}${vm.last_seen ? ' <span class="last-seen">(last seen ' + timeAgo(vm.last_seen) + ')</span>' : ''}</td>`;
      html += `<td>${esc(vm.platform)}</td>`;
      html += `<td>${esc(vm.geo || "—")}</td>`;
      const vmIPs = (vm.ips && vm.ips.length) ? [...vm.ips].sort().join(", ") : "";
      html += `<td class="mono"${vmIPs ? ` title="${esc(vmIPs)}"` : ""}>${formatIPs(vm.ips)}</td>`;
      html += `<td>${esc(vm.description) || "—"}</td>`;
      html += `<td>${esc(vm.guest_os) || "—"}</td>`;
      html += `<td>${vm.cpu_count || "—"}</td>`;
      html += `<td>${vm.memory_bytes ? formatBytes(vm.memory_bytes) : "—"}</td>`;
      html += `<td>${vm.disk_total_bytes ? formatBytes(vm.disk_total_bytes) : "—"}</td>`;
      html += `</tr>`;
    }
    html += `</tbody></table>`;
  }

  // LXD table.
  if (allLXDs.length) {
    html += `<table><caption>LXD Containers</caption><thead><tr>`;
    html += `<th>Host</th><th>Name</th><th>Geo</th><th>IPs</th><th>Description</th><th>OS/Image</th><th>CPU</th><th>RAM</th><th>Disk</th>`;
    html += `</tr></thead><tbody>`;
    for (const ct of allLXDs) {
      if (geoFilter && ct.geo !== geoFilter) continue;
      if (hostFilter && ct.host_id !== hostFilter) continue;
      if (searchTerm && !resourceMatches(ct, "lxd", searchTerm)) continue;
      const ctIPs = (ct.ips && ct.ips.length) ? [...ct.ips].sort().join(", ") : "";
      html += `<tr>`;
      html += `<td>${esc(ct.host_id)}</td>`;
      html += `<td>${esc(ct.name)}${ct.last_seen ? ' <span class="last-seen">(last seen ' + timeAgo(ct.last_seen) + ')</span>' : ''}</td>`;
      html += `<td>${esc(ct.geo || "—")}</td>`;
      html += `<td class="mono"${ctIPs ? ` title="${esc(ctIPs)}"` : ""}>${formatIPs(ct.ips)}</td>`;
      html += `<td>${esc(ct.description) || "—"}</td>`;
      html += `<td>${esc(ct.guest_os) || "—"}</td>`;
      html += `<td>${ct.cpu_count || "—"}</td>`;
      html += `<td>${ct.memory_bytes ? formatBytes(ct.memory_bytes) : "—"}</td>`;
      html += `<td>${ct.root_disk_bytes ? formatBytes(ct.root_disk_bytes) : "—"}</td>`;
      html += `</tr>`;
    }
    html += `</tbody></table>`;
  }

  container.innerHTML = html;

  if (searchTerm && html === "") {
    document.getElementById("no-results").hidden = false;
  }
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
  if (host.id.toLowerCase().includes(term)) return true;
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

function slugify(s) {
  return String(s).toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
}

function timeAgo(ts) {
  if (!ts) return "";
  const diff = (Date.now() - new Date(ts).getTime()) / 1000;
  if (diff < 60) return `${Math.round(diff)}s ago`;
  if (diff < 3600) return `${Math.round(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.round(diff / 3600)}h ago`;
  return `${Math.round(diff / 86400)}d ago`;
}

function formatIPs(ips) {
  if (!ips || !ips.length) return "—";
  const priority = (ip) => {
    if (/^192\.168\./.test(ip)) return 0;
    if (/^172\.(1[6-9]|2\d|3[01])\./.test(ip)) return 1;
    if (/^10\./.test(ip)) return 2;
    if (/^fd/.test(ip)) return 3; // ULA IPv6
    return 4; // other (public, link-local IPv6, etc.)
  };
  const sorted = [...ips].sort((a, b) => priority(a) - priority(b) || a.localeCompare(b));
  if (sorted.length <= 2) return sorted.map(esc).join(", ");
  return sorted.slice(0, 2).map(esc).join(", ") + ", …";
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
  return vmSort.asc ? "▲" : "▼";
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
