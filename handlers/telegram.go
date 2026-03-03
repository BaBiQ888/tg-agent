package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Tom-Jerry/TGAgent/config"
	"github.com/Tom-Jerry/TGAgent/models"
	"github.com/Tom-Jerry/TGAgent/services"
	"github.com/jackc/pgx/v5"
	"github.com/mr-tron/base58"
	"github.com/mymmrac/telego"
	"github.com/valyala/fasthttp"
)

func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}

	// Convert to runes to properly handle UTF-8 characters
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}

	// Try to truncate at word boundary if possible
	for i := n; i > 0; i-- {
		// If we find a space, truncate there
		if unicode.IsSpace(runes[i]) {
			return string(runes[:i]) + "..."
		}

		// Don't go back more than 20 characters looking for a word boundary
		if n-i > 20 {
			break
		}
	}

	// If no suitable word boundary found, truncate at the specified position
	return string(runes[:n]) + "..."
}

type UserState struct {
	Command         *models.Command
	Action          *models.Action
	MessageIDs      []int
	WaitingForInput bool
	KBState         *models.KBCommandState
	LastUpdateTime  time.Time
}

type TelegramHandler struct {
	bot        *telego.Bot
	botID      string
	commands   map[string]*models.Command
	userStates map[int64]*UserState
	cfg        *config.Config
	bots       map[string]bool
}

func NewTelegramHandler(botID string, token string, commands []models.Command, cfg *config.Config) (*TelegramHandler, error) {
	log.Printf("Initializing telegram handler for bot %s with %d commands", botID, len(commands))

	var bot *telego.Bot
	var err error

	if cfg.Proxy.URL != "" {
		// 创建代理客户端
		proxyURL, err := url.Parse(cfg.Proxy.URL)
		if err != nil {
			return nil, fmt.Errorf("error parsing proxy URL: %v", err)
		}
		client := &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
			},
		}
		bot, err = telego.NewBot(token, telego.WithHTTPClient(client))
		if err != nil {
			return nil, fmt.Errorf("error creating bot with proxy: %v", err)
		}
		log.Printf("Using proxy: %s", cfg.Proxy.URL)
	} else {
		bot, err = telego.NewBot(token)
		log.Printf("No proxy configured, using direct connection")
	}

	if err != nil {
		return nil, err
	}

	cmdMap := make(map[string]*models.Command)
	for i, cmd := range commands {
		if cmd.IsActive {
			log.Printf("Mapping command: %s -> %+v", cmd.Command, cmd)
			cmdMap[cmd.Command] = &commands[i]
		}
	}

	// 初始化 bots map
	bots := make(map[string]bool)

	// 从数据库获取所有机器人
	allBots, err := models.GetBots(context.Background(), cfg.RunEnv)
	if err != nil {
		log.Printf("Warning: Error getting bots from database: %v", err)
	} else {
		for _, bot := range allBots {
			botIDStr := bot.ID.String()
			bots[botIDStr] = true
			log.Printf("Added bot ID %s to handler.bots map", botIDStr)
		}
	}

	handler := &TelegramHandler{
		bot:        bot,
		botID:      botID,
		commands:   cmdMap,
		userStates: make(map[int64]*UserState),
		cfg:        cfg,
		bots:       bots,
	}

	// 设置命令菜单
	if err := handler.SetupCommands(); err != nil {
		log.Printf("Warning: Failed to set up commands: %v", err)
	}

	return handler, nil
}

func (h *TelegramHandler) StartPolling() {
	updates, err := h.bot.UpdatesViaLongPolling(nil)
	if err != nil {
		log.Printf("Error starting polling: %v", err)
		return
	}

	defer h.bot.StopLongPolling()

	for update := range updates {
		h.HandleUpdate(update) // 直接处理更新，而不是简单地回显消息
	}
}

func (h *TelegramHandler) SetWebhook(domain, path string) error {
	// 确保 domain 不以 / 结尾
	domain = strings.TrimSuffix(domain, "/")
	// 确保 path 以 / 开头
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	webhookURL := fmt.Sprintf("%s%s?bot_id=%s", domain, path, h.botID)
	log.Printf("Setting webhook URL: %s", webhookURL)

	// 验证 webhook URL
	if err := validateWebhookConfig(domain); err != nil {
		return fmt.Errorf("invalid webhook domain: %v", err)
	}

	params := &telego.SetWebhookParams{
		URL:                webhookURL,
		MaxConnections:     100,
		AllowedUpdates:     []string{"message", "callback_query"},
		DropPendingUpdates: true, // 丢弃待处理的更新
	}

	if err := h.bot.SetWebhook(params); err != nil {
		return fmt.Errorf("error setting webhook: %v", err)
	}

	// 验证设置
	info, err := h.bot.GetWebhookInfo()
	if err != nil {
		return fmt.Errorf("error getting webhook info: %v", err)
	}

	// 详细记录 webhook 信息
	log.Printf("Webhook set successfully: URL=%s, PendingUpdateCount=%d, LastErrorDate=%d, LastErrorMessage=%s",
		info.URL, info.PendingUpdateCount, info.LastErrorDate, info.LastErrorMessage)

	// 检查是否有错误信息
	if info.LastErrorMessage != "" {
		return fmt.Errorf("webhook has error: %s", info.LastErrorMessage)
	}

	return nil
}

// 添加检查管理员权限的方法
func (h *TelegramHandler) isAdmin(chatID int64, userID int64) bool {
	// 如果是私聊，直接返回true
	chat, err := h.bot.GetChat(&telego.GetChatParams{
		ChatID: telego.ChatID{ID: chatID},
	})
	if err != nil {
		log.Printf("Error getting chat info: %v", err)
		return false
	}

	if chat.Type == "private" {
		return true
	}

	// 获取用户在群组中的权限
	member, err := h.bot.GetChatMember(&telego.GetChatMemberParams{
		ChatID: telego.ChatID{ID: chatID},
		UserID: userID,
	})
	if err != nil {
		log.Printf("Error getting member info: %v", err)
		return false
	}

	// 检查是否是管理员或创建者
	status := member.MemberStatus()
	return status == "administrator" || status == "creator"
}

// HandleUpdate 处理所有类型的更新
func (h *TelegramHandler) HandleUpdate(update telego.Update) {
	if update.Message == nil && update.CallbackQuery == nil {
		log.Println("Received empty update")
		return
	}

	var chatID, userID int64
	var messageText string
	var commandMessageID int

	if update.Message != nil {
		chatID = update.Message.Chat.ID
		userID = update.Message.From.ID
		messageText = update.Message.Text
		commandMessageID = update.Message.MessageID
		log.Printf("Received message: %s from user %d in chat %d", messageText, userID, chatID)
	} else if update.CallbackQuery != nil {
		chatID = update.CallbackQuery.Message.Chat.ID
		userID = update.CallbackQuery.From.ID
		messageText = update.CallbackQuery.Data
		log.Printf("Received callback: %s from user %d in chat %d", messageText, userID, chatID)
	}

	// 检查权限
	if !h.isAdmin(chatID, userID) {
		log.Printf("User %d is not admin in chat %d", userID, chatID)
		if update.Message != nil && update.Message.Chat.Type != "private" {
			return
		}
	}

	// 处理命令
	if update.Message != nil && len(messageText) > 0 && messageText[0] == '/' {
		log.Printf("Entering command processing for message ID %d, command: %s", commandMessageID, messageText)
		h.handleCommand(update.Message, commandMessageID)
		state := h.userStates[chatID]
		log.Printf("state: %+v", state)
		return
	}

	// 处理回调查询
	if update.CallbackQuery != nil {
		h.handleCallback(update.CallbackQuery)
		return
	}

	// 安全地检查用户状态
	state, exists := h.userStates[chatID]
	log.Printf("state: %+v", state)
	if exists {
		log.Printf("Found user state, WaitingForInput: %v", state.WaitingForInput)
		// 处理用户输入状态
		if update.Message != nil && state.WaitingForInput {
			log.Printf("Processing user input")
			h.handleUserInput(update.Message)
			return
		}
	}

	// 处理聊天消息（群组和私聊）
	if update.Message != nil {
		log.Printf("Processing chat message")
		h.handleChatMessage(update.Message)
		return
	}

	// HandleUpdate 中的状态检查
	log.Printf("Message check: update.Message=%v, userState=%v, waitingForInput=%v",
		update.Message != nil,
		exists,
		exists && state.WaitingForInput)
}

