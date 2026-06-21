# Copyright the Dploy authors.
# SPDX-License-Identifier: MIT
"""
Tiny client for the Dploy REST API.

Maps the four challenge actions onto Dploy's `/api/run/{template}` endpoints:

    start   -> GET    /api/run/{template}           (create-or-get)
    status  -> GET    /api/run/{template}/status
    extend  -> POST   /api/run/{template}/extend
    stop    -> DELETE /api/run/{template}

Every call carries the caller's CTFd-issued id_token as a Bearer token.
"""

import requests

TIMEOUT = 20


class DployError(Exception):
    """A non-2xx response from Dploy. ``status`` is the HTTP code, ``payload``
    the decoded JSON body (or raw text)."""

    def __init__(self, status, payload):
        self.status = status
        self.payload = payload
        msg = payload.get("error") if isinstance(payload, dict) else str(payload)
        super().__init__(msg or "dploy request failed")


class DployClient:
    def __init__(self, base_url, token):
        self.base = base_url.rstrip("/")
        self.token = token

    # -- low level ----------------------------------------------------------

    def _request(self, method, path):
        url = self.base + path
        headers = {
            "Authorization": "Bearer " + self.token,
            "Accept": "application/json",
        }
        resp = requests.request(method, url, headers=headers, timeout=TIMEOUT)
        if resp.status_code == 204:
            return {}
        try:
            body = resp.json()
        except ValueError:
            body = resp.text
        if not (200 <= resp.status_code < 300):
            raise DployError(resp.status_code, body)
        return body

    # -- actions ------------------------------------------------------------

    def start(self, template):
        return self._request("GET", "/api/run/%s" % template)

    def status(self, template):
        return self._request("GET", "/api/run/%s/status" % template)

    def extend(self, template):
        return self._request("POST", "/api/run/%s/extend" % template)

    def stop(self, template):
        return self._request("DELETE", "/api/run/%s" % template)
