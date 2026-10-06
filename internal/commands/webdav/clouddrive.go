package webdav

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

const cloudDriveChunkSize int64 = 80_000_000

type cloudDriveClient struct{ dav *davClient }

func (c cloudDriveClient) test(ctx context.Context) error {
	res, err := c.dav.request(ctx, "OPTIONS", "", nil, "", 0)
	if err != nil {
		return err
	}
	if res.status != 200 && res.status != 204 {
		return fmt.Errorf("WebDAV 分片能力检查失败：HTTP %d；请检查 CDN 放行及人机验证", res.status)
	}
	for _, capability := range strings.Split(res.header.Get("DAV"), ",") {
		if strings.EqualFold(strings.TrimSpace(capability), "sabredav-partialupdate") {
			return nil
		}
	}
	return errors.New("服务端未声明 sabredav-partialupdate，无法使用 HTTP 分片续写")
}

func (c cloudDriveClient) upload(ctx context.Context, file, destination string, size int64, digest []byte, progress func(int64, int64)) error {
	if err := c.test(ctx); err != nil {
		return err
	}
	source, err := os.Open(file)
	if err != nil {
		return errors.New("无法读取本地临时文件")
	}
	defer source.Close()
	temporary := destination + ".partial-" + uuid.NewString()
	for offset := int64(0); offset < size; {
		length := min(cloudDriveChunkSize, size-offset)
		method := "PATCH"
		headers := map[string]string{
			"Content-Type":   "application/x-sabredav-partialupdate",
			"X-Update-Range": fmt.Sprintf("bytes=%d-%d", offset, offset+length-1),
		}
		if offset == 0 {
			method = "PUT"
			headers = map[string]string{"Content-Type": "application/octet-stream", "If-None-Match": "*"}
		}
		res, err := c.dav.requestReader(ctx, method, temporary, headers, io.NewSectionReader(source, offset, length), length)
		if err != nil {
			return err
		}
		if (method == "PUT" && res.status != 200 && res.status != 201 && res.status != 204) ||
			(method == "PATCH" && res.status != 200 && res.status != 204) {
			return fmt.Errorf("CloudDrive HTTP %s 分片失败：HTTP %d；请检查 CDN 放行、请求上限及续写权限，未完成文件已保留", method, res.status)
		}
		offset += length
		if progress != nil {
			progress(offset, size)
		}
	}
	// Some servers acknowledge PATCH without preserving all bytes. Read back
	// and hash the staging object before publishing or writing a success record.
	if err := c.verifyContent(ctx, temporary, size, digest); err != nil {
		return err
	}
	return c.dav.finish(ctx, temporary, destination, size)
}

func (c cloudDriveClient) verifyContent(ctx context.Context, relative string, size int64, digest []byte) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.dav.url(relative), nil)
	if err != nil {
		return errors.New("WebDAV 校验地址无效")
	}
	req.SetBasicAuth(c.dav.config.Username, c.dav.config.Password)
	req.Header.Set("User-Agent", "Laowangbot-WebDAV/1.0")
	req.Header.Set("Accept-Encoding", "identity")
	client := *c.dav.http
	client.Timeout = time.Hour
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("远端内容校验读取失败，未完成文件已保留")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("远端内容校验失败：HTTP %d；未完成文件已保留", res.StatusCode)
	}
	hash := sha256.New()
	n, err := io.CopyBuffer(hash, io.LimitReader(res.Body, size+1), make([]byte, 64<<10))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("远端内容校验读取失败，未完成文件已保留")
	}
	if n != size || !bytes.Equal(hash.Sum(nil), digest) {
		return errors.New("远端文件大小或 SHA256 校验失败，不写成功记录；未完成文件已保留")
	}
	return nil
}