// 解析命令和机器人名称
func parseCommand(text string) (command string, botName string) {
	parts := strings.Split(text, "@")
	command = parts[0]
	if len(parts) > 1 {
		botName = parts[1]
	}
	return
}

// handleCommand 处理命令消息
func (h *TelegramHandler) handleCommand(msg *telego.Message, commandMessageID int) {
	command, botName := parseCommand(msg.Text)
	log.Printf("Processing command: %s, bot name: %s", command, botName)

	// 如果是群组消息，检查发送者是否为管理员
	if msg.Chat.Type != "private" {
		if !h.isAdmin(msg.Chat.ID, msg.From.ID) {
			// 发送权限不足提示
			params := &telego.SendMessageParams{
				ChatID: telego.ChatID{ID: msg.Chat.ID},
				Text:   "抱歉，只有群组管理员才能使用此命令。",
			}
			h.bot.SendMessage(params)
			return
		}
	}

	// 如果是群组消息，需要检查是否是发给正确的机器人
	if msg.Chat.Type != "private" && botName != "" {
		// 获取 bot ID
		botID, err := models.GetBotIDByName(context.Background(), botName)
		if err != nil {
			log.Printf("Error getting bot ID for name %s: %v", botName, err)
			return
		}
		// 如果不是发给当前机器人的命令，忽略
		if botID != h.botID {
			log.Printf("Command not for this bot (ID: %s)", h.botID)
			return
		}
	}

	if strings.HasPrefix(command, "/kb") {
		if err := h.handleKBCommand(msg); err != nil {
			log.Printf("Error handling kb command: %v", err)
			h.sendMessage(msg.Chat.ID, fmt.Sprintf("命令执行失败: %v", err))
		}
		return
	}

	if cmd, exists := h.commands[command]; exists {
		log.Printf("Found command: %s with actions: %v", msg.Text, cmd.Actions)

		// 检查 Action 的 Platform 和 Path
		for actionName, action := range cmd.Actions {
			log.Printf("Action %s: Platform=%s, Path=%s",
				actionName, action.Platform, action.Path)
		}

		h.userStates[msg.Chat.ID] = &UserState{
			Command:         cmd,
			Action:          nil,
			MessageIDs:      []int{commandMessageID},
			WaitingForInput: false,
		}

		// 如果只有一个 action，直接发送操作提示
		if len(cmd.Actions) == 1 {
			var action models.Action
			for _, a := range cmd.Actions {
				action = a
				break
			}
			h.userStates[msg.Chat.ID].Action = &action

			log.Printf("Single action found: Platform=%s, Path=%s",
				action.Platform, action.Path)

			// 如果需要确认
			if action.NeedConfirm {
				// 创建确认按钮
				buttons := [][]telego.InlineKeyboardButton{
					{
						{Text: "Yes", CallbackData: "confirm_" + action.Path},
						{Text: "No", CallbackData: "cancel_" + action.Path},
					},
				}

				// 发送确认消息
				params := &telego.SendMessageParams{
					ChatID: telego.ChatID{ID: msg.Chat.ID},
					Text:   action.ActionTip,
					ReplyMarkup: &telego.InlineKeyboardMarkup{
						InlineKeyboard: buttons,
					},
				}

				confirmMsg, err := h.bot.SendMessage(params)
				if err != nil {
					log.Printf("Error sending confirmation message: %v", err)
					return
				}

				// 保存状态
				h.userStates[msg.Chat.ID] = &UserState{
					Command:         cmd,
					Action:          &action,
					MessageIDs:      []int{commandMessageID, confirmMsg.MessageID},
					KBState:         &models.KBCommandState{},
					WaitingForInput: true,
				}
				return
			}

			// 如果是本地函数，直接调用而不是等待输入
			if action.Platform == "LocalFunction" {
				log.Printf("Executing local function: %s", action.Path)
				var result string
				var err error

				// 根据 path 调用对应的本地函数
				switch action.Path {
				case "handleStartCmd":
					log.Printf("Calling local HandleStartCmd function")
					result, err = h.HandleStartCmd(msg.Chat.ID, h.botID, msg.From.ID)
				case "handleStopCmd":
					log.Printf("Calling local HandleStopCmd function")
					result, err = h.HandleStopCmd(msg.Chat.ID, h.botID)
				case "handleQueryAgent":
					go h.handleQueryAgent(msg)
					return
				case "handleRoleList":
					go h.handleRoleList(msg)
					return
				default:
					err = fmt.Errorf("unknown local function: %s", action.Path)
				}

				if err != nil {
					log.Printf("Error executing local function: %v", err)
					return
				}
				log.Printf("Local function executed successfully: %s", result)

				// 发送结果
				params := &telego.SendMessageParams{
					ChatID: telego.ChatID{ID: msg.Chat.ID},
					Text:   result,
				}
				h.bot.SendMessage(params)
				return
			}

			// 对于非本地函数，设置等待输入状态
			h.userStates[msg.Chat.ID].WaitingForInput = true
			log.Printf("h.userStates[msg.Chat.ID].WaitingForInput: %v", h.userStates[msg.Chat.ID].WaitingForInput)

			// 发送操作提示
			params := &telego.SendMessageParams{
				ChatID: telego.ChatID{ID: msg.Chat.ID},
				Text:   action.ActionTip,
			}
			msg, err := h.bot.SendMessage(params)
			if err != nil {
				log.Printf("Error sending action tip: %v", err)
				return
			}

			// 保存消息ID用于后续删除
			h.userStates[msg.Chat.ID].MessageIDs = append(h.userStates[msg.Chat.ID].MessageIDs, msg.MessageID)
			return
		}

		// 如果有多个 action，显示按钮列表
		var buttons [][]telego.InlineKeyboardButton
		var currentRow []telego.InlineKeyboardButton

		baseTip := strings.Split(cmd.ReplyTip, "\n")[0]

		for key := range cmd.Actions {
			button := telego.InlineKeyboardButton{
				Text:         key,
				CallbackData: key,
			}
			currentRow = append(currentRow, button)

			if len(currentRow) == 3 {
				buttons = append(buttons, currentRow)
				currentRow = nil
			}
		}

		if len(currentRow) > 0 {
			buttons = append(buttons, currentRow)
		}

		// 发送选项消息并保存消息ID
		params := &telego.SendMessageParams{
			ChatID: telego.ChatID{ID: msg.Chat.ID},
			Text:   baseTip,
			ReplyMarkup: &telego.InlineKeyboardMarkup{
				InlineKeyboard: buttons,
			},
		}

		msg, err := h.bot.SendMessage(params)
		if err != nil {
			log.Printf("Error sending message: %v", err)
			return
		}

		// 保存消息ID用于后续删除
		h.userStates[msg.Chat.ID].MessageIDs = append(h.userStates[msg.Chat.ID].MessageIDs, msg.MessageID)
		return
	}
	log.Printf("No command found for message: %s", msg.Text)
}

// handleCallback 处理回调查询
func (h *TelegramHandler) handleCallback(query *telego.CallbackQuery) {
	// 解析回调数据
	data := query.Data
	if strings.HasPrefix(data, "confirm_") {
		// 用户确认执行操作
		if state, exists := h.userStates[query.Message.Chat.ID]; exists && state.Action != nil {
			// 执行原来的操作
			if state.Action.Platform == "LocalFunction" {
				// 处理本地函数
				result, err := h.handleLocalFunction(query.Message, state.Action)
				if err != nil {
					log.Printf("Error executing local function: %v", err)
					// 发送错误消息给用户
					params := &telego.SendMessageParams{
						ChatID: telego.ChatID{ID: query.Message.Chat.ID},
						Text:   fmt.Sprintf("操作失败: %v", err),
					}
					h.bot.SendMessage(params)
					return
				}
				// 发送结果
				params := &telego.SendMessageParams{
					ChatID: telego.ChatID{ID: query.Message.Chat.ID},
					Text:   result,
				}
				h.bot.SendMessage(params)
			}
		}
	} else if strings.HasPrefix(data, "cancel_") {
		// 用户取消操作
		params := &telego.SendMessageParams{
			ChatID: telego.ChatID{ID: query.Message.Chat.ID},
			Text:   "操作已取消",
		}
		h.bot.SendMessage(params)
	}

	// 删除确认消息
	if state, exists := h.userStates[query.Message.Chat.ID]; exists {
		for _, msgID := range state.MessageIDs {
			h.bot.DeleteMessage(&telego.DeleteMessageParams{
				ChatID:    telego.ChatID{ID: query.Message.Chat.ID},
				MessageID: msgID,
			})
		}
		delete(h.userStates, query.Message.Chat.ID)
	}

	// 回答回调查询
	h.bot.AnswerCallbackQuery(&telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
	})
}

