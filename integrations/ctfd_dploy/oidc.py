# Copyright the Dploy authors.
# SPDX-License-Identifier: MIT
"""
Token broker for the Dploy CTFd plugin.

CTFd is itself the OIDC Provider of Dploy (via the companion
`ctfd-oidc-provider` plugin), so the only thing the proxy needs in order to call
the Dploy API on a user's behalf is a CTFd-issued ``id_token`` for that user.

We obtain it with a *standard* Authorization-Code + PKCE flow against CTFd's own
``/oauth/authorize`` and ``/oauth/token`` endpoints. Because the OAuth client is
registered as **trusted**, a logged-in user hitting ``/oauth/authorize`` is
auto-approved and bounced straight back with a code — no consent screen — so the
round-trip is effectively invisible. The resulting ``id_token`` is cached in the
*server-side* Flask session and never exposed to the browser.

The audience (``aud``) of the token equals the configured client id, so it must
match Dploy's ``JWT_AUDIENCE`` (default ``dploy``); the username comes from the
``name`` claim, so the ``profile`` scope must be requested (Dploy's default
``JWT_USERNAME_CLAIM=name``). Get those two right and, as intended, no auth
problem can appear.
"""

import base64
import hashlib
import json
import os
import secrets
import time
from urllib.parse import urlencode

import requests
from flask import current_app, request, session, url_for

# --- Configuration ----------------------------------------------------------
#
# Every value is read at call time so it can come from an environment variable
# *or* the CTFd app config (``app.config[...]``), with env taking precedence.

_DISCOVERY_CACHE = {}
_DISCOVERY_TTL = 600  # seconds

SESSION_KEY = "dploy_oidc"          # holds {"id_token", "exp"}
SESSION_FLOW = "dploy_oidc_flow"    # holds {"state", "verifier", "next", "popup"}


def cfg(name, default=None):
    """Look up a setting from the environment first, then CTFd app config."""
    val = os.environ.get(name)
    if val is not None and val != "":
        return val
    try:
        val = current_app.config.get(name)
    except Exception:
        val = None
    return val if val not in (None, "") else default


def issuer():
    """
    OIDC issuer to talk to. Defaults to the CTFd provider's own issuer
    (``OIDC_PROVIDER_ISSUER``), falling back to this request's host so a
    single-host dev setup works with zero config.
    """
    iss = cfg("DPLOY_OIDC_ISSUER") or cfg("OIDC_PROVIDER_ISSUER")
    if iss:
        return iss.rstrip("/")
    return request.url_root.rstrip("/")


def client_id():
    # Must match Dploy's JWT_AUDIENCE (default "dploy") so the token's `aud`
    # passes Dploy's audience check.
    return cfg("DPLOY_OIDC_CLIENT_ID", "dploy")


def client_secret():
    return cfg("DPLOY_OIDC_CLIENT_SECRET", "")


def scopes():
    # `profile` is required so the token carries the `name` claim that Dploy
    # uses as the owner key (JWT_USERNAME_CLAIM=name).
    return cfg("DPLOY_OIDC_SCOPES", "openid profile email")


def redirect_uri():
    explicit = cfg("DPLOY_OIDC_REDIRECT_URI")
    if explicit:
        return explicit
    # Derived from the running CTFd host; must be registered as a redirect URI
    # on the OAuth client.
    return url_for("dploy.auth_callback", _external=True)


# --- Discovery --------------------------------------------------------------

def discovery():
    """Fetch and cache the issuer's OpenID discovery document."""
    iss = issuer()
    cached = _DISCOVERY_CACHE.get(iss)
    now = time.time()
    if cached and cached["fetched"] + _DISCOVERY_TTL > now:
        return cached["doc"]
    url = iss + "/.well-known/openid-configuration"
    resp = requests.get(url, timeout=10)
    resp.raise_for_status()
    doc = resp.json()
    _DISCOVERY_CACHE[iss] = {"doc": doc, "fetched": now}
    return doc


# --- PKCE helpers -----------------------------------------------------------

def _b64url(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode("ascii")


def _new_pkce():
    verifier = _b64url(secrets.token_bytes(40))
    challenge = _b64url(hashlib.sha256(verifier.encode("ascii")).digest())
    return verifier, challenge


# --- Flow -------------------------------------------------------------------

def begin_login(next_url="/challenges", popup=False):
    """
    Stash flow state in the session and return the provider authorize URL the
    browser should be sent to.
    """
    verifier, challenge = _new_pkce()
    state = secrets.token_urlsafe(24)
    session[SESSION_FLOW] = {
        "state": state,
        "verifier": verifier,
        "next": next_url,
        "popup": bool(popup),
    }
    params = {
        "response_type": "code",
        "client_id": client_id(),
        "redirect_uri": redirect_uri(),
        "scope": scopes(),
        "state": state,
        "code_challenge": challenge,
        "code_challenge_method": "S256",
    }
    return discovery()["authorization_endpoint"] + "?" + urlencode(params)


def complete_login(req):
    """
    Handle the provider callback: validate state, exchange the code for tokens,
    and cache the ``id_token`` in the session. Returns the stored flow dict
    (so the caller knows where to redirect / whether it was a popup).
    """
    flow = session.pop(SESSION_FLOW, None)
    if not flow:
        raise ValueError("no in-progress Dploy login (missing session state)")

    if req.args.get("error"):
        raise ValueError(
            "identity provider rejected the login: %s" % req.args.get("error")
        )

    if not secrets.compare_digest(req.args.get("state", ""), flow["state"]):
        raise ValueError("state mismatch")

    code = req.args.get("code")
    if not code:
        raise ValueError("missing authorization code")

    token_endpoint = discovery()["token_endpoint"]
    data = {
        "grant_type": "authorization_code",
        "code": code,
        "redirect_uri": redirect_uri(),
        "client_id": client_id(),
        "code_verifier": flow["verifier"],
    }
    auth = None
    secret = client_secret()
    if secret:
        # Confidential client → client_secret_basic.
        auth = (client_id(), secret)
    resp = requests.post(token_endpoint, data=data, auth=auth, timeout=15)
    if resp.status_code != 200:
        raise ValueError("token exchange failed: %s %s" % (resp.status_code, resp.text[:200]))
    payload = resp.json()
    id_token = payload.get("id_token")
    if not id_token:
        raise ValueError("token response had no id_token")

    session[SESSION_KEY] = {"id_token": id_token, "exp": _jwt_exp(id_token)}
    return flow


def current_token():
    """Return a still-valid cached ``id_token`` for this session, or ``None``."""
    data = session.get(SESSION_KEY)
    if not data:
        return None
    # 30 s skew so we don't hand Dploy a token that expires mid-request.
    if data.get("exp", 0) <= time.time() + 30:
        session.pop(SESSION_KEY, None)
        return None
    return data["id_token"]


def clear_token():
    session.pop(SESSION_KEY, None)


def _jwt_exp(token):
    """Read the ``exp`` claim from a JWT without verifying it (we trust our own
    session storage; verification is Dploy's job)."""
    try:
        payload = token.split(".")[1]
        payload += "=" * (-len(payload) % 4)
        claims = json.loads(base64.urlsafe_b64decode(payload))
        return int(claims.get("exp", 0))
    except Exception:
        return 0
