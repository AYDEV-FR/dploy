"""HTTP routes for the Dploy CTFd plugin.

Copyright the Dploy authors.
SPDX-License-Identifier: MIT

Player routes act only on the caller's own claim: the owner is derived from the
CTFd session, never from request input. They also carry CTFd's own challenge
gates — visibility, CTF time window, verified email — and resolve the requested
challenge through `_visible_challenge`, because a challenge id *is* request
input and an unreleased challenge must not become a running environment. Admin
routes are `@admins_only`. POSTs and DELETEs are CSRF-checked by CTFd — the
front-end scripts send the `CSRF-Token` header.

A CTFd challenge opts into dploy through its **Connection Information** field,
by putting a JSON object there instead of a connection string:

    {"template": "kali", "ttlSeconds": 1800, "waitForPool": false}

Only `template` is required. The keys are named after DployInstanceClaimSpec
because that is what they are — the request, minus the identity, which comes
from the session. Connection Information is the right field for it: it is the
per-challenge slot for "how you reach this challenge", it already exists on
every challenge type, and for a deployed challenge the answer is "dploy hands
you an environment" rather than a fixed host and port.

A challenge whose Connection Information is not a JSON object — the usual
`nc host 1337` — is simply not a dploy challenge, and is left alone.
"""

import json
import os
import re

from CTFd.models import Challenges, Solves
from CTFd.utils import get_config
from CTFd.utils.challenges import get_all_challenges
from CTFd.utils.decorators import (
    admins_only,
    authed_only,
    during_ctf_time_only,
    require_verified_emails,
)
from CTFd.utils.decorators.visibility import check_challenge_visibility
from CTFd.utils.user import get_current_team, get_current_user, is_admin
from flask import Blueprint, jsonify, render_template_string, request, send_from_directory

from .k8s import (
    CREATED_BY,
    LABEL_CREATED_BY,
    LABEL_OWNER,
    LABEL_TEMPLATE,
    K8sClient,
    K8sError,
    dploy_namespace,
)

plugin_bp = Blueprint("ctfd_dploy", __name__, url_prefix="/")

_k8s = K8sClient()
_NAMESPACE = dploy_namespace()
_ASSETS = os.path.join(os.path.dirname(__file__), "assets")
_TEMPLATES = os.path.join(os.path.dirname(__file__), "templates")

# Keys read out of a challenge's Connection Information JSON.
KEY_TEMPLATE = "template"
KEY_TTL = "ttlSeconds"
KEY_WAIT = "waitForPool"
KEY_STOP_ON_SOLVE = "stopOnSolve"

# Fallback extend step for templates that don't set spec.ttl.extendSeconds. The
# operator clamps whatever we ask for to the template's real ceiling, so this
# only decides the size of a click, never the maximum.
_EXTEND_SECONDS = int(os.environ.get("DPLOY_EXTEND_SECONDS", "3600"))

# Claim phases that can never make progress again. Re-deploying past one means
# replacing the object, not waiting on it.
_TERMINAL = ("Expired", "Rejected")

_MAX_OWNER_LEN = 63


# ---------------------------------------------------------------------------
# Identity: CTFd's session is the authentication, and the competitor is the
# owner. Teams mode gives the whole team one shared environment and one shared
# quota, which is what `ownerClaim: groups` buys on the OIDC side.
# ---------------------------------------------------------------------------


def _sanitize_owner(value):
    """Normalise a display name into a label-safe owner key.

    Mirrors sanitizeOwner in internal/kube/owner.go (and sanitize in
    internal/controller): lowercase, `.`/`@` folded to `-`, anything outside
    [a-z0-9-] dropped, trimmed and cut to 63 characters. Keeping the two in
    step matters because the operator labels instances with this value.
    """
    value = value.lower().replace(".", "-").replace("@", "-")
    value = re.sub(r"[^a-z0-9-]", "", value)
    value = value.strip("-")
    if len(value) > _MAX_OWNER_LEN:
        value = value[:_MAX_OWNER_LEN].strip("-")
    return value


class Player:
    """The calling competitor: the team in teams mode, the user in users mode.

    `key` is the stable identity (t<id> / u<id>) and is what claim names are
    built from — renaming a team must not orphan its running environment.
    `owner` is the prettier key the operator labels and templates with; it
    keeps the id as a prefix so it stays unique whatever the display name is.
    """

    def __init__(self, key, owner, display_name, params):
        self.key = key
        self.owner = owner
        self.display_name = display_name
        self.params = params


