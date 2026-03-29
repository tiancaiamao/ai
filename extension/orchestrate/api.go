package team

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// API provides team operations for workers
type API struct {
	storage *Storage
}

// NewAPI creates a new API instance
func NewAPI(storage *Storage) *API {
	return &API{storage: storage}
}

// generateToken generates a random claim token
func generateToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// CreateTask creates a new task
func (a *API) CreateTask(subject, description string, blockedBy []string) (*Task, error) {
	// Generate task ID (simple increment or timestamp-based)
	tasks, _ := a.storage.ListTasks()
	taskID := fmt.Sprintf("%d", len(tasks)+1)

	now := time.Now().UTC()
	task := &Task{
		ID:          taskID,
		Subject:     subject,
		Description: description,
		Status:      StatePending,
		BlockedBy:   blockedBy,
		CreatedAt:   now,
		RetryCount:  0,
	}

	if err := a.storage.WriteTask(task); err != nil {
		return nil, fmt.Errorf("failed to create task: %w", err)
	}

	return task, nil
}

// UpdateTask updates task fields
func (a *API) UpdateTask(taskID string, updates map[string]interface{}) error {
	return a.storage.AtomicUpdate(taskID, func(task *Task) error {
		if v, ok := updates["subject"].(string); ok {
			task.Subject = v
		}
		if v, ok := updates["description"].(string); ok {
			task.Description = v
		}
		// Handle blocked_by - JSON unmarshals to []interface{}, not []string
		if v, ok := updates["blocked_by"].([]interface{}); ok {
			var blockedBy []string
			for _, id := range v {
				if s, ok := id.(string); ok {
					blockedBy = append(blockedBy, s)
				}
			}
			task.BlockedBy = blockedBy
		} else if v, ok := updates["blocked_by"].([]string); ok {
			task.BlockedBy = v
		}
		return nil
	})
}

// ClaimTask claims a task for a worker
func (a *API) ClaimTask(taskID, workerName string) (*Task, string, error) {
	var claimToken string
	var claimedTask *Task

	err := a.storage.AtomicUpdate(taskID, func(task *Task) error {
		if task.Status != StatePending {
			return fmt.Errorf("task %s is not pending (status: %s)", taskID, task.Status)
		}

		// Check if dependencies are satisfied
		for _, depID := range task.BlockedBy {
			dep, err := a.storage.ReadTask(depID)
			if err != nil {
				return fmt.Errorf("dependency %s not found", depID)
			}
			if dep.Status != StateCompleted {
				return fmt.Errorf("dependency %s not completed (status: %s)", depID, dep.Status)
			}
		}

		now := time.Now().UTC()
		task.Status = StateClaimed
		task.ClaimedBy = workerName
		task.ClaimedAt = &now
		claimToken = generateToken()
		task.ClaimToken = claimToken
		task.StartedAt = &now
		
		claimedTask = task
		return nil
	})

	if err != nil {
		return nil, "", err
	}

	return claimedTask, claimToken, nil
}

// StartTask transitions task to in_progress
func (a *API) StartTask(taskID, claimToken string) error {
	return a.storage.AtomicUpdate(taskID, func(task *Task) error {
		if task.ClaimToken != claimToken {
			return fmt.Errorf("invalid claim token")
		}
		if task.Status != StateClaimed {
			return fmt.Errorf("task %s is not claimed", taskID)
		}
		task.Status = StateInProgress
		return nil
	})
}

// CompleteTask marks a task as completed
func (a *API) CompleteTask(taskID, claimToken, summary string) error {
	return a.storage.AtomicUpdate(taskID, func(task *Task) error {
		if task.ClaimToken != claimToken {
			return fmt.Errorf("invalid claim token")
		}
		if task.Status != StateInProgress && task.Status != StateClaimed {
			return fmt.Errorf("task %s is not in progress", taskID)
		}
		now := time.Now().UTC()
		task.Status = StateCompleted
		task.Result = summary
		task.CompletedAt = &now
		task.ClaimToken = "" // Clear token
		return nil
	})
}

