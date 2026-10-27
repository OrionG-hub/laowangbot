// Package httpx 是带限制的 HTTP 客户端，所有要联网的命令都走它：
// 共用一个 transport，每个请求有超时，响应体有字节上限，重定向最多三次。
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// UserAgent 是默认发送的 User-Agent，请求可以自己覆盖。
const UserAgent = "laowangbot/1"

// ErrTooLarge 表示响应体超过了请求设定的字节上限。
var ErrTooLarge = errors.New("response body exceeds the size limit")

var shared = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
	},
	CheckRedirect: checkRedirect,
}

// ErrCredentialRedirect 表示带凭证的请求被重定向到了别的源。
var ErrCredentialRedirect = errors.New("refusing credential redirect to another origin")

// credentialHeaders 是常见的 API 凭证请求头。自定义请求头不会像
// Authorization 一样被 net/http 在跨源重定向时自动清除。
var credentialHeaders = []string{"Authorization", "Cookie", "X-Api-Key", "Api-Key", "X-Goog-Api-Key"}

// checkRedirect 最多跟随三次重定向；带凭证时只允许留在同一个 scheme 和 host。
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > 3 {
		return errors.New("too many redirects")
	}
	first := via[0]
	credential := false
	for name, values := range first.URL.Query() {
		if strings.EqualFold(name, "key") {
			for _, value := range values {
				if value != "" {
					credential = true
					break
				}
			}
		}
		if credential {
			break
		}
	}
	for _, name := range credentialHeaders {
		credential = credential || first.Header.Get(name) != ""
	}
	if credential && (!strings.EqualFold(req.URL.Host, first.URL.Host) || !strings.EqualFold(req.URL.Scheme, first.URL.Scheme)) {
		return ErrCredentialRedirect
	}
	return nil
}

// Request 描述一次调用。
type Request struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
	// NoRedirect 禁止跟随重定向，返回原始 3xx 响应。
	NoRedirect bool
	// Timeout 限制整个调用的时长。零值表示 30 秒。
	Timeout time.Duration
	// MaxBytes 限制响应体大小。零值表示 2 MB。
	MaxBytes int64
}

// Response 是返回的结果。
type Response struct {
	Status int
	Body   []byte
}

// OK 判断状态码是不是 2xx。
func (r Response) OK() bool { return r.Status >= 200 && r.Status < 300 }

// Do 执行请求。非 2xx 状态码不算错误：404 意味着什么由调用方决定。
func Do(ctx context.Context, request Request) (Response, error) {
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	limit := request.MaxBytes
	if limit <= 0 {
		limit = 2 << 20
	}
	method := request.Method
	if method == "" {
		method = http.MethodGet
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var body io.Reader
	if request.Body != nil {
		body = bytes.NewReader(request.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, request.URL, body)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("User-Agent", UserAgent)
	for key, value := range request.Headers {
		req.Header.Set(key, value)
	}
	client := shared
	if request.NoRedirect {
		copy := *shared
		copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &copy
	}
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return Response{Status: resp.StatusCode}, err
	}
	if int64(len(data)) > limit {
		return Response{Status: resp.StatusCode}, ErrTooLarge
	}
	return Response{Status: resp.StatusCode, Body: data}, nil
}

// GetJSON 获取并解码一个 JSON 文档。
func GetJSON(ctx context.Context, url string, timeout time.Duration, maxBytes int64, out any) error {
	response, err := Do(ctx, Request{URL: url, Timeout: timeout, MaxBytes: maxBytes})
	if err != nil {
		return err
	}
	if !response.OK() {
		return &StatusError{Status: response.Status}
	}
	if err := json.Unmarshal(response.Body, out); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

// PostJSON 发送 JSON 请求体，返回原始响应。
func PostJSON(ctx context.Context, url string, headers map[string]string, payload any, timeout time.Duration, maxBytes int64) (Response, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Response{}, err
	}
	merged := map[string]string{"Content-Type": "application/json"}
	for key, value := range headers {
		merged[key] = value
	}
	return Do(ctx, Request{Method: http.MethodPost, URL: url, Headers: merged, Body: encoded, Timeout: timeout, MaxBytes: maxBytes})
}

// StatusError 表示非 2xx 的响应。
type StatusError struct{ Status int }

func (e *StatusError) Error() string { return fmt.Sprintf("HTTP %d", e.Status) }

// Reason 把传输失败转成能发到聊天里的说明，不泄露 URL 和主机名。
func Reason(err error) string {
	var status *StatusError
	switch {
	case errors.As(err, &status):
		if status.Status == 429 {
			return "请求过于频繁，请稍后重试"
		}
		return fmt.Sprintf("服务返回 HTTP %d", status.Status)
	case errors.Is(err, context.DeadlineExceeded):
		return "请求超时，请稍后重试"
	case errors.Is(err, ErrTooLarge):
		return "响应数据过大"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "请求超时，请稍后重试"
	}
	return "网络请求失败，请稍后重试"
}

// Download streams a bounded response to disk without retaining an executable
// in memory. It shares the standard client's transport and redirect policy.
func Download(ctx context.Context, request Request, dst io.Writer) (int64, error) {
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	limit := request.MaxBytes
	if limit <= 0 {
		limit = 2 << 20
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, request.URL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", UserAgent)
	for key, value := range request.Headers {
		req.Header.Set(key, value)
	}
	client := shared
	if request.NoRedirect {
		copy := *shared
		copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &copy
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, &StatusError{Status: resp.StatusCode}
	}
	if resp.ContentLength > limit {
		return 0, ErrTooLarge
	}
	n, err := io.CopyBuffer(dst, io.LimitReader(resp.Body, limit+1), make([]byte, 32<<10))
	if err != nil {
		return n, err
	}
	if n > limit {
		return n, ErrTooLarge
	}
	return n, nil
}