# Characters a param value may contain. An allow-list, not a list of the
# dangerous ones: the value ends up substituted as raw text into a
# valuesTemplate whose rendering is then parsed as YAML, so a quote, a newline
# or a colon in a display name is not a formatting nuisance — it is syntax, and
# it can add a top-level key to the Helm values of the challenge. Enumerating
# what YAML treats as special is a bet; enumerating what belongs in a name is
# not.
_PARAM_SAFE = re.compile(r"[^A-Za-z0-9 ._@-]")
_MAX_PARAM_LEN = 64


def _safe_param(value):
    """Normalise a self-asserted profile string into something inert.

    CTFd validates display names and team names for length only (128 chars) and
    lets players change them freely, so these are attacker-controlled strings
    that reach the cluster. `Alice O'Brien` becomes `Alice OBrien`: slightly
    lossy on the display side, which is the right trade against a name that can
    rewrite a chart's securityContext.
    """
    return _PARAM_SAFE.sub("", (value or "").strip())[:_MAX_PARAM_LEN]


def _current_player():
    """Resolve the calling competitor, or None when there is no usable session
    (a user in teams mode who hasn't joined a team yet)."""
    user = get_current_user()
    if user is None:
        return None

    if get_config("user_mode") == "teams":
        team = get_current_team()
        if team is None:
            return None
        key = f"t{team.id}"
        display_name = team.name or key
        groups = team.name or ""
    else:
        key = f"u{user.id}"
        display_name = user.name or key
        groups = ""

    owner = _sanitize_owner(f"{key}-{display_name}") or key

    # Mirror the API's FORWARDED_CLAIMS defaults (sub, preferred_username,
    # email, name, groups) so a valuesTemplate written against `.Params` renders
    # the same whether the request came from the dploy API or from CTFd.
    # `sub` is built here from an id, so it is inert by construction; every
    # other value is typed by the player and goes through _safe_param.
    params = {
        "sub": key,
        "preferred_username": _safe_param(user.name),
        "email": _safe_param(user.email),
        "name": _safe_param(display_name),
        "groups": _safe_param(groups),
    }
    return Player(key, owner, display_name, {k: v for k, v in params.items() if v})


# ---------------------------------------------------------------------------
# Challenge → template mapping, read off the challenge's Connection Information.
# ---------------------------------------------------------------------------


class Binding:
    """What a challenge's Connection Information asks for."""

    def __init__(self, template, ttl_seconds, wait_for_pool, stop_on_solve=True):
        self.template = template
        self.ttl_seconds = ttl_seconds
        self.wait_for_pool = wait_for_pool
        self.stop_on_solve = stop_on_solve


def _requirements_met(challenge):
    """Whether the caller has solved this challenge's prerequisites.

    Mirrors CTFd's own check in CTFd/api/v1/challenges.py, including the
    intersection with existing challenge ids — a prerequisite pointing at a
    deleted challenge must not lock everyone out forever.
    """
    requirements = getattr(challenge, "requirements", None)
    if not requirements:
        return True
    prereqs = requirements.get("prerequisites", []) if isinstance(requirements, dict) else []
    if not prereqs:
        return True
    user = get_current_user()
    if user is None:
        return False
    solve_ids = {
        cid for cid, in Solves.query.with_entities(Solves.challenge_id)
        .filter_by(account_id=user.account_id).all()
    }
    all_ids = {c.id for c in Challenges.query.with_entities(Challenges.id).all()}
    return solve_ids >= set(prereqs).intersection(all_ids)


def _visible_challenge(challenge_id):
    """The challenge as CTFd would let this caller see it, or None.

    A challenge id arrives from request input, so resolving it with a bare
    Challenges.query would hand out challenges CTFd itself hides: `hidden` and
    `locked` states, and challenges gated behind prerequisites the caller has
    not solved. In a CTF the environment usually *is* the challenge, so that
    would mean a live box — and its name — before release.

    The state filter comes from get_all_challenges() rather than being spelled
    out again here: a rule that lives in two places is a rule that drifts, which
    is exactly how this went wrong the first time. Admins keep the unfiltered
    view so they can still preview an unreleased challenge.
    """
    admin = is_admin()
    listed = {c.id: c for c in get_all_challenges(admin=admin)}.get(challenge_id)
    if listed is None:
        return None
    if not admin and not _requirements_met(listed):
        return None
    # get_all_challenges returns a projection without connection_info, so the
    # row itself is still needed — but only once the caller is allowed it.
    return Challenges.query.filter_by(id=challenge_id).first()


