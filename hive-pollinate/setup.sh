#!/usr/bin/env bash
set -euo pipefail

echo "[*] detecting architecture..."
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)
    echo "[!] Unsupported arch: $ARCH"
    exit 1
  ;;
esac
echo "[*] arch = $ARCH"

echo "[*] installing base dependencies..."
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y \
  curl ca-certificates git iproute2 apt-transport-https gnupg lsb-release

# ----------------------------
# docker
# ----------------------------
if ! command -v docker >/dev/null 2>&1; then
  echo "[*] installing docker..."
  curl -fsSL https://get.docker.com | sh
else
  echo "[*] docker already present"
fi

echo "[*] enabling docker..."
systemctl enable --now docker

# ----------------------------
# kind
# ----------------------------
if ! command -v kind >/dev/null 2>&1; then
  echo "[*] installing kind..."
  curl -Lo /usr/local/bin/kind \
    "https://kind.sigs.k8s.io/dl/v0.26.0/kind-linux-${ARCH}"
  chmod +x /usr/local/bin/kind
else
  echo "[*] kind already present"
fi

# ----------------------------
# kubectl
# ----------------------------
if ! command -v kubectl >/dev/null 2>&1; then
  echo "[*] installing kubectl..."
  KVER="$(curl -s https://dl.k8s.io/release/stable.txt)"
  curl -Lo /usr/local/bin/kubectl \
    "https://dl.k8s.io/release/${KVER}/bin/linux/${ARCH}/kubectl"
  chmod +x /usr/local/bin/kubectl
else
  echo "[*] kubectl already present"
fi

echo "[*] verifying installs..."
docker --version || true
kind --version || true
kubectl version --client || true

echo "[✓] host bootstrap complete"
