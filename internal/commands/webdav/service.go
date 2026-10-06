package webdav

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OrionG-hub/laowangbot/internal/bot"
	"github.com/OrionG-hub/laowangbot/internal/command"
	"github.com/OrionG-hub/laowangbot/internal/store"
	"github.com/gotd/td/tg"
)

const headroom int64 = 256 << 20

type pageSession struct {
	ids     []int64
	page    int
	expires time.Time
	filter  string
}
type service struct {
	root       string
	config     *store.Store[Config]
	records    *store.Store[Records]
	http       *http.Client
	now        func() time.Time
	space      func(string) (int64, error)
	mu         sync.Mutex
	active     context.CancelFunc
	activeDone chan struct{}
	closing    bool
	sessions   map[string]*pageSession
}
type request struct {
	args           []string
	value          string
	chatID, prefix string
	owner, saved   bool
	edit           func(context.Context, string) error
	hide           func(context.Context) error
	notice         func(context.Context)
	reply          func(context.Context) (*bot.Message, error)
	name           func(*bot.Message) string
	download       func(context.Context, *bot.MediaSource, *os.File) error
}

func newService(root string) *service {
	return &service{root: root, config: store.New(filepath.Join(root, "data", "webdav-config.json"), func() Config { return Config{MaxFileMiB: 1024} }), records: store.New(filepath.Join(root, "data", "webdav-records.json"), func() Records { return Records{Chats: map[string]string{}, Uploads: []Record{}} }), http: &http.Client{Timeout: 2 * time.Minute}, now: time.Now, space: diskAvailable, sessions: map[string]*pageSession{}}
}
func arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}
func (s *service) handle(ctx context.Context, r *request) error {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return errors.New("WebDAV 正在关闭")
	}
	s.mu.Unlock()
	if !r.owner {
		return errors.New("WebDAV 仅账号本人可用")
	}
	action := strings.ToLower(arg(r.args, 0))
	switch action {
	case "help":
		return r.edit(ctx, help(r.prefix))
	case "config":
		return s.configure(ctx, r)
	case "cancel":
		s.mu.Lock()
		cancel := s.active
		s.mu.Unlock()
		if cancel == nil {
			return r.edit(ctx, "当前没有上传任务。")
		}
		cancel()
		return r.edit(ctx, "已请求取消；远端可能保留 .partial- 文件，不会自动删除云端内容。")
	case "list", "next", "prev", "page":
		return s.list(ctx, r)
	case "info":
		id, err := strconv.ParseInt(arg(r.args, 1), 10, 64)
		if err != nil || id < 1 || len(r.args) != 2 {
			return errors.New("用法：.dav info 记录ID")
		}
		db, err := s.records.Read()
		if err != nil {
			return errors.New("无法读取 WebDAV 记录")
		}
		for _, row := range db.Uploads {
			if row.ID == id {
				return r.edit(ctx, fmt.Sprintf("☁️ <b>记录 #%d</b>\n文件：%s\n时间：%s\n会话：%s · %s\n大小：%d 字节\n路径：%s\n本地文件 SHA256：%s", row.ID, command.Escape(row.Filename), command.Escape(row.CreatedAt), command.Escape(row.ChatName), command.Code(row.ChatID), row.Bytes, command.Code("/"+row.RemotePath), command.Code(row.SHA256)))
			}
		}
		return errors.New("记录不存在")
	case "test":
		c, err := s.loadConfig()
		if err != nil {
			return err
		}
		test, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		d := davClient{c, s.http}
		res, err := d.request(test, "PROPFIND", "", map[string]string{"Depth": "0"}, "", 0)
		if err != nil {
			return err
		}
		if res.status != 207 {
			return fmt.Errorf("WebDAV 连接检查失败：HTTP %d", res.status)
		}
		if c.UploadMode == "clouddrive" {
			if err := (cloudDriveClient{&d}).test(test); err != nil {
				return err
			}
			return r.edit(ctx, "✅ WebDAV 根目录可访问，服务端声明支持 HTTP 分片续写（只读检测；实际上传仍需写入、续写和移动权限）")
		}
		return r.edit(ctx, "✅ WebDAV 根目录可访问（只读检测；实际上传还需要创建、写入和移动权限）")
	case "":
		s.mu.Lock()
		if s.closing {
			s.mu.Unlock()
			return errors.New("WebDAV 正在关闭")
		}
		if s.active != nil {
			s.mu.Unlock()
			return errors.New("已有上传任务进行中，请完成后再试，或发送 .dav cancel")
		}
		upload, cancel := context.WithTimeout(ctx, time.Hour)
		s.active = cancel
		s.activeDone = make(chan struct{})
		done := s.activeDone
		s.mu.Unlock()
		defer func() {
			cancel()
			s.mu.Lock()
			s.active = nil
			s.activeDone = nil
			close(done)
			s.mu.Unlock()
		}()
		return s.upload(upload, r)
	default:
		return r.edit(ctx, help(r.prefix))
	}
}
func (s *service) loadConfig() (Config, error) {
	c, err := s.config.Read()
	if err == nil {
		c, err = validateConfig(c)
	}
	if err != nil {
		return Config{}, errors.New("WebDAV 尚未配置或配置无效，请在收藏夹用 .dav config 配置")
	}
	return c, nil
}
func (s *service) configure(ctx context.Context, r *request) error {
	field := arg(r.args, 1)
	value := r.value
	if value == "" {
		value = strings.Join(r.args[min(2, len(r.args)):], " ")
	}
	if (field == "pass" || field == "cdtoken") && value != "" && r.hide != nil {
		if err := r.hide(ctx); err != nil {
			return errors.New("无法隐藏凭据配置命令，请先手动删除后重试")
		}
	}
	if !r.saved {
		return errors.New("为避免泄露配置，只能在收藏夹中配置 WebDAV")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		return errors.New("上传过程中不能修改配置，请等待完成")
	}
	c, err := s.config.Read()
	if err != nil {
		return errors.New("无法读取 WebDAV 配置")
	}
	if field == "" {
		password := "未设置"
		if c.Password != "" {
			password = "已设置（隐藏）"
		}
		mode := c.UploadMode
		if mode == "" {
			mode = "webdav"
		}
		limit := fmt.Sprintf("%d MiB", c.MaxFileMiB)
		if c.MaxFileMiB == 0 {
			limit = "不限（保留本机磁盘保护）"
		}
		return r.edit(ctx, "🔐 <b>WebDAV 配置</b>\n地址："+command.Escape(c.URL)+"\n用户名："+command.Escape(c.Username)+"\n密码："+password+"\n上传模式："+command.Escape(mode)+"\n单文件上限："+limit+"\n\n"+command.Code(r.prefix+"dav config url https://example.com/dav")+"\n"+command.Code(r.prefix+"dav config user 用户名")+"\n"+command.Code(r.prefix+"dav config pass 密码")+"\n"+command.Code(r.prefix+"dav config limit 0")+"\n\n"+command.Code(r.prefix+"dav config mode webdav")+"\n普通 WebDAV（默认）：整文件上传，受服务端及 CDN 大小限制。\n"+command.Code(r.prefix+"dav config mode clouddrive")+"\nCloudDrive：HTTP 按 80 MB 分片，需支持分片续写；无需 Token。\n两种模式均使用 WebDAV 账号密码；CDN 需放行 WebDAV 请求并关闭该路径的人机验证。\n"+command.Code(r.prefix+"dav test")+" 只读检查连接。\n保存立即生效；凭据经 Telegram 云聊天传输，隐藏命令不能保证清除其他客户端缓存。")
	}
	if value == "" {
		return errors.New("配置值不能为空")
	}
	switch field {
	case "url":
		candidate := c
		candidate.URL = strings.TrimSpace(value)
		candidate.Username = "validate"
		candidate.Password = "validate"
		candidate, err = validateConfig(candidate)
		if err != nil {
			return err
		}
		c.URL = candidate.URL
	case "user":
		if strings.ContainsAny(value, ":\r\n") {
			return errors.New("用户名不能包含冒号或换行")
		}
		c.Username = value
	case "pass":
		c.Password = value
	case "mode", "cdtoken", "cdroot":
		candidate := c
		switch field {
		case "mode":
			candidate.UploadMode = strings.TrimSpace(value)
		case "cdtoken":
			candidate.CloudDriveToken = value
		case "cdroot":
			candidate.CloudDriveRoot = strings.TrimSpace(value)
		}
		// Validate incremental settings before URL and DAV credentials are configured.
		check := candidate
		if check.URL == "" {
			check.URL = "https://config.invalid/dav"
		}
		check.Username, check.Password = "validate", "validate"
		checked, e := validateConfig(check)
		if e != nil {
			return e
		}
		c.UploadMode, c.CloudDriveToken, c.CloudDriveRoot = candidate.UploadMode, candidate.CloudDriveToken, checked.CloudDriveRoot
	case "limit":
		n, e := strconv.ParseInt(value, 10, 64)
		if e != nil || n < 0 || n > 4096 {
			return errors.New("上限需为 0–4096 MiB，0 表示不限")
		}
		c.MaxFileMiB = n
	default:
		return errors.New("支持 url / user / pass / limit / mode / cdtoken / cdroot")
	}
	if err := s.config.Update(func(saved *Config) error { *saved = c; return nil }); err != nil {
		return errors.New("WebDAV 配置保存失败")
	}
	if field == "pass" {
		field = "密码（不回显）"
	}
	if field == "cdtoken" {
		field = "CloudDrive Token（不回显）"
	}
	return r.edit(ctx, "✅ 已保存 "+field+"，无需重启。")
}
func (s *service) list(ctx context.Context, r *request) error {
	db, err := s.records.Read()
	if err != nil {
		return errors.New("无法读取 WebDAV 记录")
	}
	s.mu.Lock()
	now := s.now()
	for key, session := range s.sessions {
		if !now.Before(session.expires) {
			delete(s.sessions, key)
		}
	}
	action := arg(r.args, 0)
	session := s.sessions[r.chatID]
	if action == "list" {
		filter := arg(r.args, 1)
		if len(r.args) > 2 || (filter != "" && !validDate(filter)) {
			s.mu.Unlock()
			return errors.New("用法：.dav list 或 .dav list YYYY-MM-DD（北京时间上传日期）")
		}
		session = &pageSession{page: 1, expires: now.Add(5 * time.Minute), filter: filter}
		for _, row := range db.Uploads {
			if filter == "" || row.Date == filter {
				session.ids = append(session.ids, row.ID)
			}
		}
		sort.Slice(session.ids, func(i, j int) bool { return session.ids[i] > session.ids[j] })
		s.sessions[r.chatID] = session
	} else {
		if session == nil {
			s.mu.Unlock()
			return errors.New("当前聊天的列表已过期或尚未创建，请重新发送 .dav list")
		}
		page := session.page
		if action == "next" {
			page++
		} else if action == "prev" {
			page--
		} else {
			page, _ = strconv.Atoi(arg(r.args, 1))
		}
		pages := max(1, (len(session.ids)+9)/10)
		if page < 1 || page > pages {
			s.mu.Unlock()
			return fmt.Errorf("页码需在 1–%d 之间", pages)
		}
		session.page = page
	}
	copySession := *session
	copySession.ids = append([]int64(nil), session.ids...)
	s.mu.Unlock()
	records := map[int64]Record{}
	for _, row := range db.Uploads {
		records[row.ID] = row
	}
	var rows []string
	start := (copySession.page - 1) * 10
	for _, id := range copySession.ids[start:min(start+10, len(copySession.ids))] {
		row, ok := records[id]
		if ok {
			rows = append(rows, fmt.Sprintf("<code>#%d</code> %s\n%s · %.2f MiB · %s", row.ID, command.Escape(truncate(row.Filename, 70)), row.Date, float64(row.Bytes)/(1<<20), command.Escape(truncate(row.ChatName, 30))))
		}
	}
	if len(rows) == 0 {
		rows = []string{"暂无记录"}
	}
	filter := copySession.filter
	if filter == "" {
		filter = "全部"
	}
	return r.edit(ctx, fmt.Sprintf("☁️ <b>上传记录 · %s</b>\n共 %d 条 · 第 %d/%d 页 · 每页10条\n\n%s\n\n%s / %s / %s\n详情：%s\n剩余 %d 秒，翻页不续期。", filter, len(copySession.ids), copySession.page, max(1, (len(copySession.ids)+9)/10), strings.Join(rows, "\n\n"), command.Code(r.prefix+"dav next"), command.Code(r.prefix+"dav prev"), command.Code(r.prefix+"dav page 页码"), command.Code(r.prefix+"dav info ID"), max(0, int(copySession.expires.Sub(now).Seconds()))))
}
func truncate(value string, n int) string { r := []rune(value); return string(r[:min(n, len(r))]) }

