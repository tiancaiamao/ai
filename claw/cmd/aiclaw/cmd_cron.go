// aiclaw - AI Claw Bot with picoclaw channels and ai agent core.
// Cron command-line interface.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tiancaiamao/ai/claw/pkg/cron"
)

func cronCmd(clawDir string) {
	if len(os.Args) < 3 {
		cronHelp()
		return
	}

	subcommand := os.Args[2]
	cronStorePath := filepath.Join(clawDir, "cron", "jobs.json")

	switch subcommand {
	case "list":
		cronListCmd(cronStorePath)
	case "add":
		cronAddCmd(cronStorePath)
	case "remove":
		if len(os.Args) < 4 {
			fmt.Println("Usage: aiclaw cron remove <job_id>")
			return
		}
		cronRemoveCmd(cronStorePath, os.Args[3])
	case "enable":
		if len(os.Args) < 4 {
			fmt.Println("Usage: aiclaw cron enable <job_id>")
			return
		}
		cronEnableCmd(cronStorePath, os.Args[3], true)
	case "disable":
		if len(os.Args) < 4 {
			fmt.Println("Usage: aiclaw cron disable <job_id>")
			return
		}
		cronEnableCmd(cronStorePath, os.Args[3], false)
	default:
		fmt.Printf("Unknown cron command: %s\n", subcommand)
		cronHelp()
	}
}

func cronHelp() {
	fmt.Println("\nCron commands:")
	fmt.Println("  list              List all scheduled jobs")
	fmt.Println("  add               Add a new scheduled job")
	fmt.Println("  remove <id>       Remove a job by ID")
	fmt.Println("  enable <id>       Enable a job")
	fmt.Println("  disable <id>      Disable a job")
	fmt.Println()
	fmt.Println("Add options:")
	fmt.Println("  -n, --name <name>       Job name (required)")
	fmt.Println("  -m, --message <msg>     Message for agent (required)")
	fmt.Println("  -e, --every <sec>       Run every N seconds")
	fmt.Println("  -c, --cron <expr>       Cron expression (e.g. '0 9 * * *')")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  aiclaw cron add -n '每日提醒' -m '检查今日待办' -c '0 9 * * *'")
	fmt.Println("  aiclaw cron add -n '心跳' -m 'ping' -e 60")
	fmt.Println("  aiclaw cron list")
	fmt.Println("  aiclaw cron remove abc123")
}

func cronListCmd(storePath string) {
	cs := cron.NewCronService(storePath, nil)
	jobs := cs.ListJobs(true)

	if len(jobs) == 0 {
		fmt.Println("No scheduled jobs.")
		return
	}

	fmt.Println("\nScheduled Jobs:")
	fmt.Println("---------------")
	for _, job := range jobs {
		var schedule string
		if job.Schedule.Kind == "every" && job.Schedule.EveryMS != nil {
			schedule = fmt.Sprintf("every %ds", *job.Schedule.EveryMS/1000)
		} else if job.Schedule.Kind == "cron" {
			schedule = job.Schedule.Expr
		} else {
			schedule = "unknown"
		}

		nextRun := "pending"
		if job.State.NextRunAtMS != nil {
			nextTime := time.UnixMilli(*job.State.NextRunAtMS)
			nextRun = nextTime.Format("2006-01-02 15:04")
		}

		status := "✓ enabled"
		if !job.Enabled {
			status = "✗ disabled"
		}

		fmt.Printf("  [%s] %s\n", job.ID, job.Name)
		fmt.Printf("      Schedule: %s\n", schedule)
		fmt.Printf("      Message:  %s\n", job.Payload.Message)
		fmt.Printf("      Status:   %s\n", status)
		fmt.Printf("      Next run: %s\n", nextRun)
		fmt.Println()
	}
}

func cronAddCmd(storePath string) {
	name := ""
	message := ""
	var everySec *int64
	cronExpr := ""

	args := os.Args[3:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-n", "--name":
			if i+1 < len(args) {
				name = args[i+1]
				i++
			}
		case "-m", "--message":
			if i+1 < len(args) {
				message = args[i+1]
				i++
			}
		case "-e", "--every":
			if i+1 < len(args) {
				var sec int64
				fmt.Sscanf(args[i+1], "%d", &sec)
				everySec = &sec
				i++
			}
		case "-c", "--cron":
			if i+1 < len(args) {
				cronExpr = args[i+1]
				i++
			}
		}
	}

	if name == "" {
		fmt.Println("Error: --name is required")
		return
	}

	if message == "" {
		fmt.Println("Error: --message is required")
		return
	}

	if everySec == nil && cronExpr == "" {
		fmt.Println("Error: Either --every or --cron must be specified")
		return
	}

	var schedule cron.CronSchedule
	if everySec != nil {
		everyMS := *everySec * 1000
		schedule = cron.CronSchedule{
			Kind:    "every",
			EveryMS: &everyMS,
		}
	} else {
		schedule = cron.CronSchedule{
			Kind: "cron",
			Expr: cronExpr,
		}
	}

	cs := cron.NewCronService(storePath, nil)
	job, err := cs.AddJob(name, schedule, message, false, "", "")
	if err != nil {
		fmt.Printf("Error adding job: %v\n", err)
		return
	}

	var scheduleDesc string
	if everySec != nil {
		scheduleDesc = fmt.Sprintf("every %ds", *everySec)
	} else {
		scheduleDesc = cronExpr
	}

	fmt.Printf("✓ Added job '%s' (%s)\n", job.Name, job.ID)
	fmt.Printf("  Schedule: %s\n", scheduleDesc)
	fmt.Printf("  Message: %s\n", message)
}

func cronRemoveCmd(storePath, jobID string) {
	cs := cron.NewCronService(storePath, nil)
	if cs.RemoveJob(jobID) {
		fmt.Printf("✓ Removed job %s\n", jobID)
	} else {
		fmt.Printf("✗ Job %s not found\n", jobID)
	}
}

func cronEnableCmd(storePath, jobID string, enable bool) {
	cs := cron.NewCronService(storePath, nil)
	job := cs.EnableJob(jobID, enable)
	if job != nil {
		status := "enabled"
		if !enable {
			status = "disabled"
		}
		fmt.Printf("✓ Job '%s' %s\n", job.Name, status)
	} else {
		fmt.Printf("✗ Job %s not found\n", jobID)
	}
}