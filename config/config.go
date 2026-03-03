package config

import (
	"log"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/goccy/go-json"
	"github.com/joho/godotenv"
)

type BotConfig struct {
	ID    string `json:"id"`
	Token string `json:"token"`
}

type WorkflowPaths map[string]string

type CommandConfig struct {
	Command       string        `json:"command"`
	Description   string        `json:"description"`
	ReplyTip      string        `json:"reply_tip"`
	WorkflowPaths WorkflowPaths `json:"workflow_paths"`
	MessageIDs    []int         `json:"-"` // 用于跟踪需要删除的消息ID
}

type Config struct {
	UseWebhook    bool   `json:"use_webhook"`
	WebhookDomain string `json:"webhook_domain"`
	Proxy         struct {
		URL string `json:"url"`
	} `json:"proxy"`
	RecvWebhookPath struct {
		Path string `json:"path"`
		Port int    `json:"port"`
	} `json:"recv_webhook_path"`
	SendWebhookPath struct {
		Path string `json:"path"`
		Port int    `json:"port"`
	} `json:"send_webhook_path"`
	Server struct {
		Port        int    `json:"port"`
		WebhookPath string `json:"webhook_path"`
		APIPath     string `json:"api_path"`
		APIKey      string `json:"api_key"`
	} `json:"server"`
	MacroDefinitions map[string]string `json:"macro_definitions"`
	Commands         []CommandConfig   `json:"commands"`
	Bots             []BotConfig       `json:"bots"`
	Database         struct {
		DSN  string `json:"dsn"`
		Pool struct {
			MaxConns        int32  `json:"max_conns"`
			MinConns        int32  `json:"min_conns"`
			MaxConnLifetime string `json:"max_conn_lifetime"`
			MaxConnIdleTime string `json:"max_conn_idle_time"`
		} `json:"pool"`
	} `json:"database"`
	AIService AIServiceConfig `json:"ai_service"`
	RunEnv    string
}

type AIServiceConfig struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Timeout struct {
		Chat   int `json:"chat"`   // seconds
		Ingest int `json:"ingest"` // seconds
		Manage int `json:"manage"` // seconds
	} `json:"timeout"`
	KB struct {
		MaxFileSize      int      `json:"max_file_size"`
		TempDir          string   `json:"temp_dir"`
		AllowedTypes     []string `json:"allowed_types"`
		DefaultChunkSize struct {
			Text int `json:"text"`
			Link int `json:"link"`
			File int `json:"file"`
		} `json:"default_chunk_size"`
		ChunkOverlap int `json:"chunk_overlap"`
	} `json:"kb"`
	LLM struct {
		DefaultProvider string `json:"default_provider"`
		DefaultModel    string `json:"default_model"`
	} `json:"llm"`
}

var (
	cfg  *Config
	once sync.Once
)

func init() {
	// 在生产环境中静默加载 .env
	if os.Getenv("RAILWAY_ENVIRONMENT") == "" {
		if err := godotenv.Load(); err != nil {
			log.Printf("Warning: Error loading .env file: %v", err)
		}
	} else {
		_ = godotenv.Load()
	}
}

func GetConfig() *Config {
	once.Do(func() {
		cfg = &Config{}
		data, err := os.ReadFile("config.json")
		if err != nil {
			log.Fatalf("Error reading config file: %v", err)
		}

		if err := json.Unmarshal(data, cfg); err != nil {
			log.Fatalf("Error parsing config file: %v", err)
		}

		// 使用 url.QueryEscape 来正确编码密码
		dbPassword := url.QueryEscape(os.Getenv("DB_PASSWORD"))
		cfg.Database.DSN = strings.Replace(
			cfg.Database.DSN,
			"${DB_PASSWORD}",
			dbPassword,
			1,
		)

		// 打印脱敏的 DSN（用 *** 替换密码）
		dsnForLog := strings.Replace(
			cfg.Database.DSN,
			dbPassword,
			"***",
			1,
		)
		log.Printf("Database DSN: %s", dsnForLog)

		// 替换代理环境变量
		cfg.Proxy.URL = os.Getenv("PROXY_URL")

		// 替换 API key 环境变量
		cfg.Server.APIKey = os.Getenv("API_KEY")
		if cfg.Server.APIKey == "" {
			log.Fatal("API_KEY environment variable is required")
		}

		// 打印代理配置（调试用）
		if cfg.Proxy.URL != "" {
			log.Printf("Using proxy: %s", cfg.Proxy.URL)
		} else {
			log.Printf("No proxy configured, using direct connection")
		}

		// AI Service 环境变量替换
		cfg.AIService.BaseURL = os.Getenv("AI_SERVICE_URL")
		cfg.AIService.APIKey = os.Getenv("AI_SERVICE_API_KEY")
		cfg.RunEnv = os.Getenv("RAILWAY_ENVIRONMENT_NAME")

		// 检查必要配置
		if cfg.RunEnv == "" {
			log.Fatal("RAILWAY_ENVIRONMENT_NAME environment variable is required")
		}
		if cfg.AIService.BaseURL == "" {
			log.Fatal("AI_SERVICE_URL environment variable is required")
		}
		if cfg.AIService.APIKey == "" {
			log.Fatal("AI_SERVICE_API_KEY environment variable is required")
		}

		log.Printf("AI Service URL: %s", cfg.AIService.BaseURL)
	})
	return cfg
}

func (c *Config) UnmarshalJSON(data []byte) error {
	type Alias Config
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(c),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	return nil
}

func (c Config) MarshalJSON() ([]byte, error) {
	type Alias Config
	aux := Alias(c)
	return json.Marshal(aux)
}

// ReplaceMacros 替换路径中的宏定义
func (c *Config) ReplaceMacros(path string) string {
	for key, value := range c.MacroDefinitions {
		path = strings.Replace(path, "${"+key+"}", value, -1)
	}
	return path
}
