#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
go build -o code .
echo "built: $(pwd)/code"
