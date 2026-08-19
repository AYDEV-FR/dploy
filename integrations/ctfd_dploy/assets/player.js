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

  // Polling cadence. Binding a warm pool member takes ~20ms on the operator
  // side, but the claim carries no status yet when POST /run returns — so a
  // flat 3s tick made an instant environment look like a three second one.
  // Being eager only just after an action costs a handful of requests and
  // covers the one moment a player is actually watching the panel.
  var POLL_EAGER = 200;    // just after a click
  var EAGER_FOR = 4000;    // how long that lasts
  var POLL_MOVING = 3000;  // converging on its own (an on-demand install)
  var POLL_SETTLED = 12000; // up and serving: only the countdown moves

  var timer = null;
  var renderedForId = null;
  var eagerUntil = 0;
  var lastHTML = null;

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

  function render(s) {
    if (s.error) {
      return card(
        '<div class="text-danger mb-0">' + esc(s.error) + "</div>"
      );
    }

    // The link is gated on `ready`, not on having a URL. A URL exists as soon
    // as an instance does, because it is rendered from the template — for an
    // on-demand instance that is a Helm install before anything answers on it,
    // and handing it over early gives the player a tab that cannot connect.
    if (s.phase === "Bound" && s.ready && (s.url || s.connectionMessage)) {
      return card(connection(s) + actions(s));
    }

    if (s.instancePhase === "Failed") {
      return card(
        '<div class="text-danger mb-2">Provisioning failed.' +
        (s.health ? " (" + esc(s.health) + ")" : "") + "</div>" +
        '<button type="button" class="btn btn-outline-danger btn-sm" data-dploy="stop">Stop</button>'
      );
    }

    if (s.phase === "Bound" || s.phase === "Pending") {
      // Pending covers "queued for a warm pool member"; Bound-but-not-ready
      // covers "the instance exists and Flux is still installing it".
      var note =
        s.phase === "Pending"
          ? s.message || "Waiting for a free environment…"
          : "Provisioning your environment…";
      return card(
        '<div class="d-flex align-items-center">' +
        '<div class="spinner-border spinner-border-sm me-2" role="status"></div>' +
        "<span>" + esc(note) + "</span>" +
        "</div>" +
        '<div class="mt-2 text-end">' +
        '<button type="button" class="btn btn-outline-danger btn-sm" data-dploy="stop">Cancel</button>' +
        "</div>"
      );
    }

    if (s.phase === "Rejected") {
      return card(
        '<div class="text-warning mb-2">' +
        esc(s.message || "Your request was rejected.") +
        "</div>" +
        '<button type="button" class="btn btn-primary btn-sm" data-dploy="run">Try again</button>'
      );
    }

    // NotStarted, or Expired — both mean "there is nothing running, offer the
    // button". Running past an Expired claim replaces it server-side.
    var label = s.phase === "Expired" ? "Run Instance again" : "Run Instance";
    return card(
      '<button type="button" class="btn btn-primary" data-dploy="run">' + label + "</button>" +
      (s.phase === "Expired"
        ? '<div class="text-muted small mt-2">Your previous environment expired.</div>'
        : "")
    );
  }

  // Still going somewhere: queued for a warm member, or holding an instance
  // that is not answering yet. Keyed on `ready` for the same reason the link
  // is — a URL is not a running environment.
  function moving(s) {
    if (s.instancePhase === "Failed") return false;
    return s.phase === "Pending" || (s.phase === "Bound" && !s.ready);
  }

  // How long to wait before asking again. Eager right after an action, then
  // back to a cadence matched to what is actually still changing.
  function nextDelay(s) {
    if (Date.now() < eagerUntil) return POLL_EAGER;
    return moving(s) ? POLL_MOVING : POLL_SETTLED;
  }

  function paint(id, s) {
    if (!modalIsOpen()) { stopPolling(); return; }
    var panel = ensurePanel();
    if (!panel) return;

    // Only touch the DOM when the markup actually changes. Rewriting innerHTML
    // on every tick destroys and recreates the buttons underneath the cursor,
    // which reads as a flicker — at 200ms after a click, five times a second.
    // Most ticks produce identical markup (the countdown only moves once a
    // minute), so this leaves the panel alone almost always.
    var html = render(s);
    if (html !== lastHTML || !panel.firstChild) {
      panel.innerHTML = html;
      lastHTML = html;
      // Listeners die with the nodes they were attached to, so they are only
      // re-bound when the nodes were actually replaced.
      wire(panel, id);
    }
    stopPolling();
    if (moving(s) || s.phase === "Bound") {
      // Keep polling while it converges, and slowly once it's up so the
      // countdown and an expiry that lands mid-session stay honest.
      timer = setTimeout(function () { poll(id); }, nextDelay(s));
    }
  }

  function wire(panel, id) {
    panel.querySelectorAll("[data-dploy]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var action = btn.getAttribute("data-dploy");
        btn.disabled = true;
        // The response already carries the claim, but a claim filed a
        // millisecond ago has no status yet — the answer lands right after it.
        eagerUntil = Date.now() + EAGER_FOR;
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
        // A failed request is not a reason to hammer: back off to the slow
        // cadence rather than retrying eagerly at 200ms.
        timer = setTimeout(function () { poll(id); }, POLL_MOVING);
      });
  }

  // --- lifecycle ------------------------------------------------------------

  function refresh() {
    if (!modalIsOpen()) {
      renderedForId = null;
      lastHTML = null;
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
        lastHTML = null;
        hideConnectionInfo();
        if (s.error) {
          // render() takes the state and returns markup — passing the panel to
          // it left the error unrendered and stopped the poll, so a
          // misconfigured challenge showed the player an empty box.
          var panel = ensurePanel();
          if (panel) panel.innerHTML = render(s);
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