def _binding_for(challenge_id):
    """The dploy request a challenge declares, for a *player* asking about it.

    Gated: the id comes from request input, so the challenge is resolved the way
    CTFd would resolve it for this caller.
    """
    ch = _visible_challenge(challenge_id)
    return parse_binding(ch) if ch is not None else None


def parse_binding(ch):
    """Parse a challenge's Connection Information as a dploy request, or return
    None when it is not one.

    Deliberately ungated, and separate from _binding_for for that reason: the
    solve release runs on a background thread with no session to authorize
    against, and needs none — CTFd already accepted the flag, which is the
    proof of access. Mixing the two is what made the first async attempt fail
    with "Working outside of request context".

    Anything that is not a JSON object naming a template is "not a dploy
    challenge" rather than an error: the field's normal use is a connection
    string, and a typo in it must not take the challenge down.
    """
    if ch is None:
        return None
    raw = (ch.connection_info or "").strip()
    if not raw.startswith("{"):
        return None
    try:
        spec = json.loads(raw)
    except ValueError:
        return None
    if not isinstance(spec, dict):
        return None

    template = spec.get(KEY_TEMPLATE)
    if not isinstance(template, str) or not template.strip():
        return None

    try:
        ttl_seconds = int(spec.get(KEY_TTL, 0) or 0)
    except (TypeError, ValueError):
        ttl_seconds = 0

    # Solving the challenge is the natural end of the environment, so tearing it
    # down is the default: it returns a pool member to the warm set and stops a
    # solved box burning a quota slot. A challenge that wants the box to survive
    # its flag — a multi-stage one, or somewhere to keep exploring — sets
    # "stopOnSolve": false.
    return Binding(template.strip(), ttl_seconds, bool(spec.get(KEY_WAIT, False)),
                   bool(spec.get(KEY_STOP_ON_SOLVE, True)))


def _challenge_id_arg(value):
    """Coerce a challenge_id from request input; None on bad input."""
    try:
        return int(value)
    except (TypeError, ValueError):
        return None


def _challenge_title(challenge_id, fallback):
    # Same gate as everywhere else: the name of an unreleased challenge is
    # itself worth withholding, and this is returned by every player endpoint.
    ch = _visible_challenge(challenge_id)
    return ch.name if ch is not None else fallback


# ---------------------------------------------------------------------------
# Claims.
# ---------------------------------------------------------------------------


def _claim_name(template, key):
    """Deterministic per (template, competitor), so the plugin can find a
    running environment again without storing anything of its own."""
    return f"{template}-{key}"


def _claim_body(binding, player):
    return {
        "apiVersion": "dploy.dev/v1alpha1",
        "kind": "DployInstanceClaim",
        "metadata": {
            "name": _claim_name(binding.template, player.key),
            "namespace": _NAMESPACE,
            "labels": {LABEL_CREATED_BY: CREATED_BY},
        },
        "spec": {
            "templateRef": binding.template,
            "owner": player.owner,
            "params": player.params,
            "ttlSeconds": binding.ttl_seconds,
            "waitForPool": binding.wait_for_pool,
        },
    }


def _phase_of(claim):
    return ((claim or {}).get("status") or {}).get("phase") or "Pending"


def _bound_message(claim):
    """The Bound condition's reason/message — where the operator explains a
    Pending (pool exhausted) or a Rejected (quota, disabled template)."""
    for c in ((claim or {}).get("status") or {}).get("conditions") or []:
        if c.get("type") == "Bound":
            return c.get("reason", ""), c.get("message", "")
    return "", ""


def _is_ready(claim):
    """Whether the environment actually answers, which is not the same thing as
    the claim holding one.

    The connection URL is derived from the template, so it exists the moment an
    instance does — long before a workload is serving on it. For a warm pool
    member the two coincide, since it was provisioned before anyone claimed it;
    for an on-demand instance they are a Helm install apart. Handing the player
    a link in between produces a browser tab that fails to connect.

    The Ready condition is the operator's own verdict, mirrored from the
    HelmRelease, so this follows whatever the operator decides readiness means
    rather than guessing from a phase name.
    """
    for c in ((claim or {}).get("status") or {}).get("conditions") or []:
        if c.get("type") == "Ready":
            return c.get("status") == "True"
    return False


