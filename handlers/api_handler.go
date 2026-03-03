package handlers

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/Tom-Jerry/TGAgent/config"
	"github.com/Tom-Jerry/TGAgent/models"
	"github.com/Tom-Jerry/TGAgent/services"
	"github.com/goccy/go-json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/valyala/fasthttp"
)

type APIHandler struct {
	webhookHandler *WebhookHandler
	apiKey         string
	cfg            *config.Config
}

func NewAPIHandler(webhookHandler *WebhookHandler, apiKey string, cfg *config.Config) *APIHandler {
	return &APIHandler{
		webhookHandler: webhookHandler,
		apiKey:         apiKey,
		cfg:            cfg,
	}
}

// 验证 API Key
func (h *APIHandler) validateAPIKey(ctx *fasthttp.RequestCtx) bool {
	apiKey := string(ctx.Request.Header.Peek("X-API-Key"))
	if apiKey != h.apiKey {
		response := APIResponse{
			Code:    401,
			Message: "Unauthorized",
			Data:    nil,
		}
		responseJSON, _ := json.Marshal(response)
		ctx.SetStatusCode(fasthttp.StatusUnauthorized)
		ctx.SetBody(responseJSON)
		return false
	}
	return true
}

// QueryAgentRequest 查询 agent 的请求结构
type QueryAgentRequest struct {
	ChatID int64  `json:"chat_id"`
	BotID  string `json:"bot_id"`
}

