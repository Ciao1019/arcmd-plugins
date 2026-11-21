package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

func decodeTestFrame(frame []byte, hdrOut any) ([]byte, error) {
	hdrLen := int(binary.BigEndian.Uint32(frame[:4]))
	if err := json.Unmarshal(frame[4:4+hdrLen], hdrOut); err != nil {
		return nil, err
	}
	return frame[4+hdrLen:], nil
}

func encodeTestFrame(hdr any, body []byte) ([]byte, error) {
	b, err := json.Marshal(hdr)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 4+len(b)+len(body))
	binary.BigEndian.PutUint32(out[:4], uint32(len(b)))
	copy(out[4:], b)
	copy(out[4+len(b):], body)
	return out, nil
}

func TestS3UploaderSigV4AndPublicURL(t *testing.T) {
	var gotMethod, gotURL string
	var gotHeaders map[string]string
	var gotBody []byte

	setNativeHostBridge(func(reqFrame []byte) ([]byte, error) {
		var req hostRequest
		body, err := decodeTestFrame(reqFrame, &req)
		if err != nil {
			t.Fatal(err)
		}
		switch req.Op {
		case "secrets.get":
			return encodeTestFrame(hostReply{OK: true, Value: "SECRET"}, nil)
		case "http.request":
			gotMethod = req.Method
			gotURL = req.URL
			gotHeaders = req.Headers
			gotBody = append([]byte(nil), body...)
			return encodeTestFrame(hostReply{OK: true, Status: 200}, nil)
		default:
			t.Fatalf("unexpected op %q", req.Op)
			return nil, nil
		}
	})
	defer setNativeHostBridge(nil)

	up, err := newS3Uploader(map[string]any{
		"s3Endpoint":      "https://r2.example.com",
		"s3Region":        "auto",
		"s3Bucket":        "media",
		"s3AccessKey":     "AKIA",
		"s3Prefix":        "images/{year}/{month}",
		"s3PublicBaseURL": "https://cdn.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	up.now = func() time.Time { return time.Date(2026, 11, 22, 6, 30, 0, 0, time.UTC) }

	payload := []byte("fake-png-bytes")
	gotPublic, err := up.Upload("My Shot.PNG", "", payload)
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPut {
		t.Fatalf("method = %q, want PUT", gotMethod)
	}
	if !strings.HasPrefix(gotURL, "https://media.r2.example.com/images/2026/11/My-Shot-") || !strings.HasSuffix(gotURL, ".png") {
		t.Fatalf("unexpected S3 PUT URL: %q", gotURL)
	}
	if !strings.HasPrefix(gotPublic, "https://cdn.example.com/images/2026/11/My-Shot-") || !strings.HasSuffix(gotPublic, ".png") {
		t.Fatalf("unexpected public URL: %q", gotPublic)
	}
	if gotHeaders["Content-Type"] != "image/png" {
		t.Fatalf("Content-Type = %q", gotHeaders["Content-Type"])
	}
	if !strings.Contains(gotHeaders["Authorization"], "Credential=AKIA/20261122/auto/s3/aws4_request") {
		t.Fatalf("bad Authorization = %q", gotHeaders["Authorization"])
	}
	if !bytes.Equal(gotBody, payload) {
		t.Fatalf("uploaded body = %q", gotBody)
	}

	// Without s3PublicBaseURL and with pathStyle=true, URL uses endpoint/bucket/key.
	up.publicBaseURL = ""
	up.pathStyle = true
	gotPathURL, err := up.Upload("shot.jpg", "image/jpeg", payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(gotPathURL, "https://r2.example.com/media/images/2026/11/shot-") || !strings.HasSuffix(gotPathURL, ".jpg") {
		t.Fatalf("unexpected path-style URL: %q", gotPathURL)
	}
}

func TestCustomUploaderMultipartAndJSONPath(t *testing.T) {
	var gotMethod, gotAuth, gotCT string
	var gotPartName, gotFileName string
	var gotFileBytes []byte

	setNativeHostBridge(func(reqFrame []byte) ([]byte, error) {
		var req hostRequest
		body, err := decodeTestFrame(reqFrame, &req)
		if err != nil {
			t.Fatal(err)
		}
		switch req.Op {
		case "secrets.get":
			return encodeTestFrame(hostReply{OK: true, Value: "Bearer secret-token"}, nil)
		case "http.request":
			gotMethod = req.Method
			if req.Method == http.MethodHead {
				return encodeTestFrame(hostReply{OK: true, Status: 405}, nil)
			}
			gotAuth = req.Headers["Authorization"]
			gotCT = req.Headers["Content-Type"]
			_, params, err := mime.ParseMediaType(gotCT)
			if err != nil {
				t.Fatal(err)
			}
			mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
			part, err := mr.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			gotPartName = part.FormName()
			gotFileName = part.FileName()
			gotFileBytes, err = io.ReadAll(part)
			if err != nil {
				t.Fatal(err)
			}
			return encodeTestFrame(hostReply{OK: true, Status: 200}, []byte(`{"code":0,"data":{"url":"https://img.example.com/abc.webp"}}`))
		default:
			t.Fatalf("unexpected op %q", req.Op)
			return nil, nil
		}
	})
	defer setNativeHostBridge(nil)

	up, err := newCustomUploader(map[string]any{
		"customURL":      "https://img.example.com/upload",
		"customField":    "image",
		"customJSONPath": "data.url",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := up.Test(); err != nil {
		t.Fatalf("HEAD 405 should pass reachability test: %v", err)
	}

	raw := []byte("webp-bytes")
	u, err := up.Upload("clip.webp", "image/webp", raw)
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotAuth != "Bearer secret-token" {
		t.Fatalf("method=%q auth=%q", gotMethod, gotAuth)
	}
	if gotPartName != "image" || gotFileName != "clip.webp" || !bytes.Equal(gotFileBytes, raw) {
		t.Fatalf("multipart part=%q file=%q bytes=%q", gotPartName, gotFileName, gotFileBytes)
	}
	if u != "https://img.example.com/abc.webp" {
		t.Fatalf("url = %q", u)
	}
}

func TestHandleRequestUploadImage(t *testing.T) {
	setNativeHostBridge(func(reqFrame []byte) ([]byte, error) {
		var req hostRequest
		if _, err := decodeTestFrame(reqFrame, &req); err != nil {
			t.Fatal(err)
		}
		switch req.Op {
		case "config.get":
			return encodeTestFrame(hostReply{
				OK: true,
				Config: map[string]any{
					"provider":       "custom",
					"customURL":      "https://img.example.com/upload",
					"customJSONPath": "url",
				},
			}, nil)
		case "secrets.get":
			return encodeTestFrame(hostReply{OK: true, Value: ""}, nil)
		case "http.request":
			return encodeTestFrame(hostReply{OK: true, Status: 200}, []byte(`{"url":"https://img.example.com/pasted.png"}`))
		default:
			t.Fatalf("unexpected op %q", req.Op)
			return nil, nil
		}
	})
	defer setNativeHostBridge(nil)

	resp, err := handleRequest(pluginRequest{
		Command: "upload-image",
		Args: map[string]any{
			"filename":    "pasted.png",
			"contentType": "image/png",
			"dataBase64":  base64.StdEncoding.EncodeToString([]byte("png")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := resp.Data["url"].(string); got != "https://img.example.com/pasted.png" {
		t.Fatalf("resp.Data = %+v", resp.Data)
	}
}