// handleUserInput 处理用户输入状态
func (h *TelegramHandler) handleUserInput(msg *telego.Message) {
	chatID := msg.Chat.ID
	state := h.userStates[chatID]
	log.Printf("Processing user input: %s", msg.Text)
	// 检查是否是退出命令
	if msg.Text == "/exit" || msg.Text == "/cancel" {
		delete(h.userStates, chatID)
		h.sendMessage(chatID, "已退出当前命令模式")
		return
	}
	// 如果是 KB 命令的后续步骤
	if state.KBState != nil {
		log.Printf("Processing KB command: %s, step: %d", state.KBState.Command, state.KBState.Step)
		state.LastUpdateTime = time.Now()
		if err := h.handleKBCommand(msg); err != nil {
			log.Printf("Error handling KB command: %v", err)
			h.sendMessage(chatID, fmt.Sprintf("操作失败: %v", err))
			// 清理状态
			delete(h.userStates, chatID)
		}
		return
	}
	// 处理其他 Action
	if state.Action != nil {
		log.Printf("Processing Action: %s", state.Action.Path)
		log.Printf("WebhookDomain: %s", h.cfg.WebhookDomain)
		// 调用 API
		resp, err := services.CallAction(
			h.cfg.WebhookDomain,
			state.Action.Path,
			state.Action.APIKey,
			state.Action.InputParam,
			state.Action.OutputParam,
			services.ActionRequest{
				UserID:  strconv.FormatInt(msg.From.ID, 10),
				BotID:   h.botID,
				ChatID:  chatID,
				Message: msg.Text,
			},
			h.cfg.Proxy.URL,
			false,
			state.Action.Platform,
			h,
		)

		if err != nil {
			log.Printf("Error calling action: %v", err)
			params := &telego.SendMessageParams{
				ChatID: telego.ChatID{ID: chatID},
				Text:   "抱歉，处理您的请求时出现错误",
			}
			h.bot.SendMessage(params)
			return
		}

		// 发送 API 响应
		// params := &telego.SendMessageParams{
		// 	ChatID: telego.ChatID{ID: chatID},
		// 	Text:   resp,
		// }
		log.Printf("Sending message: %s", resp)
		delete(h.userStates, chatID)
		// h.bot.SendMessage(params)
	}
}

// handleChatMessage 处理聊天消息（群组和私聊）
func (h *TelegramHandler) handleChatMessage(msg *telego.Message) {
	chatID := msg.Chat.ID
	messageText := msg.Text
	chatType := msg.Chat.Type

	log.Printf("Processing %s message: chat_id=%d, text=%s", chatType, chatID, messageText)

	// 处理所有类型的消息
	if chatType == "private" {
		// 私聊消息直接处理
		log.Printf("Processing private chat with bot_id=%s", h.botID)
		err := h.handleAgentAction(msg, h.botID, "chat")
		if err != nil {
			log.Printf("Error handling private chat: %v", err)
		}
		return
	}

	if msg.Entities == nil {
		// 没有实体但在群组中，尝试处理
		log.Printf("No entities in group message, trying to handle with bot_id=%s", h.botID)
		return
	}

	// 处理群组消息中的 @ 提及
	log.Printf("Found %d entities in message", len(msg.Entities))

	// 找到第一个 @ 机器人的实体（排除 @ 用户的情况）
	var firstBotMention string
	var foundSelf bool
	var mentionedBotIDs []string

	// 按照 offset 升序排序实体，确保按照消息中的顺序处理
	sort.Slice(msg.Entities, func(i, j int) bool {
		return msg.Entities[i].Offset < msg.Entities[j].Offset
	})

	for _, entity := range msg.Entities {
		log.Printf("Processing entity: type=%s, offset=%d, length=%d",
			entity.Type, entity.Offset, entity.Length)

		if entity.Type == "mention" {
			mention := messageText[entity.Offset : entity.Offset+entity.Length]
			botName := strings.TrimPrefix(mention, "@")
			botName = strings.TrimSpace(botName) // 移除可能的空格
			log.Printf("Found mention: @%s", botName)

			botID, err := models.GetBotIDByName(context.Background(), botName)
			if err != nil {
				log.Printf("Error getting bot ID for name %s: %v", botName, err)
				continue
			}
			log.Printf("Got bot ID from database: %s", botID)

			if _, exists := h.bots[botID]; exists {
				// 收集所有被提及的机器人 ID
				mentionedBotIDs = append(mentionedBotIDs, botID)
				log.Printf("Added bot ID to mentioned list: %s", botID)

				// 如果是第一个机器人提及
				if firstBotMention == "" {
					firstBotMention = botID
					log.Printf("Set first bot mention: %s", botID)
				}
				// 检查是否提及了自己
				if botID == h.botID {
					foundSelf = true
					log.Printf("Found self mention: %s", h.botID)
				}
			} else {
				log.Printf("Bot ID %s not found in handler.bots map", botID)
			}
		}
	}

	// 检查是否需要处理这条消息
	if !foundSelf {
		log.Printf("Message does not mention this bot")
		return
	}

	// 检查是否是第一个被提及的机器人
	if firstBotMention != h.botID {
		log.Printf("This bot is not the first mentioned bot (first: %s, self: %s)",
			firstBotMention, h.botID)
		return
	}

	log.Printf("Processing message as first mentioned bot")

	// 将所有被提及的机器人 ID 合并为字符串
	botIDsStr := strings.Join(mentionedBotIDs, ",")
	log.Printf("Combined bot IDs: %s", botIDsStr)

	err := h.handleAgentAction(msg, botIDsStr, "chat")
	if err != nil {
		log.Printf("Error handling group mention: %v", err)
	}
}

// handleAgentAction 处理 agent action（群组和私聊共用）
func (h *TelegramHandler) handleAgentAction(msg *telego.Message, botID string, triggerAction string) error {
	chatID := msg.Chat.ID
	messageText := msg.Text

	// 处理消息文本，移除所有 @ 提及
	if msg.Entities != nil {
		// 按照 offset 降序排序实体，这样从后向前处理不会影响前面的 offset
		sort.Slice(msg.Entities, func(i, j int) bool {
			return msg.Entities[i].Offset > msg.Entities[j].Offset
		})

		for _, entity := range msg.Entities {
			if entity.Type == "mention" {
				// 移除 @ 提及
				messageText = messageText[:entity.Offset] + messageText[entity.Offset+entity.Length:]
				messageText = strings.TrimSpace(messageText)
			}
		}
	}

	log.Printf("Processed message text (removed mentions): %s", messageText)

	// 查询 agent_actions 表
	actions, err := models.GetAgentActions(context.Background(), chatID, botID, triggerAction)
	if err != nil {
		log.Printf("Error querying agent_actions: %v", err)
		return err
	}

	if len(actions) == 0 {
		log.Printf("No agent actions found for chat_id=%d, bot_ids=%s, action=%s",
			chatID, botID, triggerAction)
		return pgx.ErrNoRows
	}

	// 处理每个找到的 action
	for _, action := range actions {
		log.Printf("Processing agent action: id=%s, action_id=%s, trigger_agent_id=%s",
			action.ID, action.ActionID, action.TriggerAgentID)

		// 获取命令动作
		cmdAction, err := models.GetCmdAction(context.Background(), action.ActionID)
		if err != nil {
			log.Printf("Error getting cmd action: %v", err)
			continue
		}

		// 构建请求参数
		actionRequest := services.ActionRequest{
			UserID:        strconv.FormatInt(msg.From.ID, 10),
			BotID:         botID,
			ChatID:        chatID,
			Message:       messageText,
			AgentActionID: action.ID,
			ChatType:      msg.Chat.Type,
		}

		// 如果不需要返回结果，使用 goroutine 异步调用
		if !cmdAction.ReturnResult {
			go func(action *models.CmdAction, request services.ActionRequest) {
				result, err := services.CallAction(
					h.cfg.WebhookDomain,
					action.Path,
					action.APIKey,
					action.InputParam,
					action.OutputParam,
					request,
					h.cfg.Proxy.URL,
					false,
					action.Platform,
					h,
				)
				if err != nil {
					log.Printf("Error in async action call: %v", err)
					return
				}
				log.Printf("Async action call completed: %s", result)
			}(cmdAction, actionRequest)
			continue
		}

		// 调用 FastGPT API
		result, err := services.CallAction(
			h.cfg.WebhookDomain,
			cmdAction.Path,
			cmdAction.APIKey,
			cmdAction.InputParam,
			cmdAction.OutputParam,
			actionRequest,
			h.cfg.Proxy.URL,
			false,
			cmdAction.Platform,
			h,
		)

		if err != nil {
			log.Printf("Error calling action: %v", err)
			continue
		}

		// 如果需要返回结果，发送消息给用户
		if cmdAction.ReturnResult {
			params := &telego.SendMessageParams{
				ChatID: telego.ChatID{ID: chatID},
				Text:   result,
			}
			if _, err := h.bot.SendMessage(params); err != nil {
				log.Printf("Error sending result message: %v", err)
				continue
			}
		}
	}

	return nil
}

