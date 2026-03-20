#!/bin/bash
set -e

TASK_DIR="$(cd "$(dirname "$0")" && pwd)"
if [ ! -d "$TASK_DIR/setup" ] || [ -z "$(ls -A "$TASK_DIR/setup" 2>/dev/null)" ]; then
  rm -rf "$TASK_DIR/setup"
  cp -R "$TASK_DIR/init" "$TASK_DIR/setup"
fi

cd "$TASK_DIR/setup"
go test ./... -v
