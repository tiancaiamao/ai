#!/usr/bin/env python3
"""Build rich planner context from evolve history, baseline, and current results.

Usage:
    build_planner_context.py \
        --baseline BASELINE_JSON \
        --current-result RESULT_JSON \
        --config-yaml CONFIG_YAML \
        --state STATE_JSON \
        --task-history TASK_HISTORY_JSON \
        --attribution ATTRIBUTION_JSON \
        --template TEMPLATE_MD \
        --output OUTPUT_FILE

Generates a formatted Markdown planner input by filling template placeholders.
"""

import argparse
import json
import os
import sys
from pathlib import Path


def load_json(path, default=None):
    """Load JSON file, return default on error."""
    if path is None:
        return default
    try:
        with open(path) as f:
            return json.load(f)
    except (FileNotFoundError, json.JSONDecodeError):
        return default


def build_current_overview(result_data):
    """Build 'Current Iteration Overview' section."""
    if result_data is None:
        return "(No previous iteration results — this is the first iteration.)"

    results = result_data.get("results", [])
    n_pass = sum(1 for r in results if r.get("passed"))
    n_total = len(results)
    rate = n_pass / max(n_total, 1) * 100

    # Use pass@1 when k>1, fall back to computed pass_rate
    k = result_data.get("k", 1)
    if k > 1 and "pass_at_1" in result_data:
        pass_at_1 = result_data["pass_at_1"]
        # pass_at_1 is a float 0..1, convert to percentage
        if isinstance(pass_at_1, (int, float)) and pass_at_1 <= 1.0:
            rate_display = pass_at_1 * 100
        else:
            rate_display = pass_at_1
        lines = [
            f"- **Pass@1 rate** (k={k}): {rate_display:.1f}%",
            f"- **Raw pass rate**: {rate:.1f}% ({n_pass}/{n_total})",
            f"- **Pass**: {n_pass} tasks",
            f"- **Fail**: {n_total - n_pass} tasks",
        ]
    else:
        lines = [
            f"- **Pass rate**: {rate:.1f}% ({n_pass}/{n_total})",
            f"- **Pass**: {n_pass} tasks",
            f"- **Fail**: {n_total - n_pass} tasks",
        ]
    return "\n".join(lines)


def build_task_classification(result_data):
    """Build 'Task Classification' section grouping tasks by pass/fail."""
    if result_data is None:
        return "(No results available.)"

    results = result_data.get("results", [])
    passed = sorted(r["task_id"] for r in results if r.get("passed"))
    failed = sorted(r["task_id"] for r in results if not r.get("passed"))

    lines = ["### Passed Tasks (" + str(len(passed)) + ")"]
    for t in passed:
        lines.append(f"- ✅ {t}")

    lines.append(f"\n### Failed Tasks ({len(failed)})")
    for t in failed:
        lines.append(f"- ❌ {t}")

    return "\n".join(lines)


