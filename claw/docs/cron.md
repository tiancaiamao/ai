# Cron Scheduled Tasks

AiClaw's cron feature allows you to create scheduled tasks that automatically trigger the agent at specified times.

## Quick Start

```bash
# Add a daily 9:00 reminder
aiclaw cron add -n "Morning Reminder" -m "Start a new day, check today's todos!" -c "0 9 * * *"

# View all tasks
aiclaw cron list
```

## Command Reference

### list - List Tasks

```bash
aiclaw cron list
```

Output example:
```
Scheduled Jobs:
---------------
  [b28a1f52] Morning Reminder
      Schedule: 0 9 * * *
      Message:  Start a new day, check today's todos!
      Status:   ✓ enabled
      Next run: 2026-03-03 09:00
```

### add - Add Task

```bash
aiclaw cron add -n <name> -m <message> (-e <seconds> | -c <cron-expression>)
```

| Parameter | Description |
|-----------|-------------|
| `-n, --name` | Task name (required) |
| `-m, --message` | Message to send to agent (required) |
| `-e, --every` | Interval in seconds |
| `-c, --cron` | Cron expression |

**Examples:**

```bash
# Fixed interval: every 60 seconds
aiclaw cron add -n "Heartbeat" -m "ping" -e 60

# Cron expression: every day at 9:00
aiclaw cron add -n "Morning Reminder" -m "Good morning!" -c "0 9 * * *"

# Every hour on the hour
aiclaw cron add -n "Hourly" -m "It's a new hour" -c "0 * * * *"

# Weekdays at 18:00
aiclaw cron add -n "End of Day" -m "Time to wrap up" -c "0 18 * * 1-5"
```

### remove - Delete Task

```bash
aiclaw cron remove <job_id>
```

### enable/disable - Enable/Disable Task

```bash
aiclaw cron enable <job_id>
aiclaw cron disable <job_id>
```

## Cron Expressions

Standard 5-field format:

```
┌───────────── minute (0 - 59)
│ ┌───────────── hour (0 - 23)
│ │ ┌───────────── day of month (1 - 31)
│ │ │ ┌───────────── month (1 - 12)
│ │ │ │ ┌───────────── day of week (0 - 6, 0=Sunday)
│ │ │ │ │
* * * * *
```

### Special Characters

| Character | Description |
|-----------|-------------|
| `*` | Any value |
| `,` | List, e.g., `1,3,5` |
| `-` | Range, e.g., `1-5` |
| `/` | Step, e.g., `*/15` every 15 minutes |

### Common Examples

| Expression | Description |
|------------|-------------|
| `0 9 * * *` | Every day at 9:00 |
| `30 18 * * *` | Every day at 18:30 |
| `0 */2 * * *` | Every 2 hours |
| `*/15 * * * *` | Every 15 minutes |
| `0 9 * * 1-5` | Mon-Fri at 9:00 |
| `0 0 1 * *` | First day of month at 0:00 |
| `0 9 1 1 *` | January 1st at 9:00 |

## Use Cases

### 1. Daily Reminders

```bash
# Morning reminder
aiclaw cron add -n "Morning" -m "New day started, check your schedule" -c "0 9 * * *"

# Lunch reminder
aiclaw cron add -n "Lunch" -m "Time for lunch" -c "0 12 * * *"

# End of day reminder
aiclaw cron add -n "End of Day" -m "Work done, generate daily summary" -c "0 18 * * 1-5"
```

### 2. Periodic Checks

```bash
# Check service status every 5 minutes
aiclaw cron add -n "Service Check" -m "Check if all services are healthy" -e 300

# Sync data every hour
aiclaw cron add -n "Data Sync" -m "Sync latest data" -c "0 * * * *"
```

### 3. Scheduled Reports

```bash
# Weekly report on Monday at 9:00
aiclaw cron add -n "Weekly Report" -m "Generate this week's summary" -c "0 9 * * 1"

# Monthly report on the 1st
aiclaw cron add -n "Monthly Report" -m "Generate last month's summary" -c "0 9 1 * *"
```

## How It Works

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│   jobs.json     │────>│  CronService    │────>│   AgentLoop     │
│  (task storage) │     │  (check/sec)    │     │  (process msg)  │
└─────────────────┘     └─────────────────┘     └─────────────────┘
```

1. **Storage**: Tasks saved in `~/.aiclaw/cron/jobs.json`
2. **Scheduling**: CronService starts with gateway, checks for due tasks every second
3. **Execution**: Calls `ProcessDirect()` to send message to agent when due
4. **Hot Reload**: Uses fsnotify to watch for file changes - CLI modifications take effect immediately

## Important Notes

1. **Gateway must be running**: Cron tasks only execute when gateway is running
2. **Timezone**: Uses system local timezone
3. **Persistence**: Tasks saved in JSON file, automatically restored after restart
4. **Hot Reload**: Gateway automatically detects changes to `jobs.json` - CLI modifications take effect immediately without restart
5. **Single Instance**: Avoid running multiple gateway instances simultaneously, may cause duplicate execution

## Troubleshooting

### Task Not Executing

1. Confirm gateway is running
2. Check task status is enabled
3. Check logs for `[cron]` output

### Task Execution Time Inaccurate

1. Check system timezone settings
2. Verify cron expression is correct

### View Execution History

```bash
# Task status includes last execution info
aiclaw cron list
```

Status fields:
- `Last run`: Last execution time
- `Status`: ok or error
- `Error`: Error message (if any)