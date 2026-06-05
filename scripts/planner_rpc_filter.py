#!/usr/bin/env python3
"""
Filter ai --mode rpc JSONL output: drop high-frequency streaming events,
extract planner artifacts (tool edits, YAML, text summary) in one pass.

Usage:
    python3 prompt.py | ai --mode rpc | python3 planner_rpc_filter.py \
        --iteration N \
        --summary-output iter-N-artifacts/planner-summary.md \
        --result-output iter-N-artifacts/planner-result.json
"""

import json, sys, os, argparse, re

# Events to completely drop (high-frequency streaming chunks)
DROP_EVENTS = frozenset({
    'message_update', 'text_delta', 'thinking_delta', 'tool_call_delta'
})


def _parse_field(line: str, field: str) -> str | None:
    """Extract value from a '**Field**: value' line.

    Handles bullet-point lines like '- **Field**: value' or '* **Field**: value'
    by stripping the leading bullet (but not bold markers).
    """
    line = line.strip()
    # Strip a single leading bullet ('-', '*', or '+') followed by whitespace.
    # Do NOT use lstrip('-*') — that would eat the bold markers too.
    if line and line[0] in '-*+' and len(line) > 1 and line[1] in ' \t':
        line = line[1:].lstrip()
    marker = f'**{field}**:'
    if line.startswith(marker):
        return line[len(marker):].strip()
    return None


def _parse_task_list(val: str) -> list:
    """Parse a comma-separated or newline-separated list of task IDs.

    Rejects anything that doesn't look like a task ID (markdown headings,
    sentences, bullet points, etc.)."""
    val_clean = val.strip().lower()
    if val_clean in ('none expected', 'none', 'n/a', 'na', '', '(none)', '-') or val_clean.startswith('(') and val_clean.endswith(')'):
        return []
    items = []
    for chunk in re.split(r'[,\n]', val):
        chunk = chunk.strip().lstrip('-*').strip()
        # strip surrounding backticks / quotes
        chunk = chunk.strip('`"\'')
        if not chunk:
            continue
        # Reject obvious prose / markdown that isn't a task ID.
        # Task IDs look like: 003_refactor_duplicated_code, agent_001_forced_exploration,
        # tbench/kv-store-grpc, tbench_pypi-server, etc.
        if chunk.startswith('#'):  # markdown heading
            continue
        if chunk.startswith('**'):  # bold markdown label
            continue
        if len(chunk) > 80:  # task IDs are short
            continue
        if ' ' in chunk and not any(c in chunk for c in '/_'):
            # Prose sentence — real task IDs are underscored or slashed identifiers
            continue
        # Looks like a task ID
        items.append(chunk)
    return items


def extract_change_plan(assistant_texts: list) -> dict | None:
    """Extract Change Plan from planner's assistant messages.

    Tries (in order):
      1. Formal `## Change Plan` heading + bold-field lines (canonical format).
      2. `### Summary of changes` / `### Why these changes should work` /
         `### Expected Impact` (post-hoc summary blocks planner may write
         after tool edits).
      3. Bare bold-field lines anywhere in text (e.g. **Target**: ...).
      4. A `Target: ...` line (no bold).
    Returns None only if no Target-like line can be found.
    """
    full_text = "\n".join(assistant_texts)
    if not full_text.strip():
        return None

    # Strategy 1: formal `## Change Plan` section
    cp_match = re.search(
        r'^##\s+Change Plan\s*\n(.*?)(?=^##\s|\Z)',
        full_text, re.MULTILINE | re.DOTALL
    )

    if cp_match:
        block = cp_match.group(1)
        result = _parse_block_fields(block)
        if result and result.get('target'):
            return result

    # Strategy 2: post-hoc summary blocks (after edits)
    summary_match = re.search(
        r'^###\s+(?:Summary of changes|Change Summary|Why these changes should work|Expected Impact)\s*\n(.*?)(?=^##\s|^###\s|\Z)',
        full_text, re.MULTILINE | re.DOTALL
    )
    if summary_match:
        block = summary_match.group(1)
        result = _parse_block_fields(block)
        if result and result.get('target'):
            return result

    # Strategy 3 + 4: scan full text for any field marker.
    result = _parse_block_fields(full_text)
    if result and result.get('target'):
        return result

    return None


