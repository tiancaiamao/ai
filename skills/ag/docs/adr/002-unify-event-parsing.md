# ADR 002: Unify Event Parsing into `conv` Package

Three separate implementations parse events.jsonl: `parseAssistantMessages` (agent_client.go), `parseConversation` (conversation.go), and manual line-by-line in formatted_writer.go. Each handles streaming text accumulation, turn boundaries, and message extraction differently.

Extend `conv` with a `ConversationBuilder` that accumulates streaming deltas into complete messages. All three consumers use this single implementation. The builder produces `[]Message` with role, content, and turn metadata.