package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tom-Jerry/TGAgent/models"
	"github.com/google/uuid"
)

// KBService 知识库服务 — 通过 AIClient 与 Python AI 服务通信
type KBService struct {
	client *AIClient
}

// 错误类型
var (
	ErrInvalidFileSize = fmt.Errorf("file size exceeds limit")
	ErrInvalidFileType = fmt.Errorf("invalid file type")
	ErrInvalidURL      = fmt.Errorf("invalid URL")
)

const maxFileSize = 10 * 1024 * 1024 // 10MB

// NewKBService 创建知识库服务实例
func NewKBService(baseURL, apiKey string) *KBService {
	return &KBService{
		client: NewAIClientWithTimeout(baseURL, apiKey, 120*time.Second),
	}
}

// CreateDataset 创建知识库数据集
func (s *KBService) CreateDataset(name string) (string, error) {
	datasetUUID := uuid.New().String()

	// 注册到 Python AI 服务（Milvus 使用 partition key 自动分区）
	payload := map[string]interface{}{
		"dataset_id": datasetUUID,
		"name":       name,
	}

	resp, err := s.client.Post("/api/v1/datasets", payload)
	if err != nil {
		return "", fmt.Errorf("error creating dataset in AI service: %v", err)
	}
	if resp.Code != 0 {
		return "", fmt.Errorf("AI service error: %s", resp.Message)
	}

	// 创建本地数据库记录
	dataset := &models.KBDataset{
		Name:           name,
		Description:    "Agent Knowledge Base",
		EmbeddingModel: "text-embedding-3-small",
	}
	id, err := models.CreateKBDataset(context.Background(), dataset)
	if err != nil {
		return "", fmt.Errorf("failed to create local dataset record: %v", err)
	}

	log.Printf("[KBService] Created dataset: localID=%s, datasetUUID=%s", id, datasetUUID)
	return id, nil
}