// FailTask marks a task as failed
func (a *API) FailTask(taskID, claimToken, errMsg string) error {
	return a.storage.AtomicUpdate(taskID, func(task *Task) error {
		if task.ClaimToken != claimToken {
			return fmt.Errorf("invalid claim token")
		}
		if task.Status != StateInProgress && task.Status != StateClaimed {
			return fmt.Errorf("task %s is not in progress", taskID)
		}
		task.Status = StateFailed
		task.Error = errMsg
		task.ClaimToken = "" // Clear token
		return nil
	})
}

// ReleaseTask releases a task claim
func (a *API) ReleaseTask(taskID, claimToken string) error {
	return a.storage.AtomicUpdate(taskID, func(task *Task) error {
		if task.ClaimToken != claimToken {
			return fmt.Errorf("invalid claim token")
		}
		task.Status = StatePending
		task.ClaimedBy = ""
		task.ClaimedAt = nil
		task.ClaimToken = ""
		return nil
	})
}

// RetryTask resets a failed task for retry
func (a *API) RetryTask(taskID string, maxRetries int) error {
	return a.storage.AtomicUpdate(taskID, func(task *Task) error {
		if task.Status != StateFailed {
			return fmt.Errorf("task %s is not failed", taskID)
		}
		if task.RetryCount >= maxRetries {
			return fmt.Errorf("task %s exceeded max retries (%d)", taskID, maxRetries)
		}
		task.Status = StatePending
		task.Error = ""
		task.RetryCount++
		return nil
	})
}

// ListTasks lists all tasks
func (a *API) ListTasks() ([]*Task, error) {
	return a.storage.ListTasks()
}

// ReadTask reads a specific task
func (a *API) ReadTask(taskID string) (*Task, error) {
	return a.storage.ReadTask(taskID)
}

// IsReady checks if a task is ready to be claimed
func (a *API) IsReady(task *Task) bool {
	if task.Status != StatePending {
		return false
	}
	for _, depID := range task.BlockedBy {
		dep, err := a.storage.ReadTask(depID)
		if err != nil || dep.Status != StateCompleted {
			return false
		}
	}
	return true
}

// RequestReview creates a review request for a task
func (a *API) RequestReview(taskID, phaseID, workerName, summary, outputFile string) (*ReviewRequest, error) {
	req := &ReviewRequest{
		TaskID:     taskID,
		PhaseID:    phaseID,
		WorkerName: workerName,
		Summary:    summary,
		OutputFile: outputFile,
		CreatedAt:  time.Now().UTC(),
	}

	// Update task to indicate it's awaiting review
	err := a.storage.AtomicUpdate(taskID, func(task *Task) error {
		if task.Status != StateInProgress && task.Status != StateClaimed {
			return fmt.Errorf("task %s is not in progress", taskID)
		}
		// Store review request reference
		task.Result = "[AWAITING REVIEW] " + summary
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Write review request file
	if err := a.storage.WriteReviewRequest(req); err != nil {
		return nil, fmt.Errorf("failed to write review request: %w", err)
	}

	return req, nil
}

// SubmitReview submits a review decision
func (a *API) SubmitReview(taskID string, approved bool, comment, reviewer string) error {
	result := ReviewResult{
		TaskID:     taskID,
		Approved:   approved,
		Comment:    comment,
		Reviewer:   reviewer,
		ReviewedAt: time.Now().UTC(),
	}

	// Update task based on review
	err := a.storage.AtomicUpdate(taskID, func(task *Task) error {
		if approved {
			now := time.Now().UTC()
			task.Status = StateCompleted
			task.Result = comment
			task.CompletedAt = &now
		} else {
			task.Status = StateFailed
			task.Error = "Review rejected: " + comment
		}
		task.ClaimToken = ""
		return nil
	})
	if err != nil {
		return err
	}

	// Write review result
	return a.storage.WriteReviewResult(result)
}

// ListPendingReviews lists all pending review requests
func (a *API) ListPendingReviews() ([]*ReviewRequest, error) {
	return a.storage.ListReviewRequests()
}

// GetReviewResult gets the review result for a task
func (a *API) GetReviewResult(taskID string) (*ReviewResult, error) {
	return a.storage.ReadReviewResult(taskID)
}