#!/usr/bin/env bash
# Cloudflare Pages build script
# Install Dolt, clone DB, generate data, build Hugo
set -e

# Install Dolt if not present
if ! command -v dolt &>/dev/null; then
  echo "Installing Dolt..."
  curl -L https://github.com/dolthub/dolt/releases/latest/download/install.sh | bash
  export PATH="$HOME/bin:$PATH"
fi

# Clone or pull the prayer database
if [ -d "bahaiwritings" ]; then
  echo "Pulling latest bahaiwritings..."
  (cd bahaiwritings && dolt pull origin main)
else
  echo "Cloning bahaiwritings..."
  dolt clone holywritings/bahaiwritings
fi

# Generate Hugo data files from Dolt
echo "Generating data..."
go run scripts/gen_hugo_data.go --dolt-dir ./bahaiwritings --out-dir .

# Build Hugo site
echo "Building Hugo site..."
hugo --minify
