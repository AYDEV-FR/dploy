# Copyright the Dploy authors.
# SPDX-License-Identifier: MIT
"""
Dploy plugin for CTFd.

Rather than adding a new challenge type, this plugin treats *any* standard
challenge as a Dploy challenge when its **connection info** is a Dploy run URL,
e.g.::

    https://dploy.example.com/run/podinfo

Such challenges get Start / Status / Extend / Stop controls injected into the
challenge modal. From the connection URL the plugin derives both the Dploy API
base (``https://dploy.example.com``) and the template name (``podinfo``).

Auth model (BFF / Backend-For-Frontend):

  * The browser only ever talks to *this* plugin's same-origin proxy routes,
    gated by CTFd's normal session auth — no Dploy token ever reaches the page.
  * Server-side, the proxy fetches a CTFd-issued id_token for the user via a
    trusted (auto-approved) OIDC auth-code+PKCE bounce against CTFd's own
    provider, caches it in the Flask session, and forwards it to Dploy as a
    Bearer token. See ``oidc.py``.

The connection URL is resolved to (base, template) **server-side** from the
challenge's stored ``connection_info`` — never from a client-supplied value —
so a user cannot point their token at an arbitrary host.

Targets CTFd 3.6.x / 3.7.x with the bundled ``core`` theme. See README.md.
"""

import re
from urllib.parse import urlsplit

from flask import (
    Blueprint, jsonify, redirect, render_template_string, request, session,
)

from CTFd.models import Challenges
from CTFd.plugins import (
    register_plugin_assets_directory,
    register_plugin_script,
    register_plugin_stylesheet,
)
from CTFd.utils.decorators import authed_only

from .dploy_client import DployClient, DployError
from . import oidc

PLUGIN_ID = "ctfd_dploy"
ASSETS = "/plugins/%s/assets/" % PLUGIN_ID

# Default detection pattern, written in JS-regex syntax (named groups `base` and
# `template`) so the same string can drive both the in-browser test and the
# server-side parse. Override with the DPLOY_URL_PATTERN env var / app config.
DEFAULT_PATTERN = r"^(?<base>https?://[^/]+)/run/(?<template>[^/?#\s]+)"


def url_pattern():
    return oidc.cfg("DPLOY_URL_PATTERN", DEFAULT_PATTERN)


def _python_regex():
    # Python's `re` spells named groups `(?P<name>...)`, JS spells them
    # `(?<name>...)`. Translate so admins can configure one pattern.
    return re.compile(url_pattern().replace("(?<", "(?P<"))


def parse_connection_info(connection_info):
    """Return ``(base_url, template)`` for a Dploy connection URL, else ``None``.

    ``base`` defaults to scheme+host of the matched URL but a custom pattern may
    capture it explicitly; ``template`` is required.
    """
    if not connection_info:
        return None
    m = _python_regex().search(connection_info.strip())
    if not m:
        return None
    groups = m.groupdict()
    template = (groups.get("template") or "").strip()
    if not template:
        return None
    base = (groups.get("base") or "").strip()
    if not base:
        parts = urlsplit(connection_info.strip())
        if parts.scheme and parts.netloc:
            base = "%s://%s" % (parts.scheme, parts.netloc)
    # An explicit DPLOY_API_URL acts as an allowlist: when set, the derived host
    # must match it, otherwise we refuse to forward the user's token.
    allow = (oidc.cfg("DPLOY_API_URL") or "").strip()
    if allow:
        if urlsplit(base).netloc and urlsplit(base).netloc != urlsplit(allow).netloc:
            return None
        base = allow
    if not base:
        return None
    return base.rstrip("/"), template


# --- Proxy blueprint --------------------------------------------------------

dploy_bp = Blueprint("dploy", __name__)


def _client_or_auth_error(base):
    """Return (client_factory_base, None) when authed, else (None, response)."""
    token = oidc.current_token()
    if not token:
        login_url = "/plugins/%s/auth/login?popup=1" % PLUGIN_ID
        return None, (jsonify({"success": False, "error": "auth_required",
                               "login_url": login_url}), 401)
    return DployClient(base, token), None