def build_failure_details(result_data, rollout_data=None):
    """Build 'Failure Analysis' section with per-task trajectory, error, and final message."""
    if result_data is None:
        return "(No result data available for failure analysis.)"

    results = result_data.get("results", [])
    failed = [r for r in results if not r.get("passed")]

    if not failed:
        return "No failed tasks — all tasks passed. ✅"

    lines = []
    for r in failed:
        task_id = r.get("task_id", "unknown")
        duration = r.get("duration_seconds", 0)
        trajectory = r.get("trajectory", [])
        error = r.get("error", "")
        final_message = r.get("final_message", "")
        agent_output = r.get("agent_output", "")
        tools_used = r.get("tools_used", [])

        lines.append(f"### ❌ {task_id}")
        rollout_detail = ""
        if rollout_data and task_id in rollout_data:
            rd = rollout_data[task_id]
            rollout_detail = f" | **Rollouts**: {rd['n_pass']}/{rd['total']} passed"
        lines.append(f"**Duration**: {duration:.1f}s | **Tools**: {', '.join(tools_used) if tools_used else 'none'} | **Turns**: {len(trajectory)}{rollout_detail}")

        # Trajectory summary with enriched data
        if trajectory:
            lines.append("")
            lines.append("**Trajectory**:")
            if len(trajectory) > 20:
                shown = trajectory[:10]
                shown.append({"turn": "...", "tool": "...", "args_summary": "...", "result_summary": f"({len(trajectory) - 20} turns omitted)", "duration_s": 0})
                shown.extend(trajectory[-10:])
            else:
                shown = trajectory

            for step in shown:
                turn = step.get("turn", "?")
                tool = step.get("tool", "?")
                args = step.get("args_summary", "")[:300]
                result = step.get("result_summary", "")[:300]
                dur = step.get("duration_s", 0)
                line = f"  Turn {turn}: {tool}({args}) → {result} ({dur:.1f}s)"
                lines.append(line)
                # Show thinking before this tool call
                thinking = step.get("thinking_before", "")
                if thinking:
                    lines.append(f"    💭 {thinking[:300]}")
                # Show tool error
                tool_err = step.get("error", "")
                if tool_err:
                    lines.append(f"    ⚠️ Error: {tool_err[:300]}")

        # Error
        if error:
            lines.append("")
            err_display = error[:500]
            lines.append(f"**Error**: {err_display}")

        # Final message
        if final_message:
            lines.append("")
            msg_display = final_message[:500]
            lines.append(f"**Agent's final message**: {msg_display}")

        lines.append("")

    return "\n".join(lines)


def build_debugger_analysis(debugger_data):
    """Build 'AI Debugger Analysis' section from pre-computed debugger output.

    Args:
        debugger_data: dict loaded from debugger-analysis-*.json, or None.

    Returns:
        Formatted markdown string for the planner input template.
    """
    if debugger_data is None:
        return "(No debugger analysis available — first iteration or no failed tasks.)"

    lines = []

    # Top-level summary
    summary = debugger_data.get("summary", "")
    if summary:
        lines.append(f"**Summary:** {summary}")
        lines.append("")

    # Risk prediction (if available)
    risks = debugger_data.get("risks")
    if risks:
        lines.append("### 🚨 Predicted Risks")
        lines.append("")
        description = risks.get("description", "")
        if description:
            lines.append(f"**Risk Description:** {description}")
            lines.append("")
        
        affected_tasks = risks.get("affected_tasks", [])
        if affected_tasks:
            lines.append(f"**Affected Tasks:**")
            for task in affected_tasks:
                lines.append(f"- {task}")
            lines.append("")
        
        confidence = risks.get("confidence", "")
        if confidence:
            lines.append(f"**Confidence:** {confidence}")
            lines.append("")
    elif risks is None:
        lines.append("### 🚨 Risk Analysis")
        lines.append("")
        lines.append("*(Debugger did not identify specific risks)*")
        lines.append("")
    else:
        lines.append("### 🚨 Risk Analysis")
        lines.append("")
        lines.append("*(No risks identified)*")
        lines.append("")

    # Per-task analysis (from agent_debugger.py "ask" output)
    answer = debugger_data.get("answer", "")
    if answer:
        lines.append("### Detailed Analysis")
        lines.append("")
        # answer may be multi-line markdown from the LLM
        lines.append(str(answer))
        lines.append("")

    # Structured analysis (from agent_debugger.py "check" output)
    issues = debugger_data.get("issues", [])
    if issues:
        lines.append(f"### Detected Issues ({len(issues)})")
        lines.append("")
        for i, issue in enumerate(issues, 1):
            # Support both check mode (issue_type/summary/trace_id) and ask mode (root_cause/suggestion)
            trace_id = issue.get("trace_id", issue.get("task_id", "unknown"))
            # Strip rollout suffix for cleaner display
            task_name = trace_id.replace("-rollout-0", "").replace("-rollout-", "/")
            issue_type = issue.get("issue_type", "")
            summary = issue.get("summary", "")
            root_cause = issue.get("root_cause", "")
            pattern = issue.get("pattern", "")
            suggestion = issue.get("suggestion", "")
            confidence = issue.get("confidence", "")

            # Build display: prefer root_cause (ask mode), then summary (check mode)
            display = root_cause or summary or pattern or "(no detail)"

            lines.append(f"{i}. **{task_name}** [{issue_type or 'issue'}]: {display}")
            if suggestion:
                lines.append(f"   - Suggestion: {suggestion}")
            if confidence:
                lines.append(f"   - Confidence: {confidence}")
        lines.append("")

    # Metadata
    metadata = debugger_data.get("metadata", {})
    if metadata:
        model = metadata.get("model", "")
        n_traces = metadata.get("n_traces", "")
        if model or n_traces:
            lines.append(f"_Analysis model: {model} | Traces analyzed: {n_traces}_")

    if not lines:
        return "(Debugger analysis produced no output.)"

    return "\n".join(lines)


