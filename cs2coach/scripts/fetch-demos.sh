#!/usr/bin/env bash
# Convenience wrapper around `python3 -m learn.demo_fetch`.
# Usage:
#   ./scripts/fetch-demos.sh --help
#   ./scripts/fetch-demos.sh renown --leaderboard 10 --limit 5
#   PYTHON=learn/.venv/bin/python ./scripts/fetch-demos.sh 5e --user <id>
set -euo pipefail
cd "$(dirname "$0")/.."
exec "${PYTHON:-python3}" -m learn.demo_fetch "$@"
