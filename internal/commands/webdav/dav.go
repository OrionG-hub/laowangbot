package webdav

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type davClient struct {
	config Config
	http   *http.Client
}
type response struct {
	status int
	header http.Header
	body   []byte
}

func (d *davClient) url(relative string) string {
	parts := strings.Split(relative, "/")
	escaped := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			escaped = append(escaped, url.PathEscape(part))
		}
	}
	result := d.config.URL + "/" + strings.Join(escaped, "/")
	if relative != "" && strings.HasSuffix(relative, "/") {
		result += "/"
	}
	return result
}
func (d *davClient) request(ctx context.Context, method, relative string, headers map[string]string, file string, bytes int64) (response, error) {
	deadline := 2 * time.Minute
	// PUT can stream a large file; the enclosing upload still has a one-hour limit.
	if method == "PUT" {
		deadline = time.Hour
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	var reader io.Reader
	var source *os.File
	if file != "" {
		var err error
		source, err = os.Open(file)
		if err != nil {
			return response{}, errors.New("无法读取本地临时文件")
		}
		defer source.Close()
		reader = source
	}
	req, err := http.NewRequestWithContext(ctx, method, d.url(relative), reader)
	if err != nil {
		return response{}, errors.New("WebDAV 请求地址无效")
	}
	req.SetBasicAuth(d.config.Username, d.config.Password)
	req.Header.Set("User-Agent", "Laowangbot-WebDAV/1.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if source != nil {
		req.ContentLength = bytes
	}
	client := *d.http
	client.Timeout = deadline
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return response{}, ctx.Err()
		}
		return response{}, errors.New("WebDAV 网络请求失败，请检查网络、证书和地址")
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil {
		return response{}, errors.New("WebDAV 响应读取失败")
	}
	if len(body) > 2<<20 {
		return response{}, errors.New("WebDAV 响应过大")
	}
	return response{res.StatusCode, res.Header, body}, nil
}
func hasCollection(raw []byte) bool {
	dec := xml.NewDecoder(strings.NewReader(string(raw)))
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		if start, ok := tok.(xml.StartElement); ok && start.Name.Local == "collection" && (start.Name.Space == "DAV:" || start.Name.Space == "") {
			return true
		}
	}
}
func (d *davClient) directory(ctx context.Context, relative string) error {
	parts := strings.Split(relative, "/")
	for i := 1; i <= len(parts); i++ {
		path := strings.Join(parts[:i], "/") + "/"
		res, err := d.request(ctx, "MKCOL", path, nil, "", 0)
		if err != nil {
			return err
		}
		if res.status == 201 {
			continue
		}
		if res.status == 405 {
			check, err := d.request(ctx, "PROPFIND", path, map[string]string{"Depth": "0"}, "", 0)
			if err != nil {
				return err
			}
			if check.status == 207 && hasCollection(check.body) {
				continue
			}
		}
		return fmt.Errorf("创建或确认目录失败：HTTP %d", res.status)
	}
	return nil
}
func (d *davClient) upload(ctx context.Context, file, destination string, bytes int64) error {
	temporary := destination + ".partial-" + uuid.NewString()
	put, err := d.request(ctx, "PUT", temporary, map[string]string{"Content-Type": "application/octet-stream", "If-None-Match": "*"}, file, bytes)
	if err != nil {
		return err
	}
	if put.status == http.StatusRequestEntityTooLarge {
		return fmt.Errorf("WebDAV 服务端或中间代理拒绝了上传大小（HTTP 413，%.1f MiB）。请检查上传限制；CloudDrive 可配置 API 分片模式，.dav config limit 0 无法解除服务端限制。远端可能留有 .partial- 文件", float64(bytes)/(1<<20))
	}
	if put.status != 200 && put.status != 201 && put.status != 204 {
		return fmt.Errorf("上传失败：HTTP %d（未完成文件以 .partial- 标识）", put.status)
	}
	return d.finish(ctx, temporary, destination, bytes)
}

func (d *davClient) verify(ctx context.Context, path string, bytes int64) error {
	head, err := d.request(ctx, "HEAD", path, nil, "", 0)
	if err != nil {
		return err
	}
	n, e := strconv.ParseInt(head.header.Get("Content-Length"), 10, 64)
	if head.status != 200 || e != nil || n != bytes {
		return errors.New("远端文件大小核验失败，不写成功记录；远端文件已保留")
	}
	return nil
}

func (d *davClient) finish(ctx context.Context, temporary, destination string, bytes int64) error {
	if err := d.verify(ctx, temporary, bytes); err != nil {
		return err
	}
	moved, err := d.request(ctx, "MOVE", temporary, map[string]string{"Destination": d.url(destination), "Overwrite": "F"}, "", 0)
	if err != nil {
		return err
	}
	if moved.status != 201 && moved.status != 204 {
		return fmt.Errorf("完成归档失败：HTTP %d；不覆盖已有文件，临时上传已保留", moved.status)
	}
	return d.verify(ctx, destination, bytes)
}
