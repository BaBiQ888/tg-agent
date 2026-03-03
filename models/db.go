package models

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/Tom-Jerry/TGAgent/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var db *pgxpool.Pool

// Bot 结构体
type Bot struct {
	ID        uuid.UUID `json:"id"`
	BotName   string    `json:"bot_name"`
	Token     string    `json:"token"`
	RoleID    uuid.UUID `json:"role_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func InitDB(cfg *config.Config) error {
	config, err := pgxpool.ParseConfig(cfg.Database.DSN)
	if err != nil {
		return fmt.Errorf("unable to parse database config: %v", err)
	}

	// 使用简单查询协议
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	// 解析时间配置
	maxConnLifetime, err := time.ParseDuration(cfg.Database.Pool.MaxConnLifetime)
	if err != nil {
		return fmt.Errorf("invalid max_conn_lifetime: %v", err)
	}

	maxConnIdleTime, err := time.ParseDuration(cfg.Database.Pool.MaxConnIdleTime)
	if err != nil {
		return fmt.Errorf("invalid max_conn_idle_time: %v", err)
	}

	// 设置连接池参数
	config.MaxConns = cfg.Database.Pool.MaxConns
	config.MinConns = cfg.Database.Pool.MinConns
	config.MaxConnLifetime = maxConnLifetime
	config.MaxConnIdleTime = maxConnIdleTime

	db, err = pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		return fmt.Errorf("unable to connect to database: %v", err)
	}

	return db.Ping(context.Background())
}

// GetBots 获取所有机器人配置
func GetBots(ctx context.Context, runEnv string) ([]Bot, error) {
	log.Printf("Fetching bots from database...")

	// 使用简单查询而不是预处理语句
	rows, err := db.Query(ctx, `
		SELECT id, bot_name, token, created_at, updated_at 
		FROM bots WHERE run_env = $1
		ORDER BY created_at DESC
	`, runEnv)
	if err != nil {
		return nil, fmt.Errorf("error querying bots: %v", err)
	}
	defer rows.Close()

	var bots []Bot
	for rows.Next() {
		var bot Bot
		if err := rows.Scan(
			&bot.ID,
			&bot.BotName,
			&bot.Token,
			&bot.CreatedAt,
			&bot.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("error scanning bot: %v", err)
		}
		bots = append(bots, bot)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating bots: %v", err)
	}

	log.Printf("Found %d bots", len(bots))
	return bots, nil
}

// GetCommands 获取所有命令及其对应的动作
func GetCommands(ctx context.Context) ([]Command, error) {
	log.Printf("Starting to fetch commands from database...")

	// 首先检查表中的记录数
	var cmdCount int
	err := db.QueryRow(ctx, "SELECT COUNT(*) FROM bot_cmds").Scan(&cmdCount)
	if err != nil {
		return nil, fmt.Errorf("error counting bot_cmds: %v", err)
	}
	log.Printf("Found %d commands in bot_cmds table", cmdCount)

	// 检查命令是否有关联的动作
	var cmdWithActionsCount int
	err = db.QueryRow(ctx, `
		SELECT COUNT(DISTINCT c.id)
		FROM bot_cmds c
		INNER JOIN cmd_actions ca ON c.id = ca.cmd_id
		WHERE ca.deleted_at IS NULL
	`).Scan(&cmdWithActionsCount)
	if err != nil {
		return nil, fmt.Errorf("error counting commands with actions: %v", err)
	}
	log.Printf("Found %d commands with active actions", cmdWithActionsCount)

	// 如果没有命令或者没有命令关联了动作，返回空列表
	if cmdCount == 0 || cmdWithActionsCount == 0 {
		log.Printf("No valid commands with actions found")
		return []Command{}, nil
	}

	// 修改 SQL 查询以适配新的表结构
	rows, err := db.Query(ctx, `
		SELECT c.command, c.description, c.reply_tip,c.is_active,
			jsonb_object_agg(
				a.id,
				jsonb_build_object(
					'path', a.path,
					'api_key', a.api_key,
					'input_param', a.input_param,
					'output_param', a.output_param,
					'action_tip', a.action_tip,
					'platform', a.platform,
					'need_confirm', a.need_confirm
				)
			) FILTER (WHERE a.id IS NOT NULL) as actions
		FROM bot_cmds c
		JOIN cmd_actions ca ON c.id = ca.cmd_id
		JOIN actions a ON ca.action_id = a.id
		WHERE ca.deleted_at IS NULL
		GROUP BY c.command, c.description, c.reply_tip,c.is_active
	`)

	if err != nil {
		return nil, fmt.Errorf("error querying commands: %v", err)
	}
	defer rows.Close()

	var commands []Command
	for rows.Next() {
		var cmd Command
		var actionsJSON []byte
		if err := rows.Scan(&cmd.Command, &cmd.Description, &cmd.ReplyTip, &cmd.IsActive, &actionsJSON); err != nil {
			log.Printf("Error scanning command: %v", err)
			return nil, fmt.Errorf("error scanning command: %v", err)
		}

		log.Printf("Raw command data: command=%s, description=%s, reply_tip=%s",
			cmd.Command, cmd.Description, cmd.ReplyTip)
		log.Printf("IsActive: %v", cmd.IsActive)
		log.Printf("Raw actions JSON: %s", string(actionsJSON))

		if err := json.Unmarshal(actionsJSON, &cmd.Actions); err != nil {
			log.Printf("Error parsing actions JSON: %v", err)
			return nil, fmt.Errorf("error parsing actions JSON: %v", err)
		}

		commands = append(commands, cmd)
	}

	log.Printf("Total commands loaded: %d", len(commands))
	return commands, nil
}

// GetBotIDByName 通过 bot_name 获取 bot ID
func GetBotIDByName(ctx context.Context, botName string) (string, error) {
	var botID string
	err := db.QueryRow(ctx, `
		SELECT id 
		FROM bots 
		WHERE bot_name = $1
	`, botName).Scan(&botID)

	if err != nil {
		return "", fmt.Errorf("error getting bot ID: %v", err)
	}
	return botID, nil
}

// GetCmdAction 获取命令动作
func GetCmdAction(ctx context.Context, actionID string) (*CmdAction, error) {
	log.Printf("Getting action for ID: %s", actionID)

	var cmdAction CmdAction
	err := db.QueryRow(ctx, `
		SELECT path, api_key, input_param, output_param, return_result, platform
		FROM actions 
		WHERE id = $1
	`, actionID).Scan(
		&cmdAction.Path,
		&cmdAction.APIKey,
		&cmdAction.InputParam,
		&cmdAction.OutputParam,
		&cmdAction.ReturnResult,
		&cmdAction.Platform,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			log.Printf("No action found for ID %s", actionID)
			return nil, fmt.Errorf("action not found: %s", actionID)
		}
		log.Printf("Error querying action: %v", err)
		return nil, fmt.Errorf("error getting action: %v", err)
	}

	log.Printf("Found action: path=%s", cmdAction.Path)
	return &cmdAction, nil
}

// DB 返回数据库连接池实例
func DB() *pgxpool.Pool {
	return db
}
