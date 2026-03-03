package models

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
)

// KBCollection 知识库集合
type KBCollection struct {
	ID           string    `json:"id"`
	DatasetID    string    `json:"dataset_id"`
	CollectionID string    `json:"collection_id"`
	Name         string    `json:"name"`
	Type         string    `json:"type"` // text, link, file
	Content      string    `json:"content"`
	SourceName   string    `json:"source_name,omitempty"`
	ChunkCount   int       `json:"chunk_count"`
	Status       string    `json:"status"` // pending, ready, error
	ErrorMessage string    `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	DeletedAt    time.Time `json:"deleted_at,omitempty"`
}

// KBDataset 知识库数据集
type KBDataset struct {
	ID             string    `json:"id"`
	AgentID        string    `json:"agent_id"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	EmbeddingModel string    `json:"embedding_model"`
	ChunkCount     int       `json:"chunk_count"`
	Status         string    `json:"status"` // active, deleted
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	DeletedAt      time.Time `json:"deleted_at,omitempty"`
}

// KBCollectionRequest 知识库集合创建请求
type KBCollectionRequest struct {
	DatasetID    string                 `json:"dataset_id"`
	Name         string                 `json:"name"`
	ChunkSize    int                    `json:"chunk_size"`
	ChunkOverlap int                    `json:"chunk_overlap"`
	Metadata     map[string]interface{} `json:"metadata"`
}

// KBState 知识库操作状态
type KBState struct {
	ChatID    int64     `json:"chat_id"`
	Command   string    `json:"command"` // add, delete, list
	Step      int       `json:"step"`    // 操作步骤
	DatasetID string    `json:"dataset_id"`
	CreatedAt time.Time `json:"created_at"`
}

// KBDatasetResponse 知识库数据集响应
type KBDatasetResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// KBCollectionResponse 知识库集合响应
type KBCollectionResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateKBCollection 创建知识库集合记录
func CreateKBCollection(ctx context.Context, collection *KBCollection) error {
	_, err := DB().Exec(ctx, `
		INSERT INTO kb_collections (
			dataset_id, collection_id, name, type,
			content, source_name, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		)`,
		collection.DatasetID,
		collection.CollectionID,
		collection.Name,
		collection.Type,
		collection.Content,
		collection.SourceName,
	)
	return err
}

// DeleteKBCollection 删除知识库集合记录
func DeleteKBCollection(ctx context.Context, id string) error {
	_, err := DB().Exec(ctx, `
		UPDATE kb_collections
		SET deleted_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`, id)
	return err
}

// GetKBCollectionsWithCondition 通用查询方法
func GetKBCollectionsWithCondition(ctx context.Context, datasetID string, whereClause string) ([]KBCollection, error) {
	rows, err := DB().Query(ctx, `
		SELECT id, dataset_id, name, type, content, source_name, created_at, updated_at
		FROM kb_collections
		WHERE dataset_id = $1
		`+whereClause+`
		ORDER BY created_at DESC
	`, datasetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var collections []KBCollection
	for rows.Next() {
		var collection KBCollection
		err := rows.Scan(
			&collection.ID,
			&collection.DatasetID,
			&collection.Name,
			&collection.Type,
			&collection.Content,
			&collection.SourceName,
			&collection.CreatedAt,
			&collection.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		collections = append(collections, collection)
	}
	return collections, nil
}

// GetKBDataset 获取知识库数据集
func GetKBDataset(ctx context.Context, datasetID string) (*KBDataset, error) {
	var dataset KBDataset
	err := DB().QueryRow(ctx, `
		SELECT id, name, description, embedding_model, chunk_count, status, created_at, updated_at
		FROM kb_datasets
		WHERE id = $1
		AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT 1
	`, datasetID).Scan(
		&dataset.ID,
		&dataset.Name,
		&dataset.Description,
		&dataset.EmbeddingModel,
		&dataset.ChunkCount,
		&dataset.Status,
		&dataset.CreatedAt,
		&dataset.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &dataset, nil
}

// CreateKBDataset 创建知识库数据集并返回ID
func CreateKBDataset(ctx context.Context, dataset *KBDataset) (string, error) {
	var id string
	err := DB().QueryRow(ctx, `
		INSERT INTO kb_datasets (
			name, description, embedding_model,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		) RETURNING id
	`, dataset.Name, dataset.Description,
		dataset.EmbeddingModel).Scan(&id)

	if err != nil {
		return "", err
	}
	return id, nil
}

// UpdateKBDataset 更新知识库数据集
func UpdateKBDataset(ctx context.Context, dataset *KBDataset) error {
	_, err := DB().Exec(ctx, `
		UPDATE kb_datasets
		SET name = $1, description = $2, embedding_model = $3,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $4
	`, dataset.Name, dataset.Description, dataset.EmbeddingModel, dataset.ID)
	return err
}

// DeleteKBCollectionTx 在事务中删除知识库集合
func DeleteKBCollectionTx(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `
		UPDATE kb_collections
		SET deleted_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`, id)
	return err
}

// CreateKBCollectionTx 在事务中创建知识库集合
func CreateKBCollectionTx(ctx context.Context, tx pgx.Tx, collection *KBCollection) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO kb_collections (
			dataset_id, collection_id, name, type,
			content, source_name, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		)`,
		collection.DatasetID,
		collection.CollectionID,
		collection.Name,
		collection.Type,
		collection.Content,
		collection.SourceName,
	)
	return err
}

// GetKBCollections 获取知识库集合，按创建时间倒序排列
func GetKBCollections(ctx context.Context, datasetID string) ([]KBCollection, error) {
	rows, err := DB().Query(ctx, `
		SELECT id, dataset_id, collection_id, name, type, content, source_name, created_at
		FROM kb_collections
		WHERE dataset_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
	`, datasetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var collections []KBCollection
	for rows.Next() {
		var col KBCollection
		err := rows.Scan(
			&col.ID,
			&col.DatasetID,
			&col.CollectionID,
			&col.Name,
			&col.Type,
			&col.Content,
			&col.SourceName,
			&col.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		collections = append(collections, col)
	}
	return collections, nil
}

// DeleteKBCollectionByCollectionID 通过 CollectionID 删除记录
func DeleteKBCollectionByCollectionID(ctx context.Context, collectionID string) error {
	_, err := DB().Exec(ctx, `
		UPDATE kb_collections
		SET deleted_at = CURRENT_TIMESTAMP
		WHERE collection_id = $1
	`, collectionID)
	return err
}

// GetKBDatasetByID 通过ID获取知识库数据集
func GetKBDatasetByID(ctx context.Context, id string) (*KBDataset, error) {
	log.Printf("[DB] Getting dataset by ID: %s", id)

	var dataset KBDataset
	err := DB().QueryRow(ctx, `
		SELECT id, name, description, embedding_model, chunk_count, status, created_at
		FROM kb_datasets
		WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(
		&dataset.ID,
		&dataset.Name,
		&dataset.Description,
		&dataset.EmbeddingModel,
		&dataset.ChunkCount,
		&dataset.Status,
		&dataset.CreatedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			log.Printf("[DB] No dataset found for ID: %s", id)
			return nil, fmt.Errorf("dataset not found: %s", id)
		}
		log.Printf("[DB] Error getting dataset: %v", err)
		return nil, fmt.Errorf("error querying dataset: %v", err)
	}

	log.Printf("[DB] Found dataset: %+v", dataset)
	return &dataset, nil
}
