#!/usr/bin/env sh
# One command to take the latest code and leave a working app in /Applications:
#   make update              - version stamped from git
#   make update VERSION=6.0.0 - version stamped by hand, to check in the app's
#                               gear panel that this is the build you are running
# Safe to re-run. It never touches your own uncommitted work: if you have any,
# it says so and stops rather than pulling over the top of it.
set -eu
cd "$(dirname "$0")/.."
VERSION="${1:-$(git describe --tags --always 2>/dev/null || echo dev)}"
APP="/Applications/PowerDistributionSystem.app"

if [ -n "$(git status --porcelain)" ]; then
  echo "You have changes that are not committed. Commit or stash them first:"
  git status --short
  exit 1
fi

BRANCH=$(git rev-parse --abbrev-ref HEAD)
echo "Fetching $BRANCH..."
git pull --ff-only origin "$BRANCH"

echo "Building $VERSION..."
build/build-macos.sh "$VERSION"

# A running copy holds nothing open once it has quit, so the old one goes first.
osascript -e 'quit app "PowerDistributionSystem"' >/dev/null 2>&1 || true
rm -rf "$APP"
cp -R dist/PowerDistributionSystem.app "$APP"

echo ""
echo "Installed $VERSION in /Applications."
echo "Open it from Applications - the gear icon shows the version, which should read $VERSION."
