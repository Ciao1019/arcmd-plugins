//go:generate go run ./gen_bundle.go

package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type imageUploader interface {
	Name() string
	Test() error
	Upload(filename, contentType string, data []byte) (string, error)
}

func main() {
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read stdin: %v\n", err)
		os.Exit(1)
	}
	var req pluginRequest
	if len(in) > 0 {
		if err := json.Unmarshal(in, &req); err != nil {
			fmt.Fprintf(os.Stderr, "decode request: %v\n", err)
			os.Exit(1)
		}
	}
	resp, err := handleRequest(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	out, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode response: %v\n", err)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(out)
}

func handleRequest(req pluginRequest) (pluginResponse, error) {
	cfg, err := sdkGetConfig()
	if err != nil {
		return pluginResponse{}, err
	}

	switch req.Command {
	case "test-connection":
		uploader, err := uploaderFromConfig(cfg)
		if err != nil {
			return pluginResponse{
				Toast:  err.Error(),
				Status: "连接失败：" + err.Error(),
			}, nil
		}
		if err := uploader.Test(); err != nil {
			return pluginResponse{
				Toast:  "连接失败：" + err.Error(),
				Status: "连接失败：" + err.Error(),
			}, nil
		}
		msg := fmt.Sprintf("%s 连接成功。", uploader.Name())
		return pluginResponse{
			Toast:  msg,
			Status: msg,
		}, nil

	case "upload-image":
		uploader, err := uploaderFromConfig(cfg)
		if err != nil {
			return pluginResponse{}, err
		}
		b64 := strArg(req.Args, "dataBase64")
		if b64 == "" {
			return pluginResponse{}, errors.New("缺少图片数据")
		}
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return pluginResponse{}, fmt.Errorf("解码图片数据失败: %w", err)
		}
		filename := strArg(req.Args, "filename")
		contentType := strArg(req.Args, "contentType")
		uploadedURL, err := uploader.Upload(filename, contentType, data)
		if err != nil {
			return pluginResponse{}, err
		}
		return pluginResponse{
			Status: fmt.Sprintf("已上传至 %s", uploader.Name()),
			Data: map[string]any{
				"url": uploadedURL,
			},
		}, nil

	default:
		return pluginResponse{}, fmt.Errorf("unknown command %q", req.Command)
	}
}

func uploaderFromConfig(cfg map[string]any) (imageUploader, error) {
	provider := strings.ToLower(strings.TrimSpace(strCfg(cfg, "provider")))
	if provider == "" {
		provider = "s3"
	}
	switch provider {
	case "s3":
		return newS3Uploader(cfg)
	case "custom":
		return newCustomUploader(cfg)
	default:
		return nil, fmt.Errorf("未知图床服务 %q", provider)
	}
}

func strCfg(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	s, _ := cfg[key].(string)
	return s
}

func boolCfg(cfg map[string]any, key string) bool {
	if cfg == nil {
		return false
	}
	b, _ := cfg[key].(bool)
	return b
}

func strArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	s, _ := args[key].(string)
	return s
}
