package services

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"math"
	"mime/multipart"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/goccy/go-json"
)

// AIClient 与 Python AI 服务通信的 HTTP 客户端
type AIClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client

	// 熔断器状态
	failCount    int64
	lastFailTime int64 // unix timestamp
}

// AIResponse Python AI 服务的统一响应格式
type AIResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

const (
	maxRetries      = 3
	initialBackoff  = 1 * time.Second
	maxBackoff      = 10 * time.Second
	backoffMultiply = 2.0

	// 熔断器配置
	cbThreshold = 5                  // 连续失败次数阈值
	cbCooldown  = 30 * time.Second   // 熔断冷却时间
)

// NewAIClient 创建新的 AI 服务客户端
func NewAIClient(baseURL, apiKey string, timeoutSeconds int) *AIClient {
	return &AIClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: time.Duration(timeoutSeconds) * time.Second,
		},
	}
}

// NewAIClientWithTimeout 创建指定超时的客户端（用于不同端点使用不同超时）
func NewAIClientWithTimeout(baseURL, apiKey string, timeout time.Duration) *AIClient {
	return &AIClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// isCircuitOpen 检查熔断器是否打开
func (c *AIClient) isCircuitOpen() bool {
	fails := atomic.LoadInt64(&c.failCount)
	if fails < cbThreshold {
		return false
	}
	lastFail := atomic.LoadInt64(&c.lastFailTime)
	if time.Since(time.Unix(lastFail, 0)) > cbCooldown {
		// 冷却期过了，重置熔断器，允许尝试
		atomic.StoreInt64(&c.failCount, 0)
		return false
	}
	return true
}

// recordSuccess 记录成功
func (c *AIClient) recordSuccess() {
	atomic.StoreInt64(&c.failCount, 0)
}

// recordFailure 记录失败
func (c *AIClient) recordFailure() {
	atomic.AddInt64(&c.failCount, 1)
	atomic.StoreInt64(&c.lastFailTime, time.Now().Unix())
}

// isRetryable 判断是否可重试
func isRetryable(statusCode int) bool {
	return statusCode >= 500 || statusCode == 429
}

// Post 发送 JSON POST 请求到 AI 服务
func (c *AIClient) Post(path string, payload interface{}) (*AIResponse, error) {
	return c.doWithRetry("POST", path, payload, maxRetries)
}

// PostWithRetry 发送 JSON POST 请求，自定义重试次数
func (c *AIClient) PostWithRetry(path string, payload interface{}, retries int) (*AIResponse, error) {
	return c.doWithRetry("POST", path, payload, retries)
}

// Get 发送 GET 请求到 AI 服务
func (c *AIClient) Get(path string) (*AIResponse, error) {
	return c.doWithRetry("GET", path, nil, maxRetries)
}

// Delete 发送 DELETE 请求到 AI 服务
func (c *AIClient) Delete(path string) (*AIResponse, error) {
	return c.doWithRetry("DELETE", path, nil, maxRetries)
}

// PostMultipart 发送 multipart/form-data 请求（用于文件上传）
func (c *AIClient) PostMultipart(path string, fileField string, fileName string, fileData []byte, metadata map[string]string) (*AIResponse, error) {
	if c.isCircuitOpen() {
		return nil, fmt.Errorf("circuit breaker is open, AI service may be unavailable")
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(float64(initialBackoff) * math.Pow(backoffMultiply, float64(attempt-1)))
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			log.Printf("[AIClient] Retry attempt %d/%d after %v", attempt, maxRetries, backoff)
			time.Sleep(backoff)
		}

		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)

		// 添加文件
		part, err := writer.CreateFormFile(fileField, fileName)
		if err != nil {
			return nil, fmt.Errorf("error creating form file: %v", err)
		}
		if _, err := part.Write(fileData); err != nil {
			return nil, fmt.Errorf("error writing file data: %v", err)
		}

		// 添加元数据字段
		for key, value := range metadata {
			if err := writer.WriteField(key, value); err != nil {
				return nil, fmt.Errorf("error writing field %s: %v", key, err)
			}
		}

		if err := writer.Close(); err != nil {
			return nil, fmt.Errorf("error closing multipart writer: %v", err)
		}

		url := c.baseURL + path
		req, err := http.NewRequest("POST", url, body)
		if err != nil {
			return nil, fmt.Errorf("error creating request: %v", err)
		}

		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("X-API-Key", c.apiKey)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request error: %v", err)
			c.recordFailure()
			continue
		}

		result, err := c.parseResponse(resp)
		if err != nil {
			lastErr = err
			if resp.StatusCode >= 500 {
				c.recordFailure()
				continue
			}
			return nil, err
		}

		c.recordSuccess()
		return result, nil
	}

	return nil, fmt.Errorf("request failed after %d attempts: %v", maxRetries+1, lastErr)
}

// doWithRetry 执行带重试的 HTTP 请求
func (c *AIClient) doWithRetry(method, path string, payload interface{}, retries int) (*AIResponse, error) {
	if c.isCircuitOpen() {
		return nil, fmt.Errorf("circuit breaker is open, AI service may be unavailable")
	}

	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(float64(initialBackoff) * math.Pow(backoffMultiply, float64(attempt-1)))
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			log.Printf("[AIClient] Retry %s %s attempt %d/%d after %v", method, path, attempt, retries, backoff)
			time.Sleep(backoff)
		}

		result, statusCode, err := c.doRequest(method, path, payload)
		if err != nil {
			lastErr = err
			c.recordFailure()
			// 网络错误，可重试
			continue
		}

		if statusCode >= 200 && statusCode < 300 {
			c.recordSuccess()
			return result, nil
		}

		// 不可重试的状态码，直接返回
		if !isRetryable(statusCode) {
			return result, fmt.Errorf("AI service returned status %d: %s", statusCode, result.Message)
		}

		// 可重试的状态码
		lastErr = fmt.Errorf("AI service returned status %d: %s", statusCode, result.Message)
		c.recordFailure()
	}

	return nil, fmt.Errorf("request failed after %d attempts: %v", retries+1, lastErr)
}

// doRequest 执行单次 HTTP 请求
func (c *AIClient) doRequest(method, path string, payload interface{}) (*AIResponse, int, error) {
	url := c.baseURL + path

	var bodyReader io.Reader
	if payload != nil {
		jsonData, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, fmt.Errorf("error marshaling payload: %v", err)
		}
		bodyReader = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("error creating request: %v", err)
	}

	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request error: %v", err)
	}

	result, err := c.parseResponse(resp)
	if err != nil {
		return nil, resp.StatusCode, err
	}

	return result, resp.StatusCode, nil
}

// parseResponse 解析 HTTP 响应
func (c *AIClient) parseResponse(resp *http.Response) (*AIResponse, error) {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %v", err)
	}

	var result AIResponse
	if err := json.Unmarshal(body, &result); err != nil {
		// 如果无法解析为标准格式，构造一个错误响应
		return &AIResponse{
			Code:    resp.StatusCode,
			Message: string(body),
		}, nil
	}

	return &result, nil
}

// HealthCheck 检查 AI 服务健康状态
func (c *AIClient) HealthCheck() error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(c.baseURL + "/health")
	if err != nil {
		return fmt.Errorf("AI service health check failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("AI service unhealthy, status: %d", resp.StatusCode)
	}
	return nil
}
