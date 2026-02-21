#!/usr/bin/env bash
set -euo pipefail

CONTROL_PLANE="https://1.2.3.4:6443"
TOKEN="abcdef.0123456789abcdef"
CA_HASH="sha256:REPLACE_ME"

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

echo "[*] installing base deps..."
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y \
  curl ca-certificates gnupg lsb-release apt-transport-https

# ----------------------------
# docker (container runtime)
# ----------------------------
if ! command -v docker >/dev/null 2>&1; then
  echo "[*] installing docker..."
  curl -fsSL https://get.docker.com | sh
fi
systemctl enable --now docker

# ----------------------------
# kubernetes packages
# ----------------------------
echo "[*] installing kubeadm/kubelet/kubectl..."

mkdir -p /etc/apt/keyrings
curl -fsSL https://pkgs.k8s.io/core:/stable:/v1.30/deb/Release.key \
  | gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg

echo "deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] \
https://pkgs.k8s.io/core:/stable:/v1.30/deb/ /" \
  > /etc/apt/sources.list.d/kubernetes.list

apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y kubelet kubeadm kubectl
apt-mark hold kubelet kubeadm kubectl

systemctl enable kubelet

# ----------------------------
# kubeadm join
# ----------------------------
if [ ! -f /etc/kubernetes/kubelet.conf ]; then
  echo "[*] joining cluster..."
  kubeadm join 1.2.3.4:6443 \
    --token "${TOKEN}" \
    --discovery-token-ca-cert-hash "${CA_HASH}"
else
  echo "[*] node already joined"
fi

echo "[✓] node bootstrap complete"
