"""Dploy CTFd plugin.

Copyright the Dploy authors.
SPDX-License-Identifier: MIT

Turns a player's "Deploy" click into a DployInstanceClaim — per team in teams
mode, per user in users mode — which the dploy operator binds to a warm pool
member or provisions on demand. A challenge opts in with the CTFd tag
`dploy:<template>`.

The plugin talks to the Kubernetes API directly with the official `kubernetes`
client, vendored into _vendor/ at image build time and prepended to sys.path.
It does not call the dploy API: the claim CRD *is* the request interface, so
CTFd's session is the identity and there is no token to broker between the two
systems.
"""

import os
import sys

# Make the vendored `kubernetes` client (and its deps) importable before
# anything in this package imports it.
_VENDOR = os.path.join(os.path.dirname(__file__), "_vendor")
if os.path.isdir(_VENDOR) and _VENDOR not in sys.path:
    sys.path.insert(0, _VENDOR)

# Compat shim: urllib3 2.x dropped HTTPResponse.getheaders(), but the kubernetes
# client still calls it from RESTResponse and from ApiException.__init__. CTFd
# 3.8 ships urllib3 2.x, so every non-2xx response from the API server —
# including the 404 probe that means "no claim yet" — would otherwise raise
# AttributeError. Re-add the two accessors as passthroughs to .headers.
try:  # pragma: no cover
    from urllib3.response import HTTPResponse as _HR

    if not hasattr(_HR, "getheaders"):
        _HR.getheaders = lambda self: self.headers
    if not hasattr(_HR, "getheader"):
        _HR.getheader = lambda self, name, default=None: self.headers.get(name, default)
except Exception:
    pass

from CTFd.plugins import register_plugin_script  # noqa: E402

from . import solve_hook  # noqa: E402
from .routes import plugin_bp  # noqa: E402


def load(app):
    """CTFd plugin entry point.

    The admin manager page is wired through config.json's `route` field — CTFd
    builds the `Plugins > Dploy` dropdown entry from it, so no separate menu
    registration is needed.
    """
    app.register_blueprint(plugin_bp)
    # Solving a challenge releases its environment: back to the warm pool, and
    # off the player's quota. Registered after the blueprint so the teardown can
    # reuse the plugin's own Kubernetes client.
    solve_hook.register(app)
    # Player-facing: the deploy/connect/extend/stop panel injected into the
    # challenge modal for challenges tagged `dploy:<template>`.
    register_plugin_script("/plugins/ctfd_dploy/assets/player.js")
