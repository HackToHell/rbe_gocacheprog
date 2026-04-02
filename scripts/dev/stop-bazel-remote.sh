#!/usr/bin/env bash
set -euo pipefail

echo "Stopping bazel-remote..."
docker stop bazel-remote 2>/dev/null || echo "bazel-remote is not running"
