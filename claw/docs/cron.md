# Cron Scheduled Tasks

AiClaw's cron feature allows you to create scheduled tasks that automatically trigger the agent at specified times.

## Quick Start

```
# Add a daily 9:00 reminder
/cron add "0 9 * * *" "Good morning! Check today's todos."

# View all tasks
/cron list
```

## Command Reference

### list - List Tasks

```
/cron list
```

Output example:
```
Cron Jobs (2):

[0] b28a1f52
    Name: Morning Reminder
    Status: enabled
    Schedule: 0 9 * * *
    Message: Good morning! Check today's todos.
    Next Run: 2026-03-04 09:00:00
```

### add - Add Task

```
/cron add <cron-expression> <message>
```

| Parameter | Description |
|-----------|-------------|
| `cron-expression` | Cron expression (e.g., "0 9 * * *") |
| `message` | Message to send to agent |

**Examples:**

```
# Every day at 9:00
/cron add "0 9 * * *" "Good morning!"

# Every hour on the hour
/cron add "0 * * * *" "Hourly check-in"

# Weekdays at 18:00
/cron add "0 18 * * 1-5" "Time to wrap up"

# Every 15 minutes
/cron add "*/15 * * * *" "Quarter hour check"
```

### remove - Delete Task

```
/cron remove <job_id>
```

Use the first 8 characters of the job ID:
```
/cron remove b28a1f52
```

### enable/disable - Enable/Disable Task

```
/cron enable <job_id>
/cron disable <job_id>
```

### status - Show Service Status

```
/cron status
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

```
/cron add "0 9 * * *" "New day started, check your schedule"
/cron add "0 12 * * *" "Time for lunch"
/cron add "0 18 * * 1-5" "Work done, generate daily summary"
```

### 2. Periodic Checks

```
/cron add "*/5 * * * *" "Check if all services are healthy"
/cron add "0 * * * *" "Sync latest data"
```

### 3. Scheduled Reports

```
/cron add "0 9 * * 1" "Generate this week's summary"
/cron add "0 9 1 * *" "Generate last month's summary"
```

## How It Works

```
┌─────────────────┐     ┌─────────────────────────────────┐     ┌─────────────────┐
│   Chat          │────>│   AgentLoop (command registry)  │────>│   jobs.json     │
│   /cron add     │     │   - In-memory state (truth)     │     │   (persistence) │
│   /cron list    │     │   - CronService (check/sec)     │     │                 │
└─────────────────┘     │   - ProcessDirect (exec)        │     └─────────────────┘
                        └─────────────────────────────────┘
```

1. **Chat commands**: Send `/cron` commands in any chat
2. **Command registry**: Commands registered in AgentLoop
3. **In-memory state**: CronService holds the source of truth
4. **Persistence**: Changes saved to `~/.aiclaw/cron/jobs.json`
5. **Scheduling**: CronService checks for due tasks every second
6. **Execution**: Calls `ProcessDirect()` to send message to agent

## Important Notes

1. **Aiclaw must be running**: Cron tasks only execute when aiclaw is running
2. **No restart needed**: Changes take effect immediately
3. **Timezone**: Uses system local timezone
4. **Persistence**: Tasks saved in JSON file, restored after restart
5. **Single instance**: Avoid running multiple aiclaw instances simultaneously

## Troubleshooting

### Task Not Executing

1. Confirm aiclaw is running
2. Check task status: `/cron list`
3. Check logs for `[cron]` output

### Task Execution Time Inaccurate

1. Check system timezone settings
2. Verify cron expression is correct

### View Execution History

```
/cron list
```

Status fields:
- `Last Run`: Last execution time
- `Last Status`: ok or error
- `Next Run`: Next scheduled execution time