def _run(action, cid):
    challenge = Challenges.query.filter_by(id=cid).first()
    if challenge is None:
        return jsonify({"success": False, "error": "unknown challenge"}), 404

    resolved = parse_connection_info(getattr(challenge, "connection_info", None))
    if resolved is None:
        return jsonify({"success": False,
                        "error": "this challenge is not a Dploy challenge"}), 400
    base, template = resolved

    client, err = _client_or_auth_error(base)
    if err is not None:
        return err

    try:
        data = getattr(client, action)(template)
        return jsonify({"success": True, "data": data})
    except DployError as e:
        # A 401 from Dploy means our cached token is no longer accepted; drop it
        # so the next call re-bounces through the provider.
        if e.status == 401:
            oidc.clear_token()
            login_url = "/plugins/%s/auth/login?popup=1" % PLUGIN_ID
            return jsonify({"success": False, "error": "auth_required",
                            "login_url": login_url}), 401
        msg = e.payload.get("error") if isinstance(e.payload, dict) else str(e.payload)
        return jsonify({"success": False, "error": msg, "status": e.status}), e.status
    except Exception as e:  # network / unexpected
        return jsonify({"success": False, "error": "Dploy request failed: %s" % e}), 502


@dploy_bp.route("/plugins/ctfd_dploy/config", methods=["GET"])
def config():
    # Lets the injected script know the detection pattern (kept in one place).
    return jsonify({"pattern": url_pattern()})


@dploy_bp.route("/plugins/ctfd_dploy/challenges/<int:cid>/start", methods=["POST"])
@authed_only
def start(cid):
    return _run("start", cid)


@dploy_bp.route("/plugins/ctfd_dploy/challenges/<int:cid>/status", methods=["GET"])
@authed_only
def status(cid):
    return _run("status", cid)


@dploy_bp.route("/plugins/ctfd_dploy/challenges/<int:cid>/extend", methods=["POST"])
@authed_only
def extend(cid):
    return _run("extend", cid)


@dploy_bp.route("/plugins/ctfd_dploy/challenges/<int:cid>/stop", methods=["POST"])
@authed_only
def stop(cid):
    return _run("stop", cid)


# --- OIDC bounce routes -----------------------------------------------------

@dploy_bp.route("/plugins/ctfd_dploy/auth/login", methods=["GET"])
@authed_only
def auth_login():
    next_url = request.args.get("next", "/challenges")
    popup = request.args.get("popup") == "1"
    try:
        url = oidc.begin_login(next_url=next_url, popup=popup)
    except Exception as e:
        return _auth_result_page(ok=False, popup=popup, next_url=next_url,
                                 message="Could not start Dploy login: %s" % e)
    return redirect(url)


@dploy_bp.route("/plugins/ctfd_dploy/auth/callback", methods=["GET"])
@authed_only
def auth_callback():
    flow = session.get(oidc.SESSION_FLOW) or {}
    popup = bool(flow.get("popup"))
    next_url = flow.get("next", "/challenges")
    try:
        flow = oidc.complete_login(request)
        popup = bool(flow.get("popup"))
        next_url = flow.get("next", "/challenges")
    except Exception as e:
        return _auth_result_page(ok=False, popup=popup, next_url=next_url,
                                 message="Dploy login failed: %s" % e)
    return _auth_result_page(ok=True, popup=popup, next_url=next_url,
                             message="Connected to Dploy.")


# Minimal page shown after the OIDC bounce: in popup mode it notifies the opener
# and closes; otherwise it redirects back to where the user came from.
_AUTH_PAGE = """<!doctype html><html><head><meta charset="utf-8">
<title>Dploy</title></head><body style="font-family:sans-serif;padding:2rem">
<p>{{ message }}</p>
<script>
  var ok = {{ 'true' if ok else 'false' }};
  var popup = {{ 'true' if popup else 'false' }};
  var next = {{ next_url|tojson }};
  if (popup && window.opener) {
    try { window.opener.postMessage(
      {type: "dploy-auth", ok: ok}, window.location.origin); } catch (e) {}
    window.close();
  } else {
    setTimeout(function(){ window.location = next; }, ok ? 300 : 2500);
  }
</script>
</body></html>"""


def _auth_result_page(ok, popup, next_url, message):
    return render_template_string(_AUTH_PAGE, ok=ok, popup=popup,
                                  next_url=next_url, message=message)


# --- Plugin entrypoint ------------------------------------------------------

def load(app):
    register_plugin_assets_directory(app, base_path=ASSETS)
    # Injected into every themed page; it decorates Dploy challenges in-place.
    register_plugin_script(ASSETS + "dploy.js")
    register_plugin_stylesheet(ASSETS + "dploy.css")
    app.register_blueprint(dploy_bp)
