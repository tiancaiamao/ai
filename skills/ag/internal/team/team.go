package team

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/genius/ag/internal/storage"
)

const (
	controlRootDir   = ".ag"
	currentTeamFile  = "current-team"
	teamsDirName     = "teams"
	teamMetaFileName = "team.json"
)

var validTeamID = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type Meta struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"` // active | done
	CreatedAt   int64  `json:"createdAt"`
	FinishedAt  int64  `json:"finishedAt,omitempty"`
}

type Summary struct {
	Meta
	Current       bool `json:"current"`
	RunningAgents int  `json:"runningAgents"`
	TasksTotal    int  `json:"tasksTotal"`
	TasksPending  int  `json:"tasksPending"`
}

func controlPath(parts ...string) string {
	all := append([]string{controlRootDir}, parts...)
	return filepath.Join(all...)
}

func teamsRoot() string {
	return controlPath(teamsDirName)
}

func teamDir(teamID string) string {
	return filepath.Join(teamsRoot(), teamID)
}

func teamMetaPath(teamID string) string {
	return filepath.Join(teamDir(teamID), teamMetaFileName)
}

func runtimeDir(teamID string) string {
	return teamDir(teamID)
}

func currentTeamPath() string {
	return controlPath(currentTeamFile)
}

func ValidateID(teamID string) error {
	teamID = strings.TrimSpace(teamID)
	if teamID == "" {
		return fmt.Errorf("team id is required")
	}
	if !validTeamID.MatchString(teamID) {
		return fmt.Errorf("invalid team id %q: use [A-Za-z0-9._-]", teamID)
	}
	return nil
}

func Init(teamID, description string) (*Meta, error) {
	if err := ValidateID(teamID); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(teamsRoot(), 0755); err != nil {
		return nil, err
	}
	td := teamDir(teamID)
	if storage.Exists(td) {
		return nil, fmt.Errorf("team already exists: %s", teamID)
	}
	if err := os.MkdirAll(filepath.Join(td, "agents"), 0755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(td, "channels"), 0755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(td, "tasks"), 0755); err != nil {
		return nil, err
	}

	meta := &Meta{
		ID:          teamID,
		Description: strings.TrimSpace(description),
		Status:      "active",
		CreatedAt:   time.Now().Unix(),
	}
	if err := storage.AtomicWriteJSON(teamMetaPath(teamID), meta); err != nil {
		return nil, err
	}
	if err := Use(teamID); err != nil {
		return nil, err
	}
	return meta, nil
}

func Use(teamID string) error {
	if err := ValidateID(teamID); err != nil {
		return err
	}
	if !storage.Exists(teamMetaPath(teamID)) {
		return fmt.Errorf("team not found: %s", teamID)
	}
	if err := os.MkdirAll(controlRootDir, 0755); err != nil {
		return err
	}
	return storage.AtomicWrite(currentTeamPath(), []byte(teamID+"\n"))
}

func Current() (string, error) {
	data, err := os.ReadFile(currentTeamPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func ClearCurrent() error {
	err := os.Remove(currentTeamPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func ResolveBaseDir() (string, string, error) {
	if env := strings.TrimSpace(os.Getenv("AG_BASE_DIR")); env != "" {
		return env, "", nil
	}
	cur, err := Current()
	if err != nil {
		return "", "", err
	}
	if cur == "" {
		return controlRootDir, "", nil
	}
	return runtimeDir(cur), cur, nil
}

func loadMeta(teamID string) (*Meta, error) {
	meta := &Meta{}
	if err := storage.ReadJSON(teamMetaPath(teamID), meta); err != nil {
		return nil, err
	}
	return meta, nil
}

func List() ([]Summary, error) {
	if err := os.MkdirAll(teamsRoot(), 0755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(teamsRoot())
	if err != nil {
		return nil, err
	}
	current, _ := Current()
	out := make([]Summary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		teamID := entry.Name()
		meta, err := loadMeta(teamID)
		if err != nil {
			continue
		}
		s := Summary{
			Meta:    *meta,
			Current: teamID == current,
		}
		s.RunningAgents = countRunningAgents(runtimeDir(teamID))
		s.TasksTotal, s.TasksPending = countTasks(runtimeDir(teamID))
		out = append(out, s)
	}
	return out, nil
}

func Done(teamID string) (*Meta, error) {
	if strings.TrimSpace(teamID) == "" {
		cur, err := Current()
		if err != nil {
			return nil, err
		}
		teamID = cur
	}
	if err := ValidateID(teamID); err != nil {
		return nil, err
	}
	meta, err := loadMeta(teamID)
	if err != nil {
		return nil, fmt.Errorf("team not found: %s", teamID)
	}
	meta.Status = "done"
	meta.FinishedAt = time.Now().Unix()
	if err := storage.AtomicWriteJSON(teamMetaPath(teamID), meta); err != nil {
		return nil, err
	}
	return meta, nil
}

func Cleanup(teamID string, force bool) error {
	if strings.TrimSpace(teamID) == "" {
		cur, err := Current()
		if err != nil {
			return err
		}
		teamID = cur
	}
	if err := ValidateID(teamID); err != nil {
		return err
	}

	td := teamDir(teamID)
	if !storage.Exists(td) {
		return fmt.Errorf("team not found: %s", teamID)
	}
	if !force {
		running := countRunningAgents(td)
		if running > 0 {
			return fmt.Errorf("team %s has %d running agent(s); use --force", teamID, running)
		}
	}
	if err := os.RemoveAll(td); err != nil {
		return err
	}

	cur, _ := Current()
	if cur == teamID {
		return ClearCurrent()
	}
	return nil
}

func countRunningAgents(base string) int {
	agentsDir := filepath.Join(base, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return 0
	}
	running := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		status := storage.ReadStatus(filepath.Join(agentsDir, entry.Name()))
		if status == "running" || status == "spawning" {
			running++
		}
	}
	return running
}

func countTasks(base string) (total int, pending int) {
	tasksDir := filepath.Join(base, "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return 0, 0
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		total++
		taskFile := filepath.Join(tasksDir, entry.Name(), "task.json")
		var t struct {
			Status string `json:"status"`
		}
		if err := storage.ReadJSON(taskFile, &t); err == nil {
			if t.Status == "pending" {
				pending++
			}
		}
	}
	return total, pending
}
