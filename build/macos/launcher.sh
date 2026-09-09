#!/bin/sh
# Bundle entry point.
#
# This script starts the real binary detached and exits straight away, on
# purpose. macOS asks a running application to come to the front when its icon
# is opened again, and answers to that request come from an AppKit event loop.
# This application has no such loop - its window belongs to the browser hosting
# the interface - so a long-lived process here makes macOS report "the
# application is not responding" on every later launch. Exiting immediately
# means there is never such a process; the already-running copy is found by the
# binary itself, which reopens its window and quits.
DIR=$(cd "$(dirname "$0")" && pwd)
case "$(uname -m)" in
  arm64) BIN="$DIR/pds-arm64" ;;
  *)     BIN="$DIR/pds-amd64" ;;
esac
nohup "$BIN" "$@" >/dev/null 2>&1 &
exit 0
