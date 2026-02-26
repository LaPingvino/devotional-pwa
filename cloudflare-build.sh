#!/usr/bin/env bash
# Cloudflare Pages build script
# Installs Dolt, clones DB, generates data files, builds Hugo site
set -e

# Install Dolt (installs to ~/bin; /usr/local/bin may not be writable)
if ! command -v dolt &>/dev/null; then
  echo "Installing Dolt..."
  curl -fsSL https://github.com/dolthub/dolt/releases/latest/download/install.sh | INSTALL_DIR="$HOME/bin" bash
  export PATH="$HOME/bin:$PATH"
fi
echo "Dolt version: $(dolt version)"

# Install Hugo (Cloudflare Pages has Hugo but may not be the right version)
HUGO_VERSION="${HUGO_VERSION:-0.156.0}"
if ! hugo version 2>/dev/null | grep -q "$HUGO_VERSION"; then
  echo "Installing Hugo $HUGO_VERSION..."
  curl -fsSL "https://github.com/gohugoio/hugo/releases/download/v${HUGO_VERSION}/hugo_extended_${HUGO_VERSION}_linux-amd64.tar.gz" \
    | tar -xz -C "$HOME/bin" hugo
fi
echo "Hugo version: $(hugo version)"

# Clone the prayer database (public DoltHub repo, no auth needed)
if [ -d "bahaiwritings/.dolt" ]; then
  echo "Pulling latest bahaiwritings..."
  (cd bahaiwritings && dolt pull origin main)
else
  echo "Cloning bahaiwritings (this takes ~2 minutes)..."
  dolt clone holywritings/bahaiwritings
fi

# Generate Hugo data files from Dolt
echo "Generating data files..."
go run scripts/gen_hugo_data.go --dolt-dir ./bahaiwritings --out-dir .

# Build Hugo site
echo "Building Hugo site..."
hugo --minify
