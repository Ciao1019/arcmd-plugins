package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
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

func TestS3VirtualHostedSigV4AndErrors(t *testing.T) {
	var gotMethod, gotURL string
	var gotHeaders map[string]string

	setNativeHostBridge(func(reqFrame []byte) ([]byte, error) {
		var req hostRequest
		if _, err := decodeTestFrame(reqFrame, &req); err != nil {
			t.Fatal(err)
		}
		gotMethod = req.Method
		gotURL = req.URL
		gotHeaders = req.Headers
		body := `<Error><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`
		return encodeTestFrame(hostReply{OK: true, Status: 403}, []byte(body))
	})
	defer setNativeHostBridge(nil)

	c, err := newS3Client("https://s3.us-east-1.amazonaws.com", "us-east-1", "my-notes", "AKIA", "SECRET", "ArcMD", false, false)
	if err != nil {
		t.Fatal(err)
	}
	c.now = func() time.Time { return time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC) }

	err = c.Test()
	if err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("expected AccessDenied error, got %v", err)
	}
	if gotMethod != http.MethodGet || !strings.HasPrefix(gotURL, "https://my-notes.s3.us-east-1.amazonaws.com/?") {
		t.Fatalf("virtual-hosted URL = %s %s", gotMethod, gotURL)
	}
	if gotHeaders["Host"] != "my-notes.s3.us-east-1.amazonaws.com" {
		t.Fatalf("Host header = %q", gotHeaders["Host"])
	}
	if !strings.Contains(gotHeaders["Authorization"], "Credential=AKIA/20261119/us-east-1/s3/aws4_request") {
		t.Fatalf("bad Authorization = %q", gotHeaders["Authorization"])
	}
}

func TestWebDAVStripHrefAndAuthError(t *testing.T) {
	rel, ok := stripDAVHref("https://dav.jianguoyun.com/dav/ArcMD/Work/%E7%AC%94%E8%AE%B0/2026%20%281%29.md", "/dav/ArcMD/Work", "/dav/ArcMD/Work")
	if !ok || rel != "笔记/2026 (1).md" {
		t.Fatalf("stripDAVHref(full) = %q, %v", rel, ok)
	}
	rel, ok = stripDAVHref("/dav/ArcMD/Work/", "/dav/ArcMD/Work", "/dav/ArcMD/Work")
	if ok || rel != "" {
		t.Fatalf("self directory should be skipped: %q, %v", rel, ok)
	}

	setNativeHostBridge(func(reqFrame []byte) ([]byte, error) {
		return encodeTestFrame(hostReply{OK: true, Status: 401}, nil)
	})
	defer setNativeHostBridge(nil)

	c, err := newWebDAVClient("https://dav.jianguoyun.com/dav/", "u", "p", "ArcMD", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Test(); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 auth error, got %v", err)
	}
}

func TestSpacePrefixesDisambiguation(t *testing.T) {
	got := spacePrefixes([]spaceInfo{
		{ID: "abcdef1234", Name: "Work"},
		{ID: "9999998888", Name: "Work"},
		{ID: "personal1", Name: "Personal"},
	})
	if got["abcdef1234"] == got["9999998888"] {
		t.Fatalf("duplicate names collided: %+v", got)
	}
	if got["personal1"] != "Personal" {
		t.Fatalf("unique name altered: %+v", got)
	}
	var _ = bytes.Compare
}

func TestThreeWayAutoMerge(t *testing.T) {
	base := "title\nline 1\nline 2\nline 3\nfooter\n"

	// Non-overlapping edits on local (top) and remote (bottom) merge cleanly.
	local := "title local\nline 1\nline 2\nline 3\nfooter\n"
	remote := "title\nline 1\nline 2\nline 3\nfooter remote\n"
	merged, ok := threeWayAutoMerge(base, local, remote)
	if !ok {
		t.Fatal("expected non-overlapping edits to auto-merge cleanly")
	}
	want := "title local\nline 1\nline 2\nline 3\nfooter remote\n"
	if merged != want {
		t.Fatalf("merged = %q, want %q", merged, want)
	}

	// Identical edits on both sides auto-merge without conflict.
	same := "title\nline 1\nline two\nline 3\nfooter\n"
	mergedSame, ok := threeWayAutoMerge(base, same, same)
	if !ok || mergedSame != same {
		t.Fatalf("identical edits failed: ok=%v merged=%q", ok, mergedSame)
	}

	// Overlapping differing edits report conflict (ok == false).
	conflictLocal := "title\nline 1\nline A\nline 3\nfooter\n"
	conflictRemote := "title\nline 1\nline B\nline 3\nfooter\n"
	if got, ok := threeWayAutoMerge(base, conflictLocal, conflictRemote); ok {
		t.Fatalf("expected overlapping conflict, got ok=true merged=%q", got)
	}
}