def _ensure_claim(binding, player):
    """Return the player's claim for this challenge, filing one if they don't
    hold it yet. Returns (claim, error).

    A terminal claim is replaced rather than waited on: Expired is never
    revived by the operator, and Rejected only re-opens when the spec changes,
    so a player who freed a slot after hitting their quota would otherwise be
    stuck looking at a dead Deploy button.
    """
    name = _claim_name(binding.template, player.key)
    try:
        existing = _k8s.get_claim(_NAMESPACE, name)
        if existing is not None:
            if _phase_of(existing) not in _TERMINAL:
                return existing, None
            _k8s.delete_claim_wait(_NAMESPACE, name)
        return _k8s.create_claim(_NAMESPACE, _claim_body(binding, player)), None
    except K8sError as e:
        return None, str(e)


def _extend_target(claim):
    """The TTL to ask for on the next extend, or None when the environment
    can't grow (unlimited already, or sitting at the ceiling)."""
    st = (claim or {}).get("status") or {}
    current = st.get("ttlSeconds", 0)
    ceiling = st.get("maxTTLSeconds", 0)
    if current == -1:
        return None  # already unlimited
    step = _EXTEND_SECONDS
    if ceiling == -1:
        return current + step
    if current >= ceiling:
        return None
    return min(current + step, ceiling)


def _claim_view(claim, binding, title):
    """The one shape every player endpoint returns. The claim's status is
    already a full projection of the instance, so this is a rename, not a
    join — no second read to resolve the URL or the phase."""
    st = (claim or {}).get("status") or {}
    reason, message = _bound_message(claim)
    return {
        "enabled": True,
        "template": binding.template,
        "title": title,
        "phase": _phase_of(claim) if claim else "NotStarted",
        # ready is what the panel gates the connection link on. phase says the
        # claim holds an instance; ready says that instance answers.
        "ready": _is_ready(claim),
        "instancePhase": st.get("instancePhase", ""),
        "health": st.get("health", ""),
        "url": st.get("connectionURL", ""),
        "connectionType": st.get("connectionType", "") or "web",
        "connectionMessage": st.get("connectionMessage", ""),
        "expiresAt": st.get("expiresAt", ""),
        "ttlSeconds": st.get("ttlSeconds", 0),
        "maxTTLSeconds": st.get("maxTTLSeconds", 0),
        "canExtend": bool(claim) and _extend_target(claim) is not None,
        "reason": reason,
        "message": message,
    }


def _disabled():
    """Response for a challenge that isn't wired to dploy at all."""
    return jsonify({"enabled": False}), 200


def _player_context():
    """(binding, player, error_response) for the challenge_id in the request."""
    challenge_id = _challenge_id_arg(
        request.args.get("challenge_id")
        if request.method == "GET"
        else (request.get_json(silent=True) or {}).get("challenge_id")
    )
    if challenge_id is None:
        return None, None, None, _disabled()
    binding = _binding_for(challenge_id)
    if binding is None:
        return None, None, None, _disabled()
    player = _current_player()
    if player is None:
        return None, None, None, (
            jsonify({"enabled": True, "error": "join a team to deploy this challenge"}),
            400,
        )
    return challenge_id, binding, player, None


# ---------------------------------------------------------------------------
# Assets.
# ---------------------------------------------------------------------------


@plugin_bp.route("/plugins/ctfd_dploy/assets/<path:filename>")
def assets(filename):
    return send_from_directory(_ASSETS, filename)


# ---------------------------------------------------------------------------
# Player: probe, status, deploy, extend, stop.
# ---------------------------------------------------------------------------