func (s *service) upload(ctx context.Context, r *request) error {
	c, err := s.loadConfig()
	if err != nil {
		return err
	}
	message, err := r.reply(ctx)
	if err != nil {
		return errors.New("无法读取回复的媒体消息")
	}
	if message == nil || message.Raw == nil {
		return errors.New("请回复一条图片、视频或文件消息后发送 .dav")
	}
	source, ok := bot.SourceOf(message.Raw)
	if !ok {
		return errors.New("请回复一条图片、视频、语音或文件消息；暂不处理纯文本和整组相册")
	}
	limit := int64(0)
	if c.MaxFileMiB > 0 {
		limit = c.MaxFileMiB << 20
	}
	if source.Size < 0 || (limit > 0 && source.Size > limit) {
		return errors.New("文件超过配置上限")
	}
	target := fmt.Sprintf("%x", sha256.Sum256([]byte(c.URL+"\x00"+c.Username)))
	name := r.name(message)
	d := davClient{c, s.http}
	_, channel := message.Peer.(*tg.PeerChannel)
	_, group := message.Peer.(*tg.PeerChat)
	folder, db, err := s.chatFolder(ctx, &d, message.ChatID, name, target, channel || group)
	if err != nil {
		return err
	}
	if name == "" {
		name = "会话"
	}
	for i := len(db.Uploads) - 1; i >= 0; i-- {
		row := db.Uploads[i]
		if row.Target == target && row.ChatID == message.ChatID && row.MessageID == message.ID {
			return r.edit(ctx, fmt.Sprintf("ℹ️ 此消息已有上传记录 #%d（未重新上传）。\n%s", row.ID, command.Code(row.RemotePath)))
		}
	}
	tempRoot := filepath.Join(s.root, "temp")
	if err := os.MkdirAll(tempRoot, 0700); err != nil {
		return errors.New("无法创建部署目录下的临时目录")
	}
	if err := s.checkSpace(tempRoot, max(source.Size, 64<<20)); err != nil {
		return err
	}
	temp, err := os.MkdirTemp(tempRoot, "webdav-")
	if err != nil {
		return errors.New("无法创建本地临时目录")
	}
	defer os.RemoveAll(temp)
	local := filepath.Join(temp, "media")
	file, err := os.OpenFile(local, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return errors.New("无法创建临时媒体文件")
	}
	defer file.Close()
	basename := source.FileName
	if basename == "" {
		ext := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "video/mp4": ".mp4", "video/webm": ".webm", "audio/ogg": ".ogg", "audio/mpeg": ".mp3", "application/x-tgsticker": ".tgs"}[source.MimeType]
		if ext == "" {
			ext = ".bin"
		}
		kind := "media"
		if source.Photo {
			kind = "photo"
			ext = ".jpg"
		}
		basename = fmt.Sprintf("%s_%d%s", kind, message.ID, ext)
	}
	basename = safeName(basename, 110)
	if err := r.edit(ctx, "⬇️ 正在从 Telegram 下载\n"+command.Code(basename)); err != nil {
		return err
	}
	if err := s.download(ctx, r, source, file, temp, limit); err != nil {
		return err
	}
	stat, err := file.Stat()
	if err != nil {
		return errors.New("无法读取下载文件大小")
	}
	bytes := stat.Size()
	if bytes == 0 || (limit > 0 && bytes > limit) || (source.Size > 0 && bytes != source.Size) {
		return errors.New("Telegram 下载文件大小校验失败")
	}
	if err := s.checkSpace(temp, 0); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return errors.New("无法读取下载文件")
	}
	hash := sha256.New()
	buffer := make([]byte, 64<<10)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, e := file.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return errors.New("下载文件校验失败")
		}
	}
	if err := file.Close(); err != nil {
		return errors.New("关闭临时文件失败")
	}
	date := dateKey(s.now())
	directory := folder + "/" + strings.ReplaceAll(date, "-", "/")
	if err := r.edit(ctx, "⬆️ 正在创建目录并上传 WebDAV…"); err != nil {
		return err
	}
	if err := d.directory(ctx, directory); err != nil {
		return err
	}
	destination, err := d.destination(ctx, directory, basename, bytes, hash.Sum(nil), target, db.Uploads)
	if err != nil {
		return err
	}
	if c.UploadMode == "clouddrive" {
		err = (cloudDriveClient{&d}).upload(ctx, local, destination, bytes, hash.Sum(nil), func(done, total int64) {
			update, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			parts := (total + cloudDriveChunkSize - 1) / cloudDriveChunkSize
			part := min(parts, (done+cloudDriveChunkSize-1)/cloudDriveChunkSize)
			phase := fmt.Sprintf("⬆️ CloudDrive 分片：%d/%d · 每片最大80 MB\n已确认 %.1f / %.1f MiB", part, parts, float64(done)/(1<<20), float64(total)/(1<<20))
			if done == total {
				phase = fmt.Sprintf("⬆️ 分片写入完成（%d/%d），正在读取远端内容并核验归档…", parts, parts)
			}
			_ = r.edit(update, phase)
		})
	} else {
		err = d.upload(ctx, local, destination, bytes)
	}
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	row := Record{Date: date, ChatID: message.ChatID, ChatName: name, MessageID: message.ID, Filename: basename, RemotePath: destination, Bytes: bytes, SHA256: fmt.Sprintf("%x", hash.Sum(nil)), CreatedAt: s.now().UTC().Format(time.RFC3339Nano), Target: target}
	if err := s.records.Update(func(db *Records) error {
		for _, prev := range db.Uploads {
			row.ID = max(row.ID, prev.ID)
		}
		row.ID++
		db.Uploads = append(db.Uploads, row)
		return nil
	}); err != nil {
		return errors.New("远端已上传并核验，但本地记录保存失败，请检查磁盘；请勿直接重复上传")
	}
	verified := "远端文件大小"
	if c.UploadMode == "clouddrive" {
		verified = "远端文件大小及 SHA256"
	}
	if err := r.edit(ctx, fmt.Sprintf("✅ 已上传并核验%s\n记录 <code>#%d</code> · %.2f MiB\n%s", verified, row.ID, float64(bytes)/(1<<20), command.Code("/"+destination))); err != nil {
		return err
	}
	if r.notice != nil {
		r.notice(ctx)
	}
	return nil
}
func (s *service) checkSpace(path string, remaining int64) error {
	available, err := s.space(path)
	if err != nil {
		return err
	}
	if available < headroom+max(0, remaining) {
		return errors.New("磁盘空间不足，已停止下载（预留256 MiB）")
	}
	return nil
}
func (s *service) download(ctx context.Context, r *request, source *bot.MediaSource, file *os.File, temp string, limit int64) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.download(ctx, source, file) }()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	last := s.now()
	check := func() error {
		stat, err := file.Stat()
		if err != nil {
			return errors.New("无法检查下载大小")
		}
		if limit > 0 && stat.Size() > limit {
			return errors.New("文件超过配置上限")
		}
		if err := s.checkSpace(temp, max(0, source.Size-stat.Size())); err != nil {
			return err
		}
		return nil
	}
	for {
		select {
		case err := <-done:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return errors.New("Telegram 下载失败")
			}
			return check()
		case <-ctx.Done():
			cancel()
			<-done
			return ctx.Err()
		case <-ticker.C:
			if err := check(); err != nil {
				cancel()
				<-done
				return err
			}
			if s.now().Sub(last) >= 5*time.Second {
				last = s.now()
				stat, err := file.Stat()
				if err == nil {
					progress, stop := context.WithTimeout(ctx, 3*time.Second)
					_ = r.edit(progress, fmt.Sprintf("⬇️ 下载中 · %.1f MiB", float64(stat.Size())/(1<<20)))
					stop()
				}
			}
		}
	}
}

// shutdown stops a running upload and waits until its downloader, file and HTTP
// streams have all closed. It is idempotent and rejects subsequent commands.
func (s *service) shutdown() {
	s.mu.Lock()
	if s.closing {
		done := s.activeDone
		s.mu.Unlock()
		if done != nil {
			<-done
		}
		return
	}
	s.closing = true
	cancel, done := s.active, s.activeDone
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}