// SetupCommands 设置机器人的命令菜单
func (h *TelegramHandler) SetupCommands() error {
	if len(h.commands) == 0 {
		log.Printf("No commands to set up, skipping SetMyCommands")
		return nil
	}

	// 检查每个命令是否有有效的动作
	var validCommands []telego.BotCommand
	for _, cmd := range h.commands {
		if len(cmd.Actions) > 0 {
			botCommand := telego.BotCommand{
				Command:     cmd.Command,
				Description: cmd.Description,
			}
			log.Printf("Adding command: %+v", botCommand)
			validCommands = append(validCommands, botCommand)
		} else {
			log.Printf("Skipping command %s: no valid actions", cmd.Command)
		}
	}

	if len(validCommands) == 0 {
		log.Printf("No valid commands with actions to set up")
		return nil
	}

	params := &telego.SetMyCommandsParams{
		Commands: validCommands,
	}

	log.Printf("Setting bot commands: %+v", params)

	if err := h.bot.SetMyCommands(params); err != nil {
		return fmt.Errorf("error setting bot commands: %v", err)
	}

	log.Printf("Successfully set up %d commands for bot %s", len(validCommands), h.botID)
	return nil
}

// HandleWebhook 处理 Telegram webhook 请求
func (h *TelegramHandler) HandleWebhook(ctx *fasthttp.RequestCtx) {
	if ctx == nil {
		log.Printf("错误：收到空的上下文对象")
		return
	}

	defer func() {
		if r := recover(); r != nil {
			log.Printf("HandleWebhook 发生panic：%v", r)
			if ctx != nil {
				ctx.SetStatusCode(fasthttp.StatusInternalServerError)
				ctx.SetBodyString("服务器内部错误")
			}
		}
	}()

	// 检查请求体是否为空
	body := ctx.Request.Body()
	if len(body) == 0 {
		log.Printf("错误：请求体为空")
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetBodyString("请求体不能为空")
		return
	}

	// 解析更新
	var update telego.Update
	if err := json.Unmarshal(body, &update); err != nil {
		log.Printf("解析更新数据失败：%v", err)
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetBodyString("请求格式无效")
		return
	}

	// 检查 handler 是否已正确初始化
	if h == nil || h.bot == nil {
		log.Printf("错误：TelegramHandler 或 bot 未正确初始化")
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetBodyString("处理器未初始化")
		return
	}

	// 处理更新
	h.HandleUpdate(update)

	// 返回成功
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBodyString("处理成功")
}

// generateChatIDStr 生成聊天 ID 字符串
func generateChatIDStr(chatID int64) string {
	// 将 int64 转换为字节数组
	bytes := make([]byte, 8)
	for i := 0; i < 8; i++ {
		bytes[7-i] = byte(chatID >> (i * 8))
	}
	// 使用 base58 编码
	return base58.Encode(bytes)
}

// HandleStartCmd 处理 /start 命令
func (h *TelegramHandler) HandleStartCmd(chatID int64, botID string, ownerID int64) (string, error) {
	log.Printf("Handling start command: chatID=%d, botID=%s, ownerID=%d", chatID, botID, ownerID)

	ctx := context.Background()

	// 首先检查是否已存在活跃的 agent
	var existingAgentName string
	err := models.DB().QueryRow(ctx, `
		SELECT a.name
		FROM agent_chats ac
		JOIN agents a ON ac.agent_id = a.id
		WHERE ac.chat_id = $1 AND ac.bot_id = $2
		AND ac.deleted_at IS NULL
	`, chatID, botID).Scan(&existingAgentName)

	// 如果找到了现有的 agent
	if err == nil {
		log.Printf("Found existing agent: %s", existingAgentName)
		return fmt.Sprintf("Agent '%s' is already active in this chat. No need to create a new one.", existingAgentName), nil
	}

	// 如果错误不是 "没有找到记录"，说明发生了其他错误
	if err != pgx.ErrNoRows {
		log.Printf("Error checking existing agent: %v", err)
		return "", fmt.Errorf("error checking existing agent: %v", err)
	}

	// 获取机器人信息
	bot, err := models.GetBotByID(ctx, botID)
	if err != nil {
		log.Printf("Error getting bot info: %v", err)
		return "", fmt.Errorf("error getting bot info: %v", err)
	}

	// 生成 agent 名称
	chatIDStr := generateChatIDStr(chatID)
	agentName := fmt.Sprintf("%s_%s", bot.Name, chatIDStr)
	log.Printf("Generated agent name: %s", agentName)

	// 创建知识库数据集
	kbService := services.NewKBService(h.cfg.AIService.BaseURL, h.cfg.AIService.APIKey)
	datasetID, err := kbService.CreateDataset(fmt.Sprintf("KB_%s", agentName))
	if err != nil {
		log.Printf("Error creating knowledge base: %v", err)
		return "", fmt.Errorf("error creating knowledge base: %v", err)
	}
	log.Printf("Created knowledge base dataset: %s", datasetID)

	// 方法 1：使用 json.Marshal
	knowledges := []string{datasetID}
	_, err = models.CreateAgent(ctx, botID, agentName, bot.RoleID, chatID, ownerID, knowledges)
	if err != nil {
		log.Printf("Error creating agent: %v", err)
		return "", fmt.Errorf("error creating agent: %v", err)
	}

	log.Printf("Successfully created agent with knowledge base")
	return fmt.Sprintf("Created new agent '%s' successfully!", agentName), nil
}

// HandleStopCmd 处理停止命令
func (h *TelegramHandler) HandleStopCmd(chatID int64, botID string) (string, error) {
	err := models.StopAgent(context.Background(), chatID, botID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("未找到需要停止的 Agent")
		}
		return "", fmt.Errorf("停止 Agent 失败: %v", err)
	}

	return "Agent stopped successfully", nil
}

// handleLocalFunction 处理本地函数调用
func (h *TelegramHandler) handleLocalFunction(msg *telego.Message, action *models.Action) (string, error) {
	switch action.Path {
	case "handleStartCmd":
		return h.HandleStartCmd(msg.Chat.ID, h.botID, msg.From.ID)
	case "handleStopCmd":
		return h.HandleStopCmd(msg.Chat.ID, h.botID)
	case "handleQueryAgent":
		go h.handleQueryAgent(msg)
		return "", nil
	case "handleRoleList":
		go h.handleRoleList(msg)
		return "", nil
	default:
		return "", fmt.Errorf("unknown local function: %s", action.Path)
	}
}

