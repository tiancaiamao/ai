// aiclaw - AI Claw Bot with picoclaw channels and ai agent core.
// Cron command-line interface.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

func cronCmd(clawDir string) {
	if len(os.Args) < 3 {
		cronHelp()
		return
	}

	subcommand := os.Args[2]

	switch subcommand {
	case "list":
		cronListCmd()
	case "add":
		cronAddCmd()
	case "remove":
		if len(os.Args) < 4 {
			fmt.Println("Usage: aiclaw cron remove <job_id>")
			return
		}
		cronRemoveCmd(os.Args[3])
	case "enable":
		if len(os.Args) < 4 {
			fmt.Println("Usage: aiclaw cron enable <job_id>")
			return
		}
		cronEnableCmd(os.Args[3], true)
	case "disable":
		if len(os.Args) < 4 {
			fmt.Println("Usage: aiclaw cron disable <job_id>")
			return
		}
		cronEnableCmd(os.Args[3], false)
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

type rpcRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params"`
	ID      int                    `json:"id"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result"`
	Error   *rpcError   `json:"error"`
	ID      int         `json:"id"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func callGatewayRPC(method string, params map[string]interface{}) (interface{}, error) {
	req := rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := http.Post("http://127.0.0.1:28789/rpc", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gateway: %w (is aiclaw running?)", err)
	}
	defer resp.Body.Close()

	var result rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("RPC error: %s", result.Error.Message)
	}

	return result.Result, nil
}

func cronListCmd() {
	result, err := callGatewayRPC("cron.list", map[string]interface{}{
		"include_disabled": true,
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	data := result.(map[string]interface{})
	jobs := data["jobs"].([]interface{})

	if len(jobs) == 0 {
		fmt.Println("No scheduled jobs.")
		return
	}

	fmt.Println("\nScheduled Jobs:")
	fmt.Println("---------------")
	for _, j := range jobs {
		job := j.(map[string]interface{})
		id := job["id"].(string)
		name := job["name"].(string)
		enabled := job["enabled"].(bool)

		// Get schedule info
		var schedule string
		if sched, ok := job["schedule"].(map[string]interface{}); ok {
			if kind, ok := sched["kind"].(string); ok {
				if kind == "every" {
					if everyMS, ok := sched["every_ms"].(float64); ok {
						schedule = fmt.Sprintf("every %ds", int(everyMS/1000))
					}
				} else if kind == "cron" {
					if expr, ok := sched["expr"].(string); ok {
						schedule = expr
					}
				}
			}
		}

		// Get message
		message := ""
		if payload, ok := job["payload"].(map[string]interface{}); ok {
			if msg, ok := payload["message"].(string); ok {
				message = msg
			}
		}

		// Get next run
		nextRun := "pending"
		if state, ok := job["state"].(map[string]interface{}); ok {
			if nextRunMS, ok := state["next_run_at_ms"].(float64); ok {
				nextTime := time.UnixMilli(int64(nextRunMS))
				nextRun = nextTime.Format("2006-01-02 15:04")
			}
		}

		status := "✓ enabled"
		if !enabled {
			status = "✗ disabled"
		}

		fmt.Printf("  [%s] %s\n", id, name)
		fmt.Printf("      Schedule: %s\n", schedule)
		fmt.Printf("      Message:  %s\n", message)
		fmt.Printf("      Status:   %s\n", status)
		fmt.Printf("      Next run: %s\n", nextRun)
		fmt.Println()
	}
}

func cronAddCmd() {
	name := ""
	message := ""
	var everySec *float64
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
				var sec float64
				fmt.Sscanf(args[i+1], "%f", &sec)
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

	params := map[string]interface{}{
		"name":    name,
		"message": message,
	}

	if everySec != nil {
		params["every"] = *everySec
	} else {
		params["cron"] = cronExpr
	}

	result, err := callGatewayRPC("cron.add", params)
	if err != nil {
		fmt.Printf("Error adding job: %v\n", err)
		return
	}

	job := result.(map[string]interface{})
	var scheduleDesc string
	if everySec != nil {
		scheduleDesc = fmt.Sprintf("every %ds", int(*everySec))
	} else {
		scheduleDesc = cronExpr
	}

	fmt.Printf("✓ Added job '%s' (%s)\n", job["name"], job["id"])
	fmt.Printf("  Schedule: %s\n", scheduleDesc)
	fmt.Printf("  Message: %s\n", message)
}

func cronRemoveCmd(jobID string) {
	result, err := callGatewayRPC("cron.remove", map[string]interface{}{
		"id": jobID,
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	data := result.(map[string]interface{})
	if removed, ok := data["removed"].(bool); ok && removed {
		fmt.Printf("✓ Removed job %s\n", jobID)
	} else {
		fmt.Printf("✗ Job %s not found\n", jobID)
	}
}

func cronEnableCmd(jobID string, enable bool) {
	method := "cron.enable"
	if !enable {
		method = "cron.disable"
	}

	result, err := callGatewayRPC(method, map[string]interface{}{
		"id": jobID,
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	job := result.(map[string]interface{})
	status := "enabled"
	if !enable {
		status = "disabled"
	}
	fmt.Printf("✓ Job '%s' %s\n", job["name"], status)
}