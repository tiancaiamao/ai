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


def extract_change_plan(assistant_texts: list) -> dict | None:
    """Extract Change Plan from planner's assistant messages."""
    full_text = "\n".join(assistant_texts)

    # Match the Change Plan block: from "## Change Plan" to the next ## heading or end
    pattern = r'## Change Plan\s*\n((?:[-*]\s*\*\*\w+[^)]*\*\*:.*\n?)+)'
    match = re.search(pattern, full_text)
    if not match:
        return None

    block = match.group(1)
    result = {}

    # Parse each line
    for line in block.strip().split('\n'):
        line = line.strip().lstrip('-*').strip()
        if line.startswith('**Target**:'):
            result['target'] = line.split('**Target**:')[1].strip()
        elif line.startswith('**Predicted fixes**:'):
            val = line.split('**Predicted fixes**:')[1].strip()
            if val.lower() in ('none expected', 'none', ''):
                result['predicted_fixes'] = []
            else:
                result['predicted_fixes'] = [t.strip() for t in val.split(',') if t.strip()]
        elif line.startswith('**Predicted risks**:'):
            val = line.split('**Predicted risks**:')[1].strip()
            if val.lower() in ('none expected', 'none', ''):
                result['predicted_risks'] = []
            else:
                result['predicted_risks'] = [t.strip() for t in val.split(',') if t.strip()]
        elif line.startswith('**Rationale**:'):
            result['rationale'] = line.split('**Rationale**:')[1].strip()
        elif line.startswith('**Change description**:'):
            result['change_description'] = line.split('**Change description**:')[1].strip()

    return result if result else None


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
    # the formal Change Plan format, synthesize one from tool_edits + text analysis
    if change_plan is None and edited_files:
        # Try to extract predictions from text
        predicted_fixes = []
        predicted_risks = []
        rationale = f'Planner edited: {", ".join(sorted(edited_files))}'

        # Look for prediction/expectation patterns in text
        for line in all_text.split('\n'):
            line = line.strip()
            if 'expected' in line.lower() or 'prediction' in line.lower() or 'should pass' in line.lower():
                predicted_fixes.append(line[:100])
            if 'risk' in line.lower() or 'regression' in line.lower():
                predicted_risks.append(line[:100])

        # Use first meaningful text line as rationale
        for line in all_text.split('\n'):
            line = line.strip()
            if line and len(line) > 30 and not line.startswith('#') and not line.startswith('['):
                rationale = line[:200]
                break

        change_plan = {
            'target': sorted(edited_files)[0] if len(edited_files) == 1 else 'multiple',
            'predicted_fixes': predicted_fixes[:5],
            'predicted_risks': predicted_risks[:5],
            'rationale': rationale,
            'change_description': f'Edited {", ".join(sorted(edited_files))}'
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