// handleKBCommand 处理知识库相关命令
func (h *TelegramHandler) handleKBCommand(msg *telego.Message) error {
	chatID := msg.Chat.ID
	log.Printf("[KB] Starting KB command processing: chatID=%d, text=%s", chatID, msg.Text)
	state := h.userStates[chatID]

	// 检查是否是私聊
	if msg.Chat.Type != "private" {
		delete(h.userStates, chatID)
		return h.sendMessage(chatID, "知识库管理命令只能在私聊中使用")
	}

	// 如果是新的命令，初始化状态
	if state == nil || state.KBState == nil {
		state = &UserState{
			KBState:         &models.KBCommandState{},
			WaitingForInput: true,
			LastUpdateTime:  time.Now(),
		}
		h.userStates[chatID] = state
	} else {
		// 检查状态是否过期
		const operationTimeout = 5 * time.Minute
		if time.Since(state.LastUpdateTime) > operationTimeout {
			delete(h.userStates, chatID)
			return h.sendMessage(chatID, `⌛️ 操作已超时！

为了保证安全，操作时间限制为 5 分钟
请重新开始操作：
1. /kbadd - 添加内容
2. /kblist - 查看内容
3. /kbdelete - 删除内容`)
		}
		// 更新最后操作时间
		state.LastUpdateTime = time.Now()
	}

	// 判断是新命令还是后续交互
	isNewCommand := strings.HasPrefix(msg.Text, "/kb")
	if isNewCommand {
		// 处理新命令
		switch msg.Text {
		case "/kbadd", "/kblist", "/kbdelete":
			state.KBState.Command = msg.Text
			state.KBState.Step = 0
			state.KBState.OwnerID = msg.From.ID
			state.WaitingForInput = true
			state.LastUpdateTime = time.Now()
		default:
			delete(h.userStates, chatID)
			return h.sendMessage(chatID, "未知的知识库命令")
		}
	} else {
		// 检查是否有活动的命令
		if state.KBState.Command == "" {
			return h.sendMessage(chatID, "请先输入知识库命令")
		}
	}

	switch state.KBState.Command {
	case "/kbadd":
		return h.handleKBAdd(msg, state)
	case "/kblist":
		return h.handleKBList(msg)
	case "/kbdelete":
		return h.handleKBDelete(msg, state)
	case "/kb":
		return h.sendMessage(chatID, `📚 知识库管理命令说明：

/kbadd - 添加新内容到知识库
  1. 选择要操作的 Agent
  2. 选择内容类型：文本、链接、文件
  3. 输入或上传内容

/kblist - 查看知识库中的所有内容
  1. 选择要查看的 Agent
  2. 查看该 Agent 的知识库内容

/kbdelete - 删除知识库中的内容
  1. 选择要操作的 Agent
  2. 选择要删除的内容
  3. 确认删除操作

🔍 使用说明：
1. 所有操作都需要在私聊中进行
2. 文件上传支持：PDF、Word、TXT、MD 格式
3. 链接必须以 http:// 或 https:// 开头
4. 可以随时输入 /cancel 取消当前操作
5. 操作超时时间为 5 分钟`)
	default:
		delete(h.userStates, chatID)
		return h.sendMessage(chatID, "未知的子命令")
	}
}

// handleKBAdd 处理添加知识库内容
func (h *TelegramHandler) handleKBAdd(msg *telego.Message, state *UserState) error {
	chatID := msg.Chat.ID
	kbState := state.KBState

	// 更新最后操作时间
	state.LastUpdateTime = time.Now()

	log.Printf("[KB] Adding content: chatID=%d, step=%d, agentSelectionDone=%v",
		chatID, kbState.Step, kbState.AgentSelectionDone)

	// 添加状态检查
	if kbState.Command != "/kbadd" {
		return fmt.Errorf("当前不在添加知识库操作中")
	}

	switch kbState.Step {
	case 0: // 列出可选的 agents
		agents, err := models.GetAgentsByBotID(context.Background(), h.botID)
		if err != nil {
			log.Printf("Error getting agents: %v", err)
			return h.sendMessage(chatID, "❌ 获取 Agent 列表失败")
		}

		if len(agents) == 0 {
			return h.sendMessage(chatID, `🤖 未找到可用的 Agent

您可以通过以下方式创建 Agent:
1️⃣ 使用 /start 命令创建新的 Agent
2️⃣ 确保您是群组管理员（如果在群组中）
3️⃣ 如需帮助，请使用 /help 命令`)
		}

		// 保存可用的 agents
		kbState.AvailableAgents = agents

		// 构建 agents 列表消息
		var result strings.Builder
		result.WriteString("🤖 请选择要操作的 Agent:\n\n")

		for i, agent := range agents {
			result.WriteString(fmt.Sprintf("%d. %s\n", i+1, agent.Name))
			if agent.Description != "" {
				result.WriteString(fmt.Sprintf("   描述: %s\n", agent.Description))
			}
			result.WriteString(fmt.Sprintf("   创建时间: %s\n", agent.CreatedAt.Format("2006-01-02 15:04:05")))
			result.WriteString("-------------------\n")
		}

		result.WriteString("\n📝 请输入序号(1-" + strconv.Itoa(len(agents)) + ")选择 Agent:")
		kbState.Step = 1
		return h.sendMessage(chatID, result.String())

	case 1: // 处理 agent 选择
		agentIndex, err := strconv.Atoi(msg.Text)
		if err != nil || agentIndex < 1 || agentIndex > len(kbState.AvailableAgents) {
			return h.sendMessage(chatID, "❌ 请输入有效的序号")
		}

		// 保存选中的 agent
		selectedAgent := kbState.AvailableAgents[agentIndex-1]
		kbState.SelectedAgent = &selectedAgent
		kbState.AgentID = selectedAgent.ID.String()
		kbState.AgentSelectionDone = true
		// 从 agent 的 knowledges 中获取 dataset ID
		if len(selectedAgent.Knowledges) == 0 {
			return h.sendMessage(chatID, fmt.Sprintf("❌ Agent「%s」未绑定知识库，请先创建知识库", selectedAgent.Name))
		}

		// 解析 JSON 格式的 datasetId
		var knowledge struct {
			DatasetID string `json:"datasetId"`
		}
		if err := json.Unmarshal([]byte(selectedAgent.Knowledges[0]), &knowledge); err != nil {
			log.Printf("Error parsing dataset ID: %v", err)
			return h.sendMessage(chatID, fmt.Sprintf("❌ Agent「%s」的知识库配置有误", selectedAgent.Name))
		}
		// 获取知识库数据集
		dataset, err := models.GetKBDataset(context.Background(), knowledge.DatasetID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				log.Printf("No dataset found for agent: %v", err)
				return h.sendMessage(chatID, fmt.Sprintf("❌ Agent「%s」未绑定知识库，请先创建知识库", kbState.SelectedAgent.Name))
			}
			log.Printf("Error getting dataset: %v", err)
			return h.sendMessage(chatID, fmt.Sprintf("❌ 获取 Agent「%s」的知识库失败", kbState.SelectedAgent.Name))
		}

		kbState.LocalDatasetID = dataset.ID
		kbState.DatasetID = dataset.ID

		// 进入选择内容类型步骤
		kbState.Step = 2
		return h.sendMessage(chatID, `📝 请选择要添加的内容类型：

1️⃣ 文本 - 直接输入文字内容
2️⃣ 链接 - 添加网页或文档链接
3️⃣ 文件 - 上传 PDF、Word 等文件

请输入序号(1-3):`)

	case 2: // 处理内容类型选择
		typeIndex, err := strconv.Atoi(msg.Text)
		if err != nil || typeIndex < 1 || typeIndex > 3 {
			return h.sendMessage(chatID, "❌ 请输入有效的序号(1-3)")
		}

		kbState.Step = 3
		switch typeIndex {
		case 1:
			kbState.AddType = "text"
			return h.sendMessage(chatID, "📝 请输入要添加的文本内容：")
		case 2:
			kbState.AddType = "link"
			return h.sendMessage(chatID, "🔗 请输入要添加的链接：\n\n支持的格式：http(s)://...")
		case 3:
			kbState.AddType = "file"
			return h.sendMessage(chatID, "📄 请上传文件：\n\n支持的格式：PDF、Word、TXT、Markdown")
		}

	case 3: // 处理内容
		log.Printf("[KB] Processing content: type=%s, datasetID=%s, contentLength=%d",
			kbState.AddType, kbState.DatasetID, len(msg.Text))
		kbService := services.NewKBService(h.cfg.AIService.BaseURL, h.cfg.AIService.APIKey)
		var err error

		switch kbState.AddType {
		case "text":
			// 生成更友好的名称，使用内容前20个字符作为标题
			title := msg.Text
			if len(title) > 20 {
				// 确保截取的是完整的 UTF-8 字符
				runeTitle := []rune(title)
				if len(runeTitle) > 20 {
					runeTitle = runeTitle[:20]
				}
				title = string(runeTitle) + "..."
			}
			// 移除任何可能导致问题的特殊字符
			title = sanitizeString(title)
			name := title + "_" + time.Now().Format("0102_150405")
			err = kbService.AddTextCollection(kbState.DatasetID, kbState.LocalDatasetID, msg.Text, name)
		case "link":
			if !strings.HasPrefix(msg.Text, "http://") && !strings.HasPrefix(msg.Text, "https://") {
				return h.sendMessage(chatID, `❌ 无效的链接格式！

链接必须以 http:// 或 https:// 开头
例如：
✅ https://example.com
✅ http://docs.example.com/file.pdf
❌ example.com
❌ www.example.com`)
			}
			err = kbService.AddLinkCollection(kbState.DatasetID, kbState.LocalDatasetID, msg.Text)
		case "file":
			if msg.Document == nil {
				return h.sendMessage(chatID, `❌ 请上传文件！

支持的格式：
📄 PDF文档 (.pdf)
📝 Word文档 (.doc, .docx)
📋 文本文件 (.txt)
📑 Markdown文件 (.md)

文件大小限制：10MB`)
			}
			// 获取文件
			file, err := h.bot.GetFile(&telego.GetFileParams{FileID: msg.Document.FileID})
			if err != nil {
				log.Printf("Error getting file: %v", err)
				return fmt.Errorf("获取文件失败: %v", err)
			}
			// 下载文件
			fileContent, err := h.downloadFile(file.FilePath)
			if err != nil {
				log.Printf("Error downloading file: %v", err)
				return fmt.Errorf("下载文件失败: %v", err)
			}
			err = kbService.AddFileCollection(kbState.DatasetID, kbState.LocalDatasetID, fileContent, msg.Document.FileName)
			if err != nil {
				log.Printf("Error adding file collection: %v", err)
				return fmt.Errorf("添加文件失败: %v", err)
			}
		}

		if err != nil {
			var errMsg string
			switch {
			case errors.Is(err, services.ErrInvalidFileSize):
				errMsg = "❌ 文件太大！\n最大支持 10MB 的文件"
			case errors.Is(err, services.ErrInvalidFileType):
				errMsg = "❌ 不支持的文件格式！\n仅支持：PDF、Word、TXT、Markdown"
			case errors.Is(err, services.ErrInvalidURL):
				errMsg = "❌ 无效的链接！\n请确保链接以 http:// 或 https:// 开头"
			default:
				errMsg = fmt.Sprintf("❌ 操作失败：%v", err)
			}
			return h.sendMessage(chatID, errMsg)
		}

		// 成功消息
		successMsg := fmt.Sprintf("✅ 内容已添加到 Agent「%s」的知识库！\n\n可以继续添加新内容，或使用 /kblist 查看所有内容", kbState.SelectedAgent.Name)
		delete(h.userStates, chatID)
		return h.sendMessage(chatID, successMsg)
	}

	return nil
}