def _parse_block_fields(block: str) -> dict | None:
    """Parse a block of text for Target / Predicted fixes / Rationale / etc."""
    target = None
    predicted_fixes_raw = None
    predicted_risks_raw = None
    rationale = None
    change_description = None

    for line in block.split('\n'):
        if target is None:
            v = _parse_field(line, 'Target')
            if v is not None:
                target = v
                continue
        if predicted_fixes_raw is None:
            v = _parse_field(line, 'Predicted fixes')
            if v is not None:
                predicted_fixes_raw = v
                continue
        if predicted_risks_raw is None:
            v = _parse_field(line, 'Predicted risks')
            if v is not None:
                predicted_risks_raw = v
                continue
        if rationale is None:
            v = _parse_field(line, 'Rationale')
            if v is not None:
                rationale = v
                continue
        if change_description is None:
            v = _parse_field(line, 'Change description')
            if v is not None:
                change_description = v
                continue

    if target is None:
        return None

    return {
        'target': target,
        'predicted_fixes': _parse_task_list(predicted_fixes_raw or ''),
        'predicted_risks': _parse_task_list(predicted_risks_raw or ''),
        'rationale': rationale or '(no rationale provided)',
        'change_description': change_description or '(no description provided)',
    }


def main():
    parser = argparse.ArgumentParser(description='Filter planner RPC output')
    parser.add_argument('--iteration', default='?', help='Iteration number for summary header')
    parser.add_argument('--summary-output', required=True, help='Path for planner-summary.md')
    parser.add_argument('--result-output', required=True, help='Path for planner-result.json')
    args = parser.parse_args()

    text_parts = []
    tool_calls = []
    current_tool_idx = -1
    event_counts = {}

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            ev = json.loads(line)
        except Exception:
            continue

        event_type = ev.get('type', '')
        event_counts[event_type] = event_counts.get(event_type, 0) + 1

        # Drop high-frequency events entirely
        if event_type in DROP_EVENTS:
            continue

                # Extract assistant text messages from message_end (message_start has empty content)
        if event_type == 'message_end':
            msg = ev.get('message', {})
            if msg.get('role') == 'assistant':
                content = msg.get('content', '')
                if isinstance(content, list):
                    for part in content:
                        if isinstance(part, dict) and part.get('type') == 'text':
                            text = part.get('text', '').strip()
                            if text:
                                text_parts.append(text)
                elif isinstance(content, str) and content.strip():
                    text_parts.append(content.strip())

        # Track tool calls
        if event_type == 'tool_execution_start':
            tool_calls.append({
                'name': ev.get('toolName', ''),
                'args': ev.get('args', {}),
                'output': None
            })
            current_tool_idx = len(tool_calls) - 1

        if event_type == 'tool_execution_end':
            if current_tool_idx >= 0 and tool_calls[current_tool_idx].get('output') is None:
                tool_calls[current_tool_idx]['output'] = ev.get('output', '')

    # ─── Extract tool edits (harness file modifications) ───
    harness_basenames = {'system_prompt.md', 'memory.md', 'context_management.md', 'agent.yaml'}
    edited_files = set()
    for tc in tool_calls:
        if tc['name'] in ('write', 'edit'):
            path = tc['args'].get('path', '')
            basename = path.rsplit('/', 1)[-1] if '/' in path else path
            if basename in harness_basenames:
                edited_files.add(basename)

    # ─── Extract YAML config from text output ───
    yaml_config = None
    all_text = '\n'.join(text_parts)

    # Try ```yaml ... ``` or ```yml ... ```
    m = re.search(r'```ya?ml\n(.*?)```', all_text, re.DOTALL)
    if m:
        yaml_config = m.group(1).strip()
    else:
        # Fallback: any code block that looks like YAML
        m = re.search(r'```\n(.*?)```', all_text, re.DOTALL)
        if m:
            content = m.group(1).strip()
            if ':' in content and '\n' in content:
                yaml_config = content

                # ─── Extract Change Plan from text output ───
    change_plan = extract_change_plan(text_parts)

    # Fallback: if planner didn't use the formal format but clearly decided no changes,
    # synthesize a no-change plan from the text
    if change_plan is None and text_parts:
        all_text_lower = all_text.lower()
        no_change_indicators = ['no change', 'no changes', 'no edits', 'not making', "won't change",
                                'no modifications', 'verdict: no change', 'no further changes',
                                'configuration is at a local maximum', 'do not need to change']
        if any(ind in all_text_lower for ind in no_change_indicators):
            # Extract first sentence or two as rationale
            rationale = 'Planner decided no changes needed.'
            for line in all_text.split('\n'):
                line = line.strip()
                if line and len(line) > 20 and not line.startswith('#') and not line.startswith('-'):
                    rationale = line[:200]
                    break
            change_plan = {
                'target': 'none',
                'predicted_fixes': [],
                'predicted_risks': [],
                'rationale': rationale,
                'change_description': 'No changes'
            }

            # Fallback 2: if planner made actual changes (tool_edits non-empty) but didn't use
    # the formal Change Plan format, synthesize a minimal change plan. We DO NOT try
    # to guess predicted_fixes/risks from free text — that produces garbage that
    # pollutes the attribution eval. Leave them empty and flag it.
    if change_plan is None and edited_files:
        rationale = f'Planner edited: {", ".join(sorted(edited_files))} (no formal Change Plan block found)'
        # Use first substantive line of text (if any) as a hint, but not as task IDs
        for line in all_text.split('\n'):
            line = line.strip()
            if line and len(line) > 30 and not line.startswith('#') and not line.startswith('['):
                rationale = line[:200]
                break

        change_plan = {
            'target': sorted(edited_files)[0] if len(edited_files) == 1 else 'multiple',
            'predicted_fixes': [],  # empty — we don't guess from prose
            'predicted_risks': [],  # empty — we don't guess from prose
            'rationale': rationale,
            'change_description': f'Edited {", ".join(sorted(edited_files))}',
            'extraction_warning': 'planner did not output formal Change Plan; predictions unavailable'
        }

    # ─── Build result JSON ───
    result = {
        'tool_edits': sorted(edited_files) if edited_files else [],
        'yaml_config': yaml_config,
        'text_messages': len(text_parts),
        'tool_calls': len(tool_calls),
        'has_changes': bool(edited_files) or yaml_config is not None,
        'event_counts': event_counts,
        'change_plan': change_plan,
    }

    # Ensure output directory exists
    os.makedirs(os.path.dirname(os.path.abspath(args.result_output)), exist_ok=True)

    with open(args.result_output, 'w') as f:
        json.dump(result, f, indent=2)
    print(f'[planner-filter] Result: {len(tool_calls)} tool calls, {len(edited_files)} harness edits, '
          f'yaml={yaml_config is not None}, text_msgs={len(text_parts)}')

    # ─── Build planner-summary.md ───
    lines = [f'# Planner Response — Iteration {args.iteration}', '']

    if text_parts:
        lines.append('## Text Output')
        lines.append('')
        for part in text_parts:
            lines.append(part)
        lines.append('')
    else:
        lines.append('## Text Output')
        lines.append('(No text output — planner used only tool calls)')
        lines.append('')

    if tool_calls:
        lines.append(f'## Tool Calls ({len(tool_calls)})')
        lines.append('')
        for i, tc in enumerate(tool_calls, 1):
            name = tc['name']
            tool_args = tc['args']

            if name in ('write', 'edit'):
                path = tool_args.get('path', '?')
                lines.append(f'{i}. **{name}** → `{path}`')
                if name == 'write':
                    content = tool_args.get('content', '')
                    if content:
                        lines.append(f'   - Content ({len(content)} chars): {content[:200]}...')
                elif name == 'edit':
                    old = tool_args.get('oldText', '')
                    new = tool_args.get('newText', '')
                    lines.append(f'   - oldText: {old[:100]}...' if len(old) > 100 else f'   - oldText: {old}')
                    lines.append(f'   - newText: {new[:100]}...' if len(new) > 100 else f'   - newText: {new}')
            elif name == 'read':
                path = tool_args.get('path', '?')
                lines.append(f'{i}. **read** → `{path}`')
            elif name == 'bash':
                cmd = tool_args.get('command', '?')
                lines.append(f'{i}. **bash**: `{cmd[:120]}`')
            elif name == 'grep':
                pattern = tool_args.get('pattern', '?')
                path = tool_args.get('path', '?')
                lines.append(f'{i}. **grep** `{pattern}` in `{path}`')
            else:
                lines.append(f'{i}. **{name}**: {json.dumps(tool_args)[:120]}')
        lines.append('')

    # Add event statistics
    lines.append('## Event Statistics')
    lines.append('')
    lines.append(f'- Total events received: {sum(event_counts.values())}')
    lines.append(f'- Dropped (streaming): {sum(event_counts.get(e, 0) for e in DROP_EVENTS)}')
    lines.append(f'- Retained: {sum(v for k, v in event_counts.items() if k not in DROP_EVENTS)}')
    lines.append('')

    os.makedirs(os.path.dirname(os.path.abspath(args.summary_output)), exist_ok=True)
    with open(args.summary_output, 'w') as f:
        f.write('\n'.join(lines))
    print(f'[planner-filter] Summary written to {args.summary_output}')


if __name__ == '__main__':
    main()