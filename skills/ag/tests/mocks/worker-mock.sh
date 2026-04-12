#!/bin/bash
# Mock: worker — transforms input by adding "WORKED:" prefix
# Usage: worker-mock.sh <input-file>
sed 's/^/WORKED: /' "$1"