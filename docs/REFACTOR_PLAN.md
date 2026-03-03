# TG-Agent 重构方案：FastGPT → LangChain + Milvus

## Context
将 TG-Agent 从 FastGPT 全托管 AI 服务迁移到自建 Go + Python 微服务架构。

## 技术选型
- **Python 框架**: FastAPI + uvicorn
- **向量库**: Milvus (Zilliz Cloud for production)
- **Embedding**: OpenAI text-embedding-3-small (1536维)
- **LLM**: Claude (主要) / OpenAI / 自定义模型
- **文档解析**: LangChain document_loaders
- **分块**: LangChain RecursiveCharacterTextSplitter
- **Go-Python 通信**: REST over HTTP/JSON

## 架构总览

```
Telegram 用户
      │
      ▼
┌──────────────────────────────────────────────┐
│  Go 主服务 (tg-agent) — fasthttp :8080       │
│  ├── handlers/telegram.go   (Bot 交互，保留)  │
│  ├── handlers/webhook.go    (Webhook，保留)   │
│  ├── handlers/api_handler.go(REST API，修改)  │
│  ├── services/ai_client.go  (新建: HTTP客户端) │
│  ├── services/kb_service.go (完全重写)        │
│  └── services/action_service.go (重构)       │
└──────────────────┬───────────────────────────┘
                   │ REST API (JSON) + X-API-Key 鉴权
                   ▼
┌──────────────────────────────────────────────┐
│  Python AI 服务 (ai-service) — FastAPI :8000  │
│  ├── ingestion/  (文档解析 → 分块 → 向量化)    │
│  ├── rag/        (检索 → 上下文组装 → LLM)     │
│  ├── core/       (LLM / Embedding 抽象层)     │
│  └── storage/    (Milvus 客户端)              │
└──────┬──────────────────┬────────────────────┘
       │                  │
       ▼                  ▼
┌──────────┐       ┌──────────────┐
│  Milvus  │       │  LLM API     │
│ Zilliz   │       │  Claude/Custom│
└──────────┘       └──────────────┘

PostgreSQL (业务数据，保持不变)
```

## Go ↔ Python 通信协议

### Chat 对话
```json
POST /api/v1/chat/completions
Request:
{
  "message": "用户消息",
  "agent_id": "agent-uuid",
  "dataset_ids": ["dataset-uuid-1"],
  "system_prompt": "You are {role_name}. Personality: {personality}...",
  "language": "English",
  "chat_type": "private",
  "llm_provider": "claude",
  "temperature": 0.7,
  "max_tokens": 4096
}

Response:
{
  "code": 0,
  "message": "success",
  "data": {
    "content": "AI 的回复内容",
    "sources": [{"chunk_text": "...", "source_name": "doc.pdf", "score": 0.92}],
    "usage": {"prompt_tokens": 1200, "completion_tokens": 350, "total_tokens": 1550}
  }
}
```

### 知识库操作
```
POST   /api/v1/datasets                   创建 dataset
GET    /api/v1/datasets/{id}              查询 dataset
DELETE /api/v1/datasets/{id}              删除 dataset
POST   /api/v1/collections/text           摄入文本
POST   /api/v1/collections/link           摄入链接
POST   /api/v1/collections/file           摄入文件 (multipart)
DELETE /api/v1/collections/{id}           删除 collection
GET    /api/v1/collections/{dataset_id}   列出 collections
GET    /health                            存活检查
GET    /readiness                         就绪检查
```

## Milvus Schema

单一 Collection `kb_vectors`，通过 `dataset_id` partition key 实现多 Agent 隔离：

| 字段 | 类型 | 说明 |
|------|------|------|
| id | VARCHAR(64) PK | UUID 主键 |
| dataset_id | VARCHAR(64) partition key | 映射 kb_datasets.id |
| collection_id | VARCHAR(64) | 映射 kb_collections.id |
| chunk_index | INT32 | 块序号 |
| text | VARCHAR(16384) | 文本内容 |
| source_type | VARCHAR(16) | text/link/file |
| source_name | VARCHAR(512) | 来源名称 |
| embedding | FLOAT_VECTOR(1536) | 向量 |
| created_at | INT64 | Unix 时间戳 |

索引: IVF_FLAT, COSINE, nlist=128

