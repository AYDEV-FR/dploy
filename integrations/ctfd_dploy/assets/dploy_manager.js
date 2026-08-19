// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT
//
// Drives the Admin → Dploy page. Both tables come from the plugin's own
// admin endpoints, which read the DployInstanceClaim and DployTemplate CRs
// directly — there is no dploy API in the path, so nothing here needs a token
// or a base URL, only the RBAC granted to CTFd's service account.
(function () {
  "use strict";

  var BASE = "/admin/plugins/ctfd_dploy";

  function esc(s) {
    return String(s == null ? "" : s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }
  function csrf() {
    try {
      return (window.init && window.init.csrfNonce) || "";
    } catch (e) {
      return "";
    }
  }
  function get(url) {
    return fetch(url, { credentials: "same-origin", headers: { Accept: "application/json" } })
      .then(function (r) {
        return r.json().catch(function () { return {}; })
          .then(function (b) {
            if (r.status >= 400 || b.error) throw new Error(b.error || "HTTP " + r.status);
            return b;
          });
      });
  }
  function showError(msg) {
    var e = document.getElementById("dploy-error");
    if (!e) return;
    if (!msg) { e.classList.add("d-none"); e.textContent = ""; return; }
    e.textContent = msg;
    e.classList.remove("d-none");
  }
  function fmt(iso) {
    if (!iso) return "—";
    var d = new Date(iso);
    return isNaN(d) ? iso : d.toLocaleString();
  }
  function link(u) {
    if (!u) return "—";
    var href = u.indexOf("://") === -1 ? "https://" + u : u;
    return '<a href="' + esc(href) + '" target="_blank" rel="noopener">' + esc(u) + "</a>";
  }
  function yn(b) { return b ? "yes" : "no"; }

  // A claim carries its own explanation for anything that isn't Bound — pool
  // exhaustion, a quota rejection, a disabled template — so the phase cell
  // shows the operator's reason rather than making the admin go read events.
  function phaseCell(c) {
    var badge = { Bound: "success", Pending: "warning", Rejected: "danger", Expired: "secondary" };
    var html = '<span class="badge badge-' + (badge[c.phase] || "secondary") + '">' +
      esc(c.phase) + "</span>";
    if (c.phase !== "Bound" && (c.message || c.reason)) {
      html += '<div class="small text-muted">' + esc(c.message || c.reason) + "</div>";
    } else if (c.instancePhase) {
      html += '<div class="small text-muted">' + esc(c.instancePhase) +
        (c.health ? " · " + esc(c.health) : "") + "</div>";
    }
    return html;
  }

  function loadClaims() {
    return get(BASE + "/claims.json").then(function (data) {
      var rows = data.items || [];
      document.getElementById("dploy-claims-count").textContent = rows.length;
      document.getElementById("dploy-claims-body").innerHTML = rows.map(function (c) {
        return "<tr>" +
          "<td>" + esc(c.name) + "</td>" +
          "<td>" + esc(c.template) + "</td>" +
          "<td>" + esc(c.owner || "—") + "</td>" +
          "<td>" + phaseCell(c) + "</td>" +
          "<td>" + esc(c.instance || "—") + "</td>" +
          "<td>" + link(c.url) + "</td>" +
          "<td>" + (c.ttlSeconds === -1 ? "∞" : fmt(c.expiresAt)) + "</td>" +
          "<td>" + esc(c.createdBy || "—") + "</td>" +
          '<td class="text-right">' +
            '<button type="button" class="btn btn-sm btn-outline-danger" data-delete="' +
              esc(c.name) + '">Delete</button>' +
          "</td>" +
          "</tr>";
      }).join("") ||
        '<tr><td colspan="9" class="text-center text-muted">No claims</td></tr>';
      wireDelete();
    });
  }

  function wireDelete() {
    document.querySelectorAll("[data-delete]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var name = btn.getAttribute("data-delete");
        if (!window.confirm("Delete claim " + name + "? Its environment is torn down.")) return;
        btn.disabled = true;
        fetch(BASE + "/claims/" + encodeURIComponent(name), {
          method: "DELETE",
          credentials: "same-origin",
          // CTFd only honours the CSRF-Token header on JSON requests; without
          // the content type it falls back to looking for a nonce form field
          // and rejects the call, so the button silently 403s.
          headers: { "CSRF-Token": csrf(), "Content-Type": "application/json" },
        }).then(loadAll).catch(function (e) { showError(e.message); });
      });
    });
  }

  function loadTemplates() {
    return get(BASE + "/templates.json").then(function (data) {
      var rows = data.items || [];
      document.getElementById("dploy-templates-count").textContent = rows.length;
      document.getElementById("dploy-templates-body").innerHTML = rows.map(function (t) {
        var pool = t.method === "pool"
          ? (t.poolAvailable + "/" + t.poolClaimed + "/" + t.poolTotal) : "—";
        return "<tr>" +
          "<td>" + esc(t.name) + "</td>" +
          "<td><code>" + esc(t.connectionInfo) + "</code></td>" +
          "<td>" + esc(t.displayName || "—") + "</td>" +
          "<td>" + esc(t.method) + "</td>" +
          "<td>" + yn(t.enabled) + "</td>" +
          "<td>" + esc(pool) + "</td>" +
          "<td>" + esc(t.connectionType) + "</td>" +
          "</tr>";
      }).join("") ||
        '<tr><td colspan="7" class="text-center text-muted">No templates</td></tr>';
    });
  }

  function loadAll() {
    showError("");
    return Promise.all([loadClaims(), loadTemplates()]).catch(function (e) {
      showError("Failed to read the cluster: " + e.message);
    });
  }

  function switchTab(pane) {
    document.querySelectorAll("#dploy-tabs .nav-link").forEach(function (a) {
      a.classList.toggle("active", a.getAttribute("data-pane") === pane);
    });
    document.getElementById("pane-claims").classList.toggle("d-none", pane !== "claims");
    document.getElementById("pane-templates").classList.toggle("d-none", pane !== "templates");
  }

  function init() {
    document.querySelectorAll("#dploy-tabs .nav-link").forEach(function (a) {
      a.addEventListener("click", function (e) {
        e.preventDefault();
        switchTab(a.getAttribute("data-pane"));
      });
    });
    var refresh = document.getElementById("dploy-refresh");
    if (refresh) refresh.addEventListener("click", loadAll);
    loadAll();
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
