# kind
curl -Lo /usr/local/bin/kind https://kind.sigs.k8s.io/dl/v0.26.0/kind-linux-amd64
chmod +x /usr/local/bin/kind

# kubectl (needed to interact with the cluster)
curl -Lo /usr/local/bin/kubectl "https://dl.k8s.io/release/$(curl -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
chmod +x /usr/local/bin/kubectl

 # Docker (if not present)
 curl -fsSL https://get.docker.com | sh
 systemctl enable --now docker

 The only real concern is architecture — amd64 vs arm64. You'd detect it:

 ARCH=$(uname -m)
 case $ARCH in
   x86_64)  ARCH="amd64" ;;
   aarch64) ARCH="arm64" ;;
   *) echo "Unsupported arch: $ARCH"; exit 1 ;;
 esac

 curl -Lo /usr/local/bin/kind "https://kind.sigs.k8s.io/dl/v0.26.0/kind-linux-${ARCH}"
 curl -Lo /usr/local/bin/kubectl "https://dl.k8s.io/release/$(curl -s https://dl.k8s.io/release/stable.txt)/bin/linux/${ARCH}/kubectl"
