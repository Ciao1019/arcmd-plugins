package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"
)

type s3Uploader struct {
	endpoint      *url.URL
	region        string
	bucket        string
	accessKey     string
	secretKey     string
	prefix        string
	publicBaseURL string
	pathStyle     bool
	allowInsecure bool
	now           func() time.Time
}

func newS3Uploader(cfg map[string]any) (*s3Uploader, error) {
	endpoint := strings.TrimSpace(strCfg(cfg, "s3Endpoint"))
	if endpoint == "" {
		return nil, errors.New("请填写 S3 Endpoint")
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return nil, fmt.Errorf("无效的 S3 Endpoint: %q", endpoint)
	}
	bucket := strings.TrimSpace(strCfg(cfg, "s3Bucket"))
	if bucket == "" {
		return nil, errors.New("请填写 S3 Bucket")
	}
	accessKey := strings.TrimSpace(strCfg(cfg, "s3AccessKey"))
	if accessKey == "" {
		return nil, errors.New("请填写 S3 Access Key ID")
	}
	secretKey, err := sdkGetSecret("s3SecretKey")
	if err != nil {
		return nil, fmt.Errorf("读取 S3 Secret Access Key 失败: %w", err)
	}
	secretKey = strings.TrimSpace(secretKey)
	if secretKey == "" {
		return nil, errors.New("请填写 S3 Secret Access Key")
	}
	region := strings.TrimSpace(strCfg(cfg, "s3Region"))
	if region == "" {
		region = "auto"
	}
	prefix := strings.TrimSpace(strCfg(cfg, "s3Prefix"))
	publicBase := strings.TrimRight(strings.TrimSpace(strCfg(cfg, "s3PublicBaseURL")), "/")
	if publicBase != "" {
		pu, perr := url.Parse(publicBase)
		if perr != nil || (pu.Scheme != "https" && pu.Scheme != "http") || pu.Hostname() == "" {
			return nil, fmt.Errorf("无效的公开访问域名: %q", publicBase)
		}
	}
	pathStyle := boolCfg(cfg, "s3PathStyle")
	host := u.Hostname()
	if net.ParseIP(host) != nil || strings.EqualFold(host, "localhost") || strings.Trim(u.Path, "/") != "" {
		pathStyle = true
	}
	return &s3Uploader{
		endpoint:      u,
		region:        region,
		bucket:        bucket,
		accessKey:     accessKey,
		secretKey:     secretKey,
		prefix:        prefix,
		publicBaseURL: publicBase,
		pathStyle:     pathStyle,
		allowInsecure: boolCfg(cfg, "s3AllowInsecure") || boolCfg(cfg, "allowInsecureHTTP"),
		now:           time.Now,
	}, nil
}

func (c *s3Uploader) Name() string { return "S3" }

func (c *s3Uploader) Test() error {
	q := map[string]string{
		"list-type": "2",
		"max-keys":  "1",
	}
	if p := strings.Trim(expandPrefix(c.prefix, c.now().UTC()), "/"); p != "" && !strings.Contains(p, "{") {
		q["prefix"] = p + "/"
	}
	resp, err := c.do(http.MethodGet, "", q, nil, nil)
	if err != nil {
		return err
	}
	if resp.Status >= 400 {
		return parseS3Error(resp.Status, resp.Body)
	}
	return nil
}

func (c *s3Uploader) Upload(filename, contentType string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("图片内容为空")
	}
	key := buildObjectKey(c.prefix, filename, data, c.now().UTC())
	if contentType == "" {
		contentType = mimeForPath(filename)
	}
	headers := map[string]string{
		"Content-Type": contentType,
	}
	resp, err := c.do(http.MethodPut, key, nil, headers, data)
	if err != nil {
		return "", err
	}
	if resp.Status >= 400 {
		return "", parseS3Error(resp.Status, resp.Body)
	}
	return c.publicObjectURL(key), nil
}

func (c *s3Uploader) publicObjectURL(objectKey string) string {
	encodedKey := awsURIEncode(objectKey, false)
	if c.publicBaseURL != "" {
		return c.publicBaseURL + "/" + encodedKey
	}
	basePath := strings.TrimRight(c.endpoint.Path, "/")
	if c.pathStyle {
		return c.endpoint.Scheme + "://" + c.endpoint.Host + basePath + "/" + awsURIEncode(c.bucket, true) + "/" + encodedKey
	}
	return c.endpoint.Scheme + "://" + c.bucket + "." + c.endpoint.Host + basePath + "/" + encodedKey
}