def build_cross_iteration_changes(baseline_data, current_data):
    """Build 'Cross-Iteration Changes' section comparing baseline vs current."""
    if baseline_data is None or current_data is None:
        return "(No comparison available — first iteration.)"

    baseline_results = {r["task_id"]: r.get("passed", False) for r in baseline_data.get("results", [])}
    current_results = {r["task_id"]: r.get("passed", False) for r in current_data.get("results", [])}

    all_tasks = sorted(set(baseline_results) | set(current_results))

    flipped = []      # fail -> pass
    regressed = []    # pass -> fail
    stable_pass = []  # pass -> pass
    stable_fail = []  # fail -> fail

    for task in all_tasks:
        b = baseline_results.get(task, False)
        c = current_results.get(task, False)
        if b and c:
            stable_pass.append(task)
        elif not b and not c:
            stable_fail.append(task)
        elif not b and c:
            flipped.append(task)
        elif b and not c:
            regressed.append(task)

    lines = [
        f"### ✅ Flipped fail→pass ({len(flipped)})",
    ]
    for t in flipped:
        lines.append(f"- {t}")

    lines.append(f"\n### 🔴 Regressed pass→fail ({len(regressed)})")
    for t in regressed:
        lines.append(f"- {t}")

    lines.append(f"\n### 🛡️ Stable pass ({len(stable_pass)})")
    for t in stable_pass:
        lines.append(f"- {t}")

    lines.append(f"\n### 📌 Stable fail ({len(stable_fail)})")
    for t in stable_fail:
        lines.append(f"- {t}")

    lines.append(f"\n- **Net change**: {len(flipped) - len(regressed):+d}")

    b_rate = sum(1 for v in baseline_results.values() if v) / max(len(baseline_results), 1) * 100
    c_rate = sum(1 for v in current_results.values() if v) / max(len(current_results), 1) * 100
    lines.append(f"- **Pass rate change**: {b_rate:.1f}% → {c_rate:.1f}% ({c_rate - b_rate:+.1f}pp)")

    return "\n".join(lines)


def build_historical_trends(state_data):
    """Build 'Historical Trends' section with per-iteration pass rate."""
    if state_data is None:
        return "(No historical data.)"

    history = state_data.get("history", [])
    if not history:
        return "(No iterations recorded yet.)"

    lines = ["| Iter | Description | Pass Rate | Delta |", "|------|-------------|-----------|-------|"]

    prev_rate = None
    for entry in history:
        it = entry.get("iteration", "?")
        desc = entry.get("description", "")[:30]
        rate = entry.get("pass_rate", 0)
        if isinstance(rate, (int, float)) and rate <= 1.0:
            rate_pct = rate * 100
        else:
            rate_pct = rate

        if prev_rate is not None:
            delta = rate_pct - prev_rate
            delta_str = f"{delta:+.1f}pp"
        else:
            delta_str = "baseline"

        lines.append(f"| {it} | {desc} | {rate_pct:.1f}% | {delta_str} |")
        prev_rate = rate_pct

    # Add best marker
        best = state_data.get("best_pass_rate", 0)
    if isinstance(best, (int, float)) and best <= 1.0:
        best_pct = best * 100
    else:
        best_pct = best
    best_iter = state_data.get("best_iter")
    if best_iter is None:
        best_iter = state_data.get("iteration", "?")
    lines.append(f"\n**Best ever**: {best_pct:.1f}% (iteration {best_iter})")

    return "\n".join(lines)


