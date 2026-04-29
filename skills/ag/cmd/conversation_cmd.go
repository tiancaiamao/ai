package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var conversationCmd = &cobra.Command{
	Use:   "conversation <id>",
	Short: "Show agent conversation in a cleaner format",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		format, _ := cmd.Flags().GetString("format")

		// 获取对话
		conversation, err := GetConversation(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		// 根据格式输出
		switch format {
		case "json":
			data, _ := json.MarshalIndent(conversation, "", "  ")
			fmt.Println(string(data))
		case "markdown":
			fmt.Println(conversation.FormatAsMarkdown())
		case "text":
			fmt.Println(conversation.FormatAsText())
		case "last-assistant":
			fmt.Println(conversation.GetLastAssistantResponse())
		case "last-user":
			fmt.Println(conversation.GetLastUserMessage())
		default:
			fmt.Println(conversation.FormatAsText())
		}
	},
}

func init() {
	conversationCmd.Flags().String("format", "text", "Output format: text, markdown, json, last-assistant, last-user")
}

func AddConversationCommand(parentCmd *cobra.Command) {
	parentCmd.AddCommand(conversationCmd)
}
