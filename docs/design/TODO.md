# ag Tool Bugs

## 1. claim-lock not cleaned on agent removal

- `ag agent rm <id>` removes the agent but leaves `.ag/tasks/<id>/.claim-lock` behind
- Subsequent `ag task claim` fails with "already claimed by another process"
- Workaround: `find .ag/tasks -name '.claim-lock' -delete`
- Fix: `ag agent rm` (or `ag task done`) should clean up `.claim-lock`

## 2. ai serve processes not killed on agent removal

- `ag agent rm` marks agent as removed but doesn't kill the underlying `ai serve` process
- Orphaned `ai serve` processes accumulate, holding resources
- Fix: `ag agent rm` should SIGTERM/SIGKILL the child process

## 3. Reviewer agent infinite read loop

- When spawned with large files, reviewer agent reads code indefinitely until timeout
- No line limit or truncation on file reads
- Fix: Add max-read constraint to reviewer prompt, or enforce in ag runtime

## 4. agent status not updating after completion

- `ag agent ls` shows `running` even after the underlying `ai serve` process has finished writing output
- `stream.log` is never created in `.ag/agents/<id>/`
- `activity.json` status stays `running` permanently
- Only `activity.json` exists — no stream.log, no stdout/stderr capture
- This also means `ag agent wait` may not detect completion correctly
- Observed with reviewer-197: output file `/tmp/review-197.json` was written at 19:24, but `ag agent ls` still showed `running` and `ag agent wait` timed out
- Root cause: ag may not be monitoring the child process PID properly