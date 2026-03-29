package orchestrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var apiCmd = &cobra.Command{
	Use:   "api",
	Short: "Worker-facing task and review APIs",
}

var apiCreateTaskCmd = &cobra.Command{
	Use:   "create-task",
	Short: "Create a task",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		api, err := newAPIForCLI()
		if err != nil {
			return err
		}
		input, err := parseAPIInput(cmd)
		if err != nil {
			return err
		}

		subject, err := requireString(input, "subject")
		if err != nil {
			return err
		}
		description, _ := optionalString(input, "description")
		blockedBy, err := optionalStringSlice(input, "blocked_by")
		if err != nil {
			return err
		}

		task, err := api.CreateTask(subject, description, blockedBy)
		if err != nil {
			return err
		}
		return writeAPIOutput(map[string]interface{}{
			"task": task,
		})
	},
}

var apiUpdateTaskCmd = &cobra.Command{
	Use:   "update-task",
	Short: "Update task fields",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		api, err := newAPIForCLI()
		if err != nil {
			return err
		}
		input, err := parseAPIInput(cmd)
		if err != nil {
			return err
		}

		taskID, err := requireString(input, "task_id")
		if err != nil {
			return err
		}

		updates := make(map[string]interface{})
		for k, v := range input {
			if k == "task_id" {
				continue
			}
			updates[k] = v
		}
		if len(updates) == 0 {
			return fmt.Errorf("no fields to update")
		}

		if err := api.UpdateTask(taskID, updates); err != nil {
			return err
		}
		return writeAPIOutput(map[string]interface{}{"ok": true})
	},
}

var apiClaimTaskCmd = &cobra.Command{
	Use:   "claim-task",
	Short: "Claim a task for a worker",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		api, err := newAPIForCLI()
		if err != nil {
			return err
		}
		input, err := parseAPIInput(cmd)
		if err != nil {
			return err
		}

		taskID, err := requireString(input, "task_id")
		if err != nil {
			return err
		}
		worker, err := requireString(input, "worker")
		if err != nil {
			return err
		}

		task, token, err := api.ClaimTask(taskID, worker)
		if err != nil {
			return err
		}
		return writeAPIOutput(map[string]interface{}{
			"task":        task,
			"claim_token": token,
		})
	},
}

var apiStartTaskCmd = &cobra.Command{
	Use:   "start-task",
	Short: "Mark a claimed task as in progress",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		api, err := newAPIForCLI()
		if err != nil {
			return err
		}
		input, err := parseAPIInput(cmd)
		if err != nil {
			return err
		}

		taskID, err := requireString(input, "task_id")
		if err != nil {
			return err
		}
		claimToken, err := requireString(input, "claim_token")
		if err != nil {
			return err
		}

		if err := api.StartTask(taskID, claimToken); err != nil {
			return err
		}
		return writeAPIOutput(map[string]interface{}{"ok": true})
	},
}

var apiCompleteTaskCmd = &cobra.Command{
	Use:   "complete-task",
	Short: "Complete a task",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		api, err := newAPIForCLI()
		if err != nil {
			return err
		}
		input, err := parseAPIInput(cmd)
		if err != nil {
			return err
		}

		taskID, err := requireString(input, "task_id")
		if err != nil {
			return err
		}
		claimToken, err := requireString(input, "claim_token")
		if err != nil {
			return err
		}
		summary, _ := optionalString(input, "summary")
		if summary == "" {
			summary = "Task completed"
		}

		if err := api.CompleteTask(taskID, claimToken, summary); err != nil {
			return err
		}
		return writeAPIOutput(map[string]interface{}{"ok": true})
	},
}

var apiFailTaskCmd = &cobra.Command{
	Use:   "fail-task",
	Short: "Fail a task",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		api, err := newAPIForCLI()
		if err != nil {
			return err
		}
		input, err := parseAPIInput(cmd)
		if err != nil {
			return err
		}

		taskID, err := requireString(input, "task_id")
		if err != nil {
			return err
		}
		claimToken, err := requireString(input, "claim_token")
		if err != nil {
			return err
		}
		errMsg, _ := optionalString(input, "error")
		if errMsg == "" {
			errMsg = "Task failed"
		}

		if err := api.FailTask(taskID, claimToken, errMsg); err != nil {
			return err
		}
		return writeAPIOutput(map[string]interface{}{"ok": true})
	},
}