// sanitizeString 清理字符串，确保是有效的 UTF-8 编码
func sanitizeString(s string) string {
	// 移除不可打印字符
	var result []rune
	for _, r := range s {
		if unicode.IsPrint(r) {
			result = append(result, r)
		}
	}
	// 替换特殊字符
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r), unicode.IsSpace(r):
			return r
		default:
			return '_'
		}
	}, string(result))
	return cleaned
}

// handleKBList 处理查询知识库内容
func (h *TelegramHandler) handleKBList(msg *telego.Message) error {
	chatID := msg.Chat.ID
	state := h.userStates[chatID]

	// 如果是新的命令，初始化状态
	if state == nil || state.KBState == nil {
		state = &UserState{
			KBState: &models.KBCommandState{
				Command: "/kblist",
				Step:    0,
			},
			WaitingForInput: true,
			LastUpdateTime:  time.Now(),
		}
		h.userStates[chatID] = state
	}

	kbState := state.KBState

	switch kbState.Step {
	case 0: // 列出可选的 agents
		agents, err := models.GetAgentsByBotID(context.Background(), h.botID)
		if err != nil {
			log.Printf("Error getting agents: %v", err)
			return h.sendMessage(chatID, "❌ 获取 Agent 列表失败")
		}

		if len(agents) == 0 {
			return h.sendMessage(chatID, `🤖 未找到可用的 Agent

您可以通过以下方式创建 Agent:
1️⃣ 使用 /start 命令创建新的 Agent
2️⃣ 确保您是群组管理员（如果在群组中）
3️⃣ 如需帮助，请使用 /help 命令`)
		}

		// 保存可用的 agents
		kbState.AvailableAgents = agents

		// 构建 agents 列表消息
		var result strings.Builder
		result.WriteString("🤖 请选择要查看的 Agent:\n\n")

		for i, agent := range agents {
			result.WriteString(fmt.Sprintf("%d. %s\n", i+1, agent.Name))
			if agent.Description != "" {
				result.WriteString(fmt.Sprintf("   描述: %s\n", agent.Description))
			}
			result.WriteString(fmt.Sprintf("   创建时间: %s\n", agent.CreatedAt.Format("2006-01-02 15:04:05")))
			result.WriteString("-------------------\n")
		}

		result.WriteString("\n📝 请输入序号(1-" + strconv.Itoa(len(agents)) + ")选择 Agent:")
		kbState.Step = 1
		return h.sendMessage(chatID, result.String())

	case 1: // 处理 agent 选择
		agentIndex, err := strconv.Atoi(msg.Text)
		if err != nil || agentIndex < 1 || agentIndex > len(kbState.AvailableAgents) {
			return h.sendMessage(chatID, "❌ 请输入有效的序号")
		}

		// 保存选中的 agent
		selectedAgent := kbState.AvailableAgents[agentIndex-1]
		kbState.SelectedAgent = &selectedAgent
		kbState.AgentID = selectedAgent.ID.String()

		// 从 agent 的 knowledges 中获取 dataset ID
		if len(selectedAgent.Knowledges) == 0 {
			return h.sendMessage(chatID, fmt.Sprintf("❌ Agent「%s」未绑定知识库，请先创建知识库", selectedAgent.Name))
		}

		// 解析 JSON 格式的 datasetId
		var knowledge struct {
			DatasetID string `json:"datasetId"`
		}
		if err := json.Unmarshal([]byte(selectedAgent.Knowledges[0]), &knowledge); err != nil {
			log.Printf("Error parsing dataset ID: %v", err)
			return h.sendMessage(chatID, fmt.Sprintf("❌ Agent「%s」的知识库配置有误", selectedAgent.Name))
		}

		// 获取知识库数据集
		dataset, err := models.GetKBDataset(context.Background(), knowledge.DatasetID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				log.Printf("No dataset found: %v", err)
				return h.sendMessage(chatID, fmt.Sprintf("❌ Agent「%s」的知识库不存在，请重新配置", selectedAgent.Name))
			}
			log.Printf("Error getting dataset: %v", err)
			return h.sendMessage(chatID, fmt.Sprintf("❌ 获取 Agent「%s」的知识库失败", selectedAgent.Name))
		}

		kbState.LocalDatasetID = dataset.ID
		kbState.DatasetID = dataset.ID

		// 获取知识库内容
		collections, err := models.GetKBCollections(context.Background(), kbState.LocalDatasetID)
		if err != nil {
			log.Printf("Error getting collections: %v", err)
			return h.sendMessage(chatID, fmt.Sprintf("❌ 获取 Agent「%s」的知识库内容失败", kbState.SelectedAgent.Name))
		}

		if len(collections) == 0 {
			delete(h.userStates, chatID)
			return h.sendMessage(chatID, fmt.Sprintf("📚 Agent「%s」的知识库暂无内容", kbState.SelectedAgent.Name))
		}

		var result strings.Builder
		result.WriteString(fmt.Sprintf("📚 Agent「%s」的知识库内容：\n", kbState.SelectedAgent.Name))
		result.WriteString("-------------------\n\n")

		for i, col := range collections {
			var typeIcon string
			switch col.Type {
			case "text":
				typeIcon = "📝"
			case "link":
				typeIcon = "🔗"
			case "file":
				typeIcon = "📄"
			}
			result.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, typeIcon, col.Name))
			result.WriteString(fmt.Sprintf("   类型: %s\n", col.Type))
			result.WriteString(fmt.Sprintf("   预览: %s\n", truncateString(col.Content, 50)))
			result.WriteString(fmt.Sprintf("   字数: %d\n", len(col.Content)))
		}

		result.WriteString(fmt.Sprintf("\n共 %d 条内容", len(collections)))
		return h.sendMessage(chatID, result.String())
	}

	return nil
}

