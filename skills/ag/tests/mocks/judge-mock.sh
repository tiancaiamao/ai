#!/bin/bash
# Mock: judge — approves if input contains "WORKED:", otherwise rejects
# Usage: judge-mock.sh <input-file>
if grep -q "WORKED:" "$1"; then
    echo "APPROVED: the work looks good"
else
    echo "REJECTED: missing WORKED prefix, try again"
fi