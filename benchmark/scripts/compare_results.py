#!/usr/bin/env python3
"""
Compare two benchmark result files and show differences.
Usage: python3 compare_results.py baseline.json current.json
"""

import json
import sys


def main():
    if len(sys.argv) < 3:
        print("Usage: python3 compare_results.py baseline.json current.json")
        sys.exit(1)

    baseline_file = sys.argv[1]
    current_file = sys.argv[2]

    try:
        with open(baseline_file) as f:
            baseline = json.load(f)
    except Exception as e:
        print(f"Error reading baseline: {e}")
        sys.exit(1)

    try:
        with open(current_file) as f:
            current = json.load(f)
    except Exception as e:
        print(f"Error reading current: {e}")
        sys.exit(1)

    print()
    print("=" * 60)
    print("Benchmark Comparison")
    print("=" * 60)
    print()
    print(f"Baseline: {baseline.get('timestamp', 'N/A')} ({baseline.get('git_commit', 'unknown')})")
    print(f"Current:  {current.get('timestamp', 'N/A')} ({current.get('git_commit', 'unknown')})")
    print()
    print("         Baseline    Current     Diff")
    print("         --------    -------     ----")

    baseline_rate = baseline.get('pass_rate', 0) or 0
    current_rate = current.get('pass_rate', 0) or 0
    print(f"Pass Rate: {baseline_rate:>5.1f}%    {current_rate:>5.1f}%    {current_rate - baseline_rate:+.1f}%")

    if 'functional_pass_rate' in baseline or 'functional_pass_rate' in current:
        b = baseline.get('functional_pass_rate', 0) or 0
        c = current.get('functional_pass_rate', 0) or 0
        print(f"Functional:{b:>5.1f}%    {c:>5.1f}%    {c - b:+.1f}%")

    if 'agentic_pass_rate' in baseline or 'agentic_pass_rate' in current:
        b = baseline.get('agentic_pass_rate', 0) or 0
        c = current.get('agentic_pass_rate', 0) or 0
        print(f"Agentic:   {b:>5.1f}%    {c:>5.1f}%    {c - b:+.1f}%")

    if 'avg_agentic_score' in baseline or 'avg_agentic_score' in current:
        b = baseline.get('avg_agentic_score', 0) or 0
        c = current.get('avg_agentic_score', 0) or 0
        print(f"Agt Score: {b:>5.1f}      {c:>5.1f}      {c - b:+.1f}")

    baseline_passed = baseline.get('passed', 0) or 0
    current_passed = current.get('passed', 0) or 0
    print(f"Passed:    {baseline_passed:>5d}      {current_passed:>5d}      {current_passed - baseline_passed:+d}")

    baseline_failed = baseline.get('failed', 0) or 0
    current_failed = current.get('failed', 0) or 0
    print(f"Failed:    {baseline_failed:>5d}      {current_failed:>5d}      {current_failed - baseline_failed:+d}")

    # Check for regressions and improvements
    baseline_map = {}
    for r in baseline.get('results', []) or []:
        baseline_map[r['task_id']] = r.get('passed', False)

    regressions = []
    improvements = []

    for r in current.get('results', []) or []:
        task_id = r['task_id']
        current_passed = r.get('passed', False)
        old_passed = baseline_map.get(task_id)

        if old_passed is not None and old_passed != current_passed:
            if current_passed:
                improvements.append(task_id)
            else:
                regressions.append(task_id)

    print()
    if regressions:
        print("⚠️  REGRESSIONS:")
        for t in regressions:
            print(f"  - {t}")
    if improvements:
        print("✅ IMPROVEMENTS:")
        for t in improvements:
            print(f"  - {t}")
    if not regressions and not improvements:
        print("✅ No changes detected")

    print()
    print("=" * 60)

    if regressions:
        sys.exit(1)


if __name__ == "__main__":
    main()