## PostgreSQL Schema 变更

### kb_datasets
```sql
ALTER TABLE kb_datasets DROP COLUMN ai_dataset_id;
ALTER TABLE kb_datasets DROP COLUMN vector_model;
ALTER TABLE kb_datasets DROP COLUMN agent_model;
ALTER TABLE kb_datasets ADD COLUMN embedding_model VARCHAR(64) DEFAULT 'text-embedding-3-small';
ALTER TABLE kb_datasets ADD COLUMN chunk_count INTEGER DEFAULT 0;
ALTER TABLE kb_datasets ADD COLUMN status VARCHAR(16) DEFAULT 'active';
```

### kb_collections
```sql
ALTER TABLE kb_collections ADD COLUMN chunk_count INTEGER DEFAULT 0;
ALTER TABLE kb_collections ADD COLUMN status VARCHAR(16) DEFAULT 'pending';
ALTER TABLE kb_collections ADD COLUMN error_message TEXT;
```

### agents.knowledges
```
之前: [{"datasetId": "fastgpt-external-id"}]
之后: ["local-dataset-uuid-1", "local-dataset-uuid-2"]
```

## Go 服务变更清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `config/config.go` | 替换 | FastGPTConfig → AIServiceConfig |
| `config.json` | 替换 | fastgpt 配置段 → ai_service |
| `services/ai_client.go` | 新建 | HTTP 客户端 (重试/超时/熔断) |
| `services/kb_service.go` | 完全重写 | FastGPT API → Python 服务 |
| `services/action_service.go` | 重大重构 | CallAction → Python chat API |
| `handlers/api_handler.go` | 修改 | Agent create + AgentTopic |
| `handlers/telegram.go` | 小改 | 传递 role context |
| `models/kb.go` | 修改 | 移除 AIDatasetID，新增 status |
| `models/agent.go` | 修改 | knowledges 简化 |
| `main.go` | 修改 | 移除 FastGPT 校验 |
| `workflow/` | 删除 | FastGPT 工作流 |

## 实施进度

### Phase 1: 基础搭建 ✅ 已完成
- [x] 技术方案文档
- [x] Go 配置层替换 (FastGPTConfig → AIServiceConfig)
- [x] Go AI HTTP 客户端 (ai_client.go)
- [x] Python FastAPI 服务骨架
- [x] 更新 main.go 和 Dockerfile

### Phase 2: Milvus + Embedding ✅ 已完成
- [x] Milvus 连接 + kb_vectors Schema (milvus_schema.py + milvus_client.py)
- [x] OpenAI Embedding 封装 (embedding.py, 支持单条/批量/重试)
- [x] 文本分块器 (chunker.py, LangChain RecursiveCharacterTextSplitter)
- [x] 文本摄入 Pipeline (pipeline.py: parse→chunk→embed→store)
- [x] knowledge.py 端点真实实现 (text摄入/dataset CRUD/collection 删除)
- [x] health.py 就绪检查 (Milvus + Embedding 连通性)
- [x] main.py lifespan 初始化 (Milvus 连接 + Embedding 验证)

### Phase 3: 完整文档摄入 ✅ 已完成
- [x] PDF/DOCX/MD 文件解析 (file_parser.py)
- [x] 网页链接爬取 (link_parser.py)
- [x] Go KB Service 重写 (kb_service.go → AIClient)
- [x] PostgreSQL Schema 迁移 (001_remove_fastgpt_fields.sql)

### Phase 4: RAG + Chat ✅ 已完成
- [x] Milvus 检索器 (rag/retriever.py)
- [x] 上下文构建器 (rag/context_builder.py)
- [x] LLM 提供商抽象 (core/llm_provider.py: Claude/OpenAI/Custom)
- [x] Chat 端点真实实现 (api/chat.py: RAG pipeline)
- [x] Go Action Service 重构 (action_service.go → Python AI 服务)

### Phase 5: 部署配置 ✅ 已完成
- [x] Python 依赖管理 (requirements.txt)
- [x] Python 服务 Dockerfile (ai-service/Dockerfile)
- [x] Docker Compose 编排 (Go + Python + Milvus + etcd + MinIO)
- [x] 环境配置 (.env.example, .env.ai-service.example)
- [x] 文档更新 (REFACTOR_PLAN.md)
