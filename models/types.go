package models

import (
	"encoding/json"
	"time"
)

// Command 命令结构
type Command struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	ReplyTip    string `json:"reply_tip"`
	// Actions 是一个 map，其中键是 action id，值是 Action 结构
	Actions  map[string]Action `json:"actions"`
	IsActive bool              `json:"is_active"`
}

// Action 动作结构
type Action struct {
	Path        string          `json:"path"`
	APIKey      string          `json:"api_key"`
	InputParam  json.RawMessage `json:"input_param"`
	OutputParam json.RawMessage `json:"output_param"`
	ActionTip   string          `json:"action_tip"`
	Platform    string          `json:"platform"`
	NeedConfirm bool            `json:"need_confirm"`
}

type CustomWebhookRequest struct {
	BotID   string `json:"bot_id"`
	ChatID  int64  `json:"chat_id"`
	Message string `json:"message"`
}

// AgentAction 代表 agent_actions 表的记录
type AgentAction struct {
	ID             string          `json:"id"`
	TriggerAction  string          `json:"trigger_action"`
	TriggerChatID  int64           `json:"trigger_chat_id"`
	TriggerAgentID string          `json:"trigger_agent_id"`
	ActionID       string          `json:"action_id"`
	ActionAgents   json.RawMessage `json:"action_agents"`
	TriggerBotID   string          `json:"trigger_bot_id"`
}

// CmdAction 命令动作结构体
type CmdAction struct {
	Path         string          `json:"path"`
	APIKey       string          `json:"api_key"`
	InputParam   json.RawMessage `json:"input_param"`
	OutputParam  json.RawMessage `json:"output_param"`
	ReturnResult bool            `json:"return_result"`
	Platform     string          `json:"platform"`
}

// UserState 用户状态
type UserState struct {
	Command         *Command        `json:"command,omitempty"`
	Action          *Action         `json:"action,omitempty"`
	MessageIDs      []int           `json:"message_ids,omitempty"`
	WaitingForInput bool            `json:"waiting_for_input"`
	KBState         *KBCommandState `json:"kb_state,omitempty"`
	LastMessageID   int             `json:"last_message_id,omitempty"`
	LastUpdateTime  time.Time       `json:"last_update_time"`
}
