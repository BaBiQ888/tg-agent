# TG-Agent

Telegram Bot Agent 平台 — 基于 Go 构建，支持多 Bot 管理、知识库 RAG 对话、角色扮演。

## 架构

```
Telegram 用户
      │
      ▼
┌──────────────────────────────────────────┐
│  Go 主服务 (tg-agent) — fasthttp :80     │
│  ├── handlers/telegram.go  Bot 交互      │
│  ├── handlers/api_handler.go REST API    │
│  ├── services/ai_client.go  AI 服务客户端 │
│  ├── services/kb_service.go 知识库服务    │
│  └── services/action_service.go 对话服务  │
└──────────────────┬───────────────────────┘
                   │ REST API (JSON) + X-API-Key
                   ▼
         Python AI 服务 (独立部署)
         ├── 文档摄入 (text/file/link)
         ├── RAG 检索 + 上下文构建
         ├── LLM 调用 (Claude/OpenAI/Custom)
         └── Milvus 向量存储

PostgreSQL (业务数据)
```

## 功能

- 多 Bot 管理 — 支持同时运行多个 Telegram Bot
- 知识库管理 — 文本/文件/链接摄入，向量化存储
- RAG 对话 — 基于知识库的检索增强生成
- 角色扮演 — 可配置的 Agent 角色和人设
- 命令系统 — 可扩展的 Bot 命令和动作
- Webhook/Polling — 支持两种 Telegram 接入方式

## 前置依赖

- Go 1.21+
- PostgreSQL
- Python AI 服务（独立部署，见下方说明）

## 快速开始

### 1. 配置

```bash
# 复制环境变量模板
cp .env.example .env

# 编辑 .env，填入真实配置
# - DB_PASSWORD: 数据库密码
# - AI_SERVICE_URL: Python AI 服务地址
# - AI_SERVICE_API_KEY: AI 服务认证密钥
# - API_KEY: 本服务 API 密钥
```

### 2. 数据库初始化

```bash
# 按顺序执行 SQL 建表脚本
psql -d your_database -f sql/bots.sql
psql -d your_database -f sql/agent_roles.sql
psql -d your_database -f sql/agents.sql
psql -d your_database -f sql/agent_chats.sql
psql -d your_database -f sql/bot_cmds.sql
psql -d your_database -f sql/actions.sql
psql -d your_database -f sql/cmd_actions.sql
psql -d your_database -f sql/agent_actions.sql
psql -d your_database -f sql/kb_datasets.sql
psql -d your_database -f sql/kb_collections.sql
```

如果是从旧版本（FastGPT）升级，执行迁移脚本：

```bash
psql -d your_database -f migrations/001_remove_fastgpt_fields.sql
```

### 3. 编译运行

```bash
go mod download
go build -o bot
./bot
```

### 4. Docker 部署

```bash
docker build -t tg-agent .

docker run -d \
  --name tg-agent \
  -p 80:80 \
  --env-file .env \
  tg-agent
```

## Python AI 服务

本项目依赖独立部署的 Python AI 服务提供 RAG 和 LLM 能力。AI 服务需要提供以下 API：

| 端点 | 方法 | 说明 |
|------|------|------|
| `/health` | GET | 存活检查 |
| `/readiness` | GET | 就绪检查 |
| `/api/v1/chat/completions` | POST | RAG 对话 |
| `/api/v1/datasets` | POST | 创建知识库 |
| `/api/v1/datasets/{id}` | GET/DELETE | 查询/删除知识库 |
| `/api/v1/collections/text` | POST | 摄入文本 |
| `/api/v1/collections/link` | POST | 摄入链接 |
| `/api/v1/collections/file` | POST | 摄入文件 (multipart) |
| `/api/v1/collections/{id}` | DELETE | 删除集合 |

通信协议详见 `docs/REFACTOR_PLAN.md`。

## 环境变量

| 变量 | 说明 | 必填 |
|------|------|------|
| `DB_PASSWORD` | PostgreSQL 密码 | 是 |
| `AI_SERVICE_URL` | Python AI 服务地址 | 是 |
| `AI_SERVICE_API_KEY` | AI 服务认证密钥 | 是 |
| `API_KEY` | 本服务 REST API 密钥 | 是 |
| `PORT` | 服务端口（默认 80） | 否 |
| `PROXY_URL` | HTTP 代理地址 | 否 |
| `RAILWAY_ENVIRONMENT_NAME` | Railway 部署环境名 | 否 |

## 项目结构

```
tg-agent/
├── config/          # 配置加载
├── handlers/        # HTTP 和 Telegram 处理器
├── interfaces/      # 接口定义
├── models/          # 数据模型和数据库操作
├── services/        # 业务逻辑（AI 客户端、知识库、对话）
├── sql/             # 数据库建表脚本
├── migrations/      # 数据库迁移脚本
├── docs/            # 技术文档
├── config.json      # 运行时配置
├── Dockerfile       # Docker 构建
└── main.go          # 入口
```

## License

MIT
