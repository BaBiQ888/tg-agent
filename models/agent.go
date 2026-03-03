package models

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Agent 代表 agents 表的记录
type Agent struct {
	ID          uuid.UUID `json:"id"`
	BotID       uuid.UUID `json:"bot_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Language    string    `json:"language"`
	RoleID      uuid.UUID `json:"role_id"`
	OwnerID     int64     `json:"owner_id"`
	Knowledges  []string  `json:"knowledges"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	DeletedAt   time.Time `json:"deleted_at,omitempty"`
}

// AgentRole 代表 agent_roles 表的记录
type AgentRole struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// KBCommandState 知识库命令状态
type KBCommandState struct {
	Command            string
	Step               int
	OwnerID            int64
	AgentID            string
	LocalDatasetID     string
	DatasetID          string
	AddType            string
	CollectionID       string
	AgentSelectionDone bool
	AvailableAgents    []Agent
	SelectedAgent      *Agent
}

// CreateAgent 创建新的 agent 及相关记录
func CreateAgent(ctx context.Context, botID string, agentName string, roleID uuid.UUID, chatID int64, ownerID int64, knowledges []string) (uuid.UUID, error) {
	log.Printf("Creating agent: name=%s, botID=%s, chatID=%d, ownerID=%d", agentName, botID, chatID, ownerID)

	tx, err := DB().Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("error starting transaction: %v", err)
	}
	defer tx.Rollback(ctx)

	// 获取角色名称
	var roleName string
	err = tx.QueryRow(ctx, `
		SELECT name FROM agent_roles WHERE id = $1
	`, roleID).Scan(&roleName)
	if err != nil {
		return uuid.Nil, fmt.Errorf("error getting role name: %v", err)
	}

	log.Printf("Got role name: %s", roleName)

	// 处理机器人名称
	botName := strings.TrimSuffix(agentName, "Bot")
	description := fmt.Sprintf("%s(%s)", botName, roleName)

	log.Printf("Creating agent with name: %s, description: %s", agentName, description)

	// knowledgesJSON, err := json.Marshal(knowledges)
	// if err != nil {
	// 	return uuid.Nil, fmt.Errorf("error marshaling knowledges: %v", err)
	// }

	var newAgentID uuid.UUID
	err = tx.QueryRow(ctx, `
		WITH new_agent AS (
			INSERT INTO agents (
				bot_id, name, description, role_id,
				language, owner_id, status, knowledges
			) VALUES (
				$1, $2, $3, $4,
				'English', $6, 1, array(SELECT jsonb_build_object('datasetId', v) FROM unnest($7::text[]) AS v)
			) RETURNING id
		),
		new_agent_chat AS (
			INSERT INTO agent_chats (
				chat_id, bot_id, agent_id
			) SELECT $5, $1, id FROM new_agent
		),
		new_agent_action AS (
			INSERT INTO agent_actions (
				trigger_action, trigger_chat_id, trigger_agent_id,
				action_id, action_agents, trigger_bot_id
			) SELECT 
				'chat', $5, id,
				'baf75e65-efc2-45e2-a19d-c78d4617352d',
				jsonb_build_array(id::text),
				$1
			FROM new_agent
		)
		SELECT id FROM new_agent
	`, botID, agentName, description, roleID, chatID, ownerID, knowledges).Scan(&newAgentID)

	if err != nil {
		log.Printf("Error creating agent: %v", err)
		return uuid.Nil, fmt.Errorf("error executing combined insert: %v", err)
	}

	log.Printf("Successfully created agent with ID: %s", newAgentID)

	if err = tx.Commit(ctx); err != nil {
		log.Printf("Error committing transaction: %v", err)
		return uuid.Nil, fmt.Errorf("error committing transaction: %v", err)
	}

	log.Printf("Successfully committed transaction")
	return newAgentID, nil
}

// StopAgent 停止 agent 及相关记录
func StopAgent(ctx context.Context, chatID int64, botID string) error {
	tx, err := DB().Begin(ctx)
	if err != nil {
		return fmt.Errorf("error starting transaction: %v", err)
	}
	defer tx.Rollback(ctx)

	var agentID string
	err = tx.QueryRow(ctx, `
		SELECT agent_id
		FROM agent_chats
		WHERE chat_id = $1 AND bot_id = $2
		AND deleted_at IS NULL
	`, chatID, botID).Scan(&agentID)

	if err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("agent not found")
		}
		return fmt.Errorf("error finding agent: %v", err)
	}

	// 删除 agent_actions 记录
	_, err = tx.Exec(ctx, `
		UPDATE agent_actions
		SET deleted_at = NOW()
		WHERE trigger_agent_id = $1
	`, agentID)
	if err != nil {
		return fmt.Errorf("error deleting agent_actions: %v", err)
	}

	// 删除 agent_chats 记录
	_, err = tx.Exec(ctx, `
		UPDATE agent_chats
		SET deleted_at = NOW()
		WHERE agent_id = $1
	`, agentID)
	if err != nil {
		return fmt.Errorf("error deleting agent_chats: %v", err)
	}

	// 删除 agents 记录
	_, err = tx.Exec(ctx, `
		UPDATE agents
		SET deleted_at = NOW()
		WHERE id = $1
	`, agentID)
	if err != nil {
		return fmt.Errorf("error deleting agent: %v", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("error committing transaction: %v", err)
	}

	return nil
}

