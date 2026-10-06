// Package webdav archives Telegram media using bounded disk and HTTP streams.
package webdav

import (
	"errors"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Config preserves the original plugin configuration format.
type Config struct {
	URL             string `json:"url"`
	Username        string `json:"username"`
	Password        string `json:"password"`
	MaxFileMiB      int64  `json:"maxFileMiB"`
	UploadMode      string `json:"uploadMode,omitempty"`
	CloudDriveToken string `json:"cloudDriveToken,omitempty"`
	CloudDriveRoot  string `json:"cloudDriveRoot,omitempty"`
}

// Record preserves every original uploads SQLite column, including imported IDs.
type Record struct {
	ID         int64  `json:"id"`
	Date       string `json:"date"`
	ChatID     string `json:"chat_id"`
	ChatName   string `json:"chat_name"`
	MessageID  int    `json:"message_id"`
	Filename   string `json:"filename"`
	RemotePath string `json:"remote_path"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
	CreatedAt  string `json:"created_at"`
	Target     string `json:"target"`
}

// DirectoryRename persists an unfinished remote MOVE until paths are committed.
type DirectoryRename struct {
	Target string `json:"target"`
	ChatID string `json:"chat_id"`
	From   string `json:"from"`
	To     string `json:"to"`
	Name   string `json:"name"`
}

// Records is data/webdav-records.json. Chats maps chat IDs to current folder names.
type Records struct {
	Chats   map[string]string          `json:"chats"`
	Uploads []Record                   `json:"uploads"`
	Renames map[string]DirectoryRename `json:"renames,omitempty"`
}

func validateConfig(c Config) (Config, error) {
	u, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return c, errors.New("地址必须为 HTTPS WebDAV 根地址，不带账号、查询参数或片段")
	}
	if c.Username == "" || c.Password == "" || strings.ContainsAny(c.Username, ":\r\n") {
		return c, errors.New("请配置有效用户名和密码")
	}
	if c.MaxFileMiB < 0 || c.MaxFileMiB > 4096 {
		return c, errors.New("文件上限需为 0–4096 MiB，0 表示不限")
	}
	if c.UploadMode != "" && c.UploadMode != "webdav" && c.UploadMode != "clouddrive" {
		return c, errors.New("上传模式需为 webdav 或 clouddrive")
	}
	if strings.IndexFunc(c.CloudDriveToken, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return c, errors.New("CloudDrive API Token 不能包含空白或控制字符")
	}
	if c.CloudDriveRoot == "" {
		c.CloudDriveRoot = "/"
	}
	if !strings.HasPrefix(c.CloudDriveRoot, "/") || strings.ContainsAny(c.CloudDriveRoot, "\\\x00\r\n") {
		return c, errors.New("CloudDrive API 根路径必须为绝对目录")
	}
	for _, part := range strings.Split(c.CloudDriveRoot, "/") {
		if part == ".." {
			return c, errors.New("CloudDrive API 根路径不能包含 ..")
		}
	}
	c.CloudDriveRoot = path.Clean(c.CloudDriveRoot)
	c.URL = strings.TrimRight(u.String(), "/")
	return c, nil
}
func safeName(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			return '_'
		}
		return r
	}, norm.NFC.String(value))
	value = strings.TrimRight(strings.TrimLeft(value, "."), ". ")
	rs := []rune(value)
	if len(rs) > limit {
		value = string(rs[:limit])
	}
	if value == "" {
		return "未命名"
	}
	return value
}

var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func validDate(value string) bool {
	if !datePattern.MatchString(value) {
		return false
	}
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}
func dateKey(now time.Time) string {
	return now.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02")
}
