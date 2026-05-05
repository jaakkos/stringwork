package collab

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

// registerSendMessage registers the send_message tool.
func registerSendMessage(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("send_message",
			mcp.WithDescription("Send a message to another AI agent. Use this to communicate findings, ask questions, or coordinate work."),
			mcp.WithString("from", mcp.Required(), mcp.Description("Sender identifier (e.g., 'cursor', 'claude-code')")),
			mcp.WithString("to", mcp.Required(), mcp.Description("Recipient identifier (e.g., 'cursor', 'claude-code', 'all')")),
			mcp.WithString("content", mcp.Required(), mcp.Description("Message content - can include code, questions, suggestions")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			from, _ := args["from"].(string)
			to, _ := args["to"].(string)
			content, _ := args["content"].(string)

			if from == "" || to == "" || content == "" {
				return nil, fmt.Errorf("from, to, and content are required")
			}

			var msgID int
			if err := svc.Run(func(state *domain.CollabState) error {
				extra := app.RegisteredAgentNames(state)
				if err := app.ValidateAgent(from, state, false, false, extra...); err != nil {
					return err
				}
				if err := app.ValidateAgent(to, state, false, true, extra...); err != nil {
					return err
				}

				now := time.Now()
				msg := domain.Message{
					ID:        state.NextMsgID,
					From:      from,
					To:        to,
					Content:   content,
					Timestamp: now,
					Read:      false,
				}
				state.Messages = append(state.Messages, msg)
				msgID = state.NextMsgID
				state.NextMsgID++

				// Track the most recent send_message per agent so the
				// watchdog's auto-cancel logic can grant a "recent send"
				// grace window — we don't want to kill a worker that's
				// mid-deliverable just because heartbeats lapsed.
				if state.LastSendByAgent == nil {
					state.LastSendByAgent = make(map[string]time.Time)
				}
				state.LastSendByAgent[from] = now

				pruned := app.PruneMessages(state, svc.Policy().MessageRetentionMax(), svc.Policy().MessageRetentionDays())
				if pruned > 0 {
					logger.Printf("Pruned %d old messages", pruned)
				}
				return nil
			}); err != nil {
				return nil, err
			}

			logger.Printf("Message sent from %s to %s", from, to)
			return mcp.NewToolResultText(fmt.Sprintf("Message #%d sent to %s", msgID, to)), nil
		},
	)
}

// registerReadMessages registers the read_messages tool.
func registerReadMessages(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("read_messages",
			mcp.WithDescription("Read messages sent to you from other AI agents. Check this regularly to see if your pair has sent you anything."),
			mcp.WithString("for", mcp.Required(), mcp.Description("Read messages for this recipient (e.g., 'cursor', 'claude-code-1')")),
			mcp.WithString("agent_type", mcp.Description("Optional: also include messages addressed to this agent type (e.g. 'claude-code'). Pool worker instances pass their type so type-level messages drain when the first instance reads them, instead of perpetually piling up because each instance only matches its own InstanceID.")),
			mcp.WithBoolean("unread_only", mcp.Description("Only show unread messages (default: false)")),
			mcp.WithNumber("limit", mcp.Description("Maximum number of messages to return (default: 10)")),
			mcp.WithBoolean("mark_read", mcp.Description("Mark returned messages as read (default: true)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			recipient, _ := args["for"].(string)
			if recipient == "" {
				return nil, fmt.Errorf("'for' is required")
			}
			agentType, _ := args["agent_type"].(string)

			unreadOnly := false
			if v, ok := args["unread_only"].(bool); ok {
				unreadOnly = v
			}
			limit := 10
			if v, ok := args["limit"].(float64); ok {
				limit = int(v)
				if limit < 1 {
					limit = 1
				}
				if limit > 100 {
					limit = 100
				}
			}
			markRead := true
			if v, ok := args["mark_read"].(bool); ok {
				markRead = v
			}

			var messages []domain.Message
			// Use Query (read-only) when markRead is false to avoid unnecessary DB writes.
			readFn := func(state *domain.CollabState) error {
				extra := app.RegisteredAgentNames(state)
				if err := app.ValidateAgent(recipient, state, false, false, extra...); err != nil {
					return err
				}

				collected := make([]domain.Message, 0, limit)
				for i := len(state.Messages) - 1; i >= 0 && len(collected) < limit; i-- {
					msg := state.Messages[i]
					if !messageAddressedTo(msg.To, recipient, agentType) {
						continue
					}
					if unreadOnly && msg.Read {
						continue
					}
					collected = append(collected, msg)
					if markRead {
						state.Messages[i].Read = true
					}
				}
				messages = collected
				if markRead && len(messages) > 0 {
					if agentCtx, exists := state.AgentContexts[recipient]; exists {
						agentCtx.LastCheckedMsgID = state.NextMsgID - 1
						agentCtx.LastCheckTime = time.Now()
					} else {
						state.AgentContexts[recipient] = &domain.AgentContext{
							Agent:             recipient,
							LastCheckedMsgID:  state.NextMsgID - 1,
							LastCheckedTaskID: state.NextTaskID - 1,
							LastCheckTime:     time.Now(),
						}
					}
				}
				return nil
			}
			var err error
			if markRead {
				err = svc.Run(readFn)
			} else {
				err = svc.Query(readFn)
			}
			if err != nil {
				return nil, err
			}

			if len(messages) == 0 {
				return mcp.NewToolResultText("No messages"), nil
			}

			var result string
			for _, msg := range messages {
				result += fmt.Sprintf("--- Message #%d from %s (%s) ---\n%s\n\n",
					msg.ID, msg.From, msg.Timestamp.Format("2006-01-02 15:04:05"), msg.Content)
			}

			if agentType != "" {
				logger.Printf("Read %d messages for %s (incl. type %s)", len(messages), recipient, agentType)
			} else {
				logger.Printf("Read %d messages for %s", len(messages), recipient)
			}
			return mcp.NewToolResultText(result), nil
		},
	)
}

// messageAddressedTo reports whether a message's To field matches a
// reader. The reader matches if the message is broadcast ("all"), if To
// equals the reader's instance ID (recipient), or — when agentType is
// non-empty — if To equals the reader's pool agent type. Pool worker
// instances pass agentType so type-level messages drain when ANY
// instance reads. Without that, messages addressed to e.g. "claude-code"
// pile up forever because each instance only matches its own
// "claude-code-N" InstanceID, fueling a perpetual unread-driven spawn
// loop in WorkerManager.Check.
func messageAddressedTo(to, recipient, agentType string) bool {
	if to == "all" || to == recipient {
		return true
	}
	if agentType != "" && to == agentType {
		return true
	}
	return false
}
