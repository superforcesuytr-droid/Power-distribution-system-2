#!/bin/sh
# Picks the build matching this Mac's processor. Apple Silicon reports arm64,
# Intel reports x86_64.
DIR=$(cd "$(dirname "$0")" && pwd)
case "$(uname -m)" in
  arm64) exec "$DIR/pds-arm64" "$@" ;;
  *)     exec "$DIR/pds-amd64" "$@" ;;
esac