@plugin_bp.route("/plugins/ctfd_dploy/info", methods=["GET"])
@check_challenge_visibility
@during_ctf_time_only
@require_verified_emails
@authed_only
def info():
    """Modal probe, called once per open: is this challenge wired to a dploy
    template, and does that template still exist and accept claims?"""
    challenge_id = _challenge_id_arg(request.args.get("challenge_id"))
    if challenge_id is None:
        return _disabled()
    binding = _binding_for(challenge_id)
    if binding is None:
        return _disabled()
    try:
        tmpl = _k8s.get_template(_NAMESPACE, binding.template)
    except K8sError as e:
        return jsonify({"enabled": True, "error": str(e)}), 200
    if tmpl is None:
        return jsonify({
            "enabled": True,
            "error": f"DployTemplate {binding.template!r} not found in {_NAMESPACE}",
        }), 200
    spec = tmpl.get("spec") or {}
    if not spec.get("enabled"):
        return jsonify({
            "enabled": True,
            "error": f"DployTemplate {binding.template!r} is disabled",
        }), 200
    return jsonify({
        "enabled": True,
        "template": binding.template,
        "title": spec.get("displayName") or binding.template,
        "description": spec.get("description", ""),
        "method": spec.get("method", "on-demand"),
        "connectionType": spec.get("connectionType", "") or "web",
    }), 200


@plugin_bp.route("/plugins/ctfd_dploy/status", methods=["GET"])
@check_challenge_visibility
@during_ctf_time_only
@require_verified_emails
@authed_only
def status():
    """What the modal polls. Read-only: it never files a claim, so a player
    who never clicked Deploy is never charged an environment."""
    challenge_id, binding, player, err = _player_context()
    if err is not None:
        return err
    name = _claim_name(binding.template, player.key)
    try:
        claim = _k8s.get_claim(_NAMESPACE, name)
    except K8sError as e:
        return jsonify({"enabled": True, "error": str(e)}), 200
    title = _challenge_title(challenge_id, binding.template)
    return jsonify(_claim_view(claim, binding, title)), 200


@plugin_bp.route("/plugins/ctfd_dploy/run", methods=["POST"])
@check_challenge_visibility
@during_ctf_time_only
@require_verified_emails
@authed_only
def run():
    """File the claim — the whole of "run this instance". Idempotent: clicking
    twice returns the same claim, and the operator decides whether that means a
    warm pool member, a fresh instance or a queue slot.

    Named after the dploy API's own /run/:env rather than "deploy": the player
    is asking for their instance, not shipping anything.
    """
    challenge_id, binding, player, err = _player_context()
    if err is not None:
        return err
    claim, error = _ensure_claim(binding, player)
    if error:
        return jsonify({"enabled": True, "error": error}), 502
    title = _challenge_title(challenge_id, binding.template)
    return jsonify(_claim_view(claim, binding, title)), 202


@plugin_bp.route("/plugins/ctfd_dploy/extend", methods=["POST"])
@check_challenge_visibility
@during_ctf_time_only
@require_verified_emails
@authed_only
def extend():
    """Extending is a patch that raises spec.ttlSeconds. There is no extend
    verb and no extend counter to keep: the ceiling lives on the template and
    the operator clamps to it."""
    challenge_id, binding, player, err = _player_context()
    if err is not None:
        return err
    name = _claim_name(binding.template, player.key)
    title = _challenge_title(challenge_id, binding.template)
    try:
        claim = _k8s.get_claim(_NAMESPACE, name)
        if claim is None:
            return jsonify({"enabled": True, "error": "nothing to extend"}), 404
        target = _extend_target(claim)
        if target is None:
            return jsonify(_claim_view(claim, binding, title)), 200
        claim = _k8s.patch_claim_ttl(_NAMESPACE, name, target)
    except K8sError as e:
        return jsonify({"enabled": True, "error": str(e)}), 502
    return jsonify(_claim_view(claim, binding, title)), 200


@plugin_bp.route("/plugins/ctfd_dploy/stop", methods=["POST"])
@check_challenge_visibility
@during_ctf_time_only
@require_verified_emails
@authed_only
def stop():
    """Delete the claim. The instance follows through the owner reference, and
    a pool template gets its member back once the operator replenishes."""
    challenge_id, binding, player, err = _player_context()
    if err is not None:
        return err
    try:
        _k8s.delete_claim(_NAMESPACE, _claim_name(binding.template, player.key))
    except K8sError as e:
        return jsonify({"enabled": True, "error": str(e)}), 502
    title = _challenge_title(challenge_id, binding.template)
    return jsonify(_claim_view(None, binding, title)), 200


# ---------------------------------------------------------------------------
# Admin: the Dploy manager page, now reading the cluster directly.
# ---------------------------------------------------------------------------