// GetAgentActions 获取 agent actions
func GetAgentActions(ctx context.Context, chatID int64, botID string, triggerAction string) ([]AgentAction, error) {
	rows, err := DB().Query(ctx, `
		SELECT id, action_id, trigger_agent_id, action_agents
		FROM agent_actions 
		WHERE trigger_chat_id = $1 
		AND trigger_bot_id = (
			SELECT UNNEST(STRING_TO_ARRAY($2, ',')::uuid[]) 
			LIMIT 1
		)
		AND trigger_action = $3
		AND deleted_at IS NULL
		LIMIT 1
	`, chatID, botID, triggerAction)

	if err != nil {
		return nil, fmt.Errorf("error querying agent_actions: %v", err)
	}
	defer rows.Close()

	var actions []AgentAction
	for rows.Next() {
		var action AgentAction
		err := rows.Scan(&action.ID, &action.ActionID, &action.TriggerAgentID, &action.ActionAgents)
		if err != nil {
			return nil, fmt.Errorf("error scanning agent_action: %v", err)
		}
		actions = append(actions, action)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %v", err)
	}

	return actions, nil
}

// GetBotByID 从数据库获取机器人信息
func GetBotByID(ctx context.Context, botID string) (*Bot, error) {
	var bot Bot
	err := DB().QueryRow(ctx, `
		SELECT id, bot_name, role_id, bot_name as name
		FROM bots WHERE id = $1
	`, botID).Scan(&bot.ID, &bot.BotName, &bot.RoleID, &bot.Name)
	if err != nil {
		return nil, err
	}
	return &bot, nil
}

// GetAgentID 通过 chat_id 和 bot_id 获取 agent_id
func GetAgentID(ctx context.Context, chatID int64, botID string) (string, error) {
	var agentID string
	err := DB().QueryRow(ctx, `
		SELECT agent_id
		FROM agent_chats
		WHERE chat_id = $1 AND bot_id = $2
		AND deleted_at IS NULL
	`, chatID, botID).Scan(&agentID)

	if err != nil {
		return "", fmt.Errorf("error getting agent ID: %v", err)
	}
	return agentID, nil
}

// GetAgentByChat 新增联合查询方法
func GetAgentByChat(ctx context.Context, chatID int64, botID string) (*Agent, error) {
	var agent Agent
	err := DB().QueryRow(ctx, `
		SELECT 
			a.id, a.name, a.description, 
			a.language, a.role_id, a.created_at,
			a.owner_id, a.knowledges
		FROM agents a
		JOIN agent_chats ac ON a.id = ac.agent_id
		WHERE ac.chat_id = $1 
		AND ac.bot_id = $2::uuid
		AND a.deleted_at IS NULL
		LIMIT 1
	`, chatID, botID).Scan(
		&agent.ID,
		&agent.Name,
		&agent.Description,
		&agent.Language,
		&agent.RoleID,
		&agent.CreatedAt,
		&agent.OwnerID,
		&agent.Knowledges,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pgx.ErrNoRows
		}
		return nil, fmt.Errorf("scan agent error: %w", err)
	}
	return &agent, nil
}

// GetRoleByID 新增角色查询方法
func GetRoleByID(ctx context.Context, roleID string) (*AgentRole, error) {
	var role AgentRole
	err := DB().QueryRow(ctx, `
		SELECT id, name, description 
		FROM agent_roles 
		WHERE id = $1
	`, roleID).Scan(&role.ID, &role.Name, &role.Description)

	if err != nil {
		return nil, fmt.Errorf("role query error: %v", err)
	}
	return &role, nil
}

// GetAgentsByBotID 获取指定 bot 下的所有活跃 agents
func GetAgentsByBotID(ctx context.Context, botID string) ([]Agent, error) {
	var agents []Agent
	rows, err := DB().Query(ctx, `
		SELECT 
			a.id, a.name, a.description, 
			a.language, a.role_id, a.created_at,
			a.owner_id, a.knowledges
		FROM agents a
		WHERE a.bot_id = $1::uuid 
		AND a.deleted_at IS NULL
		ORDER BY a.created_at DESC
	`, botID)

	if err != nil {
		return nil, fmt.Errorf("query agents error: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var agent Agent
		err := rows.Scan(
			&agent.ID,
			&agent.Name,
			&agent.Description,
			&agent.Language,
			&agent.RoleID,
			&agent.CreatedAt,
			&agent.OwnerID,
			&agent.Knowledges,
		)
		if err != nil {
			return nil, fmt.Errorf("scan agent error: %w", err)
		}
		agents = append(agents, agent)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows error: %w", err)
	}

	return agents, nil
}
