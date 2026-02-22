#!/usr/bin/env bash
set -euo pipefail

CONTROL_PLANE="https://142.93.222.37:6443"
TOKEN="K1074e4fc0b86d1ba344dd3a1a6211640cdaed2051ba32aac65b9bf2ca4bdfdd1a8::server:159ad4b111afa9331ca11c63f609908c"

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
# docker (optional but keeping since you had it)
# k3s ships containerd already, so docker is NOT required.
# remove this block if you want lean nodes.
# ----------------------------
if ! command -v docker >/dev/null 2>&1; then
  echo "[*] installing docker..."
  curl -fsSL https://get.docker.com | sh
fi
systemctl enable --now docker || true

# ----------------------------
# install Go
# ----------------------------
if ! command -v go >/dev/null 2>&1; then
  echo "[*] installing Go..."
  GO_VERSION="1.22.5"

  curl -LO "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz"
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "go${GO_VERSION}.linux-${ARCH}.tar.gz"

  echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
  export PATH=$PATH:/usr/local/go/bin
fi

go version

# ----------------------------
# install k3s agent (worker)
# ----------------------------
if ! systemctl is-active --quiet k3s-agent; then
  echo "[*] installing k3s agent..."

  curl -sfL https://get.k3s.io | \
    K3S_URL="${CONTROL_PLANE}" \
    K3S_TOKEN="${TOKEN}" \
    sh -

mkdir -p /etc/rancher/k3s

cat > /etc/rancher/k3s/registries.yaml <<'EOF'
mirrors:
  "142.93.222.37:5000":
    endpoint:
      - "http://142.93.222.37:5000"
EOF

systemctl restart k3s-agent

else
  echo "[*] k3s already installed"
fi

echo "[✓] node bootstrap complete"


