// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT
//
// Player-side panel injected into the CTFd challenge modal for any challenge
// tagged `dploy:<template>`.
//
// The panel is the whole flow — deploy, wait, connect, extend, stop — because
// a DployInstanceClaim projects the instance's entire state onto itself: one
// poll of /status answers "is it up, where is it, when does it die". There is
// no dploy API in the loop; the plugin writes the claim straight to the
// cluster and CTFd's session is the identity.
//
// Design notes:
//   - One /info fetch per modal open; /status polls only while it is open and
//     the claim is still moving.
//   - The Alpine view DOM is recreated on every open, so the panel is rebuilt
//     rather than cached.
(function () {
  "use strict";

  var PANEL_ID = "dploy-modal-panel";
  var BASE = "/plugins/ctfd_dploy";
  var POLL_MS = 3000;

  var timer = null;
  var renderedForId = null;

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

  function get(path) {
    return fetch(path, {
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    }).then(function (r) { return r.json(); });
  }

  function post(path, body) {
    return fetch(path, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "CSRF-Token": csrf() },
      body: JSON.stringify(body || {}),
    }).then(function (r) { return r.json(); });
  }

  // --- modal plumbing -------------------------------------------------------

  function modalEl() {
    return document.getElementById("challenge-window");
  }
  function modalIsOpen() {
    var m = modalEl();
    return !!(m && m.classList.contains("show"));
  }
  function challengeRoot() {
    var m = modalEl();
    return m ? m.querySelector('[x-data="Challenge"]') : null;
  }
  function injectionAnchor() {
    var root = challengeRoot();
    if (!root) return null;
    return (
      root.querySelector("#challenge") ||
      root.querySelector(".tab-pane") ||
      root.querySelector(".modal-body") ||
      root
    );
  }
  function extractChallengeId() {
    var root = challengeRoot();
    if (root) {
      var xi = root.getAttribute("x-init") || "";
      var m = xi.match(/\bid\s*=\s*(\d+)/);
      if (m) return parseInt(m[1], 10);
    }
    try {
      var st = window.Alpine && window.Alpine.store && window.Alpine.store("challenge");
      var did = st && st.data && st.data.id;
      if (did) return parseInt(did, 10);
    } catch (e) { /* ignore */ }
    return null;
  }

  function ensurePanel() {
    var anchor = injectionAnchor();
    if (!anchor) return null;
    var p = anchor.querySelector(":scope > #" + PANEL_ID);
    if (p) return p;
    p = document.createElement("div");
    p.id = PANEL_ID;
    p.className = "mt-3";
    anchor.appendChild(p);
    return p;
  }

  function removePanel() {
    var root = challengeRoot();
    if (!root) return;
    var p = root.querySelector("#" + PANEL_ID);
    if (p) p.remove();
  }

  // The theme prints Connection Information verbatim, which for a dploy
  // challenge is the request JSON — noise to the player, and the panel below it
  // is the real answer to "how do I reach this". Hide the block the theme wraps
  // it in (span.challenge-connection-info inside its own div).
  function hideConnectionInfo() {
    var root = challengeRoot();
    if (!root) return;
    var span = root.querySelector(".challenge-connection-info");
    if (!span) return;
    var block = span.closest("div") || span;
    block.style.display = "none";
  }

  function stopPolling() {
    if (timer) { clearTimeout(timer); timer = null; }
  }

  // --- rendering ------------------------------------------------------------

  function countdown(expiresAt) {
    if (!expiresAt) return "";
    var left = Math.floor((new Date(expiresAt).getTime() - Date.now()) / 1000);
    if (isNaN(left)) return "";
    if (left <= 0) return "expiring";
    var h = Math.floor(left / 3600);
    var m = Math.floor((left % 3600) / 60);
    return h > 0 ? h + "h " + m + "m left" : m + "m left";
  }

  function card(inner) {
    return '<div class="card"><div class="card-body">' + inner + "</div></div>";
  }

  function actions(s) {
    var buttons = "";
    if (s.canExtend) {
      buttons +=
        '<button type="button" class="btn btn-outline-secondary btn-sm" data-dploy="extend">' +
        "Extend</button> ";
    }
    buttons +=
      '<button type="button" class="btn btn-outline-danger btn-sm" data-dploy="stop">' +
      "Stop</button>";
    var left = countdown(s.expiresAt);
    return (
      '<div class="d-flex justify-content-between align-items-center mt-3">' +
      '<small class="text-muted">' + esc(left) + "</small>" +
      "<div>" + buttons + "</div>" +
      "</div>"
    );
  }

  function connection(s) {
    if (s.connectionType === "instructions") {
      return (
        '<div class="text-muted small mb-1">Connect with:</div>' +
        '<pre class="mb-0" style="white-space:pre-wrap;word-break:break-all;">' +
        "<code>" + esc(s.connectionMessage || s.url) + "</code></pre>"
      );
    }
    var href = s.url || "";
    if (href && href.indexOf("://") === -1) href = "https://" + href;
    return (
      '<a class="btn btn-success" target="_blank" rel="noopener" href="' + esc(href) + '">' +
      "Open your environment</a>"
    );
  }

  function render(panel, s) {
    if (s.error) {
      panel.innerHTML = card(
        '<div class="text-danger mb-0">' + esc(s.error) + "</div>"
      );
      return;
    }

    if (s.phase === "Bound" && (s.url || s.connectionMessage)) {
      panel.innerHTML = card(connection(s) + actions(s));
      return;
    }

    if (s.phase === "Bound" || s.phase === "Pending") {
      // Pending covers "queued for a warm pool member"; Bound-without-a-URL
      // covers "the instance exists but Flux is still converging".
      var note =
        s.phase === "Pending"
          ? s.message || "Waiting for a free environment…"
          : "Provisioning your environment…";
      panel.innerHTML = card(
        '<div class="d-flex align-items-center">' +
        '<div class="spinner-border spinner-border-sm me-2" role="status"></div>' +
        "<span>" + esc(note) + "</span>" +
        "</div>" +
        '<div class="mt-2 text-end">' +
        '<button type="button" class="btn btn-outline-danger btn-sm" data-dploy="stop">Cancel</button>' +
        "</div>"
      );
      return;
    }

    if (s.phase === "Rejected") {
      panel.innerHTML = card(
        '<div class="text-warning mb-2">' +
        esc(s.message || "Your request was rejected.") +
        "</div>" +
        '<button type="button" class="btn btn-primary btn-sm" data-dploy="run">Try again</button>'
      );
      return;
    }

    // NotStarted, or Expired — both mean "there is nothing running, offer the
    // button". Running past an Expired claim replaces it server-side.
    var label = s.phase === "Expired" ? "Run Instance again" : "Run Instance";
    panel.innerHTML = card(
      '<button type="button" class="btn btn-primary" data-dploy="run">' + label + "</button>" +
      (s.phase === "Expired"
        ? '<div class="text-muted small mt-2">Your previous environment expired.</div>'
        : "")
    );
  }

  function moving(s) {
    return s.phase === "Pending" || (s.phase === "Bound" && !s.url && !s.connectionMessage);
  }

  function paint(id, s) {
    if (!modalIsOpen()) { stopPolling(); return; }
    var panel = ensurePanel();
    if (!panel) return;
    render(panel, s);
    wire(panel, id);
    stopPolling();
    if (moving(s) || s.phase === "Bound") {
      // Keep polling while it converges, and slowly once it's up so the
      // countdown and an expiry that lands mid-session stay honest.
      timer = setTimeout(function () { poll(id); }, moving(s) ? POLL_MS : POLL_MS * 4);
    }
  }

  function wire(panel, id) {
    panel.querySelectorAll("[data-dploy]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var action = btn.getAttribute("data-dploy");
        btn.disabled = true;
        post(BASE + "/" + action, { challenge_id: id })
          .then(function (s) { paint(id, s); })
          .catch(function () { poll(id); });
      });
    });
  }

  function poll(id) {
    get(BASE + "/status?challenge_id=" + id)
      .then(function (s) { paint(id, s); })
      .catch(function () {
        timer = setTimeout(function () { poll(id); }, POLL_MS);
      });
  }

  // --- lifecycle ------------------------------------------------------------

  function refresh() {
    if (!modalIsOpen()) {
      renderedForId = null;
      stopPolling();
      return;
    }
    var id = extractChallengeId();
    if (!id) return;
    if (renderedForId === id && document.getElementById(PANEL_ID)) return;

    get(BASE + "/info?challenge_id=" + id)
      .then(function (s) {
        if (!modalIsOpen()) return;
        if (!s || !s.enabled) { removePanel(); return; }
        renderedForId = id;
        hideConnectionInfo();
        if (s.error) {
          var panel = ensurePanel();
          if (panel) render(panel, s);
          return;
        }
        poll(id);
      })
      .catch(function () { /* transient */ });
  }

  function attach() {
    var win = modalEl();
    if (!win) return;
    var obs = new MutationObserver(function () { setTimeout(refresh, 0); });
    obs.observe(win, {
      childList: true,
      subtree: true,
      attributes: true,
      attributeFilter: ["class"],
    });
    window.addEventListener("hashchange", function () {
      renderedForId = null;
      stopPolling();
      setTimeout(refresh, 0);
    });
    setTimeout(refresh, 0);
  }

  if (document.readyState !== "loading") attach();
  else document.addEventListener("DOMContentLoaded", attach);
})();
