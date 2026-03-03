package main

import (
	"context"
	"log"
	"os"
	"sort"
	"time"

	"github.com/Tom-Jerry/TGAgent/config"
	"github.com/Tom-Jerry/TGAgent/handlers"
	"github.com/Tom-Jerry/TGAgent/models"
	"github.com/joho/godotenv"
	"github.com/valyala/fasthttp"
)

func init() {
	// 尝试加载 .env 文件，但在生产环境中不输出警告
	if os.Getenv("RAILWAY_ENVIRONMENT") == "" {
		// 本地开发环境
		if err := godotenv.Load(); err != nil {
			log.Printf("Warning: Error loading .env file: %v", err)
		}
	} else {
		// Railway 或其他生产环境
		_ = godotenv.Load()
	}

	// 打印关键环境变量
	log.Println("=== Environment Variables ===")
	log.Printf("RAILWAY_ENVIRONMENT: %q", os.Getenv("RAILWAY_ENVIRONMENT"))
	log.Printf("AI_SERVICE_URL: %q", os.Getenv("AI_SERVICE_URL"))
	log.Printf("PORT: %q", os.Getenv("PORT"))
	log.Println("===========================")

	// 打印所有环境变量
	log.Println("All Environment Variables:")
	envVars := os.Environ()
	sort.Strings(envVars) // 排序以便更容易找到
	for _, env := range envVars {
		log.Printf("  %s", env)
	}
}

func main() {
	cfg := config.GetConfig()

	// 初始化数据库连接
	if err := models.InitDB(cfg); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	webhookHandler := handlers.NewWebhookHandler()

	// 从数据库获取机器人和命令配置
	bots, err := models.GetBots(context.Background(), cfg.RunEnv)
	if err != nil {
		log.Fatalf("Failed to get bots from database: %v", err)
	}

	commands, err := models.GetCommands(context.Background())
	if err != nil {
		log.Fatalf("Failed to get commands from database: %v", err)
	}
	log.Printf("Loaded %d commands from database", len(commands))

	// 初始化所有机器人
	for _, bot := range bots {
		handler, err := handlers.NewTelegramHandler(
			bot.ID.String(), // 使用 UUID 格式的 ID
			bot.Token,
			commands,
			cfg,
		)
		if err != nil {
			log.Printf("Error initializing bot %s: %v", bot.BotName, err)
			continue
		}

		webhookHandler.AddBot(bot.ID.String(), handler)

		if cfg.UseWebhook {
			log.Printf("Setting webhook for bot %s", bot.BotName)
			if err := handler.SetWebhook(cfg.WebhookDomain, cfg.RecvWebhookPath.Path); err != nil {
				log.Printf("Error setting webhook for bot %s: %v", bot.BotName, err)
				continue
			}
		} else {
			go handler.StartPolling()
		}
	}

	// 创建 API 处理器
	apiHandler := handlers.NewAPIHandler(webhookHandler, cfg.Server.APIKey, cfg)

	// 创建路由处理器
	routerHandler := func(ctx *fasthttp.RequestCtx) {
		path := string(ctx.Path())
		switch path {
		case cfg.SendWebhookPath.Path:
			webhookHandler.HandleCustomWebhook(ctx)
		case cfg.RecvWebhookPath.Path:
			botID := string(ctx.QueryArgs().Peek("bot_id"))
			requestPath := string(ctx.RequestURI())
			method := string(ctx.Method())

			log.Printf("[Webhook] Received %s request for bot_id: %s, path: %s",
				method, botID, requestPath)

			// 验证请求方法
			if method != "POST" {
				log.Printf("[Webhook] Invalid method: %s", method)
				ctx.SetStatusCode(fasthttp.StatusMethodNotAllowed)
				ctx.SetBodyString("Method not allowed")
				return
			}

			if handler, ok := webhookHandler.GetBot(botID); ok {
				log.Printf("[Webhook] Processing update for bot: %s", botID)
				handler.HandleWebhook(ctx)
			} else {
				log.Printf("[Webhook] Bot not found: %s", botID)
				ctx.SetStatusCode(fasthttp.StatusNotFound)
				ctx.SetBodyString("Bot not found")
			}
		case cfg.Server.APIPath + "/queryAgent":
			apiHandler.HandleQueryAgent(ctx)
		case cfg.Server.APIPath + "/editAgent":
			apiHandler.HandleEditAgent(ctx)
		case cfg.Server.APIPath + "/stop":
			apiHandler.HandleStop(ctx)
		case cfg.Server.APIPath + "/agent":
			apiHandler.HandleAgent(ctx)
		case cfg.Server.APIPath + "/agentTopic":
			apiHandler.HandleAgentTopic(ctx)
		case cfg.Server.WebhookPath:
			webhookHandler.HandleCustomWebhook(ctx)
		default:
			ctx.Error("Not found", fasthttp.StatusNotFound)
		}
	}

	// 创建自定义的服务器配置
	server := &fasthttp.Server{
		Handler: routerHandler,
		Name:    "TGAgent",
		// 增加并发处理能力
		Concurrency: 100000,
		// 增加读写超时
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		// 增加最大请求体大小
		MaxRequestBodySize: 10 * 1024 * 1024, // 10MB
		// 禁用保持活动连接以避免连接重用问题
		DisableKeepalive: true,
		// 日志记录器
		Logger: &customLogger{},
	}

	// 获取 PORT 环境变量
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// 启动服务器
	log.Printf("Starting server on port %s with enhanced configuration", port)
	if err := server.ListenAndServe(":" + port); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}

// 自定义日志记录器
type customLogger struct{}

func (cl *customLogger) Printf(format string, args ...interface{}) {
	log.Printf("[Server] "+format, args...)
}
