# Builds the Dploy CTFd plugin. Build from the repository root:
#
#   docker build -f integrations/ctfd_dploy/Dockerfile -t dploy-ctfd-plugin .
#
# Two final targets:
#
#   --target plugin-volume  (default)
#       A `FROM scratch` filesystem whose root *is* the plugin directory, meant
#       to be mounted into CTFd pods as a Kubernetes image volume
#       (volumes[].image) at /opt/CTFd/CTFd/plugins/ctfd_dploy.
#
#   --target ctfd-baked
#       A runnable `ctfd/ctfd` image with the plugin copied into
#       /opt/CTFd/CTFd/plugins/. For clusters without the ImageVolume feature
#       (before Kubernetes 1.33) or a chart that has no plugin-mount hook.
#       Image volumes themselves work fine on kind — verified on kind v1.35
#       with the podman provider.
#
# Both targets carry the same payload: the plugin's Python modules plus the
# vendored `kubernetes` client under _vendor/, which __init__.py prepends to
# sys.path. CTFd does not ship that client, and the plugin cannot reach the
# apiserver without it.

ARG CTFD_VERSION=3.8.4

# --- build stage: vendor the kubernetes client into _vendor/ -----------------
FROM python:3.11-slim AS pyvendor

ARG K8S_CLIENT_VERSION=32.0.1

WORKDIR /plugin
COPY integrations/ctfd_dploy/ ./ctfd_dploy/

RUN pip install --no-cache-dir --target ./ctfd_dploy/_vendor \
        "kubernetes==${K8S_CLIENT_VERSION}" \
    && rm -f ./ctfd_dploy/Dockerfile ./ctfd_dploy/rbac.yaml ./ctfd_dploy/README.md \
    && find ./ctfd_dploy -maxdepth 1 -name __pycache__ -type d -exec rm -rf {} +

# --- final stage (ctfd-baked): CTFd with the plugin pre-installed ------------
# Defined before the default stage so a plain `docker build` (no --target)
# still yields the image-volume artifact below.
FROM ctfd/ctfd:${CTFD_VERSION} AS ctfd-baked
COPY --from=pyvendor /plugin/ctfd_dploy/ /opt/CTFd/CTFd/plugins/ctfd_dploy/

# --- final stage (plugin-volume, default): plain filesystem for image volume -
FROM scratch AS plugin-volume
COPY --from=pyvendor /plugin/ctfd_dploy/ /