var apiRequestReviewCmd = &cobra.Command{
	Use:   "request-review",
	Short: "Request human review for a task",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		api, err := newAPIForCLI()
		if err != nil {
			return err
		}
		input, err := parseAPIInput(cmd)
		if err != nil {
			return err
		}

		taskID, err := requireString(input, "task_id")
		if err != nil {
			return err
		}
		phaseID, err := requireString(input, "phase_id")
		if err != nil {
			return err
		}
		workerName, err := requireString(input, "worker_name")
		if err != nil {
			return err
		}
		summary, _ := optionalString(input, "summary")
		outputFile, _ := optionalString(input, "output_file")

		req, err := api.RequestReview(taskID, phaseID, workerName, summary, outputFile)
		if err != nil {
			return err
		}
		return writeAPIOutput(map[string]interface{}{
			"status": "pending",
			"review": req,
		})
	},
}

var apiCheckReviewCmd = &cobra.Command{
	Use:   "check-review",
	Short: "Check review status for a task",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		api, err := newAPIForCLI()
		if err != nil {
			return err
		}
		input, err := parseAPIInput(cmd)
		if err != nil {
			return err
		}

		taskID, err := requireString(input, "task_id")
		if err != nil {
			return err
		}

		result, err := api.GetReviewResult(taskID)
		if err == nil {
			return writeAPIOutput(map[string]interface{}{
				"status":     "completed",
				"approved":   result.Approved,
				"comment":    result.Comment,
				"reviewer":   result.Reviewer,
				"reviewedAt": result.ReviewedAt,
			})
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}

		req, err := api.storage.ReadReviewRequest(taskID)
		if err == nil {
			return writeAPIOutput(map[string]interface{}{
				"status": "pending",
				"review": req,
			})
		}
		if errors.Is(err, os.ErrNotExist) {
			return writeAPIOutput(map[string]interface{}{
				"status": "not_found",
			})
		}
		return err
	},
}

func init() {
	apiCmd.AddCommand(
		apiCreateTaskCmd,
		apiUpdateTaskCmd,
		apiClaimTaskCmd,
		apiStartTaskCmd,
		apiCompleteTaskCmd,
		apiFailTaskCmd,
		apiRequestReviewCmd,
		apiCheckReviewCmd,
	)

	for _, c := range []*cobra.Command{
		apiCreateTaskCmd,
		apiUpdateTaskCmd,
		apiClaimTaskCmd,
		apiStartTaskCmd,
		apiCompleteTaskCmd,
		apiFailTaskCmd,
		apiRequestReviewCmd,
		apiCheckReviewCmd,
	} {
		c.Flags().String("input", "", "JSON input payload")
		_ = c.MarkFlagRequired("input")
	}
}

func newAPIForCLI() (*API, error) {
	storage := NewStorage(getCwd())
	if err := storage.Init(); err != nil {
		return nil, err
	}
	return NewAPI(storage), nil
}

func parseAPIInput(cmd *cobra.Command) (map[string]interface{}, error) {
	raw, _ := cmd.Flags().GetString("input")
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("--input is required")
	}
	var input map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return nil, fmt.Errorf("invalid --input JSON: %w", err)
	}
	return input, nil
}

func requireString(input map[string]interface{}, key string) (string, error) {
	value, ok := input[key]
	if !ok {
		return "", fmt.Errorf("missing field %q", key)
	}
	s, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("field %q must be a string", key)
	}
	return s, nil
}

func optionalString(input map[string]interface{}, key string) (string, error) {
	value, ok := input[key]
	if !ok || value == nil {
		return "", nil
	}
	s, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("field %q must be a string", key)
	}
	return s, nil
}

func optionalStringSlice(input map[string]interface{}, key string) ([]string, error) {
	value, ok := input[key]
	if !ok || value == nil {
		return nil, nil
	}

	if typed, ok := value.([]string); ok {
		return typed, nil
	}
	items, ok := value.([]interface{})
	if !ok {
		return nil, fmt.Errorf("field %q must be an array of strings", key)
	}

	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("field %q must be an array of strings", key)
		}
		out = append(out, s)
	}
	return out, nil
}

func writeAPIOutput(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
