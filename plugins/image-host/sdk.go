package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

type pluginRequest struct {
	Command     string         `json:"command,omitempty"`
	Panel       string         `json:"panel,omitempty"`
	Args        map[string]any `json:"args,omitempty"`
	ActiveSpace string         `json:"activeSpace,omitempty"`
}

type pluginResponse struct {
	Markdown string         `json:"markdown,omitempty"`
	Toast    string         `json:"toast,omitempty"`
	Status   string         `json:"status,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

type hostRequest struct {
	Op                string            `json:"op"`
	Key               string            `json:"key,omitempty"`
	Method            string            `json:"method,omitempty"`
	URL               string            `json:"url,omitempty"`
	Headers           map[string]string `json:"headers,omitempty"`
	AllowInsecureHTTP bool              `json:"allowInsecureHttp,omitempty"`
}

type hostReply struct {
	OK      bool              `json:"ok"`
	Error   string            `json:"error,omitempty"`
	Code    string            `json:"code,omitempty"`
	Status  int               `json:"status,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Value   string            `json:"value,omitempty"`
	Config  map[string]any    `json:"config,omitempty"`
}

type hostError struct {
	Code    string
	Message string
}

func (e *hostError) Error() string { return e.Message }

func callHost(req hostRequest, body []byte) (hostReply, []byte, error) {
	hdr, err := json.Marshal(req)
	if err != nil {
		return hostReply{}, nil, err
	}
	frame := make([]byte, 4+len(hdr)+len(body))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(hdr)))
	copy(frame[4:], hdr)
	copy(frame[4+len(hdr):], body)

	rawReply, err := exchangeHostFrame(frame)
	if err != nil {
		return hostReply{}, nil, err
	}
	if len(rawReply) < 4 {
		return hostReply{}, nil, errors.New("truncated host reply")
	}
	rHdrLen := int(binary.BigEndian.Uint32(rawReply[:4]))
	if rHdrLen < 0 || 4+rHdrLen > len(rawReply) {
		return hostReply{}, nil, errors.New("invalid host reply header length")
	}
	var rep hostReply
	if err := json.Unmarshal(rawReply[4:4+rHdrLen], &rep); err != nil {
		return hostReply{}, nil, err
	}
	if !rep.OK {
		return rep, nil, &hostError{Code: rep.Code, Message: rep.Error}
	}
	return rep, rawReply[4+rHdrLen:], nil
}

func sdkGetConfig() (map[string]any, error) {
	rep, _, err := callHost(hostRequest{Op: "config.get"}, nil)
	if err != nil {
		return nil, err
	}
	return rep.Config, nil
}

func sdkGetSecret(key string) (string, error) {
	rep, _, err := callHost(hostRequest{Op: "secrets.get", Key: key}, nil)
	if err != nil {
		return "", err
	}
	return rep.Value, nil
}

type httpResponse struct {
	Status  int
	Headers map[string]string
	Body    []byte
}

func sdkDoHTTP(method, rawURL string, headers map[string]string, body []byte, allowInsecure bool) (httpResponse, error) {
	rep, respBody, err := callHost(hostRequest{
		Op:                "http.request",
		Method:            method,
		URL:               rawURL,
		Headers:           headers,
		AllowInsecureHTTP: allowInsecure,
	}, body)
	if err != nil {
		return httpResponse{}, fmt.Errorf("%s %s: %w", method, rawURL, err)
	}
	return httpResponse{
		Status:  rep.Status,
		Headers: rep.Headers,
		Body:    respBody,
	}, nil
}
