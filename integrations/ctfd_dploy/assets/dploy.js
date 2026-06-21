// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT
//
// Injected on every CTFd page. When a challenge whose connection-info is a Dploy
// run URL (e.g. https://dploy.example.com/run/podinfo) is opened, this script
// adds Start / Status / Extend / Stop controls to the challenge modal and wires
// them to the plugin's same-origin proxy. No Dploy token ever lives in the page.
(function () {
  "use strict";

  var PLUGIN = "/plugins/ctfd_dploy";
  var pattern = null; // RegExp, loaded from the backend so both sides agree.

  // ---- helpers -------------------------------------------------------------

  function csrf() {
    try {
      if (window.CTFd && CTFd.config && CTFd.config.csrfNonce) return CTFd.config.csrfNonce;
    } catch (e) {}
    return window.csrfNonce || (window.init && window.init.csrfNonce) || "";
  }

  function api(method, url) {
    return fetch(url, {
      method: method,
      credentials: "same-origin",
      headers: { "Accept": "application/json", "CSRF-Token": csrf() },
    }).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (body) {
        return { status: r.status, body: body };
      });
    });
  }

  // Run the trusted (auto-approved) OIDC popup so the backend can mint a Dploy
  // token for this user, then resolve. Falls back to a full-page redirect when
  // popups are blocked.
  function ensureAuth(loginUrl) {
    return new Promise(function (resolve, reject) {
      var win = window.open(loginUrl, "dploy-auth", "width=480,height=640");
      if (!win) {
        window.location = loginUrl.replace("popup=1", "next=" + encodeURIComponent(location.pathname));
        return; // navigating away
      }
      var done = false;
      function onMsg(ev) {
        if (ev.origin !== window.location.origin) return;
        if (!ev.data || ev.data.type !== "dploy-auth") return;
        done = true;
        window.removeEventListener("message", onMsg);
        ev.data.ok ? resolve() : reject(new Error("Dploy sign-in was cancelled"));
      }
      window.addEventListener("message", onMsg);
      var poll = setInterval(function () {
        if (win.closed) {
          clearInterval(poll);
          window.removeEventListener("message", onMsg);
          if (!done) reject(new Error("Dploy sign-in window closed"));
        }
      }, 500);
    });
  }

  // Call a proxy action, transparently handling the one-time auth bounce.
  function action(cid, verb, method, retried) {
    return api(method, PLUGIN + "/challenges/" + cid + "/" + verb).then(function (res) {
      if (res.status === 401 && res.body && res.body.error === "auth_required" && !retried) {
        return ensureAuth(res.body.login_url).then(function () {
          return action(cid, verb, method, true);
        });
      }
      if (!res.body || res.body.success !== true) {
        var msg = (res.body && res.body.error) || ("HTTP " + res.status);
        throw new Error(msg);
      }
      return res.body.data || {};
    });
  }

  // ---- panel rendering -----------------------------------------------------

  function fmtExpiry(iso) {
    if (!iso) return "no expiry";
    var d = new Date(iso);
    if (isNaN(d)) return iso;
    var mins = Math.round((d - new Date()) / 60000);
    if (mins <= 0) return "expired";
    if (mins < 60) return "expires in " + mins + " min";
    return "expires in " + Math.round(mins / 60) + "h " + (mins % 60) + "m";
  }

  function renderState(panel, data) {
    var status = (data && (data.status || data.Status)) || "";
    var url = (data && (data.url || data.URL)) || "";
    var exp = (data && (data.expiresAt || data.ExpiresAt)) || "";
    var msg = (data && (data.connectionMessage || data.ConnectionMessage)) || "";
    var running = !!status && status.toLowerCase() !== "pending" && status !== "Deleting";

    var bits = [];
    if (status) bits.push('<span class="dploy-badge">' + escapeHtml(status) + "</span>");
    if (running) bits.push('<span class="dploy-exp">' + escapeHtml(fmtExpiry(exp)) + "</span>");
    panel.querySelector(".dploy-state").innerHTML = bits.join(" ") || "Not running";

    var link = panel.querySelector(".dploy-link");
    if (url) {
      link.innerHTML = '<a href="' + escapeAttr(url) + '" target="_blank" rel="noopener">' +
        escapeHtml(url) + "</a>";
    } else if (msg) {
      link.textContent = msg;
    } else {
      link.textContent = "";
    }

    panel.querySelector('[data-act="start"]').disabled = running;
    panel.querySelector('[data-act="extend"]').disabled = !running;
    panel.querySelector('[data-act="stop"]').disabled = !running;
  }

  function setBusy(panel, busy, note) {
    panel.classList.toggle("dploy-busy", !!busy);
    if (note !== undefined) panel.querySelector(".dploy-msg").textContent = note || "";
  }

  function buildPanel(cid) {
    var el = document.createElement("div");
    el.className = "dploy-panel";
    el.setAttribute("data-dploy-cid", String(cid));
    el.innerHTML =
      '<div class="dploy-head">Environment</div>' +
      '<div class="dploy-state">…</div>' +
      '<div class="dploy-link"></div>' +
      '<div class="dploy-actions">' +
      '  <button type="button" class="dploy-btn dploy-start" data-act="start">Start</button>' +
      '  <button type="button" class="dploy-btn" data-act="status">Refresh</button>' +
      '  <button type="button" class="dploy-btn" data-act="extend">Extend</button>' +
      '  <button type="button" class="dploy-btn dploy-stop" data-act="stop">Stop</button>' +
      "</div>" +
      '<div class="dploy-msg"></div>';

    var verbs = {
      start: { method: "POST", note: "Starting environment…" },
      status: { method: "GET", note: "" },
      extend: { method: "POST", note: "Extending…" },
      stop: { method: "POST", note: "Stopping…" },
    };

    el.querySelectorAll("[data-act]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var act = btn.getAttribute("data-act");
        var spec = verbs[act];
        setBusy(el, true, spec.note);
        action(cid, act, spec.method)
          .then(function (data) {
            if (act === "stop") {
              renderState(el, {});
              setBusy(el, false, "Environment stopped.");
            } else {
              renderState(el, data);
              setBusy(el, false, act === "extend" ? "Extended." : "");
            }
          })
          .catch(function (err) {
            setBusy(el, false, "Error: " + err.message);
          });
      });
    });
    return el;
  }

  function refresh(panel, cid) {
    setBusy(panel, true, "");
    action(cid, "status", "GET")
      .then(function (data) { renderState(panel, data); setBusy(panel, false); })
      .catch(function (err) {
        // 404 / not-running is normal — show the idle state, surface real errors.
        renderState(panel, {});
        setBusy(panel, false, /not running|not found|404|unknown/i.test(err.message) ? "" : err.message);
      });
  }

  // ---- modal detection -----------------------------------------------------

  // The currently-open challenge object CTFd stashes while rendering the modal.
  function currentChallenge() {
    try { return window.CTFd._internal.challenge.data || null; } catch (e) { return null; }
  }

  function findModalBody() {
    // The visible challenge modal varies by theme; try the common anchors.
    var sel = [
      ".modal.show .modal-body",
      "#challenge-window .modal-body",
      "#challenge-window",
      ".challenge-desc",
    ];
    for (var i = 0; i < sel.length; i++) {
      var nodes = document.querySelectorAll(sel[i]);
      for (var j = 0; j < nodes.length; j++) {
        if (nodes[j].offsetParent !== null) return nodes[j];
      }
    }
    return null;
  }

  function maybeDecorate() {
    if (!pattern) return;
    var chal = currentChallenge();
    if (!chal || !chal.id) return;
    var ci = chal.connection_info || chal.connectionInfo || "";
    if (!ci || !pattern.test(ci)) return;

    var body = findModalBody();
    if (!body) return;

    var existing = body.querySelector(".dploy-panel");
    if (existing) {
      if (existing.getAttribute("data-dploy-cid") === String(chal.id)) return; // already done
      existing.remove(); // a different challenge reused the modal
    }
    var panel = buildPanel(chal.id);
    body.appendChild(panel);
    refresh(panel, chal.id);
  }

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }
  function escapeAttr(s) { return escapeHtml(s); }

  // ---- bootstrap -----------------------------------------------------------

  function start() {
    fetch(PLUGIN + "/config", { credentials: "same-origin" })
      .then(function (r) { return r.json(); })
      .then(function (cfg) {
        try { pattern = new RegExp(cfg.pattern); } catch (e) { pattern = null; }
        var obs = new MutationObserver(function () { maybeDecorate(); });
        obs.observe(document.body, { childList: true, subtree: true });
        maybeDecorate();
      })
      .catch(function () { /* config unavailable; stay inert */ });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start);
  } else {
    start();
  }
})();