def build_task_stability(task_history, min_iterations=2, rollout_data=None):
    """Build 'Task Stability' section classifying tasks by historical behavior."""
    if task_history is None:
        return "(No task history — first iteration.)"

    stable_pass = []
    stable_fail = []
    unstable = []
    possibly_unstable = []

    for task_name, entries in task_history.items():
        verdicts = [e[1] for e in entries if e[1] in ("pass", "fail")]

        if not verdicts:
            continue

        has_pass = "pass" in verdicts
        has_fail = "fail" in verdicts

        if has_pass and has_fail:
            if len(verdicts) >= min_iterations:
                unstable.append(task_name)
            else:
                possibly_unstable.append(task_name)
        elif has_pass and not has_fail:
            stable_pass.append(task_name)
        elif has_fail and not has_pass:
            stable_fail.append(task_name)

    lines = [
        f"### 🛡️ Stable pass ({len(stable_pass)}) — always pass, protect these",
    ]
    for t in sorted(stable_pass):
        lines.append(f"- {t}")

    lines.append(f"\n### 📌 Stable fail ({len(stable_fail)}) — always fail, need new strategy")
    for t in sorted(stable_fail):
        lines.append(f"- {t}")

    lines.append(f"\n### ⚡ Unstable ({len(unstable)}) — sometimes pass, sometimes fail, highest optimization potential")
    for t in sorted(unstable):
        detail = ""
        if rollout_data and t in rollout_data:
            rd = rollout_data[t]
            detail = f" ({rd['n_pass']}/{rd['total']} rollouts passed)"
        lines.append(f"- {t}{detail}")

    if possibly_unstable:
        lines.append(f"\n### ❓ Possibly unstable ({len(possibly_unstable)}) — insufficient data")
        for t in sorted(possibly_unstable):
            detail = ""
            if rollout_data and t in rollout_data:
                rd = rollout_data[t]
                detail = f" ({rd['n_pass']}/{rd['total']} rollouts passed)"
            lines.append(f"- {t}{detail}")

    return "\n".join(lines)


