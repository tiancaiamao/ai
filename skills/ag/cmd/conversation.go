package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConversationMessage 表示对话中的一条消息
type ConversationMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Turn    int    `json:"turn,omitempty"`
}

// Conversation 表示整个对话
type Conversation struct {
	Messages []ConversationMessage `json:"messages"`
}

// GetConversation 从 AI 适配器的 agent 获取清晰的对话格式
func GetConversation(agentID string) (*Conversation, error) {
	// 获取对应的 run ID
	runID, err := aiAdapter.getRunIDForAgent(agentID)
	if err != nil {
		return nil, fmt.Errorf("get run ID for agent %s: %w", agentID, err)
	}

	// 读取 events.jsonl 文件
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	eventsFile := filepath.Join(homeDir, ".ai", "runs", runID, "events.jsonl")
	data, err := os.ReadFile(eventsFile)
	if err != nil {
		return nil, fmt.Errorf("read events.jsonl: %w", err)
	}

	// 解析 events.jsonl 并构建清晰的对话
	return parseConversation(data)
}

// parseConversation 从 events.jsonl 数据中构建清晰的对话
func parseConversation(data []byte) (*Conversation, error) {
	lines := strings.Split(string(data), "\n")
	var conversation Conversation
	var currentMessage strings.Builder
	var currentRole string
	var currentTurn int
	var lastTurn int
	var messageID int // 用于跟踪消息的唯一ID

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue // 跳过无效的 JSON 行
		}

		// 处理消息相关的事件
		if eventType, ok := event["type"].(string); ok {
			switch eventType {
			case "message_start", "message_update":
				if msg, ok := event["message"].(map[string]interface{}); ok {
					if role, ok := msg["role"].(string); ok {
						// 如果是新角色，保存前一条消息
						if role != currentRole && currentMessage.Len() > 0 {
							content := strings.TrimSpace(currentMessage.String())
							if content != "" {
								conversation.Messages = append(conversation.Messages, ConversationMessage{
									Role:    currentRole,
									Content: content,
									Turn:    currentTurn,
								})
								messageID++
							}
							currentMessage.Reset()
						}

						currentRole = role

						// 如果是用户消息，结束当前回合
						if role == "user" && currentTurn > 0 {
							lastTurn = currentTurn
							currentTurn = 0
						}

						// 提取内容
						if content, ok := msg["content"].([]interface{}); ok {
							for _, item := range content {
								if contentItem, ok := item.(map[string]interface{}); ok {
									itemType, _ := contentItem["type"].(string)
									switch itemType {
									case "text":
										if text, ok := contentItem["text"].(string); ok {
											currentMessage.WriteString(text)
										}
									case "thinking":
										// 忽略思考过程
									}
								}
							}
						}
					}
				}

			case "turn_start":
				// 新回合开始
				if currentRole == "assistant" && currentTurn == 0 {
					currentTurn = lastTurn + 1
				}

			case "message_end":
				// 消息结束，保存当前消息
				if currentMessage.Len() > 0 {
					content := strings.TrimSpace(currentMessage.String())
					if content != "" {
						conversation.Messages = append(conversation.Messages, ConversationMessage{
							Role:    currentRole,
							Content: content,
							Turn:    currentTurn,
						})
						currentMessage.Reset()
					}
				}
			}
		}
	}

	// 添加最后一条消息
	if currentMessage.Len() > 0 {
		content := strings.TrimSpace(currentMessage.String())
		if content != "" {
			conversation.Messages = append(conversation.Messages, ConversationMessage{
				Role:    currentRole,
				Content: content,
				Turn:    currentTurn,
			})
		}
	}

	return &conversation, nil
}

// FormatAsText 将对话格式化为纯文本
func (c *Conversation) FormatAsText() string {
	var builder strings.Builder

	for _, msg := range c.Messages {
		if msg.Role == "user" {
			builder.WriteString(fmt.Sprintf("User %d:\n", msg.Turn))
		} else if msg.Role == "assistant" {
			builder.WriteString(fmt.Sprintf("Assistant %d:\n", msg.Turn))
		} else {
			builder.WriteString(fmt.Sprintf("%s:\n", msg.Role))
		}
		builder.WriteString(msg.Content)
		builder.WriteString("\n\n")
	}

	return builder.String()
}

// FormatAsMarkdown 将对话格式化为 Markdown
func (c *Conversation) FormatAsMarkdown() string {
	var builder strings.Builder

	for _, msg := range c.Messages {
		if msg.Role == "user" {
			builder.WriteString(fmt.Sprintf("### User %d\n\n", msg.Turn))
		} else if msg.Role == "assistant" {
			builder.WriteString(fmt.Sprintf("### Assistant %d\n\n", msg.Turn))
		} else {
			builder.WriteString(fmt.Sprintf("### %s\n\n", msg.Role))
		}
		builder.WriteString(msg.Content)
		builder.WriteString("\n\n---\n\n")
	}

	return builder.String()
}

// GetLastAssistantResponse 获取助手的最后回复
func (c *Conversation) GetLastAssistantResponse() string {
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if c.Messages[i].Role == "assistant" {
			return c.Messages[i].Content
		}
	}
	return ""
}

// GetLastUserMessage 获取用户的最后消息
func (c *Conversation) GetLastUserMessage() string {
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if c.Messages[i].Role == "user" {
			return c.Messages[i].Content
		}
	}
	return ""
}