// handleKBDelete 处理删除知识库内容
func (h *TelegramHandler) handleKBDelete(msg *telego.Message, state *UserState) error {
	chatID := msg.Chat.ID
	kbState := state.KBState

	// 更新最后操作时间
	state.LastUpdateTime = time.Now()

	log.Printf("[KB] Deleting content: chatID=%d, step=%d, agentSelectionDone=%v",
		chatID, kbState.Step, kbState.AgentSelectionDone)

	// 添加状态检查
	if kbState.Command != "/kbdelete" {
		return fmt.Errorf("当前不在删除知识库操作中")
	}

	switch kbState.Step {
	case 0: // 列出可选的 agents
		agents, err := models.GetAgentsByBotID(context.Background(), h.botID)
		if err != nil {
			log.Printf("Error getting agents: %v", err)
			return h.sendMessage(chatID, "❌ 获取 Agent 列表失败")
		}

		if len(agents) == 0 {
			return h.sendMessage(chatID, `🤖 未找到可用的 Agent

您可以通过以下方式创建 Agent:
1️⃣ 使用 /start 命令创建新的 Agent
2️⃣ 确保您是群组管理员（如果在群组中）
3️⃣ 如需帮助，请使用 /help 命令`)
		}

		// 保存可用的 agents
		kbState.AvailableAgents = agents

		// 构建 agents 列表消息
		var result strings.Builder
		result.WriteString("🤖 请选择要操作的 Agent:\n\n")

		for i, agent := range agents {
			result.WriteString(fmt.Sprintf("%d. %s\n", i+1, agent.Name))
			if agent.Description != "" {
				result.WriteString(fmt.Sprintf("   描述: %s\n", agent.Description))
			}
			result.WriteString(fmt.Sprintf("   创建时间: %s\n", agent.CreatedAt.Format("2006-01-02 15:04:05")))
			result.WriteString("-------------------\n")
		}

		result.WriteString("\n📝 请输入序号(1-" + strconv.Itoa(len(agents)) + ")选择 Agent:")
		kbState.Step = 1
		return h.sendMessage(chatID, result.String())

	case 1: // 处理 agent 选择并显示可删除的内容
		agentIndex, err := strconv.Atoi(msg.Text)
		if err != nil || agentIndex < 1 || agentIndex > len(kbState.AvailableAgents) {
			return h.sendMessage(chatID, "❌ 请输入有效的序号")
		}

		// 保存选中的 agent
		selectedAgent := kbState.AvailableAgents[agentIndex-1]
		kbState.SelectedAgent = &selectedAgent
		kbState.AgentID = selectedAgent.ID.String()
		kbState.AgentSelectionDone = true

		// 从 agent 的 knowledges 中获取 dataset ID
		if len(selectedAgent.Knowledges) == 0 {
			return h.sendMessage(chatID, fmt.Sprintf("❌ Agent「%s」未绑定知识库，请先创建知识库", selectedAgent.Name))
		}

		// 解析 JSON 格式的 datasetId
		var knowledge struct {
			DatasetID string `json:"datasetId"`
		}
		if err := json.Unmarshal([]byte(selectedAgent.Knowledges[0]), &knowledge); err != nil {
			log.Printf("Error parsing dataset ID: %v", err)
			return h.sendMessage(chatID, fmt.Sprintf("❌ Agent「%s」的知识库配置有误", selectedAgent.Name))
		}

		// 获取知识库数据集
		dataset, err := models.GetKBDataset(context.Background(), knowledge.DatasetID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				log.Printf("No dataset found: %v", err)
				return h.sendMessage(chatID, fmt.Sprintf("❌ Agent「%s」的知识库不存在，请重新配置", selectedAgent.Name))
			}
			log.Printf("Error getting dataset: %v", err)
			return h.sendMessage(chatID, fmt.Sprintf("❌ 获取 Agent「%s」的知识库失败", selectedAgent.Name))
		}

		kbState.LocalDatasetID = dataset.ID
		kbState.DatasetID = dataset.ID

		// 获取可删除的内容列表
		collections, err := models.GetKBCollections(context.Background(), kbState.LocalDatasetID)
		if err != nil {
			log.Printf("Error getting collections: %v", err)
			return h.sendMessage(chatID, fmt.Sprintf("❌ 获取 Agent「%s」的知识库内容失败", kbState.SelectedAgent.Name))
		}

		if len(collections) == 0 {
			delete(h.userStates, chatID)
			return h.sendMessage(chatID, fmt.Sprintf("📚 Agent「%s」的知识库暂无内容", kbState.SelectedAgent.Name))
		}

		var result strings.Builder
		result.WriteString(fmt.Sprintf("🗑 请选择要从 Agent「%s」知识库中删除的内容：\n", kbState.SelectedAgent.Name))
		result.WriteString("-------------------\n\n")

		for i, col := range collections {
			var typeIcon string
			switch col.Type {
			case "text":
				typeIcon = "📝"
			case "link":
				typeIcon = "🔗"
			case "file":
				typeIcon = "📄"
			}
			result.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, typeIcon, col.Name))
			result.WriteString(fmt.Sprintf("   类型: %s\n", col.Type))
			switch col.Type {
			case "text":
				result.WriteString(fmt.Sprintf("   预览: %s\n", truncateString(col.Content, 30)))
			case "link":
				result.WriteString(fmt.Sprintf("   链接: %s\n", col.Content))
			case "file":
				result.WriteString(fmt.Sprintf("   文件名: %s\n", col.SourceName))
			}
			result.WriteString(fmt.Sprintf("   添加时间: %s\n", col.CreatedAt.Format("2006-01-02 15:04:05")))
			result.WriteString("-------------------\n")
		}

		result.WriteString("\n⚠️ 请输入要删除的内容序号：")
		kbState.Step = 2
		return h.sendMessage(chatID, result.String())

	case 2: // 选择要删除的内容并确认
		collections, err := models.GetKBCollections(context.Background(), kbState.LocalDatasetID)
		if err != nil {
			log.Printf("Error getting collections: %v", err)
			return h.sendMessage(chatID, fmt.Sprintf("❌ 获取 Agent「%s」的知识库内容失败", kbState.SelectedAgent.Name))
		}

		collectionIndex, err := strconv.Atoi(msg.Text)
		if err != nil || collectionIndex < 1 || collectionIndex > len(collections) {
			return h.sendMessage(chatID, "❌ 请输入有效的序号")
		}

		collection := collections[collectionIndex-1]
		kbState.CollectionID = collection.CollectionID

		var result strings.Builder
		result.WriteString(fmt.Sprintf("🗑 从 Agent「%s」知识库中删除以下内容：\n\n", kbState.SelectedAgent.Name))
		result.WriteString("名称: " + collection.Name + "\n")
		result.WriteString(fmt.Sprintf("类型: %s\n", collection.Type))
		switch collection.Type {
		case "text":
			result.WriteString(fmt.Sprintf("   预览: %s\n", truncateString(collection.Content, 30)))
		case "link":
			result.WriteString(fmt.Sprintf("   链接: %s\n", collection.Content))
		case "file":
			result.WriteString(fmt.Sprintf("   文件名: %s\n", collection.SourceName))
		}
		result.WriteString(fmt.Sprintf("添加时间: %s\n", collection.CreatedAt.Format("2006-01-02 15:04:05")))
		result.WriteString("\n⚠️ 确认删除这条内容吗？\n")
		result.WriteString("此操作不可撤销！\n\n")
		result.WriteString("回复 'yes' 确认删除，或输入其他内容取消")
		kbState.Step = 3
		return h.sendMessage(chatID, result.String())

	case 3: // 处理确认
		if strings.ToLower(msg.Text) != "yes" {
			delete(h.userStates, chatID)
			return h.sendMessage(chatID, "✅ 操作已取消")
		}

		// 执行删除操作
		kbService := services.NewKBService(h.cfg.AIService.BaseURL, h.cfg.AIService.APIKey)
		if err := kbService.DeleteCollection(kbState.CollectionID); err != nil {
			log.Printf("Error deleting collection: %v", err)
			return fmt.Errorf("删除内容失败: %v", err)
		}

		delete(h.userStates, chatID)
		return h.sendMessage(chatID, fmt.Sprintf("✅ 已从 Agent「%s」的知识库中删除内容！\n\n可以使用 /kblist 查看剩余内容", kbState.SelectedAgent.Name))
	}

	return nil
}