func expandPrefix(prefix string, now time.Time) string {
	r := strings.NewReplacer(
		"{year}", now.Format("2006"),
		"{month}", now.Format("01"),
		"{day}", now.Format("02"),
	)
	return strings.Trim(r.Replace(strings.ReplaceAll(prefix, `\`, "/")), "/")
}

func buildObjectKey(prefix, filename string, data []byte, now time.Time) string {
	base := path.Base(strings.ReplaceAll(strings.TrimSpace(filename), `\`, "/"))
	if base == "" || base == "." || base == "/" {
		base = "image.png"
	}
	ext := strings.ToLower(path.Ext(base))
	if ext == "" {
		ext = ".png"
	}
	stem := sanitizeObjectStem(strings.TrimSuffix(base, path.Ext(base)))
	hashHex := sha256HexBytes(data)
	if len(hashHex) > 12 {
		hashHex = hashHex[:12]
	}
	name := stem + "-" + hashHex + ext
	dir := expandPrefix(prefix, now)
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

func sanitizeObjectStem(stem string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.TrimSpace(stem) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '.':
			b.WriteRune(r)
			prevDash = false
		case r == '-' || unicode.IsSpace(r):
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		default:
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(r)
				prevDash = false
			} else if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "image"
	}
	return out
}

func mimeForPath(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}

func (c *s3Uploader) do(method, objectKey string, query map[string]string, extraHeaders map[string]string, body []byte) (httpResponse, error) {
	basePath := strings.TrimRight(c.endpoint.Path, "/")
	host := c.endpoint.Host
	var rawPath string
	if c.pathStyle {
		rawPath = basePath + "/" + awsURIEncode(c.bucket, true)
	} else {
		host = c.bucket + "." + c.endpoint.Host
		rawPath = basePath
	}
	if objectKey != "" {
		rawPath += "/" + awsURIEncode(objectKey, false)
	}
	if rawPath == "" {
		rawPath = "/"
	}

	canonicalQuery := buildCanonicalQuery(query)
	wireURL := c.endpoint.Scheme + "://" + host + rawPath
	if canonicalQuery != "" {
		wireURL += "?" + canonicalQuery
	}

	now := c.now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	payloadHash := sha256HexBytes(body)

	headers := map[string]string{
		"Host":                 host,
		"x-amz-date":           amzDate,
		"x-amz-content-sha256": payloadHash,
	}
	for k, v := range extraHeaders {
		headers[k] = v
	}

	canonicalHeaders, signedHeaders := buildCanonicalHeaders(headers)
	canonicalRequest := strings.Join([]string{
		method,
		rawPath,
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := dateStamp + "/" + c.region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256HexBytes([]byte(canonicalRequest)),
	}, "\n")

	kDate := hmacSHA256([]byte("AWS4"+c.secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, c.region)
	kService := hmacSHA256(kRegion, "s3")
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	headers["Authorization"] = fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		c.accessKey, scope, signedHeaders, signature,
	)

	return sdkDoHTTP(method, wireURL, headers, body, c.allowInsecure)
}

func buildCanonicalQuery(q map[string]string) string {
	if len(q) == 0 {
		return ""
	}
	type kv struct{ k, v string }
	pairs := make([]kv, 0, len(q))
	for k, v := range q {
		pairs = append(pairs, kv{awsURIEncode(k, true), awsURIEncode(v, true)})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k != pairs[j].k {
			return pairs[i].k < pairs[j].k
		}
		return pairs[i].v < pairs[j].v
	})
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = p.k + "=" + p.v
	}
	return strings.Join(parts, "&")
}

func buildCanonicalHeaders(headers map[string]string) (canonical string, signed string) {
	lower := make(map[string]string, len(headers))
	keys := make([]string, 0, len(headers))
	for k, v := range headers {
		lk := strings.ToLower(strings.TrimSpace(k))
		if lk == "" {
			continue
		}
		if _, exists := lower[lk]; !exists {
			keys = append(keys, lk)
		}
		lower[lk] = strings.Join(strings.Fields(strings.TrimSpace(v)), " ")
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte(':')
		b.WriteString(lower[k])
		b.WriteByte('\n')
	}
	return b.String(), strings.Join(keys, ";")
}

func awsURIEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '-' || c == '~' || c == '.' || (c == '/' && !encodeSlash) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func sha256HexBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type s3ErrorXML struct {
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

func parseS3Error(status int, body []byte) error {
	var e s3ErrorXML
	if len(body) > 0 && xml.Unmarshal(body, &e) == nil && (e.Code != "" || e.Message != "") {
		if e.Message != "" {
			return fmt.Errorf("S3 错误 %d (%s): %s", status, e.Code, e.Message)
		}
		return fmt.Errorf("S3 错误 %d (%s)", status, e.Code)
	}
	return fmt.Errorf("S3 HTTP %d", status)
}