// QueryAgentResponse agent 信息响应结构
type QueryAgentResponse struct {
	AgentID     string `json:"agent_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Language    string `json:"language"`
	RoleName    string `json:"role_name"`
}

// HandleQueryAgent 处理查询 agent 的请求
func (h *APIHandler) HandleQueryAgent(ctx *fasthttp.RequestCtx) {
	if !h.validateAPIKey(ctx) {
		return
	}

	var req QueryAgentRequest
	if err := json.Unmarshal(ctx.Request.Body(), &req); err != nil {
		response := APIResponse{
			Code:    400,
			Message: "Invalid request format",
			Data:    nil,
		}
		responseJSON, _ := json.Marshal(response)
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetBody(responseJSON)
		return
	}

	// 查询 agent 信息
	var agentInfo QueryAgentResponse
	err := models.DB().QueryRow(context.Background(), `
		WITH agent_info AS (
			SELECT a.id, a.name, a.description, a.language, a.role_id
			FROM agent_chats ac
			JOIN agents a ON ac.agent_id = a.id
			WHERE ac.chat_id = $1 AND ac.bot_id = $2
			AND ac.deleted_at IS NULL
		)
		SELECT ai.id, ai.name, ai.description, ai.language, ar.name
		FROM agent_info ai
		JOIN agent_roles ar ON ai.role_id = ar.id
	`, req.ChatID, req.BotID).Scan(
		&agentInfo.AgentID,
		&agentInfo.Name,
		&agentInfo.Description,
		&agentInfo.Language,
		&agentInfo.RoleName,
	)

	if err != nil {
		response := APIResponse{
			Code:    404,
			Message: "Agent not found",
			Data:    nil,
		}
		responseJSON, _ := json.Marshal(response)
		ctx.SetStatusCode(fasthttp.StatusNotFound)
		ctx.SetBody(responseJSON)
		return
	}

	response := APIResponse{
		Code:    0,
		Message: "Success",
		Data:    agentInfo,
	}
	responseJSON, _ := json.Marshal(response)
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(responseJSON)
}

// EditAgentRequest 编辑 agent 的请求结构
type EditAgentRequest struct {
	ChatID      int64   `json:"chat_id"`
	BotID       string  `json:"bot_id"`
	Description *string `json:"description,omitempty"`
	Language    *string `json:"language,omitempty"`
	RoleName    *string `json:"role_name,omitempty"`
}

// HandleEditAgent 处理编辑 agent 的请求
func (h *APIHandler) HandleEditAgent(ctx *fasthttp.RequestCtx) {
	if !h.validateAPIKey(ctx) {
		return
	}

	var req EditAgentRequest
	if err := json.Unmarshal(ctx.Request.Body(), &req); err != nil {
		response := APIResponse{
			Code:    400,
			Message: "Invalid request format",
			Data:    nil,
		}
		responseJSON, _ := json.Marshal(response)
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetBody(responseJSON)
		return
	}

	// 获取 agent_id
	var agentID string
	err := models.DB().QueryRow(context.Background(), `
		SELECT agent_id
		FROM agent_chats
		WHERE chat_id = $1 AND bot_id = $2
		AND deleted_at IS NULL
	`, req.ChatID, req.BotID).Scan(&agentID)

	if err != nil {
		response := APIResponse{
			Code:    404,
			Message: "Agent not found",
			Data:    nil,
		}
		responseJSON, _ := json.Marshal(response)
		ctx.SetStatusCode(fasthttp.StatusNotFound)
		ctx.SetBody(responseJSON)
		return
	}

	// 更新 agents 表
	_, err = models.DB().Exec(context.Background(), `
		UPDATE agents
		SET description = COALESCE($1, description),
		    language = COALESCE($2, language)
		WHERE id = $3
	`, req.Description, req.Language, agentID)

	if err != nil {
		response := APIResponse{
			Code:    500,
			Message: fmt.Sprintf("Error updating agent: %v", err),
			Data:    nil,
		}
		responseJSON, _ := json.Marshal(response)
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetBody(responseJSON)
		return
	}

	// 查询更新后的 agent 信息
	var agent struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Language    string `json:"language"`
		RoleName    string `json:"role_name"`
	}

	err = models.DB().QueryRow(context.Background(), `
		SELECT a.name, a.description, a.language, ar.name as role_name
		FROM agents a
		LEFT JOIN agent_roles ar ON a.role_id = ar.id
		WHERE a.id = $1
	`, agentID).Scan(&agent.Name, &agent.Description, &agent.Language, &agent.RoleName)

	if err != nil {
		response := APIResponse{
			Code:    500,
			Message: fmt.Sprintf("Error getting updated agent info: %v", err),
			Data:    nil,
		}
		responseJSON, _ := json.Marshal(response)
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetBody(responseJSON)
		return
	}

	response := APIResponse{
		Code:    0,
		Message: "Success",
		Data:    agent,
	}
	responseJSON, _ := json.Marshal(response)
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(responseJSON)
}

// StopRequest 停止 agent 的请求结构
type StopRequest struct {
	ChatID int64  `json:"chat_id"`
	BotID  string `json:"bot_id"`
}

// HandleStop 处理停止 agent 的请求
func (h *APIHandler) HandleStop(ctx *fasthttp.RequestCtx) {
	if !h.validateAPIKey(ctx) {
		return
	}

	var req StopRequest
	if err := json.Unmarshal(ctx.Request.Body(), &req); err != nil {
		response := APIResponse{
			Code:    400,
			Message: "Invalid request format",
			Data:    nil,
		}
		responseJSON, _ := json.Marshal(response)
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetBody(responseJSON)
		return
	}

	result, err := h.handleStopCmd(req.ChatID, req.BotID)
	if err != nil {
		response := APIResponse{
			Code:    500,
			Message: fmt.Sprintf("Error stopping agent: %v", err),
			Data:    nil,
		}
		responseJSON, _ := json.Marshal(response)
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetBody(responseJSON)
		return
	}

	response := APIResponse{
		Code:    0,
		Message: "Success",
		Data:    result,
	}
	responseJSON, _ := json.Marshal(response)
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(responseJSON)
}

// handleStopCmd 实现停止 agent 的逻辑
func (h *APIHandler) handleStopCmd(chatID int64, botID string) (string, error) {
	ctx := context.Background()
	tx, err := models.DB().Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("error starting transaction: %v", err)
	}
	defer tx.Rollback(ctx)

	// 获取 agent_id
	var agentID string
	err = tx.QueryRow(ctx, `
		SELECT agent_id
		FROM agent_chats
		WHERE chat_id = $1 AND bot_id = $2
		AND deleted_at IS NULL
	`, chatID, botID).Scan(&agentID)

	if err != nil {
		return "", fmt.Errorf("agent not found: %v", err)
	}

	// 删除 agent_actions 记录
	_, err = tx.Exec(ctx, `
		UPDATE agent_actions
		SET deleted_at = NOW()
		WHERE trigger_agent_id = $1
	`, agentID)
	if err != nil {
		return "", fmt.Errorf("error deleting agent_actions: %v", err)
	}

	// 删除 agent_chats 记录
	_, err = tx.Exec(ctx, `
		UPDATE agent_chats
		SET deleted_at = NOW()
		WHERE agent_id = $1
	`, agentID)
	if err != nil {
		return "", fmt.Errorf("error deleting agent_chats: %v", err)
	}

	// 删除 agents 记录
	_, err = tx.Exec(ctx, `
		UPDATE agents
		SET deleted_at = NOW()
		WHERE id = $1
	`, agentID)
	if err != nil {
		return "", fmt.Errorf("error deleting agent: %v", err)
	}

	// 提交事务
	if err = tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("error committing transaction: %v", err)
	}

	return "Agent stopped successfully", nil
}

// StartRequest 启动 agent 的请求结构
type StartRequest struct {
	ChatID  int64  `json:"chat_id"`
	BotID   string `json:"bot_id"`
	OwnerID int64  `json:"owner_id"`
}

// HandleStart 处理启动 agent 的请求
func (h *APIHandler) HandleStart(ctx *fasthttp.RequestCtx) {
	if !h.validateAPIKey(ctx) {
		return
	}

	var req StartRequest
	if err := json.Unmarshal(ctx.Request.Body(), &req); err != nil {
		response := APIResponse{
			Code:    400,
			Message: "Invalid request format",
			Data:    nil,
		}
		responseJSON, _ := json.Marshal(response)
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetBody(responseJSON)
		return
	}

	// 获取对应的 bot handler
	handler, ok := h.webhookHandler.GetBot(req.BotID)
	if !ok {
		response := APIResponse{
			Code:    404,
			Message: "Bot not found",
			Data:    nil,
		}
		responseJSON, _ := json.Marshal(response)
		ctx.SetStatusCode(fasthttp.StatusNotFound)
		ctx.SetBody(responseJSON)
		return
	}

	result, err := handler.HandleStartCmd(req.ChatID, req.BotID, req.OwnerID)
	if err != nil {
		response := APIResponse{
			Code:    500,
			Message: fmt.Sprintf("Error starting agent: %v", err),
			Data:    nil,
		}
		responseJSON, _ := json.Marshal(response)
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetBody(responseJSON)
		return
	}

	response := APIResponse{
		Code:    0,
		Message: "Success",
		Data:    result,
	}
	responseJSON, _ := json.Marshal(response)
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(responseJSON)
}

// AgentOperationRequest 统一的 agent 操作请求结构
type AgentOperationRequest struct {
	OpType    string    `json:"op_type"` // create/query/update
	AgentInfo AgentInfo `json:"agent_info"`
}

// AgentInfo agent 信息结构
type AgentInfo struct {
	ChatID      int64     `json:"chat_id"`
	BotID       uuid.UUID `json:"bot_id"`         // 改为 UUID 类型
	OwnerID     int64     `json:"owner_id"`       // 创建时必需
	Name        *string   `json:"name,omitempty"` // 添加 name 字段
	RoleName    string    `json:"role_name"`      // Changed from RoleID
	Description *string   `json:"description,omitempty"`
	Language    *string   `json:"language,omitempty"`
	Status      *int16    `json:"status,omitempty"` // 添加 status 字段
}

// HandleAgent 统一处理 agent 相关操作
func (h *APIHandler) HandleAgent(ctx *fasthttp.RequestCtx) {
	log.Printf("[API] Handling agent request: %s", string(ctx.Request.Body()))

	// 验证 API Key
	if !h.validateAPIKey(ctx) {
		log.Printf("[API] API Key validation failed")
		return
	}

	// 解析请求
	var req AgentOperationRequest
	if err := json.Unmarshal(ctx.Request.Body(), &req); err != nil {
		log.Printf("[API] Error parsing request: %v", err)
		h.sendErrorResponse(ctx, 400, "Invalid request format", err)
		return
	}
	log.Printf("[API] Parsed request: %+v", req)

	// 验证操作类型
	if req.OpType == "" {
		log.Printf("[API] Missing operation type")
		h.sendErrorResponse(ctx, 400, "Missing operation type", nil)
		return
	}
	log.Printf("[API] Operation type: %s", req.OpType)

	// 获取 agent_id
	var agentID string
	err := models.DB().QueryRow(context.Background(), `
		SELECT agent_id
		FROM agent_chats
		WHERE chat_id = $1 AND bot_id = $2
		AND deleted_at IS NULL
	`, req.AgentInfo.ChatID, req.AgentInfo.BotID).Scan(&agentID)

	if err != nil {
		log.Printf("[API] Error getting agent ID: %v", err)
		h.sendErrorResponse(ctx, 404, "Agent not found", err)
		return
	}
	log.Printf("[API] Found agent ID: %s", agentID)

	var result interface{}

	// 根据操作类型调用相应的处理函数
	switch req.OpType {
	case "create":
		// 1. 检查必要参数
		if req.AgentInfo.OwnerID == 0 || req.AgentInfo.RoleName == "" {
			response := APIResponse{
				Code:    400,
				Message: "Missing required fields: owner_id or role_name for create operation",
				Data:    nil,
			}
			responseJSON, _ := json.Marshal(response)
			ctx.SetStatusCode(fasthttp.StatusBadRequest)
			ctx.SetBody(responseJSON)
			return
		}

		// 2. 检查是否已存在 agent
		var existingAgent struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Language    string `json:"language"`
			RoleName    string `json:"role_name"`
		}
		err := models.DB().QueryRow(context.Background(), `
			WITH agent_info AS (
				SELECT a.id, a.name, a.description, a.language, a.role_id
				FROM agent_chats ac
				JOIN agents a ON ac.agent_id = a.id
				WHERE ac.chat_id = $1 AND ac.bot_id = $2
				AND ac.deleted_at IS NULL
			)
			SELECT ai.id, ai.name, ai.description, ai.language, ar.name
			FROM agent_info ai
			JOIN agent_roles ar ON ai.role_id = ar.id
		`, req.AgentInfo.ChatID, req.AgentInfo.BotID.String()).Scan(
			&existingAgent.ID,
			&existingAgent.Name,
			&existingAgent.Description,
			&existingAgent.Language,
			&existingAgent.RoleName,
		)

		if err == nil {
			// Agent 已存在，返回现有信息
			log.Printf("Agent already exists: id=%s, name=%s", existingAgent.ID, existingAgent.Name)
			response := APIResponse{
				Code:    409,
				Message: "Agent already exists",
				Data:    existingAgent,
			}
			responseJSON, _ := json.Marshal(response)
			ctx.SetStatusCode(fasthttp.StatusConflict)
			ctx.SetBody(responseJSON)
			return
		}

		if err != pgx.ErrNoRows {
			// 发生其他错误
			log.Printf("Error checking existing agent: %v", err)
			response := APIResponse{
				Code:    500,
				Message: fmt.Sprintf("Error checking existing agent: %v", err),
				Data:    nil,
			}
			responseJSON, _ := json.Marshal(response)
			ctx.SetStatusCode(fasthttp.StatusInternalServerError)
			ctx.SetBody(responseJSON)
			return
		}

		// 3. 获取 RoleID
		roleID, err := getRoleIDByName(context.Background(), req.AgentInfo.RoleName)
		if err != nil {
			log.Printf("Error getting role ID: %v", err)
			response := APIResponse{
				Code:    400,
				Message: fmt.Sprintf("Invalid role_name: %v", err),
				Data:    nil,
			}
			responseJSON, _ := json.Marshal(response)
			ctx.SetStatusCode(fasthttp.StatusBadRequest)
			ctx.SetBody(responseJSON)
			return
		}

		// 4. 生成 agent 名称
		agentName := ""
		if req.AgentInfo.Name != nil {
			agentName = *req.AgentInfo.Name
		} else {
			chatIDStr := generateChatIDStr(req.AgentInfo.ChatID)
			agentName = fmt.Sprintf("agent_%s", chatIDStr)
		}

		// 5. 创建知识库
		kbService := services.NewKBService(h.cfg.AIService.BaseURL, h.cfg.AIService.APIKey)
		datasetID, err := kbService.CreateDataset(fmt.Sprintf("KB_%s", agentName))
		if err != nil {
			log.Printf("Error creating knowledge base: %v", err)
			response := APIResponse{
				Code:    500,
				Message: fmt.Sprintf("Error creating knowledge base: %v", err),
				Data:    nil,
			}
			responseJSON, _ := json.Marshal(response)
			ctx.SetStatusCode(fasthttp.StatusInternalServerError)
			ctx.SetBody(responseJSON)
			return
		}
		log.Printf("Created knowledge base dataset: %s", datasetID)

		// 6. 创建 agent
		knowledges := []string{datasetID}
		newAgentID, err := models.CreateAgent(
			context.Background(),
			req.AgentInfo.BotID.String(),
			agentName,
			roleID,
			req.AgentInfo.ChatID,
			req.AgentInfo.OwnerID,
			knowledges,
		)
		if err != nil {
			log.Printf("Error creating agent: %v", err)
			response := APIResponse{
				Code:    500,
				Message: fmt.Sprintf("Error creating agent: %v", err),
				Data:    nil,
			}
			responseJSON, _ := json.Marshal(response)
			ctx.SetStatusCode(fasthttp.StatusInternalServerError)
			ctx.SetBody(responseJSON)
			return
		}

		// 7. 查询创建的 agent 信息
		var agent struct {
			ID          string   `json:"id"`
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Language    string   `json:"language"`
			RoleName    string   `json:"role_name"`
			Knowledges  []string `json:"knowledges"`
		}
		err = models.DB().QueryRow(context.Background(), `
			SELECT a.id, a.name, a.description, a.language, ar.name, a.knowledges
			FROM agents a
			LEFT JOIN agent_roles ar ON a.role_id = ar.id
			WHERE a.id = $1
		`, newAgentID).Scan(&agent.ID, &agent.Name, &agent.Description, &agent.Language, &agent.RoleName, &agent.Knowledges)

		if err != nil {
			log.Printf("Error fetching created agent: %v", err)
			response := APIResponse{
				Code:    500,
				Message: fmt.Sprintf("Error fetching created agent: %v", err),
				Data:    nil,
			}
			responseJSON, _ := json.Marshal(response)
			ctx.SetStatusCode(fasthttp.StatusInternalServerError)
			ctx.SetBody(responseJSON)
			return
		}

		result = agent

	case "query":
		var agentInfo QueryAgentResponse
		err = models.DB().QueryRow(context.Background(), `
			WITH agent_info AS (
				SELECT a.id, a.name, a.description, a.language, a.role_id
				FROM agent_chats ac
				JOIN agents a ON ac.agent_id = a.id
				WHERE ac.chat_id = $1 AND ac.bot_id = $2
				AND ac.deleted_at IS NULL
			)
			SELECT ai.id, ai.name, ai.description, ai.language, ar.name
			FROM agent_info ai
			JOIN agent_roles ar ON ai.role_id = ar.id
		`, req.AgentInfo.ChatID, req.AgentInfo.BotID.String()).Scan(
			&agentInfo.AgentID,
			&agentInfo.Name,
			&agentInfo.Description,
			&agentInfo.Language,
			&agentInfo.RoleName,
		)
		result = agentInfo

	case "update":
		// 获取 agent_id
		var agentID string
		err = models.DB().QueryRow(context.Background(), `
			SELECT agent_id
			FROM agent_chats
			WHERE chat_id = $1 AND bot_id = $2
			AND deleted_at IS NULL
		`, req.AgentInfo.ChatID, req.AgentInfo.BotID.String()).Scan(&agentID)

		if err == nil {
			// 构建动态 UPDATE 语句
			var updateFields []string
			var args []interface{}
			argPosition := 1

			// 检查并添加各个字段的更新
			if req.AgentInfo.Description != nil {
				updateFields = append(updateFields, fmt.Sprintf("description = $%d", argPosition))
				args = append(args, req.AgentInfo.Description)
				argPosition++
			}
			if req.AgentInfo.Language != nil {
				updateFields = append(updateFields, fmt.Sprintf("language = $%d", argPosition))
				args = append(args, req.AgentInfo.Language)
				argPosition++
			}
			if req.AgentInfo.RoleName != "" {
				roleID, err := getRoleIDByName(context.Background(), req.AgentInfo.RoleName)
				if err != nil {
					response := APIResponse{
						Code:    400,
						Message: fmt.Sprintf("Invalid role_name: %v", err),
						Data:    nil,
					}
					responseJSON, _ := json.Marshal(response)
					ctx.SetStatusCode(fasthttp.StatusBadRequest)
					ctx.SetBody(responseJSON)
					return
				}
				updateFields = append(updateFields, fmt.Sprintf("role_id = $%d", argPosition))
				args = append(args, roleID)
				argPosition++
			}
			if req.AgentInfo.Name != nil {
				updateFields = append(updateFields, fmt.Sprintf("name = $%d", argPosition))
				args = append(args, req.AgentInfo.Name)
				argPosition++
			}

			// 如果没有需要更新的字段，返回成功
			if len(updateFields) == 0 {
				response := APIResponse{
					Code:    0,
					Message: "No fields to update",
					Data:    nil,
				}
				responseJSON, _ := json.Marshal(response)
				ctx.SetStatusCode(fasthttp.StatusOK)
				ctx.SetBody(responseJSON)
				return
			}

			// 添加 agent ID 作为 WHERE 条件的参数
			args = append(args, agentID)

			// 构建并执行 UPDATE 语句
			updateQuery := fmt.Sprintf(`
				UPDATE agents
				SET %s
				WHERE id = $%d
			`, strings.Join(updateFields, ", "), argPosition)

			_, err = models.DB().Exec(context.Background(), updateQuery, args...)

			if err == nil {
				// 查询更新后的信息
				var agent struct {
					Name        string `json:"name"`
					Description string `json:"description"`
					Language    string `json:"language"`
					RoleName    string `json:"role_name"`
				}
				err = models.DB().QueryRow(context.Background(), `
					SELECT a.name, a.description, a.language, ar.name as role_name
					FROM agents a
					LEFT JOIN agent_roles ar ON a.role_id = ar.id
					WHERE a.id = $1
				`, agentID).Scan(&agent.Name, &agent.Description, &agent.Language, &agent.RoleName)
				result = agent
			}
		}

	default:
		response := APIResponse{
			Code:    400,
			Message: "Invalid operation type",
			Data:    nil,
		}
		responseJSON, _ := json.Marshal(response)
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetBody(responseJSON)
		return
	}

	if err != nil {
		code := 500
		if err == pgx.ErrNoRows {
			code = 404
		}
		response := APIResponse{
			Code:    code,
			Message: fmt.Sprintf("Error processing %s operation: %v", req.OpType, err),
			Data:    nil,
		}
		responseJSON, _ := json.Marshal(response)
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetBody(responseJSON)
		return
	}

	response := APIResponse{
		Code:    0,
		Message: "Success",
		Data:    result,
	}
	responseJSON, _ := json.Marshal(response)
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(responseJSON)
}

func getRoleIDByName(ctx context.Context, roleName string) (uuid.UUID, error) {
	var roleID uuid.UUID
	err := models.DB().QueryRow(ctx, `
		SELECT id FROM agent_roles 
		WHERE name = $1 AND deleted_at IS NULL
	`, roleName).Scan(&roleID)

	if err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, fmt.Errorf("role not found with name: %s", roleName)
		}
		return uuid.Nil, fmt.Errorf("error querying role: %v", err)
	}

	return roleID, nil
}

// AgentRoundRequest 定义请求结构
type AgentRoundRequest struct {
	ChatTopic string `json:"chat_topic"` // 话题，用逗号分隔
	Round     int    `json:"round"`      // 轮次数
	ChatID    int64  `json:"chat_id"`    // 聊天ID
	BotID     string `json:"bot_id"`     // 多个bot id，用逗号分隔
}

// RoundAgentInfo 定义单个 agent 的轮次信息结构
type RoundAgentInfo struct {
	AgentID          string              `json:"agent_id"`
	BotID            uuid.UUID           `json:"bot_id"`
	AgentName        string              `json:"agent_name"`
	AgentDesc        string              `json:"agent_desc"`
	AgentLanguage    string              `json:"agent_language"`
	AgentKnowledges  []map[string]string `json:"agent_knowledges"` // 修改为新的类型
	RoleName         string              `json:"role_name"`
	RolePersonality  string              `json:"role_personality"`
	RoleSkills       string              `json:"role_skills"`
	RoleConstraints  string              `json:"role_constraints"`
	RoleOutputFormat string              `json:"role_output_format"`
	RoleTarget       string              `json:"role_target"`
	RoleGender       string              `json:"role_gender"`
	Status           int16               `json:"status"`
	Topic            string              `json:"topic,omitempty"`
}

// AgentRoundResponse 定义响应结构
type AgentRoundResponse struct {
	ChatID     int64            `json:"chat_id"`
	AgentsInfo []RoundAgentInfo `json:"agents_info"` // 使用新的类型名
}

// HandleAgentTopic 处理轮次查询请求
func (h *APIHandler) HandleAgentTopic(ctx *fasthttp.RequestCtx) {
	if !h.validateAPIKey(ctx) {
		return
	}

	var req AgentRoundRequest
	if err := json.Unmarshal(ctx.Request.Body(), &req); err != nil {
		h.sendErrorResponse(ctx, 400, "Invalid request format", err)
		return
	}

	// 验证参数
	if req.Round <= 0 {
		h.sendErrorResponse(ctx, 400, "Round must be greater than 0", nil)
		return
	}

	// 分割并验证 bot IDs
	botIDs := strings.Split(req.BotID, ",")
	if len(botIDs) == 0 {
		h.sendErrorResponse(ctx, 400, "No valid bot IDs provided", nil)
		return
	}

	if len(botIDs) == 1 {
		req.Round = 1
	}

	// 验证每个 bot ID 是否为有效的 UUID
	var validBotIDs []uuid.UUID
	for _, botID := range botIDs {
		if uid, err := uuid.Parse(strings.TrimSpace(botID)); err == nil {
			validBotIDs = append(validBotIDs, uid)
		}
	}

	if len(validBotIDs) == 0 {
		h.sendErrorResponse(ctx, 400, "No valid bot IDs found", nil)
		return
	}

	// 构建 bot_ids 字符串，用于 SQL 查询
	var botIDStrings []string
	for _, id := range validBotIDs {
		botIDStrings = append(botIDStrings, id.String())
	}
	botIDsStr := strings.Join(botIDStrings, ",")

	// 修改查询语句，添加 kb_datasets 表的关联
	query := `
		WITH RECURSIVE rounds AS (
			SELECT 
				a.id as agent_id,
				ac.bot_id,
				a.name as agent_name,
				a.description as agent_desc,
				a.language as agent_language,
				a.knowledges as agent_knowledges,
				ar.name as role_name,
				ar.personality as role_personality,
				ar.skills as role_skills,
				ar.constraints as role_constraints,
				ar.output_format as role_output_format,
				ar.target as role_target,
				ar.gender as role_gender,
				a.status,
				1 as round_number
			FROM agents a
			JOIN agent_chats ac ON a.id = ac.agent_id
			JOIN agent_roles ar ON a.role_id = ar.id
			WHERE ac.chat_id = $1
			AND ac.bot_id = ANY(STRING_TO_ARRAY($2, ',')::uuid[])
			AND ac.deleted_at IS NULL
			
			UNION ALL
			
			SELECT 
				a.id,
				ac.bot_id,
				a.name,
				a.description,
				a.language,
				a.knowledges,
				ar.name,
				ar.personality,
				ar.skills,
				ar.constraints,
				ar.output_format,
				ar.target,
				ar.gender,
				a.status,
				r.round_number + 1
			FROM agents a
			JOIN agent_chats ac ON a.id = ac.agent_id
			JOIN agent_roles ar ON a.role_id = ar.id
			JOIN rounds r ON ac.bot_id = r.bot_id
			WHERE ac.chat_id = $1
			AND r.round_number < $3
			AND ac.deleted_at IS NULL
		)
		SELECT 
			agent_id,
			bot_id,
			agent_name,
			agent_desc,
			agent_language,
			agent_knowledges,
			role_name,
			role_personality,
			role_skills,
			role_constraints,
			role_output_format,
			role_target,
			role_gender,
			status,
			round_number
		FROM rounds
		ORDER BY round_number, bot_id;
	`

	// 使用字符串形式的 bot IDs 进行查询
	rows, err := models.DB().Query(context.Background(), query, req.ChatID, botIDsStr, req.Round)
	if err != nil {
		h.sendErrorResponse(ctx, 500, "Database query error", err)
		return
	}
	defer rows.Close()

	var agentsInfo []RoundAgentInfo
	for rows.Next() {
		var agent RoundAgentInfo
		var roundNumber int
		var rawKnowledges []string
		err := rows.Scan(
			&agent.AgentID,
			&agent.BotID,
			&agent.AgentName,
			&agent.AgentDesc,
			&agent.AgentLanguage,
			&rawKnowledges, // 先扫描到临时变量
			&agent.RoleName,
			&agent.RolePersonality,
			&agent.RoleSkills,
			&agent.RoleConstraints,
			&agent.RoleOutputFormat,
			&agent.RoleTarget,
			&agent.RoleGender,
			&agent.Status,
			&roundNumber,
		)
		if err != nil {
			h.sendErrorResponse(ctx, 500, "Error scanning row", err)
			return
		}

		// 查询并映射 knowledge dataset IDs
		// 现在 PG id 直接作为 Milvus dataset_id，无需 ai_dataset_id 映射
		mappedKnowledges := make([]map[string]string, 0)
		for _, datasetJson := range rawKnowledges {
			log.Printf("Processing dataset: %s", datasetJson)

			// 尝试直接使用 datasetJson 作为 datasetID
			datasetID := datasetJson

			// 如果是 JSON 格式，则解析它
			if strings.HasPrefix(datasetJson, "{") {
				var dataset Dataset
				err := json.Unmarshal([]byte(datasetJson), &dataset)
				if err != nil {
					log.Printf("Error unmarshalling dataset JSON: %v, using raw string as ID", err)
				} else {
					datasetID = dataset.DatasetID
				}
			}

			// 验证 dataset 存在
			var dbDatasetID string
			err = models.DB().QueryRow(context.Background(), `
				SELECT id
				FROM kb_datasets
				WHERE id = $1 AND deleted_at IS NULL
			`, datasetID).Scan(&dbDatasetID)

			if err != nil {
				if err == pgx.ErrNoRows {
					// 如果找不到，保留原始ID
					mappedKnowledges = append(mappedKnowledges, map[string]string{
						"datasetId": datasetID,
					})
					continue
				}
				h.sendErrorResponse(ctx, 500, "Error querying kb_datasets", err)
				return
			}

			mappedKnowledges = append(mappedKnowledges, map[string]string{
				"datasetId": dbDatasetID,
			})
		}
		// 2. 添加系统知识库 (type=1)
		systemRows, err := models.DB().Query(context.Background(), `
			SELECT id
			FROM kb_datasets
			WHERE type = 1 AND deleted_at IS NULL
		`)

		if err != nil {
			h.sendErrorResponse(ctx, 500, "Error querying system kb_datasets", err)
			return
		}

		for systemRows.Next() {
			var sysDatasetID string
			if err := systemRows.Scan(&sysDatasetID); err != nil {
				systemRows.Close()
				h.sendErrorResponse(ctx, 500, "Error scanning system kb row", err)
				return
			}

			mappedKnowledges = append(mappedKnowledges, map[string]string{
				"datasetId": sysDatasetID,
			})
		}
		systemRows.Close()

		if err = systemRows.Err(); err != nil {
			h.sendErrorResponse(ctx, 500, "Error iterating system kb rows", err)
			return
		}

		agent.AgentKnowledges = mappedKnowledges

		// 如果提供了话题，为每个 agent 分配话题
		if req.ChatTopic != "" {
			topics := strings.Split(req.ChatTopic, ",")
			if len(topics) > 0 {
				topicIndex := (roundNumber - 1) % len(topics)
				agent.Topic = strings.TrimSpace(topics[topicIndex])
			}
		}

		agentsInfo = append(agentsInfo, agent)
	}

	if err = rows.Err(); err != nil {
		h.sendErrorResponse(ctx, 500, "Error iterating rows", err)
		return
	}

	response := AgentRoundResponse{
		ChatID:     req.ChatID,
		AgentsInfo: agentsInfo,
	}

	h.sendSuccessResponse(ctx, response)
}

// 辅助函数：发送错误响应
func (h *APIHandler) sendErrorResponse(ctx *fasthttp.RequestCtx, code int, message string, err error) {
	if err != nil {
		message = fmt.Sprintf("%s: %v", message, err)
	}
	response := APIResponse{
		Code:    code,
		Message: message,
		Data:    nil,
	}
	responseJSON, _ := json.Marshal(response)
	ctx.SetStatusCode(fasthttp.StatusBadRequest)
	ctx.SetBody(responseJSON)
}

// 辅助函数：发送成功响应
func (h *APIHandler) sendSuccessResponse(ctx *fasthttp.RequestCtx, data interface{}) {
	response := APIResponse{
		Code:    0,
		Message: "Success",
		Data:    data,
	}
	responseJSON, _ := json.Marshal(response)
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(responseJSON)
}

type Dataset struct {
	DatasetID string `json:"datasetId"`
}