// downloadFile 下载文件
func (h *TelegramHandler) downloadFile(filePath string) ([]byte, error) {
	resp, err := http.Get(fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", h.bot.Token(), filePath))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return ioutil.ReadAll(resp.Body)
}

// sendMessage 发送消息
func (h *TelegramHandler) sendMessage(chatID int64, text string) error {
	params := &telego.SendMessageParams{
		ChatID: telego.ChatID{ID: chatID},
		Text:   text,
	}
	_, err := h.bot.SendMessage(params)
	return err
}

// handleQueryAgent 查询处理函数
func (h *TelegramHandler) handleQueryAgent(msg *telego.Message) {
	ctx := context.Background()
	chatID := msg.Chat.ID

	// 1. 获取agent信息
	agent, err := models.GetAgentByChat(ctx, chatID, h.botID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			h.sendMessage(chatID, `🤖 未找到关联的 Agent

您可以通过以下方式创建 Agent:
1️⃣ 使用 /start 命令创建新的 Agent
2️⃣ 确保您是群组管理员（如果在群组中）
3️⃣ 如需帮助，请使用 /help 命令`)
			return
		}
		log.Printf("Query agent error: %v", err)
		h.sendMessage(chatID, `❌ 查询出现问题

可能的原因：
1. 服务器暂时无响应
2. 网络连接不稳定
3. 系统维护中

请稍后重试，如果问题持续存在，请联系管理员。`)
		return
	}

	// 2. 获取角色信息
	role, err := models.GetRoleByID(ctx, agent.RoleID.String())
	if err != nil {
		log.Printf("Get role error: %v", err)
		h.sendMessage(chatID, "❌ 获取角色信息失败")
		return
	}

	// 3. 构建基本信息
	var response strings.Builder
	response.WriteString("🤖 *Agent 信息*\n\n")
	response.WriteString(fmt.Sprintf("▪️ 名称: %s\n", agent.Name))
	response.WriteString(fmt.Sprintf("▪️ 描述: %s\n", agent.Description))
	response.WriteString(fmt.Sprintf("▪️ 角色: %s\n", role.Name))
	response.WriteString(fmt.Sprintf("▪️ 创建时间: %s", agent.CreatedAt.Format("2006-01-02 15:04")))

	// 4. 添加知识库信息
	if len(agent.Knowledges) > 0 {
		response.WriteString("\n\n📚 *知识库信息*\n")

		for _, kbIDJson := range agent.Knowledges {
			var knowledge struct {
				DatasetID string `json:"datasetId"`
			}
			if err := json.Unmarshal([]byte(kbIDJson), &knowledge); err != nil {
				log.Printf("[Agent] 解析知识库ID失败: %v, raw=%s", err, kbIDJson)
				continue
			}

			dataset, err := models.GetKBDatasetByID(context.Background(), knowledge.DatasetID)
			if err != nil {
				log.Printf("[Agent] 获取数据集信息失败: %v", err)
				continue
			}

			collections, err := models.GetKBCollections(context.Background(), dataset.ID)
			if err != nil {
				log.Printf("[Agent] 获取知识库内容失败: %v", err)
				continue
			}

			// 按类型统计内容
			stats := make(map[string]int)
			for _, col := range collections {
				stats[col.Type]++
			}

			response.WriteString(fmt.Sprintf("\n▫️ %s\n", dataset.Name))
			if dataset.Description != "" {
				response.WriteString(fmt.Sprintf("   简介: %s\n", dataset.Description))
			}
			response.WriteString(fmt.Sprintf("   总计: %d 条内容\n", len(collections)))

			// 显示各类型内容数量
			if count := stats["text"]; count > 0 {
				response.WriteString(fmt.Sprintf("   📝 文本: %d\n", count))
			}
			if count := stats["link"]; count > 0 {
				response.WriteString(fmt.Sprintf("   🔗 链接: %d\n", count))
			}
			if count := stats["file"]; count > 0 {
				response.WriteString(fmt.Sprintf("   📄 文件: %d\n", count))
			}
		}

	}

	// 5. 发送消息
	params := &telego.SendMessageParams{
		ChatID:    telego.ChatID{ID: chatID},
		Text:      response.String(),
		ParseMode: telego.ModeMarkdown,
	}

	if _, err := h.bot.SendMessage(params); err != nil {
		log.Printf("Send message error: %v", err)
		// 如果 Markdown 发送失败，尝试发送纯文本
		plainText := strings.NewReplacer(
			"*", "",
			"_", "",
			"`", "",
			"[", "",
			"]", "",
		).Replace(response.String())
		h.sendMessage(chatID, plainText)
	}
}

// escapeMarkdown 优化 Markdown 转义
func escapeMarkdown(s string) string {
	// 更精简的Markdown特殊字符列表 - 移除了连字符(-)和一些不太需要转义的字符
	specialChars := []string{"_", "*", "[", "]", "`", "#"}
	result := s

	// 转换中文标点符号为英文标点符号
	puncMap := map[string]string{
		"，": ",",
		"。": ".",
		"！": "!",
		"？": "?",
		"；": ";",
		"：": ":",
		"（": "(",
		"）": ")",
		"【": "[",
		"】": "]",
		"「": "\"",
		"」": "\"",
		"『": "'",
		"』": "'",
		"《": "<",
		"》": ">",
		"·": "`",
	}

	// 先转换中文标点符号
	for chinese, english := range puncMap {
		result = strings.ReplaceAll(result, chinese, english)
	}

	// 再转义Markdown字符
	for _, char := range specialChars {
		result = strings.ReplaceAll(result, char, "\\"+char)
	}

	return result
}

// handleRoleList 处理角色列表
func (h *TelegramHandler) handleRoleList(msg *telego.Message) {
	ctx := context.Background()
	chatID := msg.Chat.ID

	// 获取所有角色
	roles, err := models.GetAllRoles(ctx)
	if err != nil {
		log.Printf("Get roles error: %v", err)
		h.sendMessage(chatID, "❌ 获取角色列表失败")
		return
	}

	if len(roles) == 0 {
		h.sendMessage(chatID, "📝 暂无可用角色")
		return
	}

	// 构建响应消息
	var response strings.Builder
	response.WriteString("👥 *可用角色列表*:\n\n")

	for i, role := range roles {
		// 使用安全的获取方法处理可能为 nil 的字段
		description := getSafeString(role.Description)
		personality := getSafeString(role.Personality)
		skills := getSafeString(role.Skills)
		nativeLanguage := getSafeString(role.NativeLanguage)
		gender := getSafeString(role.Gender)

		// 格式化每个角色的信息
		roleInfo := fmt.Sprintf(
			"%d. *%s*\n"+
				"   ▫️ 描述: `%s`\n"+
				"   ▫️ 性格: `%s`\n"+
				"   ▫️ 技能: `%s`\n"+
				"   ▫️ 语言: `%s`\n"+
				"   ▫️ 性别: `%s`\n"+
				"   ▫️ 创建时间: `%s`\n\n",
			i+1,
			escapeMarkdown(role.Name),
			escapeMarkdown(description),
			escapeMarkdown(truncateString(personality, 50)),
			escapeMarkdown(truncateString(skills, 50)),
			escapeMarkdown(nativeLanguage),
			escapeMarkdown(gender),
			role.CreatedAt.Format("2006-01-02 15:04"),
		)
		response.WriteString(roleInfo)
	}

	response.WriteString(fmt.Sprintf("总计: %d 个角色", len(roles)))

	// 发送 Markdown 格式消息
	params := &telego.SendMessageParams{
		ChatID:    telego.ChatID{ID: chatID},
		Text:      response.String(),
		ParseMode: telego.ModeMarkdown,
	}

	if _, err := h.bot.SendMessage(params); err != nil {
		log.Printf("Send message error: %v", err)
		// 如果 Markdown 发送失败，尝试使用纯文本发送
		plainText := strings.NewReplacer(
			"*", "",
			"_", "",
			"`", "",
			"[", "",
			"]", "",
		).Replace(response.String())
		h.sendMessage(chatID, plainText)
	}
}

// getSafeString 安全地获取字符串，处理空字符串的情况
func getSafeString(s string) string {
	if s == "" {
		return "未设置"
	}
	return s
}

func validateWebhookConfig(domain string) error {
	if !strings.HasPrefix(domain, "https://") {
		return fmt.Errorf("webhook domain must use HTTPS")
	}

	// 验证域名格式
	_, err := url.Parse(domain)
	if err != nil {
		return fmt.Errorf("invalid domain format: %v", err)
	}

	return nil
}
