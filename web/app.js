"use strict";

let currentData = null;
let searchTerm = "";

// --- Init ---

document.addEventListener("DOMContentLoaded", () => {
  loadInventory();
  document.getElementById("search").addEventListener("input", debounce(onSearch, 200));
});

// --- API ---

async function loadInventory() {
  showLoading(true);
  try {
    const resp = await fetch("/api/inventory", {
      headers: { "Accept": "application/json" }
    });
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
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
    const resp = await fetch("/api/status");
    if (resp.ok) {
      const status = await resp.json();
      const el = document.getElementById("cache-status");
      const age = timeAgo(status.cache_generated_at);
      el.textContent = `Cache: ${age} | Hosts: ${status.host_count || 0} | Resources: ${status.resource_count || 0}`;
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
      headers: {
        "Content-Type": "application/json",
        "X-Inventory-Action": "refresh"
      }
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
      headers: {
        "Content-Type": "application/json",
        "X-Inventory-Action": "publish"
      }
    });
    const result = await resp.json();
    const statusEl = document.getElementById("cache-status");
    if (result.status === "published") {
      statusEl.textContent = "Published to Confluence";
    } else if (result.status === "unchanged") {
      statusEl.textContent = "Confluence page unchanged — skipped";
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
  const geoNav = document.getElementById("geo-nav");
  const hostNav = document.getElementById("host-nav");

  let geoHTML = "";
  let geoNavHTML = "";
  let hostNavHTML = "";

  for (const geo of data.geos) {
    // Apply search filter.
    if (searchTerm && !geoMatches(geo, searchTerm)) continue;

    const anchor = slugify(geo.name);
    geoNavHTML += `<a href="#${anchor}">${esc(geo.name)}</a>`;

    geoHTML += `<section class="geo-section" id="${anchor}" data-geo="${esc(geo.name)}">`;
    geoHTML += `<h2>Geo: ${esc(geo.name)}</h2>`;

    // Hosts.
    for (const host of geo.hosts) {
      if (searchTerm && !hostMatches(host, geo, searchTerm)) continue;

      const hostAnchor = slugify(host.id);
      hostNavHTML += `<a href="#${hostAnchor}">${esc(host.id)}</a>`;

      const retainedClass = host.observation_state === "retained" ? " retained" : "";
      geoHTML += `<div class="host-card${retainedClass}" id="${hostAnchor}" data-host="${esc(host.id)}">`;
      geoHTML += `<h3>${esc(host.id)}</h3>`;
      geoHTML += `<div class="host-meta">`;
      geoHTML += `<span>Platform: ${esc(host.platform)}</span>`;
      geoHTML += `<span>OS: ${esc(host.os_name)} ${esc(host.os_version)}</span>`;
      if (host.hostname) geoHTML += `<span>Hostname: ${esc(host.hostname)}</span>`;
      geoHTML += `</div>`;
      if (host.ips && host.ips.length) {
        geoHTML += `<div class="host-ips">IPs: ${host.ips.map(esc).join(", ")}</div>`;
      }
      if (host.last_seen) {
        geoHTML += `<div class="last-seen">Last seen ${timeAgo(host.last_seen)}</div>`;
      }

      // CPU bar.
      if (host.cpu && host.cpu.threads) {
        const used = host.cpu.used_thread_equivalents;
        const free = host.cpu.free_thread_equivalents;
        if (used != null && free != null) {
          const pctUsed = Math.round((used / host.cpu.threads) * 100);
          const pctFree = 100 - pctUsed;
          geoHTML += `<div class="bar-container">`;
          geoHTML += `<div class="bar-label">CPU: ${used.toFixed(1)} of ${host.cpu.threads} threads used (${host.cpu.model || "unknown"})</div>`;
          geoHTML += `<div class="bar">`;
          geoHTML += `<div class="bar-segment used" style="width:${pctUsed}%" aria-label="${pctUsed}% used">${pctUsed}%</div>`;
          geoHTML += `<div class="bar-segment available" style="width:${pctFree}%" aria-label="${pctFree}% free">${pctFree}%</div>`;
          geoHTML += `</div></div>`;
        } else {
          geoHTML += `<div class="bar-label">CPU: ${host.cpu.model || "unknown"} (${host.cpu.sockets}s × ${host.cpu.cores}c × ${host.cpu.threads}t)</div>`;
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
          geoHTML += `<div class="bar-container">`;
          geoHTML += `<div class="bar-label">RAM: ${formatBytes(used)} used / ${formatBytes(normalFree)} free / ${formatBytes(total)} total</div>`;
          geoHTML += `<div class="bar">`;
          geoHTML += `<div class="bar-segment used" style="width:${usedPct}%" aria-label="Used ${usedPct}%">${usedPct}%</div>`;
          if (hpPct > 0) geoHTML += `<div class="bar-segment hugepage-free" style="width:${hpPct}%" aria-label="Hugepage free ${hpPct}%">${hpPct}%</div>`;
          geoHTML += `<div class="bar-segment normal-free" style="width:${freePct}%" aria-label="Free ${freePct}%">${freePct}%</div>`;
          geoHTML += `</div></div>`;
        } else {
          geoHTML += `<div class="bar-label">RAM: ${formatBytes(host.memory.total_bytes)} total</div>`;
        }
      }

      // Disks.
      if (host.disks && host.disks.length) {
        geoHTML += `<div class="disk-group">Disks: ${host.disks.map(d => formatBytes(d.size_bytes) + " × " + d.count).join(", ")}</div>`;
      }

      // Filesystems.
      if (host.filesystems && host.filesystems.length) {
        geoHTML += `<div class="storage-section"><h4>Filesystems</h4>`;
        for (const fs of host.filesystems) {
          const mnts = fs.mountpoints ? fs.mountpoints.join(", ") : "—";
          geoHTML += `<div>${esc(fs.filesystem_type)} (${mnts}): ${formatBytes(fs.total_bytes)} total`;
          if (fs.available_bytes != null) geoHTML += `, ${formatBytes(fs.available_bytes)} free`;
          geoHTML += `</div>`;
        }
        geoHTML += `</div>`;
      }

      // Storage pools.
      if (host.storage_pools && host.storage_pools.length) {
        geoHTML += `<div class="storage-section"><h4>Storage Pools</h4>`;
        for (const pool of host.storage_pools) {
          geoHTML += `<div>${esc(pool.pool_name)} (${esc(pool.pool_type)}): ${formatBytes(pool.total_bytes)} total`;
          if (pool.available_bytes != null) geoHTML += `, ${formatBytes(pool.available_bytes)} free`;
          geoHTML += `</div>`;
        }
        geoHTML += `</div>`;
      }

      geoHTML += `</div>`; // host-card
    }

    // VM table.
    if (geo.virtual_machines && geo.virtual_machines.length) {
      geoHTML += `<table><caption>Virtual Machines</caption><thead><tr>`;
      geoHTML += `<th>Host</th><th>Name</th><th>Platform</th><th>IPs</th><th>Description</th><th>Guest OS</th><th>vCPU</th><th>RAM</th><th>Disk</th>`;
      geoHTML += `</tr></thead><tbody>`;
      for (const vm of geo.virtual_machines) {
        if (searchTerm && !resourceMatches(vm, "vm", searchTerm)) continue;
        const ips = (vm.ips && vm.ips.length) ? vm.ips.join(", ") : "—";
        geoHTML += `<tr>`;
        geoHTML += `<td>${esc(vm.host_id)}</td>`;
        geoHTML += `<td>${esc(vm.name)}${vm.last_seen ? ' <span class="last-seen">(last seen ' + timeAgo(vm.last_seen) + ')</span>' : ''}</td>`;
        geoHTML += `<td>${esc(vm.platform)}</td>`;
        geoHTML += `<td class="mono">${esc(ips)}</td>`;
        geoHTML += `<td>${esc(vm.description) || "—"}</td>`;
        geoHTML += `<td>${esc(vm.guest_os) || "—"}</td>`;
        geoHTML += `<td>${vm.cpu_count || "—"}</td>`;
        geoHTML += `<td>${vm.memory_bytes ? formatBytes(vm.memory_bytes) : "—"}</td>`;
        geoHTML += `<td>${vm.disk_total_bytes ? formatBytes(vm.disk_total_bytes) : "—"}</td>`;
        geoHTML += `</tr>`;
      }
      geoHTML += `</tbody></table>`;
    }

    // LXD table.
    if (geo.lxd_containers && geo.lxd_containers.length) {
      geoHTML += `<table><caption>LXD Containers</caption><thead><tr>`;
      geoHTML += `<th>Host</th><th>Name</th><th>IPs</th><th>Description</th><th>OS/Image</th><th>CPU</th><th>RAM</th><th>Disk</th>`;
      geoHTML += `</tr></thead><tbody>`;
      for (const ct of geo.lxd_containers) {
        if (searchTerm && !resourceMatches(ct, "lxd", searchTerm)) continue;
        const ips = (ct.ips && ct.ips.length) ? ct.ips.join(", ") : "—";
        geoHTML += `<tr>`;
        geoHTML += `<td>${esc(ct.host_id)}</td>`;
        geoHTML += `<td>${esc(ct.name)}${ct.last_seen ? ' <span class="last-seen">(last seen ' + timeAgo(ct.last_seen) + ')</span>' : ''}</td>`;
        geoHTML += `<td class="mono">${esc(ips)}</td>`;
        geoHTML += `<td>${esc(ct.description) || "—"}</td>`;
        geoHTML += `<td>${esc(ct.guest_os) || "—"}</td>`;
        geoHTML += `<td>${ct.cpu_count || "—"} (${esc(ct.capacity_source_cpu)})</td>`;
        geoHTML += `<td>${ct.memory_bytes ? formatBytes(ct.memory_bytes) : "—"} (${esc(ct.capacity_source_ram)})</td>`;
        geoHTML += `<td>${ct.root_disk_bytes ? formatBytes(ct.root_disk_bytes) : "—"} (${esc(ct.capacity_source_disk)})</td>`;
        geoHTML += `</tr>`;
      }
      geoHTML += `</tbody></table>`;
    }

    geoHTML += `</section>`; // geo-section
  }

  container.innerHTML = geoHTML;
  geoNav.innerHTML = geoNavHTML;
  hostNav.innerHTML = hostNavHTML;

  if (searchTerm && geoHTML === "") {
    document.getElementById("no-results").hidden = false;
  }
}

// --- Search ---

function onSearch(e) {
  searchTerm = e.target.value.trim().toLowerCase();
  if (currentData) render(currentData);
}

function geoMatches(geo, term) {
  if (geo.name.toLowerCase().includes(term)) return true;
  for (const host of geo.hosts) {
    if (hostMatches(host, geo, term)) return true;
  }
  for (const vm of geo.virtual_machines || []) {
    if (resourceMatches(vm, "vm", term)) return true;
  }
  for (const ct of geo.lxd_containers || []) {
    if (resourceMatches(ct, "lxd", term)) return true;
  }
  return false;
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

function formatBytes(bytes) {
  if (!bytes || bytes === 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  const val = bytes / Math.pow(1024, i);
  return `${val.toFixed(i > 0 ? 1 : 0)} ${units[i]}`;
}

function debounce(fn, delay) {
  let timer;
  return (...args) => {
    clearTimeout(timer);
    timer = setTimeout(() => fn(...args), delay);
  };
}
