package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strconv"
	"strings"
)

type customUploader struct {
	endpoint      string
	field         string
	authHeader    string
	jsonPath      string
	allowInsecure bool
}

func newCustomUploader(cfg map[string]any) (*customUploader, error) {
	endpoint := strings.TrimSpace(strCfg(cfg, "customURL"))
	if endpoint == "" {
		return nil, errors.New("请填写图床上传接口地址")
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return nil, fmt.Errorf("无效的上传接口地址: %q", endpoint)
	}
	field := strings.TrimSpace(strCfg(cfg, "customField"))
	if field == "" {
		field = "file"
	}
	jsonPath := strings.TrimSpace(strCfg(cfg, "customJSONPath"))
	if jsonPath == "" {
		jsonPath = "url"
	}
	authHeader, _ := sdkGetSecret("customAuthHeader")
	authHeader = strings.TrimSpace(authHeader)
	return &customUploader{
		endpoint:      endpoint,
		field:         field,
		authHeader:    authHeader,
		jsonPath:      jsonPath,
		allowInsecure: boolCfg(cfg, "customAllowInsecure") || boolCfg(cfg, "allowInsecureHTTP"),
	}, nil
}

func (c *customUploader) Name() string { return "自定义 HTTP 图床" }

func (c *customUploader) Test() error {
	headers := map[string]string{}
	if c.authHeader != "" {
		headers["Authorization"] = c.authHeader
	}
	resp, err := sdkDoHTTP(http.MethodHead, c.endpoint, headers, nil, c.allowInsecure)
	if err != nil {
		return err
	}
	if resp.Status == http.StatusUnauthorized || resp.Status == http.StatusForbidden {
		return fmt.Errorf("认证失败（HTTP %d）", resp.Status)
	}
	if resp.Status >= 500 {
		return fmt.Errorf("图床服务返回错误 HTTP %d", resp.Status)
	}
	return nil
}

func (c *customUploader) Upload(filename, contentType string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("图片内容为空")
	}
	base := path.Base(strings.ReplaceAll(strings.TrimSpace(filename), `\`, "/"))
	if base == "" || base == "." || base == "/" {
		base = "image.png"
	}
	if contentType == "" {
		contentType = mimeForPath(base)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, escapeQuotes(c.field), escapeQuotes(base)))
	h.Set("Content-Type", contentType)
	part, err := mw.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	headers := map[string]string{
		"Content-Type": mw.FormDataContentType(),
	}
	if c.authHeader != "" {
		headers["Authorization"] = c.authHeader
	}
	resp, err := sdkDoHTTP(http.MethodPost, c.endpoint, headers, body.Bytes(), c.allowInsecure)
	if err != nil {
		return "", err
	}
	if resp.Status >= 400 {
		msg := strings.TrimSpace(string(resp.Body))
		if msg != "" {
			if len(msg) > 200 {
				msg = msg[:200]
			}
			return "", fmt.Errorf("HTTP %d: %s", resp.Status, msg)
		}
		return "", fmt.Errorf("HTTP %d", resp.Status)
	}

	rawURL, err := extractUploadedURL(resp.Body, c.jsonPath)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return "", fmt.Errorf("图床返回的 URL 无效: %q", rawURL)
	}
	return rawURL, nil
}

var quoteEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

func escapeQuotes(s string) string {
	return quoteEscaper.Replace(s)
}

func extractUploadedURL(body []byte, jsonPath string) (string, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "", errors.New("图床响应为空")
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return trimmed, nil
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("解析图床 JSON 响应失败: %w", err)
	}
	cur := payload
	for seg := range strings.SplitSeq(jsonPath, ".") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[seg]
			if !ok {
				return "", fmt.Errorf("响应 JSON 缺少字段 %q（路径 %q）", seg, jsonPath)
			}
			cur = next
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return "", fmt.Errorf("响应 JSON 无法索引 %q（路径 %q）", seg, jsonPath)
			}
			cur = node[idx]
		default:
			return "", fmt.Errorf("响应 JSON 路径 %q 无法继续取值", jsonPath)
		}
	}
	s, ok := cur.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("响应 JSON 路径 %q 不是有效 URL 字符串", jsonPath)
	}
	return strings.TrimSpace(s), nil
}
