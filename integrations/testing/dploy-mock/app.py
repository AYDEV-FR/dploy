# Copyright the Dploy authors.
# SPDX-License-Identifier: MIT
"""
Mock Dploy API for testing the CTFd plugin end-to-end.

It implements the four `/api/run/{template}` endpoints the plugin calls and,
crucially, validates the Bearer token the same way real Dploy does:

  * RS256 signature against the CTFd provider's JWKS
  * issuer (`iss`) and audience (`aud`) checks
  * owner key derived from the configured username claim (default `name`),
    sanitized identically to Dploy (lowercase; `.`/`@` -> `-`; drop the rest)

Environment state is in-memory, keyed by (owner, template) — enough to exercise
start / status / extend / stop and the TTL/expiry the panel renders. It does NOT
provision anything real; that needs a Kubernetes cluster (`make setup`).
"""

import os
import re
import time
import uuid

import jwt  # PyJWT
from jwt import PyJWKClient
from flask import Flask, jsonify, request

app = Flask(__name__)

JWKS_URL = os.environ["DPLOY_JWKS_URL"]
ISSUER = os.environ.get("DPLOY_JWT_ISSUER") or None
AUDIENCE = os.environ.get("DPLOY_JWT_AUDIENCE") or None
USERNAME_CLAIM = os.environ.get("DPLOY_JWT_USERNAME_CLAIM", "name")
DEFAULT_TTL = int(os.environ.get("DPLOY_TTL_SECONDS", "3600"))
EXTEND_TTL = int(os.environ.get("DPLOY_EXTEND_SECONDS", "3600"))

# Lazily-initialized JWKS client (CTFd may still be starting up).
_jwks = PyJWKClient(JWKS_URL)

# (owner, template) -> {"uuid", "expires"}
_instances = {}


def sanitize(name):
    """Match Dploy's SanitizeUsername: lowercase, '.'/'@' -> '-', keep [a-z0-9-]."""
    name = (name or "").lower().replace(".", "-").replace("@", "-")
    return re.sub(r"[^a-z0-9-]", "", name)


def authed_owner():
    """Return (owner, None) on success or (None, (json, status)) on failure."""
    header = request.headers.get("Authorization", "")
    if not header.startswith("Bearer "):
        return None, (jsonify(error="Missing Authorization header"), 401)
    token = header[len("Bearer "):]
    try:
        signing_key = _jwks.get_signing_key_from_jwt(token)
        claims = jwt.decode(
            token,
            signing_key.key,
            algorithms=["RS256"],
            audience=AUDIENCE,
            issuer=ISSUER,
            options={"verify_aud": AUDIENCE is not None},
        )
    except Exception as e:
        return None, (jsonify(error="token validation failed: %s" % e), 401)
    owner = sanitize(claims.get(USERNAME_CLAIM))
    if not owner:
        return None, (jsonify(error="missing or invalid %s claim" % USERNAME_CLAIM), 403)
    return owner, None


def _iso(ts):
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(ts))


def _response(owner, template, inst):
    short = inst["uuid"].split("-")[0]
    return {
        "uuid": inst["uuid"],
        "status": "Healthy",
        "url": "http://%s-%s.dploy.local" % (owner, short),
        "expiresAt": _iso(inst["expires"]),
        "owner": owner,
        "shared": False,
        "connectionType": "url",
        "connectionMessage": "",
    }


@app.get("/health")
def health():
    return jsonify(status="ok")


@app.get("/api/run/<template>")
def start(template):
    owner, err = authed_owner()
    if err:
        return err
    key = (owner, template)
    inst = _instances.get(key)
    if inst is None:
        inst = {"uuid": str(uuid.uuid4()), "expires": time.time() + DEFAULT_TTL}
        _instances[key] = inst
        app.logger.info("created %s for %s", template, owner)
    return jsonify(_response(owner, template, inst))


@app.get("/api/run/<template>/status")
def status(template):
    owner, err = authed_owner()
    if err:
        return err
    inst = _instances.get((owner, template))
    if inst is None:
        return jsonify(error='environment "%s" not found' % template), 404
    return jsonify(_response(owner, template, inst))


@app.post("/api/run/<template>/extend")
def extend(template):
    owner, err = authed_owner()
    if err:
        return err
    key = (owner, template)
    inst = _instances.get(key)
    if inst is None:
        return jsonify(error='environment "%s" not found' % template), 404
    inst["expires"] = max(inst["expires"], time.time()) + EXTEND_TTL
    return jsonify(expiresAt=_iso(inst["expires"]))


@app.delete("/api/run/<template>")
def stop(template):
    owner, err = authed_owner()
    if err:
        return err
    if _instances.pop((owner, template), None) is None:
        return jsonify(error='environment "%s" not found' % template), 404
    app.logger.info("deleted %s for %s", template, owner)
    return ("", 204)


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=8080)
