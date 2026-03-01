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

# Install weasyprint (for PDF generation)
if ! command -v weasyprint &>/dev/null; then
  echo "Installing weasyprint..."
  pip install weasyprint --quiet --user 2>&1 | tail -3
fi

# Install Noto fonts for weasyprint (covers all Unicode scripts in the prayer book)
FONTS_DIR="$HOME/.local/share/fonts/noto"
mkdir -p "$FONTS_DIR"
if [ ! -f "$FONTS_DIR/.installed" ]; then
  echo "Downloading Noto fonts (~300MB, needed for Arabic, CJK, Ethiopic, Devanagari, etc.)..."
  pushd /tmp >/dev/null
  # apt-get download requires no root — just downloads .deb files to cwd
  apt-get download fonts-noto-core fonts-noto-extra fonts-noto-cjk 2>&1 | tail -5 || true
  echo "  Downloaded: $(ls fonts-noto*.deb 2>/dev/null | tr '\n' ' ' || echo 'none')"
  mkdir -p noto-extract
  for deb in fonts-noto*.deb; do
    [ -f "$deb" ] && dpkg -x "$deb" noto-extract/ && echo "  Extracted: $deb" || true
  done
  # Include .ttc/.otc (TrueType/OpenType Collections used by fonts-noto-cjk)
  FONT_COUNT=$(find noto-extract \( -name "*.ttf" -o -name "*.otf" -o -name "*.ttc" -o -name "*.otc" \) 2>/dev/null | wc -l)
  echo "  Font files found in packages: $FONT_COUNT"
  find noto-extract \( -name "*.ttf" -o -name "*.otf" -o -name "*.ttc" -o -name "*.otc" \) \
    -exec cp {} "$FONTS_DIR/" \; 2>/dev/null || true
  rm -rf noto-extract fonts-noto*.deb
  INSTALLED=$(find "$FONTS_DIR" \( -name "*.ttf" -o -name "*.otf" -o -name "*.ttc" -o -name "*.otc" \) 2>/dev/null | wc -l)
  if [ "$INSTALLED" -gt 0 ]; then
    fc-cache -f "$FONTS_DIR"
    touch "$FONTS_DIR/.installed"
    echo "Noto fonts ready: $INSTALLED font files installed"
  else
    echo "Warning: Noto font download failed — non-Latin scripts may not render correctly in PDFs"
  fi
  popd >/dev/null
fi

# Install pandoc (for EPUB generation)
PANDOC_VERSION="${PANDOC_VERSION:-3.6.4}"
if ! command -v pandoc &>/dev/null; then
  echo "Installing pandoc $PANDOC_VERSION..."
  curl -fsSL "https://github.com/jgm/pandoc/releases/download/${PANDOC_VERSION}/pandoc-${PANDOC_VERSION}-linux-amd64.tar.gz" \
    | tar -xz --strip-components=2 -C "$HOME/bin" "pandoc-${PANDOC_VERSION}/bin/pandoc"
fi
echo "Pandoc version: $(pandoc --version | head -1)"

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

# Generate PDF and EPUB downloads per language
echo "Generating PDF and EPUB downloads..."
mkdir -p static/downloads

# Per-language downloads (--both generates PDF + EPUB)
go run scripts/gen_pdf.go \
  --db ./bahaiwritings \
  --lang all \
  --both \
  --out-dir ./static/downloads \
  --phelps-base-url /phelps/

# Combined all-languages PDF and EPUB
echo "Generating combined all-languages downloads..."
go run scripts/gen_pdf.go \
  --db ./bahaiwritings \
  --lang all \
  --html-only \
  --out-dir /tmp/prayers_html

# Combine all HTML files into one big document for the "all" download
python3 - <<'PYEOF'
import os, glob, re

html_dir = "/tmp/prayers_html"
files = sorted(glob.glob(os.path.join(html_dir, "prayers_*.html")))

bodies = []
for f in files:
    content = open(f).read()
    # Extract body content between <body> and </body>
    m = re.search(r'<body[^>]*>(.*?)</body>', content, re.DOTALL)
    if m:
        bodies.append(m.group(1))

combined = """<!DOCTYPE html>
<html lang="mul">
<head>
<meta charset="UTF-8">
<title>Bahá'í Prayers — All Languages</title>
<style>
body { font-family: "Noto Serif", serif; font-size: 11pt; line-height: 1.7; }
.title-page { page-break-after: always; text-align: center; padding: 8cm 3cm; }
h1.category-header { font-size: 16pt; border-bottom: 2px solid #2c3e50; margin: 2em 0 1em; }
.prayer { page-break-inside: avoid; margin-bottom: 2em; padding-bottom: 1.5em; border-bottom: 1px solid #eee; }
.prayer-meta { font-size: 8pt; color: #aaa; font-family: monospace; }
.trans-note { font-size: 8pt; color: #bbb; font-style: italic; }
p.verse { margin-left: 1.5em; font-style: italic; }
p.note { font-size: 9pt; color: #666; }
</style>
</head>
<body>
<div class="title-page">
  <h1>Bahá'í Prayers</h1>
  <p style="font-style:italic; color:#888">All languages</p>
</div>
""" + "\n<hr style='page-break-after:always'>\n".join(bodies) + "\n</body>\n</html>"

open("/tmp/prayers_all.html", "w").write(combined)
print(f"Combined {len(files)} language files")
PYEOF

if command -v weasyprint &>/dev/null; then
  weasyprint /tmp/prayers_all.html static/downloads/prayers_all.pdf
  echo "Written: static/downloads/prayers_all.pdf"
fi

if command -v pandoc &>/dev/null; then
  pandoc --metadata title="Bahá'í Prayers — All Languages" \
    --metadata lang=mul \
    -f html -t epub --toc --toc-depth=1 \
    -o static/downloads/prayers_all.epub \
    /tmp/prayers_all.html
  echo "Written: static/downloads/prayers_all.epub"
fi

# Build Hugo site
echo "Building Hugo site..."
hugo --minify
