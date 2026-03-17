#!/usr/bin/env bash
# Cloudflare Pages build script
# Installs Dolt, clones DB, generates data files, builds Hugo site
set -e

# Install Dolt binary directly (install.sh requires root; downloading binary doesn't)
mkdir -p "$HOME/bin"
export PATH="$HOME/bin:$HOME/.local/bin:$PATH"
if ! command -v dolt &>/dev/null; then
  echo "Installing Dolt..."
  curl -fsSL "https://github.com/dolthub/dolt/releases/latest/download/dolt-linux-amd64.tar.gz" \
    | tar -xz --strip-components=2 -C "$HOME/bin" dolt-linux-amd64/bin/dolt
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

# Install fonttools for font subsetting (reduces embedded font size in PDFs significantly)
pip install fonttools 2>/dev/null || pip3 install fonttools 2>/dev/null || true
echo "fonttools: $(pyftsubset --version 2>&1 || echo 'not available (PDFs will use full fonts)')"

# Download additional Noto fonts for non-Latin scripts (CJK, Indic, SE Asian, etc.)
# These are too large to keep in the repo but essential for the Asian/Other PDF.
NOTO_BASE="https://github.com/notofonts/notofonts.github.io/raw/main/fonts"
mkdir -p fonts
download_font() {
  local file="$1" url="$2"
  [ -f "fonts/$file" ] && return
  echo "  Downloading $file..."
  curl -fsSL "$url" -o "fonts/$file" || echo "  Warning: failed to download $file (skipping)"
}
# CJK: variable TTF uses TrueType (glyf) outlines, unlike the static OTF (CFF)
# gofpdf only supports TrueType outlines, so we use the variable TTF here.
# pyftsubset creates a subset; gofpdf uses the glyf table and ignores gvar.
download_font "NotoSerifCJKsc-VF.ttf" \
  "https://github.com/googlefonts/noto-cjk/raw/main/Serif/Variable/TTF/NotoSerifCJKsc-VF.ttf"
# Indic scripts
download_font "NotoSerifDevanagari-Regular.ttf" \
  "$NOTO_BASE/NotoSerifDevanagari/full/ttf/NotoSerifDevanagari-Regular.ttf"
download_font "NotoSerifBengali-Regular.ttf" \
  "$NOTO_BASE/NotoSerifBengali/full/ttf/NotoSerifBengali-Regular.ttf"
download_font "NotoSerifTamil-Regular.ttf" \
  "$NOTO_BASE/NotoSerifTamil/full/ttf/NotoSerifTamil-Regular.ttf"
download_font "NotoSerifTelugu-Regular.ttf" \
  "$NOTO_BASE/NotoSerifTelugu/full/ttf/NotoSerifTelugu-Regular.ttf"
download_font "NotoSerifMalayalam-Regular.ttf" \
  "$NOTO_BASE/NotoSerifMalayalam/full/ttf/NotoSerifMalayalam-Regular.ttf"
download_font "NotoSerifKannada-Regular.ttf" \
  "$NOTO_BASE/NotoSerifKannada/full/ttf/NotoSerifKannada-Regular.ttf"
download_font "NotoSerifGujarati-Regular.ttf" \
  "$NOTO_BASE/NotoSerifGujarati/full/ttf/NotoSerifGujarati-Regular.ttf"
download_font "NotoSerifGurmukhi-Regular.ttf" \
  "$NOTO_BASE/NotoSerifGurmukhi/full/ttf/NotoSerifGurmukhi-Regular.ttf"
# Southeast Asian
download_font "NotoSerifThai-Regular.ttf" \
  "$NOTO_BASE/NotoSerifThai/full/ttf/NotoSerifThai-Regular.ttf"
download_font "NotoSerifLao-Regular.ttf" \
  "$NOTO_BASE/NotoSerifLao/full/ttf/NotoSerifLao-Regular.ttf"
download_font "NotoSerifKhmer-Regular.ttf" \
  "$NOTO_BASE/NotoSerifKhmer/full/ttf/NotoSerifKhmer-Regular.ttf"
# Other
download_font "NotoSerifHebrew-Regular.ttf" \
  "$NOTO_BASE/NotoSerifHebrew/full/ttf/NotoSerifHebrew-Regular.ttf"
download_font "NotoSerifEthiopic-Regular.ttf" \
  "$NOTO_BASE/NotoSerifEthiopic/full/ttf/NotoSerifEthiopic-Regular.ttf"
echo "Fonts in fonts/: $(ls fonts/*.ttf fonts/*.otf 2>/dev/null | wc -l) files"

# Generate per-language PDFs and EPUBs using gofpdf (pure Go, ~2 min for all languages)
# NotoSerif + NotoNaskhArabic in repo; other scripts downloaded above.
echo "Generating prayer book PDFs and EPUBs..."
mkdir -p static/downloads
go run scripts/gen_pdf.go \
  --db ./bahaiwritings \
  --lang all \
  --both \
  --font-dir ./fonts \
  --out-dir ./static/downloads \
  --phelps-base-url /phelps/

# Generate combined all-languages PDFs and EPUB (Latin/European + Asian/Other + all)
echo "Generating combined all-languages PDF and EPUB..."
go run scripts/gen_pdf.go \
  --db ./bahaiwritings \
  --lang all \
  --combined \
  --both \
  --font-dir ./fonts \
  --out-dir ./static/downloads \
  --phelps-base-url /phelps/

# Generate Short Obligatory Prayer in all languages
echo "Generating Short Obligatory Prayer (BH11209) all-languages document..."
go run scripts/gen_pdf.go \
  --db ./bahaiwritings \
  --phelps-only BH11209 \
  --both \
  --font-dir ./fonts \
  --out-dir ./static/downloads \
  --phelps-base-url /phelps/

echo "PDF/EPUB generation complete: $(ls static/downloads/*.pdf 2>/dev/null | wc -l) PDFs"

# Build Hugo site
echo "Building Hugo site..."
hugo --minify