def build_strategy_history(state_data, benchmarks_dir):
    """Build 'Strategy History' section from evolve-state.json + attribution-N.json files.

    Shows all past iterations with their changes, verdicts, and warnings for repeated
    ineffective strategies.
    """
    history = state_data.get("history", [])
    if not history:
        return "(No iteration history available — first iteration.)"

    benchmarks_path = Path(benchmarks_dir)
    lines = []
    # Track change descriptions to detect repeats
    desc_outcomes = {}  # normalized description → list of (iter, verdict)

    prev_rate = None
    for entry in history:
        iter_num = entry.get("iter", entry.get("iteration", "?"))
        pass_rate = entry.get("pass_rate", 0)

        # Calculate delta from history if not explicitly stored
        delta = entry.get("delta", None)
        if delta is None and prev_rate is not None:
            delta = pass_rate - prev_rate
        elif delta is None:
            delta = 0

        prev_rate = pass_rate

        changes_desc = entry.get("changes", entry.get("description", ""))
        improvements = entry.get("improvements", [])
        regressions = entry.get("regressions", [])
        decision = entry.get("decision", entry.get("status", ""))

        # Load attribution for this iteration
        attr_path = benchmarks_path / f"attribution-{iter_num}.json"
        attr_data = load_json(str(attr_path), default=None)

        # Determine verdict from attribution
        verdict = ""
        predicted_fixes = []
        predicted_risks = []
        if attr_data:
            predicted_fixes = attr_data.get("predicted_fixes", [])
            predicted_risks = attr_data.get("predicted_risks", [])
            changes_desc = attr_data.get("changes_description", changes_desc)

            # Derive verdict from results
            pfix_set = set(predicted_fixes)
            prank_set = set(predicted_risks)
            actually_fixed = [t for t in improvements if t in pfix_set]
            risk_realized = [t for t in prank_set if t in regressions]

            if actually_fixed and not risk_realized and not regressions:
                verdict = "EFFECTIVE ✅"
            elif actually_fixed and risk_realized:
                verdict = "MIXED ⚠️"
            elif risk_realized and not actually_fixed:
                verdict = "HARMFUL 🔴"
            elif actually_fixed:
                verdict = "PARTIAL 🟡"
            else:
                verdict = "INEFFECTIVE ⚪"
        else:
            # No attribution file — derive from status/delta
            if delta > 10:
                verdict = "IMPROVEMENT 🟢"
            elif delta > 0:
                verdict = "MINOR 🟡"
            elif delta == 0:
                verdict = "NO CHANGE ⚪"
            else:
                verdict = "REGRESSION 🔴"

        # Build one-line summary
        trunc_desc = changes_desc[:120].replace("\n", " ").strip()
        if len(changes_desc) > 120:
            trunc_desc += "…"

        pred_str = f"predicted: {', '.join(predicted_fixes[:5])}" if predicted_fixes else ""
        actual_str = f"improved: {', '.join(improvements[:5])}" if improvements else ""
        regr_str = f"regressed: {', '.join(regressions[:5])}" if regressions else ""

        parts = [f"  Iter {iter_num}: {trunc_desc}"]
        parts.append(f"    {decision} ({pass_rate:.1f}%, Δ{delta:+.1f}%)")
        if pred_str:
            parts.append(f"    [{pred_str}]")
        if actual_str:
            parts.append(f"    [{actual_str}]")
        if regr_str:
            parts.append(f"    [{regr_str}]")
        parts.append(f"    → {verdict}")

        lines.append("\n".join(parts))

        # Track for repeat detection — normalize description for comparison
        if changes_desc and verdict in ("INEFFECTIVE ⚪", "NO CHANGE ⚪", "REGRESSION 🔴"):
            # Use first 80 chars as a fingerprint for repeated strategy detection
            key = changes_desc[:80].strip().lower()
            desc_outcomes.setdefault(key, []).append((iter_num, verdict))

    # Warnings for repeated ineffective strategies
    warnings = []
    for key, entries in desc_outcomes.items():
        if len(entries) >= 2:
            iters_str = ", ".join(str(i) for i, _ in entries)
            warnings.append(
                f"- ⚠️ Strategy tried {len(entries)} times (iters {iters_str}) always ineffective: "
                f"\"{key[:60]}…\""
            )

    if warnings:
        lines.append("")
        lines.append("### ⚠️ Avoid Repeating These Strategies")
        lines.extend(warnings)

    return "\n".join(lines)


