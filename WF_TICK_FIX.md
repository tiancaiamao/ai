# wf-tick fix

The fix was applied to ~/.aiclaw/cron/jobs.json to ensure wf-tick always reads the latest registry.json, config.json, and running.json files.

## Changes made
- Added 'CRITICAL: NEVER use session memory or context' instruction
- Added explicit cat commands to read three config files
- Added 'Do NOT proceed until you have read all three files' enforcement