// AddTextCollection 通过 Python AI 服务摄入文本内容
func (s *KBService) AddTextCollection(aiDatasetID string, localDatasetID string, text, name string) error {
	log.Printf("[KBService] Adding text collection: datasetID=%s, name=%s, textLen=%d",
		aiDatasetID, name, len(text))

	collectionID := uuid.New().String()

	payload := map[string]interface{}{
		"dataset_id":    aiDatasetID,
		"collection_id": collectionID,
		"name":          name,
		"text":          text,
		"chunk_size":    512,
		"chunk_overlap": 50,
	}

	resp, err := s.client.Post("/api/v1/collections/text", payload)
	if err != nil {
		return fmt.Errorf("error adding text collection: %v", err)
	}
	if resp.Code != 0 {
		return fmt.Errorf("AI service error: %s", resp.Message)
	}

	// 解析响应
	var result struct {
		CollectionID string `json:"collection_id"`
		ChunkCount   int    `json:"chunk_count"`
		Status       string `json:"status"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		log.Printf("[KBService] Warning: could not parse response data: %v", err)
	}

	log.Printf("[KBService] Text ingestion complete: collectionID=%s, chunks=%d",
		collectionID, result.ChunkCount)

	// 创建本地数据库记录
	collection := models.KBCollection{
		DatasetID:    localDatasetID,
		CollectionID: collectionID,
		Name:         name,
		Type:         "text",
		Content:      text,
		SourceName:   name,
	}

	ctx := context.Background()
	tx, err := models.DB().Begin(ctx)
	if err != nil {
		return fmt.Errorf("transaction begin failed: %v", err)
	}
	defer tx.Rollback(ctx)

	if err := models.CreateKBCollectionTx(ctx, tx, &collection); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// AddLinkCollection 通过 Python AI 服务摄入网页链接
func (s *KBService) AddLinkCollection(aiDatasetID string, localDatasetID string, link string) error {
	log.Printf("[KBService] Adding link collection: datasetID=%s, link=%s", aiDatasetID, link)

	if err := s.validateURL(link); err != nil {
		log.Printf("[KBService] URL validation failed: %v", err)
		return err
	}

	collectionID := uuid.New().String()

	payload := map[string]interface{}{
		"dataset_id":    aiDatasetID,
		"collection_id": collectionID,
		"url":           link,
		"chunk_size":    512,
		"chunk_overlap": 50,
	}

	resp, err := s.client.Post("/api/v1/collections/link", payload)
	if err != nil {
		return fmt.Errorf("error adding link collection: %v", err)
	}
	if resp.Code != 0 {
		return fmt.Errorf("AI service error: %s", resp.Message)
	}

	var result struct {
		CollectionID string `json:"collection_id"`
		ChunkCount   int    `json:"chunk_count"`
		Status       string `json:"status"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		log.Printf("[KBService] Warning: could not parse response data: %v", err)
	}

	log.Printf("[KBService] Link ingestion complete: collectionID=%s, chunks=%d",
		collectionID, result.ChunkCount)

	// 创建本地数据库记录
	collection := models.KBCollection{
		DatasetID:    localDatasetID,
		CollectionID: collectionID,
		Name:         collectionID,
		Type:         "link",
		Content:      link,
		SourceName:   link,
	}

	ctx := context.Background()
	tx, err := models.DB().Begin(ctx)
	if err != nil {
		return fmt.Errorf("transaction begin failed: %v", err)
	}
	defer tx.Rollback(ctx)

	if err := models.CreateKBCollectionTx(ctx, tx, &collection); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// AddFileCollection 通过 Python AI 服务摄入文件
func (s *KBService) AddFileCollection(aiDatasetID string, localDatasetID string, file []byte, filename string) error {
	log.Printf("[KBService] Adding file collection: datasetID=%s, filename=%s, size=%d",
		aiDatasetID, filename, len(file))

	if len(file) > maxFileSize {
		log.Printf("[KBService] File size exceeds limit: %d > %d", len(file), maxFileSize)
		return fmt.Errorf("%w: %d bytes", ErrInvalidFileSize, len(file))
	}

	if err := s.validateFileType(filename); err != nil {
		log.Printf("[KBService] File type validation failed: %v", err)
		return err
	}

	collectionID := uuid.New().String()

	// 构建 metadata JSON（Python 端 Form 字段）
	metaJSON, err := json.Marshal(map[string]interface{}{
		"dataset_id":    aiDatasetID,
		"collection_id": collectionID,
		"chunk_size":    512,
		"chunk_overlap": 50,
	})
	if err != nil {
		return fmt.Errorf("error marshaling metadata: %v", err)
	}

	resp, err := s.client.PostMultipart(
		"/api/v1/collections/file",
		"file",
		filename,
		file,
		map[string]string{"metadata": string(metaJSON)},
	)
	if err != nil {
		return fmt.Errorf("error adding file collection: %v", err)
	}
	if resp.Code != 0 {
		return fmt.Errorf("AI service error: %s", resp.Message)
	}

	var result struct {
		CollectionID string `json:"collection_id"`
		ChunkCount   int    `json:"chunk_count"`
		Status       string `json:"status"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		log.Printf("[KBService] Warning: could not parse response data: %v", err)
	}

	log.Printf("[KBService] File ingestion complete: collectionID=%s, chunks=%d",
		collectionID, result.ChunkCount)

	// 创建本地数据库记录
	collection := models.KBCollection{
		DatasetID:    localDatasetID,
		CollectionID: collectionID,
		Name:         filename,
		Type:         "file",
		Content:      fmt.Sprintf("[file: %s, %d bytes]", filename, len(file)),
		SourceName:   filename,
	}

	ctx := context.Background()
	tx, err := models.DB().Begin(ctx)
	if err != nil {
		return fmt.Errorf("transaction begin failed: %v", err)
	}
	defer tx.Rollback(ctx)

	if err := models.CreateKBCollectionTx(ctx, tx, &collection); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// DeleteCollection 删除集合（从 Milvus + 本地数据库）
func (s *KBService) DeleteCollection(collectionID string) error {
	log.Printf("[KBService] Deleting collection: %s", collectionID)

	// 从 Milvus 删除向量
	resp, err := s.client.Delete(fmt.Sprintf("/api/v1/collections/%s", collectionID))
	if err != nil {
		return fmt.Errorf("error deleting collection from AI service: %v", err)
	}
	if resp.Code != 0 {
		return fmt.Errorf("AI service error: %s", resp.Message)
	}

	// 删除本地数据库记录
	if err := models.DeleteKBCollectionByCollectionID(context.Background(), collectionID); err != nil {
		return fmt.Errorf("error deleting local record: %v", err)
	}

	log.Printf("[KBService] Collection deleted: %s", collectionID)
	return nil
}

// BindDatasetToAgent 绑定知识库到 Agent
func (s *KBService) BindDatasetToAgent(agentID, datasetID string) error {
	ctx := context.Background()
	tx, err := models.DB().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var agent models.Agent
	err = tx.QueryRow(ctx, `
		SELECT id, knowledges
		FROM agents
		WHERE id = $1 AND deleted_at IS NULL
	`, agentID).Scan(&agent.ID, &agent.Knowledges)
	if err != nil {
		return err
	}

	// 检查是否已绑定
	for _, kb := range agent.Knowledges {
		if kb == datasetID {
			return fmt.Errorf("knowledge base already bound")
		}
	}

	agent.Knowledges = append(agent.Knowledges, datasetID)

	_, err = tx.Exec(ctx, `
		UPDATE agents
		SET knowledges = $1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2
	`, agent.Knowledges, agent.ID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// UnbindDatasetFromAgent 从 Agent 解绑知识库
func (s *KBService) UnbindDatasetFromAgent(agentID, datasetID string) error {
	ctx := context.Background()
	tx, err := models.DB().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var agent models.Agent
	err = tx.QueryRow(ctx, `
		SELECT id, knowledges
		FROM agents
		WHERE id = $1 AND deleted_at IS NULL
	`, agentID).Scan(&agent.ID, &agent.Knowledges)
	if err != nil {
		return err
	}

	newKnowledges := make([]string, 0)
	for _, kb := range agent.Knowledges {
		if kb != datasetID {
			newKnowledges = append(newKnowledges, kb)
		}
	}
	agent.Knowledges = newKnowledges

	_, err = tx.Exec(ctx, `
		UPDATE agents
		SET knowledges = $1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2
	`, agent.Knowledges, agent.ID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// validateFileType 校验文件类型
func (s *KBService) validateFileType(filename string) error {
	allowedExt := map[string]struct{}{
		".txt":  {},
		".pdf":  {},
		".doc":  {},
		".docx": {},
		".md":   {},
	}

	ext := strings.ToLower(filepath.Ext(filename))
	if _, ok := allowedExt[ext]; !ok {
		return fmt.Errorf("%w: 仅支持 PDF、Word、TXT、Markdown 格式", ErrInvalidFileType)
	}
	return nil
}

// validateURL 校验 URL 格式
func (s *KBService) validateURL(urlStr string) error {
	_, err := url.ParseRequestURI(urlStr)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	return nil
}