def _render_page(filename, **ctx):
    with open(os.path.join(_TEMPLATES, filename), encoding="utf-8") as f:
        return render_template_string(f.read(), **ctx)


@plugin_bp.route("/admin/plugins/ctfd_dploy", methods=["GET"])
@admins_only
def admin_page():
    return _render_page("dploy_manager.html", namespace=_NAMESPACE)


@plugin_bp.route("/admin/plugins/ctfd_dploy/claims.json", methods=["GET"])
@admins_only
def admin_claims():
    try:
        claims = _k8s.list_claims(_NAMESPACE)
    except K8sError as e:
        return jsonify({"error": str(e)}), 502

    items = []
    for c in claims:
        meta = c.get("metadata") or {}
        spec = c.get("spec") or {}
        st = c.get("status") or {}
        reason, message = _bound_message(c)
        items.append({
            "name": meta.get("name", ""),
            "template": spec.get("templateRef", ""),
            "owner": spec.get("owner", ""),
            "phase": st.get("phase", "Pending"),
            "instance": st.get("instanceRef", ""),
            "instancePhase": st.get("instancePhase", ""),
            "health": st.get("health", ""),
            "url": st.get("connectionURL", ""),
            "boundAt": st.get("boundAt", ""),
            "expiresAt": st.get("expiresAt", ""),
            "ttlSeconds": st.get("ttlSeconds", 0),
            "createdBy": (meta.get("labels") or {}).get(LABEL_CREATED_BY, ""),
            "createdAt": meta.get("creationTimestamp", ""),
            "reason": reason,
            "message": message,
        })
    items.sort(key=lambda r: (r["template"], r["owner"]))
    return jsonify({"items": items}), 200


@plugin_bp.route("/admin/plugins/ctfd_dploy/templates.json", methods=["GET"])
@admins_only
def admin_templates():
    """Templates plus the exact Connection Information an author has to paste
    onto a challenge to use one — the mapping is the part that's easy to get
    wrong, so the page spells it out rather than making the author remember the
    convention."""
    try:
        templates = _k8s.list_templates(_NAMESPACE)
    except K8sError as e:
        return jsonify({"error": str(e)}), 502

    items = []
    for t in templates:
        meta = t.get("metadata") or {}
        spec = t.get("spec") or {}
        st = t.get("status") or {}
        name = meta.get("name", "")
        items.append({
            "name": name,
            "connectionInfo": json.dumps({KEY_TEMPLATE: name}),
            "displayName": spec.get("displayName", ""),
            "method": spec.get("method", "on-demand"),
            "enabled": bool(spec.get("enabled")),
            "visible": spec.get("visible", True),
            "poolAvailable": st.get("poolAvailable", 0),
            "poolClaimed": st.get("poolClaimed", 0),
            "poolTotal": st.get("poolTotal", 0),
            "connectionType": spec.get("connectionType", "") or "web",
        })
    items.sort(key=lambda r: r["name"])
    return jsonify({"items": items}), 200


@plugin_bp.route("/admin/plugins/ctfd_dploy/claims/<name>", methods=["DELETE"])
@admins_only
def admin_claim_delete(name):
    try:
        _k8s.delete_claim(_NAMESPACE, name)
    except K8sError as e:
        return jsonify({"error": str(e)}), 502
    return jsonify({"status": "deleting"}), 202


@plugin_bp.route("/admin/plugins/ctfd_dploy/mine.json", methods=["GET"])
@authed_only
def my_claims():
    """Every environment the calling competitor holds, whatever challenge it
    came from — the label selector is the operator's own owner label."""
    player = _current_player()
    if player is None:
        return jsonify({"items": []}), 200
    try:
        claims = _k8s.list_claims(
            _NAMESPACE, label_selector=f"{LABEL_OWNER}={player.owner}"
        )
    except K8sError as e:
        return jsonify({"items": [], "error": str(e)}), 200
    items = []
    for c in claims:
        st = c.get("status") or {}
        items.append({
            "template": (c.get("spec") or {}).get("templateRef", ""),
            "phase": st.get("phase", "Pending"),
            "url": st.get("connectionURL", ""),
            "expiresAt": st.get("expiresAt", ""),
        })
    items.sort(key=lambda r: r["template"])
    return jsonify({"items": items}), 200


# Kept importable for tests and for anything that wants the label the operator
# lists claims by.
__all__ = ["plugin_bp", "LABEL_TEMPLATE"]
