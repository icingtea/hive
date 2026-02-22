#!/usr/bin/env bash
# Sets up a local kind cluster with a registry that the cluster can pull from.
# Run once. Safe to re-run — skips steps that are already done.
set -euo pipefail

CLUSTER_NAME="hive"
REGISTRY_NAME="hive-registry"
REGISTRY_PORT="5001"

# ── Registry ──────────────────────────────────────────────────────────────────
if ! docker inspect "$REGISTRY_NAME" &>/dev/null; then
  echo "Creating local registry..."
  docker run -d --restart=always -p "127.0.0.1:${REGISTRY_PORT}:5000" \
    --network bridge --name "$REGISTRY_NAME" registry:2
else
  echo "Registry '$REGISTRY_NAME' already running."
fi

# ── Kind cluster ──────────────────────────────────────────────────────────────
if ! kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  echo "Creating kind cluster '${CLUSTER_NAME}'..."
  kind create cluster --name "$CLUSTER_NAME"
else
  echo "Cluster '${CLUSTER_NAME}' already exists."
fi

# ── Connect registry to kind network ─────────────────────────────────────────
if ! docker network inspect kind 2>/dev/null | grep -q "\"$REGISTRY_NAME\""; then
  echo "Connecting registry to kind network..."
  docker network connect kind "$REGISTRY_NAME" 2>/dev/null || true
fi

# ── Patch containerd on kind node to trust the local registry ────────────────
# Pods address the registry as hive-registry:5000 (Docker DNS on the kind network).
# Tell containerd to treat that address as an insecure HTTP registry.
echo "Patching containerd registry config on kind node..."
docker exec "${CLUSTER_NAME}-control-plane" bash -c "
  mkdir -p '/etc/containerd/certs.d/${REGISTRY_NAME}:5000'
  cat > '/etc/containerd/certs.d/${REGISTRY_NAME}:5000/hosts.toml' << 'EOF'
[host.\"http://${REGISTRY_NAME}:5000\"]
  capabilities = [\"pull\", \"resolve\"]
  skip_verify = true
EOF
"
docker exec "${CLUSTER_NAME}-control-plane" systemctl restart containerd
sleep 2

# ── Namespace ─────────────────────────────────────────────────────────────────
kubectl create namespace hive-agents --context "kind-${CLUSTER_NAME}" 2>/dev/null || true

echo ""
echo "Done."
echo "  Registry : localhost:${REGISTRY_PORT}  (accessible inside cluster as ${REGISTRY_NAME}:5000)"
echo "  Cluster  : kind-${CLUSTER_NAME}"
echo "  Namespace: hive-agents"
