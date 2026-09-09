#!/usr/bin/env sh
# Builds PowerDistributionSystem.app for macOS (Apple Silicon and Intel) plus a
# zip ready to copy to a Mac. Requires Go 1.24+; runs on macOS or Linux.
# Usage: build/build-macos.sh [version]
set -eu
cd "$(dirname "$0")/.."
VERSION="${1:-1.0.0}"
APP="dist/PowerDistributionSystem.app"

rm -rf "$APP" dist/PowerDistributionSystem-macos.zip
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

for arch in arm64 amd64; do
  GOOS=darwin GOARCH=$arch CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" \
    -o "$APP/Contents/MacOS/pds-$arch" ./cmd/pds
done

cp build/macos/launcher.sh "$APP/Contents/MacOS/PowerDistributionSystem"
chmod +x "$APP/Contents/MacOS/PowerDistributionSystem"
cp build/macos/AppIcon.icns "$APP/Contents/Resources/AppIcon.icns"
sed "s/__VERSION__/$VERSION/g" build/macos/Info.plist > "$APP/Contents/Info.plist"

# Ad-hoc sign when building on a Mac. Cross-compiled binaries already carry the
# ad-hoc signature the Go linker adds for darwin/arm64, but signing the whole
# bundle here means one less Gatekeeper prompt for the person running it.
if command -v codesign >/dev/null 2>&1; then
  codesign --force --deep --sign - "$APP" 2>/dev/null && echo "ad-hoc signed the bundle" || \
    echo "note: could not ad-hoc sign; the app will need Gatekeeper approval on first launch"
fi

(cd dist && zip -qry PowerDistributionSystem-macos.zip PowerDistributionSystem.app)
echo "Built $APP and dist/PowerDistributionSystem-macos.zip ($VERSION)"
