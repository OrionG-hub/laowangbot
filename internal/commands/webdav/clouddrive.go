package webdav

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protowire"
)

const cloudDriveChunkSize int64 = 50_000_000
const cloudDriveResponseLimit = 2 << 20

type cloudDriveClient struct{ dav *davClient }

func protoNumber(b []byte, field protowire.Number, value uint64) []byte {
	b = protowire.AppendTag(b, field, protowire.VarintType)
	return protowire.AppendVarint(b, value)
}
func protoString(b []byte, field protowire.Number, value string) []byte {
	b = protowire.AppendTag(b, field, protowire.BytesType)
	return protowire.AppendString(b, value)
}

// Only the small response fields used by these four stable RPCs are retained.
type cloudDriveFields struct {
	numbers map[protowire.Number]uint64
	strings map[protowire.Number]string
}

func decodeCloudDriveFields(raw []byte) (cloudDriveFields, error) {
	fields := cloudDriveFields{map[protowire.Number]uint64{}, map[protowire.Number]string{}}
	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 {
			return fields, errors.New("CloudDrive API protobuf 响应无效")
		}
		raw = raw[n:]
		switch typ {
		case protowire.VarintType:
			value, size := protowire.ConsumeVarint(raw)
			n = size
			fields.numbers[num] = value
		case protowire.BytesType:
			value, size := protowire.ConsumeBytes(raw)
			n = size
			if num == 2 || num == 3 {
				fields.strings[num] = string(value)
			}
		default:
			n = protowire.ConsumeFieldValue(num, typ, raw)
		}
		if n < 0 {
			return fields, errors.New("CloudDrive API protobuf 响应无效")
		}
		raw = raw[n:]
	}
	return fields, nil
}