def build_attribution_report(attribution_data, current_results):
    """Build 'Previous Change Attribution' section."""
    if attribution_data is None:
        return "(No previous change attribution — first iteration with attribution tracking.)"

    predicted_fixes = set(attribution_data.get("predicted_fixes", []))
    predicted_risks = set(attribution_data.get("predicted_risks", []))

    if current_results is None:
        return "(Cannot evaluate — no current results available.)"

    current_map = {r["task_id"]: r.get("passed", False) for r in current_results.get("results", [])}

    # Evaluate predicted fixes
    actually_fixed = sorted(t for t in predicted_fixes if current_map.get(t, False))
    still_failed = sorted(t for t in predicted_fixes if not current_map.get(t, False))

    # Evaluate predicted risks
    risk_realized = sorted(t for t in predicted_risks if not current_map.get(t, True))

    # Verdict
    if actually_fixed and not risk_realized and not still_failed:
        verdict = "EFFECTIVE ✅"
    elif actually_fixed and risk_realized:
        verdict = "MIXED ⚠️"
    elif risk_realized and not actually_fixed:
        verdict = "HARMFUL 🔴"
    elif actually_fixed:
        verdict = "PARTIALLY_EFFECTIVE 🟡"
    else:
        verdict = "INEFFECTIVE ⚪"

    lines = [
        f"**Verdict**: {verdict}",
        "",
        f"- **Predicted fixes**: {', '.join(sorted(predicted_fixes)) or 'none'}",
        f"- **Actually fixed**: {', '.join(actually_fixed) or 'none'}",
        f"- **Still failed**: {', '.join(still_failed) or 'none'}",
        f"- **Predicted risks**: {', '.join(sorted(predicted_risks)) or 'none'}",
        f"- **Risk realized**: {', '.join(risk_realized) or 'none'}",
    ]

    # Changes description
    changes_desc = attribution_data.get("changes_description", "")
    if changes_desc:
        lines.append(f"\n**Changes made**: {changes_desc}")

    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description="Build rich planner context")
    parser.add_argument("--baseline", required=True, help="Baseline result JSON file")
    parser.add_argument("--current-result", default=None, help="Current/latest benchmark result JSON file")
    parser.add_argument("--config-yaml", required=True, help="Current agent.yaml")
    parser.add_argument("--state", required=True, help="evolve-state.json")
    parser.add_argument("--task-history", default=None, help="task_history.json")
    parser.add_argument("--attribution", default=None, help="Previous iteration attribution JSON")
    parser.add_argument("--attribution-eval", default=None, help="Path to attribution evaluation JSON")
    parser.add_argument("--prev-iter-results", default=None, help="Previous iteration actual results")
    parser.add_argument("--debugger-analysis", default=None, help="Previous iteration debugger analysis JSON")
    parser.add_argument("--benchmarks-dir", default="agent/benchmarks/", help="Directory with attribution files")
    parser.add_argument("--agent-dir", default=None, help="Agent directory containing system_prompt.md, memory.md etc. Defaults to parent of config-yaml.")
    parser.add_argument("--template", required=True, help="Planner input template Markdown")
    parser.add_argument("--output", required=True, help="Output file")
    args = parser.parse_args()

    baseline_data = load_json(args.baseline, default=None)
    current_data = load_json(args.current_result, default=None)
    state_data = load_json(args.state, default={"iteration": 0, "history": []})
    task_history = load_json(args.task_history, default=None)
    attribution = load_json(args.attribution, default=None)
    prev_iter_results = load_json(args.prev_iter_results, default=None)
    attribution_eval = load_json(args.attribution_eval, default=None)
    debugger_analysis_data = load_json(args.debugger_analysis, default=None)

    with open(args.config_yaml) as f:
        config_yaml = f.read()

    # Read harness files from the same directory as agent.yaml.
    # If agent.yaml is a copy (e.g. in a run directory), fall back to the
    # original agent/ directory via --agent-dir.
    config_dir = os.path.dirname(os.path.abspath(args.config_yaml))

    # Resolve agent directory: explicit --agent-dir > config_dir > heuristic.
    if args.agent_dir and os.path.isdir(args.agent_dir):
        agent_dir = args.agent_dir
    else:
        # Heuristic: walk up from config_yaml looking for agent/ dir at repo root.
        repo_root = Path(args.config_yaml).resolve()
        for _ in range(5):
            repo_root = repo_root.parent
        agent_dir = str(repo_root / "agent")
        if not os.path.isdir(agent_dir):
            agent_dir = config_dir

        system_prompt_path = os.path.join(config_dir, "system_prompt.md")
    memory_path = os.path.join(config_dir, "memory.md")
    context_mgmt_path = os.path.join(config_dir, "context_management.md")

    # Use file path references instead of full content — planner can read files itself
    def _file_ref(label, path):
        if os.path.isfile(path):
            return f"File: {path} (use read tool to examine and modify)"
        return f"({label} not found)"

    system_prompt_ref = _file_ref("system_prompt.md", system_prompt_path)
    memory_ref = _file_ref("memory.md", memory_path)
    context_mgmt_ref = _file_ref("context_management.md", context_mgmt_path)

    with open(args.template) as f:
        template = f.read()

    # Build all sections
    # Extract rollout data from current results for enhanced stability/failure reporting.
        rollout_data = None
    if current_data and "per_task_rollouts" in current_data:
        rollout_data = current_data.get("per_task_rollouts", {})

    current_overview = build_current_overview(current_data)
    task_classification = build_task_classification(current_data or baseline_data)
    failure_details = build_failure_details(current_data or baseline_data, rollout_data=rollout_data)
    cross_changes = build_cross_iteration_changes(baseline_data, current_data)
    trends = build_historical_trends(state_data)
    stability = build_task_stability(task_history, rollout_data=rollout_data)
    strategy_history = build_strategy_history(state_data, args.benchmarks_dir)
    attribution_report = build_attribution_report(attribution, prev_iter_results or current_data)
    debugger_section = build_debugger_analysis(debugger_analysis_data)

    # Build attribution verdict section from evaluation JSON.
    attribution_verdict = ""
    if attribution_eval is not None:
        verdict = attribution_eval.get("verdict", "UNKNOWN")
        fix_rate = attribution_eval.get("fix_rate", "N/A")
        lines = [
            f"**Verdict: {verdict}**",
            f"**Fix rate: {fix_rate}**",
            "",
        ]
        predicted_fixes = attribution_eval.get("predicted_fixes", [])
        actually_fixed = attribution_eval.get("actually_fixed", [])
        if predicted_fixes:
            lines.append("### Predicted fixes vs actual")
            lines.append("")
            lines.append("| Task | Predicted | Actual |")
            lines.append("|------|-----------|--------|")
            for t in predicted_fixes:
                actual = "✅ FIXED" if t in actually_fixed else "❌ STILL FAIL"
                lines.append(f"| {t} | Yes | {actual} |")
            lines.append("")
        attribution_verdict = "\n".join(lines)

        # Fill template
    template = template.replace("{{CURRENT_OVERVIEW}}", current_overview)
    template = template.replace("{{TASK_CLASSIFICATION}}", task_classification)
    template = template.replace("{{FAILURE_DETAILS}}", failure_details)
    template = template.replace("{{DEBUGGER_ANALYSIS}}", debugger_section)
    template = template.replace("{{CROSS_ITERATION_CHANGES}}", cross_changes)
    template = template.replace("{{HISTORICAL_TRENDS}}", trends)
    template = template.replace("{{TASK_STABILITY}}", stability)
    template = template.replace("{{PREVIOUS_ATTRIBUTION}}", attribution_report)
    template = template.replace("{{ATTRIBUTION_VERDICT}}", attribution_verdict)
    template = template.replace("{{STRATEGY_HISTORY}}", strategy_history)
    template = template.replace("{{CURRENT_CONFIG_YAML}}", config_yaml)
    template = template.replace("{{SYSTEM_PROMPT}}", system_prompt_ref)
    template = template.replace("{{MEMORY}}", memory_ref)
    template = template.replace("{{CONTEXT_MANAGEMENT}}", context_mgmt_ref)

    with open(args.output, "w") as f:
        f.write(template)

    print(f"Planner context written to {args.output}", file=sys.stderr)


if __name__ == "__main__":
    main()
