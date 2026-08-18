"""Direct Kubernetes access for the Dploy CTFd plugin.

Copyright the Dploy authors.
SPDX-License-Identifier: MIT

Thin wrapper over the `kubernetes` client's CustomObjectsApi, scoped to the
two CRDs the plugin touches: DployTemplate (read) and DployInstanceClaim
(read / list / create / patch / delete).

There is deliberately no HTTP call to the dploy API here. A claim *is* the
request: the operator binds it to a warm pool member or provisions one on
demand, enforces the per-owner quota, anchors the TTL and projects the whole
instance onto `status`. So the plugin writes one object and reads one object,
and CTFd's own session is the authentication — no OIDC round trip, no bearer
token, no shared secret.
"""

import os
import time

from kubernetes import client, config

GROUP = "dploy.dev"
VERSION = "v1alpha1"
TEMPLATES = "dploytemplates"
CLAIMS = "dployinstanceclaims"

# Labels the operator stamps on the objects it manages (api/v1alpha1/labels.go).
LABEL_MANAGED = "dploy.dev/managed"
LABEL_OWNER = "dploy.dev/owner"
LABEL_TEMPLATE = "dploy.dev/template"
# Informational label of our own, so an admin can tell claims filed by CTFd
# apart from claims filed by the dploy API or by hand with kubectl.
LABEL_CREATED_BY = "dploy.dev/created-by"
CREATED_BY = "ctfd-dploy-plugin"


class K8sError(Exception):
    """Any failure talking to the Kubernetes API."""


def dploy_namespace():
    """The namespace dploy's CRs live in: $DPLOY_NAMESPACE, else `dploy-system`
    (the API server's own default).

    Note this is *not* the pod's own namespace: CTFd runs in its namespace and
    reaches into dploy's, which is why the RoleBinding in rbac.yaml is filed on
    the dploy side and names the CTFd service account as its subject.
    """
    return os.environ.get("DPLOY_NAMESPACE") or "dploy-system"


class K8sClient:
    """Lazily-initialised client — in-cluster first, kubeconfig as a fallback
    so the plugin can be exercised from a dev machine against kind."""

    def __init__(self):
        self._api = None

    @property
    def api(self):
        if self._api is None:
            try:
                config.load_incluster_config()
            except config.ConfigException:
                config.load_kube_config()
            self._api = client.CustomObjectsApi()
        return self._api

    # -- templates ---------------------------------------------------------

    def list_templates(self, namespace):
        """All DployTemplate CRs in the namespace."""
        try:
            resp = self.api.list_namespaced_custom_object(
                GROUP, VERSION, namespace, TEMPLATES
            )
        except client.ApiException as e:
            raise K8sError(f"list dploytemplates: {e.reason}") from e
        return resp.get("items", [])

    def get_template(self, namespace, name):
        """A DployTemplate by name, or None."""
        try:
            return self.api.get_namespaced_custom_object(
                GROUP, VERSION, namespace, TEMPLATES, name
            )
        except client.ApiException as e:
            if e.status == 404:
                return None
            raise K8sError(f"get dploytemplate {name}: {e.reason}") from e

    # -- claims ------------------------------------------------------------

    def list_claims(self, namespace, label_selector=None):
        """DployInstanceClaim CRs in the namespace, optionally label-filtered."""
        kwargs = {}
        if label_selector:
            kwargs["label_selector"] = label_selector
        try:
            resp = self.api.list_namespaced_custom_object(
                GROUP, VERSION, namespace, CLAIMS, **kwargs
            )
        except client.ApiException as e:
            raise K8sError(f"list dployinstanceclaims: {e.reason}") from e
        return resp.get("items", [])

    def get_claim(self, namespace, name):
        """A DployInstanceClaim by name, or None."""
        try:
            return self.api.get_namespaced_custom_object(
                GROUP, VERSION, namespace, CLAIMS, name
            )
        except client.ApiException as e:
            if e.status == 404:
                return None
            raise K8sError(f"get dployinstanceclaim {name}: {e.reason}") from e

    def create_claim(self, namespace, body):
        """Create a claim. An already-existing one is returned as-is, which is
        what makes a repeated Deploy click idempotent."""
        try:
            return self.api.create_namespaced_custom_object(
                GROUP, VERSION, namespace, CLAIMS, body
            )
        except client.ApiException as e:
            if e.status == 409:
                return self.get_claim(namespace, body["metadata"]["name"])
            raise K8sError(f"create dployinstanceclaim: {e.reason}") from e

    def patch_claim_ttl(self, namespace, name, ttl_seconds):
        """Raise spec.ttlSeconds — the whole of "extend" in this API. The
        operator clamps the value to the template's ceiling and reports what it
        actually granted in status.ttlSeconds, so the plugin never has to know
        the maximum to ask for one."""
        body = {"spec": {"ttlSeconds": int(ttl_seconds)}}
        try:
            return self.api.patch_namespaced_custom_object(
                GROUP, VERSION, namespace, CLAIMS, name, body
            )
        except client.ApiException as e:
            if e.status == 404:
                return None
            raise K8sError(f"patch dployinstanceclaim {name}: {e.reason}") from e

    def delete_claim(self, namespace, name, timeout=None):
        """Delete a claim. A missing claim is a no-op.

        Deleting the claim is the only teardown verb there is: the claim owns
        the instance, so the garbage collector cascades, and the instance's own
        finalizer unwinds the HelmRelease and the workload namespace.

        `timeout` bounds the call: callers that delete outside a request (the
        solve release thread) must not be pinned for minutes by an API server
        that stopped answering.
        """
        kwargs = {"_request_timeout": timeout} if timeout else {}
        try:
            self.api.delete_namespaced_custom_object(
                GROUP, VERSION, namespace, CLAIMS, name, **kwargs
            )
        except client.ApiException as e:
            if e.status != 404:
                raise K8sError(f"delete dployinstanceclaim {name}: {e.reason}") from e

    def delete_claim_wait(self, namespace, name, timeout=30, poll=1.0):
        """Delete a claim and block until the object is really gone.

        Needed because claim names are deterministic per (template, player):
        recreating one under a name whose tombstone still exists would just hand
        the player back the dead claim. Raises K8sError on timeout.
        """
        self.delete_claim(namespace, name)
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if self.get_claim(namespace, name) is None:
                return
            time.sleep(poll)
        raise K8sError(f"timeout waiting for dployinstanceclaim {name} to be deleted")
