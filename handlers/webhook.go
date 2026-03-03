package handlers

import (
	"log"
	"regexp"
	"strings"
	"sync"

	"github.com/goccy/go-json"
	"github.com/mymmrac/telego"
	"github.com/valyala/fasthttp"
)

type WebhookHandler struct {
	sync.RWMutex
	bots map[string]*TelegramHandler
}

func NewWebhookHandler() *WebhookHandler {
	return &WebhookHandler{
		bots: make(map[string]*TelegramHandler),
	}
}

func (wh *WebhookHandler) AddBot(botID string, handler *TelegramHandler) {
	wh.Lock()
	defer wh.Unlock()
	wh.bots[botID] = handler
}

func (wh *WebhookHandler) GetBot(botID string) (*TelegramHandler, bool) {
	wh.RLock()
	defer wh.RUnlock()
	handler, ok := wh.bots[botID]
	return handler, ok
}

// CustomWebhookRequest 自定义 webhook 请求结构
type CustomWebhookRequest struct {
	BotID  string `json:"bot_id"`
	ChatID int64  `json:"chat_id"`
	Text   string `json:"text"`
}

// APIResponse 标准 API 响应格式
type APIResponse struct {
	Code    int         `json:"code"`    // 状态码：0-成功，非0-失败
	Message string      `json:"message"` // 响应消息
	Data    interface{} `json:"data"`    // 响应数据
}

// HandleCustomWebhook 处理自定义 webhook 请求
func (h *WebhookHandler) HandleCustomWebhook(ctx *fasthttp.RequestCtx) {
	// 设置响应头
	ctx.SetContentType("application/json")

	// 解析请求
	var req CustomWebhookRequest
	if err := json.Unmarshal(ctx.Request.Body(), &req); err != nil {
		log.Printf("Error parsing webhook request: %v", err)
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
	handler, ok := h.GetBot(req.BotID)
	if !ok {
		log.Printf("Bot not found: %s", req.BotID)
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

	// TODO: 如果需要发送消息，取消注释以下代码
	params := &telego.SendMessageParams{
		ChatID:    telego.ChatID{ID: req.ChatID},
		Text:      req.Text,
		ParseMode: telego.ModeMarkdownV2,
	}

	log.Printf("Sending message to bot %s", req.BotID)
	log.Printf("Message: %s", req.Text)
	log.Printf("ChatID: %d", req.ChatID)
	log.Printf("WebhookDomain: %s", handler.cfg.WebhookDomain)
	// 发送消息并获取返回结果
	message, err := handler.bot.SendMessage(params)
	if err != nil {
		log.Printf("Error sending message: %v", err)
		params.ParseMode = ""
		params.Text = removeMarkdownKeepLinks(req.Text)
		message, err = handler.bot.SendMessage(params)
		if err != nil {
			log.Printf("Error sending plain text message: %v", err)
			response := APIResponse{
				Code:    500,
				Message: "Failed to send message: " + err.Error(),
				Data:    nil,
			}
			responseJSON, _ := json.Marshal(response)
			ctx.SetStatusCode(fasthttp.StatusInternalServerError)
			ctx.SetBody(responseJSON)
			return
		}
	}

	// 返回成功，包含消息 ID
	response := APIResponse{
		Code:    0,
		Message: "Success",
		Data: map[string]interface{}{
			"bot_id":     req.BotID,
			"chat_id":    req.ChatID,
			"text":       req.Text,
			"message_id": message.MessageID, // 添加消息 ID
		},
	}
	responseJSON, _ := json.Marshal(response)
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(responseJSON)

	log.Printf("Success handle webhook request: %s, %d, %s", req.BotID, req.ChatID, req.Text)
}

func removeMarkdownKeepLinks(text string) string {
	// 预编译正则表达式（提升性能）
	var (
		linkRe         = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`) // Standard Markdown links
		boldItalicRe   = regexp.MustCompile(`[*_]{1,3}([^*_]+)[*_]{1,3}`)
		codeRe         = regexp.MustCompile("`{1,3}([^`]+)`{1,3}")
		listRe         = regexp.MustCompile(`(?m)^[ \t]*[-*+] `)
		headerRe       = regexp.MustCompile(`#{1,6} `)
		escapeRe       = regexp.MustCompile(`\\([*_\[\]()~\` + "`" + `><#+-=|{}.!])`)
		imageRe        = regexp.MustCompile(`!\[([^\]]*)\]\([^)]+\)`)
		telegramLinkRe = regexp.MustCompile(`\[(.*?)\]\((https?://[^\s)]+)\)`) // Telegram-specific links
	)

	// 处理步骤
	steps := []struct {
		re   *regexp.Regexp
		repl string
	}{
		// 1. 转换链接格式：[显示文本](URL) → 显示文本 (URL)
		{telegramLinkRe, "[$1]($2)"},

		{linkRe, "$1 ($2)"},

		// 2. 移除粗体/斜体/删除线等修饰
		{boldItalicRe, "$1"},

		// 3. 保留代码内容（移除反引号）
		{codeRe, "$1"},

		// 4. 移除列表标记
		{listRe, ""},

		// 5. 移除标题标记
		{headerRe, ""},

		// 6. 处理转义字符
		{escapeRe, "$1"},

		// 7. 转换图片标记：![alt](url) → alt
		{imageRe, "$1"},
	}

	// 按顺序执行处理
	for _, step := range steps {
		text = step.re.ReplaceAllString(text, step.repl)
	}

	return strings.TrimSpace(text)
}

// TODO: 如果需要处理 Telegram webhook 请求，实现此函数
/*
func (h *WebhookHandler) HandleUpdate(ctx *fasthttp.RequestCtx) {
	// ... 现有代码 ...
}
*/
