package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"Qavor/internal/model/dto/request"
	"Qavor/pkg/errors"
	pkgllm "Qavor/pkg/llm"
)

// defaultRemoteModelsTimeout 远程拉取模型列表的请求超时。
const defaultRemoteModelsTimeout = 15 * time.Second

// FetchRemoteModels 远程拉取供应商的模型列表，供模型配置弹窗选择。
// 按协议分派：ollama 走 /api/tags，其余按 OpenAI 兼容 /models 处理，统一返回模型名列表。
func (s *modelService) FetchRemoteModels(ctx context.Context, req *request.FetchRemoteModelsRequest) ([]string, error) {
	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL == "" {
		return nil, errors.New(errors.CodeInvalidParam, "请填写 Base URL")
	}
	protocol := strings.TrimSpace(req.Protocol)
	if protocol == "" {
		protocol = "openai"
	}

	ctx, cancel := context.WithTimeout(ctx, defaultRemoteModelsTimeout)
	defer cancel()

	apiKey := s.resolveRemoteModelsKey(req)

	var (
		models []string
		err    error
	)
	if protocol == "ollama" {
		models, err = fetchOllamaModels(ctx, baseURL)
	} else {
		models, err = fetchOpenAICompatibleModels(ctx, baseURL, apiKey)
	}
	if err != nil {
		// 空列表等业务错误直接返回友好提示
		if errors.IsBizError(err) {
			return nil, err
		}
		// 复用公共错误分类：超时/连接失败/认证失败等转友好提示，detail 脱敏原始错误
		classified := pkgllm.ClassifyError(err)
		return nil, errors.NewWithDetail(errors.CodeLLMRequestFailed, classified.Friendly, redactConnectionTestError(err, apiKey))
	}
	if len(models) == 0 {
		return nil, errors.New(errors.CodeLLMResponseInvalid, "未获取到模型列表，请确认该服务已部署模型")
	}
	return models, nil
}

// resolveRemoteModelsKey 解析拉取列表使用的 API Key。
// 优先使用请求携带的 Key；编辑已有配置时用户通常不会重新粘贴密钥，此时回退到已保存配置的解密 Key。
// 解析失败按匿名请求继续（上游会返回 401，前端仍能给出明确提示），不阻塞流程。
func (s *modelService) resolveRemoteModelsKey(req *request.FetchRemoteModelsRequest) string {
	if key := strings.TrimSpace(req.APIKey); key != "" {
		return key
	}
	if req.ModelID == 0 {
		return ""
	}
	saved, err := s.GetModelWithDecryptedKey(req.ModelID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(saved.APIKey)
}

// fetchOpenAICompatibleModels 从 OpenAI 兼容的 /models 接口拉取模型列表。
// 部分厂商（如 DeepSeek）BaseURL 不带 /v1 后缀：先试 {base}/models，请求失败再回退 {base}/v1/models。
func fetchOpenAICompatibleModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	trimmed := strings.TrimRight(baseURL, "/")
	urls := []string{trimmed + "/models"}
	if !strings.HasSuffix(trimmed, "/v1") {
		urls = append(urls, trimmed+"/v1/models")
	}

	var lastErr error
	for _, url := range urls {
		var payload struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := getJSON(ctx, url, apiKey, &payload); err != nil {
			lastErr = err
			continue
		}
		names := make([]string, 0, len(payload.Data))
		for _, m := range payload.Data {
			if id := strings.TrimSpace(m.ID); id != "" {
				names = append(names, id)
			}
		}
		return names, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("模型列表响应为空")
	}
	return nil, lastErr
}

// fetchOllamaModels 从 Ollama 的 /api/tags 接口拉取本地模型列表。
func fetchOllamaModels(ctx context.Context, baseURL string) ([]string, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/tags"
	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := getJSON(ctx, url, "", &payload); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(payload.Models))
	for _, m := range payload.Models {
		if name := strings.TrimSpace(m.Name); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// getJSON 发送 GET 请求并解析 JSON 响应。apiKey 非空时附带 Authorization: Bearer 头。
// 超时由 ctx 控制（调用方已设置 defaultRemoteModelsTimeout）。
func getJSON(ctx context.Context, url, apiKey string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s 返回 HTTP %d: %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
