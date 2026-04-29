package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/genius/ag/internal/storage"
)

// AIAdapter 封装了 ai 新基础设施的调用
type AIAdapter struct {
	// agentID 到 runID 的映射缓存
	mappings map[string]string
	mu       sync.RWMutex
}

// NewAIAdapter 创建新的 AIAdapter 实例
func NewAIAdapter() *AIAdapter {
	return &AIAdapter{
		mappings: make(map[string]string),
	}
}

// SpawnWithAIServe 使用 ai serve 命令启动一个新的 agent
func (a *AIAdapter) SpawnWithAIServe(id, system, input, cwd string) error {
	// 创建 ag 的 agent 目录（用于向后兼容和存储映射）
	agentDir := storage.AgentDir(id)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("create agent dir: %w", err)
	}

	// 写入初始的 activity.json
	activity := map[string]interface{}{
		"status":    "running",
		"backend":   "ai",
		"startedAt": time.Now().Unix(),
	}
	if err := storage.AtomicWriteJSON(filepath.Join(agentDir, "activity.json"), activity); err != nil {
		return fmt.Errorf("write activity.json: %w", err)
	}

	// 构建 ai serve 命令，在后台运行
	cmd := exec.Command("ai", "serve")

	// 添加参数
	if system != "" {
		cmd.Args = append(cmd.Args, "--system-prompt", system)
	}
	if input != "" {
		cmd.Args = append(cmd.Args, "--input", input)
	}
	if cwd != "" {
		cmd.Dir = cwd
	}

	// 设置 agent 名称以便识别
	cmd.Args = append(cmd.Args, "--name", "ag-agent-"+id)

	// 使用 CombinedOutput 来捕获所有输出，包括 stderr
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ai serve failed: %w, output: %s", err, string(output))
	}

	// 提取 run ID
	runID := strings.TrimSpace(string(output))
	if runID == "" {
		// 如果没有输出，尝试使用 ai ls 来获取最新的 run
		fmt.Println("ai serve returned no run ID, trying ai ls...")
		runID, err = a.getLatestRunID()
		if err != nil {
			return fmt.Errorf("ai serve returned no run ID and ai ls failed: %w", err)
		}
		fmt.Printf("Found run ID from ai ls: %s\n", runID)
	} else {
		fmt.Printf("Received run ID from ai serve: %s\n", runID)
	}

	// 保存映射关系
	if err := a.saveAgentRunMapping(id, runID); err != nil {
		return fmt.Errorf("save mapping: %w", err)
	}

	return nil
}

// getLatestRunID 使用 ai ls 获取最新的 run ID
func (a *AIAdapter) getLatestRunID() (string, error) {
	cmd := exec.Command("ai", "ls", "--json")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ai ls failed: %w", err)
	}

	// 解析 JSON 输出
	var runs []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal(output, &runs); err != nil {
		return "", fmt.Errorf("parse ai ls output: %w", err)
	}

	// 查找最新的运行且状态为 running 的 agent
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Status == "running" && strings.HasPrefix(runs[i].Name, "ag-agent-") {
			return runs[i].ID, nil
		}
	}

	return "", fmt.Errorf("no running ag-agent found")
}

// SendCommand 向指定的 agent 发送命令
func (a *AIAdapter) SendCommand(agentID, cmdType, message string) error {
	// 获取对应的 run ID
	runID, err := a.getRunIDForAgent(agentID)
	if err != nil {
		return err
	}

	// 构建 ai send 命令
	args := []string{"send", "--id", runID}
	if message != "" {
		args = append(args, message)
	}

	cmd := exec.Command("ai", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ai send failed: %s (output: %s)", err, string(output))
	}

	return nil
}

// GetStatus 获取 agent 的状态
func (a *AIAdapter) GetStatus(agentID string) (*RunMeta, error) {
	// 获取对应的 run ID
	runID, err := a.getRunIDForAgent(agentID)
	if err != nil {
		return nil, err
	}

	// 读取 ai 的 run.json
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	baseDir := filepath.Join(homeDir, ".ai", "runs")
	metaPath := filepath.Join(baseDir, runID, "run.json")

	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("read run.json: %w", err)
	}

	var meta RunMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse run.json: %w", err)
	}

	return &meta, nil
}

// Wait 等待 agent 完成
func (a *AIAdapter) Wait(agentID string) error {
	// 获取对应的 run ID
	_, err := a.getRunIDForAgent(agentID)
	if err != nil {
		return err
	}

	// 使用 ai ls 检查状态
	for i := 0; i < 3600; i++ { // 最多等待 1 小时
		status, err := a.GetStatus(agentID)
		if err != nil {
			// 如果文件不存在，可能已经完成了
			continue
		}

		if status.Status != "running" {
			return nil
		}

		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("timeout waiting for agent %s to complete", agentID)
}

// 私有方法

func (a *AIAdapter) saveAgentRunMapping(agentID, runID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 保存到内存
	a.mappings[agentID] = runID

	// 保存到文件
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	agDir := filepath.Join(homeDir, ".ag", "agents")
	if err := os.MkdirAll(agDir, 0755); err != nil {
		return fmt.Errorf("create ag dir: %w", err)
	}

	mappingsFile := filepath.Join(agDir, "run_mappings.json")

	// 读取现有映射
	var mappings map[string]string
	data, err := os.ReadFile(mappingsFile)
	if err == nil {
		json.Unmarshal(data, &mappings)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read mappings file: %w", err)
	}

	// 确保映射存在
	if mappings == nil {
		mappings = make(map[string]string)
	}

	// 更新映射
	mappings[agentID] = runID

	// 写回文件
	data, err = json.MarshalIndent(mappings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal mappings: %w", err)
	}

	if err := os.WriteFile(mappingsFile, data, 0644); err != nil {
		return fmt.Errorf("write mappings file: %w", err)
	}

	fmt.Printf("Saved mapping: %s -> %s to %s\n", agentID, runID, mappingsFile)
	return nil
}

func (a *AIAdapter) getRunIDForAgent(agentID string) (string, error) {
	a.mu.RLock()
	runID, exists := a.mappings[agentID]
	a.mu.RUnlock()

	if exists {
		return runID, nil
	}

	// 从文件中读取
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	mappingsFile := filepath.Join(homeDir, ".ag", "agents", "run_mappings.json")
	data, err := os.ReadFile(mappingsFile)
	if err != nil {
		return "", fmt.Errorf("read run mappings: %w", err)
	}

	var mappings map[string]string
	if err := json.Unmarshal(data, &mappings); err != nil {
		return "", fmt.Errorf("parse run mappings: %w", err)
	}

	runID, exists = mappings[agentID]
	if !exists {
		return "", fmt.Errorf("no run ID found for agent %s", agentID)
	}

	// 缓存到内存
	a.mu.Lock()
	a.mappings[agentID] = runID
	a.mu.Unlock()

	return runID, nil
}

// RunMeta 表示 ai 的运行元数据
type RunMeta struct {
	ID         string `json:"id"`
	PID        int    `json:"pid"`
	CWD        string `json:"cwd"`
	Status     string `json:"status"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
	Name       string `json:"name"`
	ParentRun  string `json:"parent_run"`
}

// 全局适配器实例
var aiAdapter = NewAIAdapter()