// The protobuf prefix and file section stream directly into a single HTTP
// request. A 50 MB chunk does not require a 50 MB client-side buffer.
func (c cloudDriveClient) rpc(ctx context.Context, method string, payload io.Reader, size int64) (cloudDriveFields, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	u, err := url.Parse(c.dav.config.URL)
	if err != nil {
		return cloudDriveFields{}, errors.New("CloudDrive API 地址无效")
	}
	u.Path = "/clouddrive.CloudDriveFileSrv/" + method
	u.RawPath = ""
	header := make([]byte, 5)
	binary.BigEndian.PutUint32(header[1:], uint32(size))
	req, err := http.NewRequestWithContext(ctx, "POST", u.String(), io.MultiReader(bytes.NewReader(header), payload))
	if err != nil {
		return cloudDriveFields{}, errors.New("CloudDrive API 请求无效")
	}
	req.ContentLength = size + 5
	req.Header.Set("Content-Type", "application/grpc-web+proto")
	req.Header.Set("Accept", "application/grpc-web+proto")
	req.Header.Set("X-Grpc-Web", "1")
	req.Header.Set("Authorization", "Bearer "+c.dav.config.CloudDriveToken)
	req.Header.Set("User-Agent", "Laowangbot-CloudDrive/1.0")
	client := *c.dav.http
	client.Timeout = 10 * time.Minute
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return cloudDriveFields{}, ctx.Err()
		}
		return cloudDriveFields{}, errors.New("CloudDrive API 网络请求失败，请检查 API 路由、CDN 放行和证书")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return cloudDriveFields{}, fmt.Errorf("CloudDrive API %s 失败：HTTP %d；请检查 CDN 请求上限及 API 放行，未完成文件已保留", method, res.StatusCode)
	}
	mediaType := strings.TrimSpace(strings.Split(res.Header.Get("Content-Type"), ";")[0])
	if mediaType != "application/grpc-web+proto" && mediaType != "application/grpc-web" {
		return cloudDriveFields{}, errors.New("CloudDrive API 返回非 gRPC-Web 响应，请放行 API 路径的人机验证")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, cloudDriveResponseLimit+1))
	if err != nil {
		return cloudDriveFields{}, errors.New("CloudDrive API 响应读取失败；未完成文件已保留")
	}
	if len(raw) > cloudDriveResponseLimit {
		return cloudDriveFields{}, errors.New("CloudDrive API 响应过大")
	}
	var message []byte
	seenMessage, seenTrailer := false, false
	status := -1
	// Some gRPC-Web implementations send trailers-only errors in HTTP headers.
	if value := res.Header.Get("Grpc-Status"); value != "" {
		status, _ = strconv.Atoi(value)
		if status == 0 {
			status = -1
		}
	}
	for len(raw) > 0 {
		if len(raw) < 5 {
			return cloudDriveFields{}, errors.New("CloudDrive API 响应帧不完整")
		}
		flags, length := raw[0], int64(binary.BigEndian.Uint32(raw[1:5]))
		raw = raw[5:]
		if length > int64(len(raw)) {
			return cloudDriveFields{}, errors.New("CloudDrive API 响应帧不完整")
		}
		frame := raw[:length]
		raw = raw[length:]
		switch flags {
		case 0:
			if seenMessage || seenTrailer {
				return cloudDriveFields{}, errors.New("CloudDrive API 响应帧顺序无效")
			}
			message, seenMessage = frame, true
		case 0x80:
			if seenTrailer || len(raw) != 0 {
				return cloudDriveFields{}, errors.New("CloudDrive API 响应尾帧无效")
			}
			seenTrailer = true
			found := false
			for _, line := range strings.Split(string(frame), "\r\n") {
				key, value, ok := strings.Cut(line, ":")
				if ok && strings.EqualFold(strings.TrimSpace(key), "grpc-status") {
					if found {
						return cloudDriveFields{}, errors.New("CloudDrive API 状态重复")
					}
					parsed, e := strconv.Atoi(strings.TrimSpace(value))
					if e != nil || parsed < 0 {
						return cloudDriveFields{}, errors.New("CloudDrive API 状态无效")
					}
					status, found = parsed, true
				}
			}
			if !found {
				return cloudDriveFields{}, errors.New("CloudDrive API 缺少完成状态")
			}
		default:
			return cloudDriveFields{}, errors.New("CloudDrive API 响应使用不支持的帧格式")
		}
	}
	if status > 0 {
		return cloudDriveFields{}, fmt.Errorf("CloudDrive API %s 失败：gRPC %d；请检查 Token 权限、版本和请求大小，未完成文件已保留", method, status)
	}
	if !seenMessage || !seenTrailer || status != 0 {
		return cloudDriveFields{}, errors.New("CloudDrive API 缺少完整成功响应")
	}
	return decodeCloudDriveFields(message)
}
func (c cloudDriveClient) smallRPC(ctx context.Context, method string, raw []byte) (cloudDriveFields, error) {
	return c.rpc(ctx, method, bytes.NewReader(raw), int64(len(raw)))
}
func (c cloudDriveClient) test(ctx context.Context) error {
	raw := protoString(nil, 1, "/")
	raw = protoString(raw, 2, c.dav.config.CloudDriveRoot)
	fields, err := c.smallRPC(ctx, "FindFileByPath", raw)
	if err != nil {
		return err
	}
	if fields.strings[3] == "" || (fields.numbers[30] != 1 && fields.numbers[5] != 0) {
		return errors.New("CloudDrive API 根路径不是有效目录")
	}
	return nil
}
func (c cloudDriveClient) close(ctx context.Context, handle uint64) error {
	fields, err := c.smallRPC(ctx, "CloseFile", protoNumber(nil, 1, handle))
	if err != nil {
		return err
	}
	if fields.numbers[1] != 1 {
		return errors.New("CloudDrive 关闭文件失败；未完成文件已保留")
	}
	return nil
}
func (c cloudDriveClient) upload(ctx context.Context, local, destination string, size int64, progress func(int64, int64)) (result error) {
	file, err := os.Open(local)
	if err != nil {
		return errors.New("无法读取本地临时文件")
	}
	defer file.Close()
	temporary := destination + ".partial-" + uuid.NewString()
	parent := path.Join(c.dav.config.CloudDriveRoot, path.Dir(temporary))
	fields, err := c.smallRPC(ctx, "CreateFile", protoString(protoString(nil, 1, parent), 2, path.Base(temporary)))
	if err != nil {
		return err
	}
	handle := fields.numbers[1]
	if handle == 0 {
		return errors.New("CloudDrive 未返回有效文件句柄；未完成文件可能已保留")
	}
	closed := false
	defer func() {
		if !closed {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if err := c.close(cleanup, handle); err != nil {
				result = fmt.Errorf("%w；CloudDrive 文件句柄关闭未确认，远端可能仍有写入任务", result)
			}
		}
	}()
	for offset := int64(0); offset < size; {
		n := min(cloudDriveChunkSize, size-offset)
		if progress != nil {
			progress(offset, size)
		}
		prefix := protoNumber(nil, 1, handle)
		prefix = protoNumber(prefix, 2, uint64(offset))
		prefix = protoNumber(prefix, 3, uint64(n))
		prefix = protowire.AppendTag(prefix, 4, protowire.BytesType)
		prefix = protowire.AppendVarint(prefix, uint64(n))
		payload := io.MultiReader(bytes.NewReader(prefix), io.NewSectionReader(file, offset, n))
		reply, err := c.rpc(ctx, "WriteToFile", payload, int64(len(prefix))+n)
		if err != nil {
			return err
		}
		if reply.numbers[1] != uint64(n) {
			return errors.New("CloudDrive 分片写入字节数不一致；未完成文件已保留，不自动重试")
		}
		offset += n
		if progress != nil {
			progress(offset, size)
		}
	}
	// Do not retry a failed close: the server may already have finalized the file.
	closed = true
	if err := c.close(ctx, handle); err != nil {
		return err
	}
	return c.dav.finish(ctx, temporary, destination, size)
}
