#!/usr/bin/env python3
"""Run one planner prompt through the ACP stdio interface."""

import argparse
import json
import subprocess
import sys


def send(stdin, message):
    stdin.write(json.dumps(message) + "\n")
    stdin.flush()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--ai-bin", required=True)
    parser.add_argument("--system-prompt", required=True)
    parser.add_argument("--agent-config", required=True)
    parser.add_argument("--prompt-file", required=True)
    args = parser.parse_args()

    process = subprocess.Popen(
        [
            args.ai_bin,
            "acp",
            "--system-prompt",
            args.system_prompt,
            "--agent-config",
            args.agent_config,
        ],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=sys.stderr,
        text=True,
        bufsize=1,
    )

    with open(args.prompt_file) as prompt_file:
        prompt = prompt_file.read()
    send(process.stdin, {
        "jsonrpc": "2.0",
        "id": 1,
        "method": "initialize",
        "params": {"protocolVersion": 1, "clientCapabilities": {}},
    })
    prompt_sent = False

    for line in process.stdout:
        sys.stdout.write(line)
        sys.stdout.flush()
        try:
            message = json.loads(line)
        except json.JSONDecodeError:
            continue

        if message.get("id") == 1 and "result" in message:
            send(process.stdin, {
                "jsonrpc": "2.0",
                "id": 2,
                "method": "session/new",
                "params": {},
            })
        elif not prompt_sent and message.get("id") == 2 and "result" in message:
            session_id = message["result"].get("sessionId")
            if not session_id:
                raise RuntimeError("ACP session/new response has no sessionId")
            send(process.stdin, {
                "jsonrpc": "2.0",
                "id": 3,
                "method": "session/prompt",
                "params": {
                    "sessionId": session_id,
                    "prompt": [{"type": "text", "text": prompt}],
                },
            })
            prompt_sent = True
        elif prompt_sent and message.get("id") == 3:
            process.stdin.close()

    return process.wait()


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (BrokenPipeError, RuntimeError) as exc:
        print(f"[planner-acp] {exc}", file=sys.stderr)
        raise SystemExit(1)