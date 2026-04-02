#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DATA_DIR="$PROJECT_ROOT/.tmp/bazel-remote"

mkdir -p "$DATA_DIR"

echo "Starting bazel-remote..."
echo "  Data dir: $DATA_DIR"
echo "  HTTP:     localhost:9090"
echo "  gRPC:     localhost:9092"

docker run --rm --name bazel-remote \
  -u "$(id -u):$(id -g)" \
  -v "$DATA_DIR:/data" \
  -p 9090:8080 -p 9092:9092 \
  quay.io/bazel-remote/bazel-remote \
  --max_size 5
