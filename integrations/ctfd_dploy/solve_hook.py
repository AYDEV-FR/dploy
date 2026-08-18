"""Tear an environment down when its challenge is solved.

Copyright the Dploy authors.
SPDX-License-Identifier: MIT

A solved challenge has no more use for its box: releasing it returns a warm pool
member to the pool and frees the player's quota slot, instead of leaving it to
burn until the TTL runs out.

Two rules shape the design, and both are about what must NOT happen to a flag
submission:

1. It must never fail because a teardown failed. Deleting a claim is a network
   call; doing it inside the SQLAlchemy flush that records the solve would hold
   a database transaction open and, on error, roll the solve back — losing a
   player's points because Kubernetes hiccuped.

2. It must never be *slowed* by one either. A synchronous delete puts the API
   server's latency in front of the player's "correct!", so an apiserver that is
   slow, or simply not answering, stalls every submission during the end-of-CTF
   rush — exactly when it matters.

So the request path does one thing: put a tuple on a queue. A daemon thread
drains it, retries a few times, and gives up loudly. An environment that outlives
its solve costs a pool slot until its TTL; a submission that hangs costs the CTF.
"""

import logging
import queue
import threading
import time

from flask import g, has_request_context
from sqlalchemy import event

from CTFd.models import Solves
from CTFd.utils import get_config

log = logging.getLogger(__name__)

_PENDING = "_dploy_solved"

# Bounded on purpose: if the drain thread is wedged, dropping the oldest release
# is better than growing without limit inside a web worker. A dropped release is
# an environment that lives until its TTL, which is the same outcome as before
# this feature existed.
_QUEUE_MAX = 1000
_RELEASES = queue.Queue(maxsize=_QUEUE_MAX)

# Retry a transient API server problem, then stop. The TTL is the backstop.
_ATTEMPTS = 4
_BACKOFF = (1, 3, 8)
# Never let one delete pin the drain thread: the python client would otherwise
# wait on its own default, which is effectively forever on a black-holed host.
_TIMEOUT = 10

_worker = None
_worker_lock = threading.Lock()


def _key_for_account(account_id):
    """The claim key for an account id.

    Solves.account_id is the team id in teams mode and the user id otherwise —
    the same numbering _current_player() builds its key from. Deriving the key
    here rather than from the session means an admin marking a solve on someone
    else's behalf still releases the right environment.
    """
    prefix = "t" if get_config("user_mode") == "teams" else "u"
    return f"{prefix}{account_id}"


@event.listens_for(Solves, "after_insert")
def _note_solve(mapper, connection, target):
    """Record the solve; do not act on it yet.

    Runs inside the flush that writes the solve, so it touches nothing but
    memory. Acting here would mean holding a transaction open across a network
    call.
    """
    if not has_request_context():
        return
    pending = getattr(g, _PENDING, None)
    if pending is None:
        pending = []
        setattr(g, _PENDING, pending)
    pending.append((target.account_id, target.challenge_id))


def _drain():
    """Release environments off the request path, forever."""
    from CTFd.models import Challenges

    from .k8s import K8sError
    from .routes import _NAMESPACE, _claim_name, _k8s, parse_binding

    while True:
        account_id, challenge_id, app = _RELEASES.get()
        try:
            # Reading the challenge and the user mode both need an application
            # context, which this thread has none of. Note this uses
            # parse_binding, not _binding_for: the latter enforces CTFd's
            # per-caller visibility rules and needs a session, and there is no
            # caller here — CTFd accepting the flag is the authorization.
            with app.app_context():
                ch = Challenges.query.filter_by(id=challenge_id).first()
                binding = parse_binding(ch)
                key = _key_for_account(account_id)
            if binding is None or not binding.stop_on_solve:
                continue
            name = _claim_name(binding.template, key)
            for attempt in range(_ATTEMPTS):
                try:
                    _k8s.delete_claim(_NAMESPACE, name, timeout=_TIMEOUT)
                    log.info("dploy: released %s after challenge %s was solved",
                             name, challenge_id)
                    break
                except K8sError as e:
                    if attempt == _ATTEMPTS - 1:
                        log.warning("dploy: gave up releasing %s after %d attempts: %s "
                                    "(it will expire on its TTL)", name, _ATTEMPTS, e)
                    else:
                        time.sleep(_BACKOFF[attempt])
        except Exception as e:  # noqa: BLE001 - this thread must never die
            log.warning("dploy: unexpected error releasing challenge %s: %s", challenge_id, e)
        finally:
            _RELEASES.task_done()


def _ensure_worker():
    global _worker
    with _worker_lock:
        if _worker is None or not _worker.is_alive():
            _worker = threading.Thread(target=_drain, name="dploy-release", daemon=True)
            _worker.start()


def register(app):
    """Queue a release at the end of the request that recorded the solve."""
    _ensure_worker()

    @app.after_request
    def _queue_release(response):
        # Only for a request that succeeded: a rejected submission has nothing
        # to release. This is a non-blocking put — the API server is not in the
        # player's path.
        pending = getattr(g, _PENDING, None) if has_request_context() else None
        if pending and response.status_code < 400:
            setattr(g, _PENDING, [])
            for account_id, challenge_id in pending:
                try:
                    _RELEASES.put_nowait((account_id, challenge_id, app))
                except queue.Full:
                    log.warning("dploy: release queue full, %s keeps its environment "
                                "until the TTL", challenge_id)
        return response
