package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/Tom-Jerry/TGAgent/config"
	"github.com/Tom-Jerry/TGAgent/interfaces"
	"github.com/Tom-Jerry/TGAgent/models"
)

type ActionRequest struct {
	UserID        string `json:"user_id"`
	BotID         string `json:"bot_id"`
	ChatID        int64  `json:"chat_id"`
	Message       string `json:"message"`
	AgentActionID string `json:"agent_action_id"`
	ChatType      string `json:"chat_type"`
}

// ChatRequest Python AI 服务对话请求
type ChatRequest struct {
	Message      string   `json:"message"`
	AgentID      string   `json:"agent_id"`
	DatasetIDs   []string `json:"dataset_ids"`
	SystemPrompt string   `json:"system_prompt"`
	Language     string   `json:"language"`
	ChatType     string   `json:"chat_type"`
	LLMProvider  string   `json:"llm_provider,omitempty"`
	LLMModel     string   `json:"llm_model,omitempty"`
	Temperature  float64  `json:"temperature"`
	MaxTokens    int      `json:"max_tokens"`
}

// ChatResponseData Python AI 服务对话响应 data 字段
type ChatResponseData struct {
	Content string `json:"content"`
	Sources []struct {
		ChunkText  string  `json:"chunk_text"`
		SourceName string  `json:"source_name"`
		SourceType string  `json:"source_type"`
		Score      float64 `json:"score"`
	} `json:"sources"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// CallAction 调用 API 或本地函数
func CallAction(
	domain string,
	path string,
	apiKey string,
	inputParam json.RawMessage,
	outputParam json.RawMessage,
	req ActionRequest,
	proxyURL string,
	detail bool,
	platform string,
	handler interfaces.StartCmdHandler,
) (string, error) {
	log.Printf("CallAction called with path=%s, platform=%s", path, platform)

	// 如果是本地函数，直接调用
	if platform == "LocalFunction" {
		log.Printf("Executing local function")
		switch path {
		case "handleStartCmd":
			log.Printf("Calling handleStartCmd with chatID=%d, botID=%s", req.ChatID, req.BotID)
			userID, err := strconv.ParseInt(req.UserID, 10, 64)
			if err != nil {
				return "", fmt.Errorf("error parsing user ID: %v", err)
			}
			return handler.HandleStartCmd(req.ChatID, req.BotID, userID)
		default:
			return "", fmt.Errorf("unknown local function: %s", path)
		}
	}

	// 通过 Python AI 服务进行 RAG 对话
	return callAIService(req)
}

// callAIService 通过 Python AI 服务完成 RAG 对话
func callAIService(req ActionRequest) (string, error) {
	log.Printf("[ActionService] Calling AI service: agent_action_id=%s, message_len=%d",
		req.AgentActionID, len(req.Message))

	// 获取 agent 关联的知识库 dataset IDs
	datasetIDs, systemPrompt, language, err := getAgentContext(req)
	if err != nil {
		log.Printf("[ActionService] Error getting agent context: %v", err)
		// 即使没有知识库，仍然可以进行纯 LLM 对话
		datasetIDs = []string{}
	}

	// 构建对话请求
	chatReq := ChatRequest{
		Message:      req.Message,
		AgentID:      req.AgentActionID,
		DatasetIDs:   datasetIDs,
		SystemPrompt: systemPrompt,
		Language:     language,
		ChatType:     req.ChatType,
		Temperature:  0.7,
		MaxTokens:    4096,
	}

	// 获取 AI 服务配置并创建客户端
	cfg, err := getAIServiceConfig()
	if err != nil {
		return "", fmt.Errorf("error getting AI service config: %v", err)
	}

	client := NewAIClient(cfg.BaseURL, cfg.APIKey, cfg.Timeout)

	// 调用 Python AI 服务
	resp, err := client.Post("/api/v1/chat/completions", chatReq)
	if err != nil {
		return "", fmt.Errorf("AI service call failed: %v", err)
	}

	if resp.Code != 0 {
		return "", fmt.Errorf("AI service error: %s", resp.Message)
	}

	// 解析响应
	var chatData ChatResponseData
	if err := json.Unmarshal(resp.Data, &chatData); err != nil {
		return "", fmt.Errorf("error parsing AI response: %v", err)
	}

	log.Printf("[ActionService] AI response: content_len=%d, sources=%d, tokens=%d",
		len(chatData.Content), len(chatData.Sources), chatData.Usage.TotalTokens)

	return chatData.Content, nil
}

// aiServiceCfg 缓存的 AI 服务配置
type aiServiceCfg struct {
	BaseURL string
	APIKey  string
	Timeout int
}

// getAIServiceConfig 从配置文件获取 AI 服务配置
func getAIServiceConfig() (*aiServiceCfg, error) {
	cfg := config.GetConfig()
	if cfg == nil {
		return nil, fmt.Errorf("config not loaded")
	}
	timeout := cfg.AIService.Timeout.Chat
	if timeout <= 0 {
		timeout = 60
	}
	return &aiServiceCfg{
		BaseURL: cfg.AIService.BaseURL,
		APIKey:  cfg.AIService.APIKey,
		Timeout: timeout,
	}, nil
}

// getAgentContext 获取 agent 关联的知识库和系统提示词
func getAgentContext(req ActionRequest) (datasetIDs []string, systemPrompt string, language string, err error) {
	ctx := context.Background()

	// 获取 agent 信息
	botIDs := strings.Split(req.BotID, ",")
	primaryBotID := botIDs[0]

	agent, err := models.GetAgentByChat(ctx, req.ChatID, primaryBotID)
	if err != nil {
		return nil, "", "English", fmt.Errorf("error getting agent: %v", err)
	}

	language = agent.Language
	if language == "" {
		language = "English"
	}

	// 获取角色 system prompt
	if agent.RoleID.String() != "" {
		role, err := models.GetRoleByID(ctx, agent.RoleID.String())
		if err == nil && role != nil {
			systemPrompt = role.Description
		}
	}

	// 收集知识库 dataset IDs
	for _, kbJSON := range agent.Knowledges {
		var knowledge struct {
			DatasetID string `json:"datasetId"`
		}
		if err := json.Unmarshal([]byte(kbJSON), &knowledge); err != nil {
			// 尝试直接作为 dataset ID
			datasetIDs = append(datasetIDs, kbJSON)
			continue
		}
		if knowledge.DatasetID != "" {
			datasetIDs = append(datasetIDs, knowledge.DatasetID)
		}
	}

	log.Printf("[ActionService] Agent context: datasets=%d, language=%s, hasPrompt=%t",
		len(datasetIDs), language, systemPrompt != "")

	return datasetIDs, systemPrompt, language, nil
}
