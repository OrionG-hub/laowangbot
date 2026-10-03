package eatgif

// 这一份把「生成一次」变成「生成一次然后留着」：编码好的贴纸按
// 「吃人的头像 + 被吃的头像 + 动画」落盘，头像一换键就变，旧成品自然不会被误用。
// 同时把命令的中间状态去掉——指令立刻删掉，动图在后台跑完再回复发出。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"github.com/OrionG-hub/laowangbot/internal/app"
	"github.com/OrionG-hub/laowangbot/internal/bot"
	"github.com/OrionG-hub/laowangbot/internal/command"
	"github.com/OrionG-hub/laowangbot/internal/store"
)

// renderFormat 是缓存键的格式版本。合成或编码的参数一变，成品就和老键对不上了，
// 把它升到 r2 就能让旧条目自然不可达，不用写失效逻辑。
const renderFormat = "r1"

// 成品的上限。这个程序的宗旨是保持轻量，而一个动图贴纸通常 100～600 KiB，
// 几十条足够覆盖「来来回回总是那几个人那几款」。
const (
	renderMaxEntries = 48       // 每个素材目录各自最多留这么多对
	renderMaxBytes   = 32 << 20 // 32 MiB
	renderMaxAge     = 30 * 24 * time.Hour
	renderQueueSize  = 8 // 后台队列最多排这么多条命令
	renderBudget     = 5 * time.Minute
	peersMaxEntries  = 200      // peers.json 里最多记这么多人/频道
	webmCacheLimit   = 20 << 20 // 和 media.StickerWebM 的 readBounded 上限一致
	webpCacheLimit   = 5 << 20  // 和 media.StickerWebP 一致
	peersFileName    = "peers.json"
	reportBudget     = 15 * time.Second
)

// renderMeta 是重发一次成品需要的全部信息。命中路径不重算任何尺寸或时长，
// 读回字节、照这份参数发出去就行。
type renderMeta struct {
	Kind     string  `json:"kind"` // "eatgif" | "eat"
	Name     string  `json:"name"` // Telegram 记录的文件名
	MimeType string  `json:"mime"`
	Alt      string  `json:"alt,omitempty"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Seconds  float64 `json:"seconds,omitempty"` // 视频贴纸的时长，静态图为 0
	Digest   string  `json:"digest,omitempty"`  // 生成时那份动画定义的摘要
	Peers    string  `json:"peers,omitempty"`   // "me=…|you=…"，只给 .eatgif cache 回显
	Bytes    int64   `json:"bytes"`
	MadeAt   int64   `json:"made_at"`
}

// renderTarget 是一次生成的身份：缓存键、落在哪个目录、什么后缀，
// 以及动图需要的定义文件（specRel）。canonical 为空表示这次认不出头像号，
// 既不读也不写缓存。
type renderTarget struct {
	canonical string
	directory string
	ext       string
	specRel   string
	peers     string
	limit     int64
}

// renderJob 是一项排在后台队列里的生成。只带身份和标签，不带图片：
// 排队的命令再多，占的也只是几百字节。
type renderJob struct {
	target  renderTarget
	label   string // 动画描述，只用在失败的那句话里
	chatID  string
	replyTo int
	me      tg.InputPeerClass
	you     tg.InputPeerClass
}

// peerPhotos 是「这个 peer 现在用的是哪张头像」需要的两样东西：缓存起来的实体，
// 和本账号自己。单独拎出来是为了能在没有连接的情况下测——头像号是整套缓存的键，
// 它错了不会报错，只会安静地把别人的脸当成这个人的。
type peerPhotos struct {
	peers *bot.PeerCache
	self  *tg.User
}

// facesOf 从一条连接上取这两样。
func facesOf(client *bot.Client) peerPhotos {
	if client == nil {
		return peerPhotos{}
	}
	return peerPhotos{peers: client.Peers(), self: client.Self()}
}

// animateWorker 是队列需要的两件活，由 eatgifService 提供。
// 分开成两步是为了让队列能拿着「这次实际用的定义」再精确查一次缓存。
type animateWorker interface {
	// spec 读出动画定义（磁盘副本过期就重下），并给出它的摘要。
	spec(ctx context.Context, relative string) (eatgifSpec, string, error)
	// render 下载双方头像、逐帧合成、编码成贴纸字节。
	render(ctx context.Context, client *bot.Client, job *renderJob, definition eatgifSpec, digest string) ([]byte, renderMeta, error)
}

// renderLane 是成品缓存，也是动图的后台队列。一个消费者，所以同时最多一个 ffmpeg
// ——这和以前那条「一次只跑一个」的闸门是同一个理由，只是不再让命令干等。
type renderLane struct {
	a      *app.App
	queue  chan *renderJob
	peers  *store.Store[eatPeers]
	worker animateWorker
}

func newRenderLane(a *app.App) *renderLane {
	return &renderLane{
		a:     a,
		queue: make(chan *renderJob, renderQueueSize),
		// 记录写在 data/eatgif/ 里面：备份有意不带 data/ 的子目录（internal/backup/backup.go），
		// 缓存和记录都不该跟着配置一起被归档。
		peers: store.New(filepath.Join(a.DataDir(), "eatgif", peersFileName), func() eatPeers {
			return eatPeers{Peers: map[string]eatPeer{}}
		}),
	}
}

func (l *renderLane) log() *slog.Logger {
	if l.a == nil || l.a.Logger == nil {
		return slog.Default()
	}
	return l.a.Logger
}

// enqueue 把一项生成排进队列；队列满了返回 false，让命令自己提示一句。
func (l *renderLane) enqueue(job *renderJob) bool {
	select {
	case l.queue <- job:
		return true
	default:
		return false
	}
}

// run 是后台消费者。结构照搬插件运行时（internal/extensions/extensions.go:369）：
// 收连接级的 ctx 和这条连接自己的 client，断线重连时旧消费者随 ctx 退出、新的接上，
// 所以活着的消费者永远只有一个，用的也永远是当下能发出去的那个 client。
// 用连接级 ctx 而不是 da.go 那种 WithoutCancel，是因为退出时靠 ctx 取消杀掉 ffmpeg
// 子进程（internal/app/app.go 的 stopCommands 注释），脱离 ctx 的任务会留下孤儿进程。
func (l *renderLane) run(ctx context.Context, client *bot.Client) {
	for {
		select {
		case <-ctx.Done():
			// 缓冲里剩下的是再没人来取的：断线重连会有新消费者接上，进程退出就不会了，
			// 只在真的没有 ctx 可依赖时说一声。
			if pending := len(l.queue); pending > 0 {
				l.log().Warn("eatgif.queue_dropped", slog.Int("jobs", pending))
			}
			return
		case job := <-l.queue:
			l.do(ctx, client, job)
		}
	}
}

// do 跑完一项生成：先查缓存，没有才合成，然后落盘并发出去。
// 任何一步失败都只回报一条短消息，不重试。
func (l *renderLane) do(ctx context.Context, client *bot.Client, job *renderJob) {
	defer func() {
		if recovered := recover(); recovered != nil {
			// 分发器的 recover 只包着 handler，后台这一步得自己兜住。
			l.log().Error("eatgif.render_panic", slog.Any("panic", recovered))
			l.fail(ctx, client, job, errors.New("生成时发生内部错误"))
		}
	}()
	runCtx, cancel := context.WithTimeout(ctx, renderBudget)
	defer cancel()

	if job.target.specRel == "" {
		l.fail(runCtx, client, job, errors.New("缺少动画定义"))
		return
	}
	definition, digest, err := l.worker.spec(runCtx, job.target.specRel)
	if err != nil {
		l.fail(runCtx, client, job, err)
		return
	}
	// 出队时再查一次，用的就是这次真正会用到的定义：
	// 重复的命令在这里自动折叠，素材仓库更新过也不会再拿旧成品糊上去。
	if data, meta, ok := l.lookup(job.target, digest); ok {
		l.send(runCtx, client, job, data, meta, true)
		return
	}
	data, meta, err := l.worker.render(runCtx, client, job, definition, digest)
	if err != nil {
		l.fail(runCtx, client, job, err)
		return
	}
	l.cache(job, data, meta)
	l.send(runCtx, client, job, data, meta, false)
}

// fail 记一条失败并通知聊天。连接已经断了就只记日志：那时发不出去，
// 而且「重启把在跑的任务掐了」也不是要向群里汇报的事。
func (l *renderLane) fail(ctx context.Context, client *bot.Client, job *renderJob, reason error) {
	if ctx.Err() != nil {
		l.log().Info("eatgif.render_aborted", slog.String("animation", job.label), slog.String("reason", ctx.Err().Error()))
		return
	}
	l.log().Error("eatgif.render_failed", slog.String("animation", job.label), slog.String("error", reason.Error()))
	l.report(ctx, client, job, reason)
}

// cache 把成品和它的元数据成对写进磁盘，顺手淘汰超限的旧条目。
// 写失败不影响这一次发送：缓存是省时间的，不是必需的。
func (l *renderLane) cache(job *renderJob, data []byte, meta renderMeta) {
	if job.target.canonical == "" {
		return
	}
	if err := l.storeRender(job.target, data, meta); err != nil {
		l.log().Warn("eatgif.render_store_failed", slog.String("error", err.Error()))
		return
	}
	if removed := pruneRender(l.a, job.target.directory); removed > 0 {
		l.log().Info("eatgif.render_pruned", slog.Int("removed", removed))
	}
}

// send 把成品发成贴纸，并记下这次是命中还是新生成。
func (l *renderLane) send(ctx context.Context, client *bot.Client, job *renderJob, data []byte, meta renderMeta, hit bool) {
	peer, err := client.InputPeerFromChatID(job.chatID)
	if err != nil {
		l.report(ctx, client, job, err)
		return
	}
	if err := sendSticker(ctx, client, peer, data, meta, job.replyTo); err != nil {
		l.report(ctx, client, job, err)
		return
	}
	if hit {
		l.log().Info("eatgif.render_hit", slog.String("animation", job.label), slog.Int("bytes", len(data)))
	} else {
		l.log().Info("eatgif.render_sent", slog.String("animation", job.label), slog.Int("bytes", len(data)))
	}
	l.note(facesOf(client), []tg.InputPeerClass{job.me, job.you}, !hit)
}

// report 把失败说成一条新消息。命令消息在入队时就删了，没有可以改写的落点，
// 所以宁可在同一个对话里多发一行，也不能让它静悄悄地什么也没有。
// 发不出去就只记日志，不重试。
func (l *renderLane) report(ctx context.Context, client *bot.Client, job *renderJob, reason error) {
	if client == nil {
		return
	}
	peer, err := client.InputPeerFromChatID(job.chatID)
	if err != nil {
		l.log().Warn("eatgif.report_failed", slog.String("error", err.Error()))
		return
	}
	// 脱离命令的 ctx：报失败的时候连接可能正在关，这里只给自己 15 秒。
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), reportBudget)
	defer cancel()
	text := "❌ " + command.Escape(job.label) + " 生成失败：" + command.Escape(command.Brief(reason))
	if _, err := client.SendHTML(detached, peer, text, bot.SendOptions{ReplyTo: job.replyTo}); err != nil {
		l.log().Warn("eatgif.report_failed", slog.String("error", err.Error()))
	}
}

// lookup 找出这个目标已经生成好的成品。动图还要求动画定义没变过：digest 是调用方
// 这次实际用的定义摘要，给不出（磁盘副本缺失或刚过期）时传 ""，这一次就当作未命中——
// 宁可重新生成一次，也不能拿旧定义做出来的图去对新定义。
func (l *renderLane) lookup(target renderTarget, digest string) ([]byte, renderMeta, bool) {
	if target.canonical == "" {
		return nil, renderMeta{}, false
	}
	blob, metaPath := renderPaths(l.a, target.directory, target.canonical, target.ext)
	data, ok := readCache(blob, target.limit)
	if !ok {
		return nil, renderMeta{}, false
	}
	raw, ok := readCache(metaPath, 1<<20)
	if !ok {
		return nil, renderMeta{}, false
	}
	var meta renderMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, renderMeta{}, false
	}
	if target.specRel != "" && (digest == "" || digest != meta.Digest) {
		return nil, renderMeta{}, false
	}
	// mtime 就是淘汰用的时钟：命中一次就把它挪回最新。
	touchRender(blob, metaPath)
	return data, meta, true
}

// storeRender 原子地写出成品和元数据这一对。
func (l *renderLane) storeRender(target renderTarget, data []byte, meta renderMeta) error {
	blob, metaPath := renderPaths(l.a, target.directory, target.canonical, target.ext)
	if err := writeCacheFile(blob, data); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(meta, "", " ")
	if err != nil {
		return err
	}
	return writeCacheFile(metaPath, append(encoded, '\n'))
}

// note 记下这两个 peer 这次的头像号。和上次不同就是换了头像：记下来、写一条日志，
// 不在聊天里说——重新生成这件事本身就是看得见的结果。
func (l *renderLane) note(p peerPhotos, peers []tg.InputPeerClass, made bool) {
	if l.peers == nil {
		return
	}
	now := time.Now().UnixMilli()
	changed := []string{}
	err := l.peers.Update(func(document *eatPeers) error {
		if document.Peers == nil {
			document.Peers = map[string]eatPeer{}
		}
		for _, peer := range peers {
			if peer == nil {
				continue
			}
			identity, photoID, name := p.identity(peer)
			if identity == "" {
				continue
			}
			entry := document.Peers[identity]
			entry.Peer, entry.Name = identity, name
			if photoID != 0 {
				if entry.PhotoID != 0 && entry.PhotoID != photoID {
					entry.ChangedAt = now
					changed = append(changed, identity)
				}
				entry.PhotoID = photoID
			}
			if entry.FirstAt == 0 {
				entry.FirstAt = now
			}
			entry.LastAt = now
			if made {
				entry.Renders++
			} else {
				entry.Hits++
			}
			document.Peers[identity] = entry
		}
		prunePeers(document)
		return nil
	})
	if err != nil {
		l.log().Warn("eatgif.peers_failed", slog.String("error", err.Error()))
		return
	}
	for _, identity := range changed {
		l.log().Info("eatgif.avatar_changed", slog.String("peer", identity))
	}
}

// cachePanel 是 .eatgif cache 的内容：缓存占了多少，记着谁，谁换过头像。
func (l *renderLane) cachePanel(p peerPhotos) string {
	lines := []string{"<b>头像动图缓存</b>", ""}
	for _, directory := range []string{"eatgif", "eat"} {
		pairs, bytes := renderUsage(l.a, directory)
		lines = append(lines, "• "+command.Code(directory)+"/render："+
			command.Bold(strconv.Itoa(pairs))+" 个成品，"+formatBytes(bytes))
	}
	document, err := l.peers.Read()
	if err != nil {
		return strings.Join(lines, "\n") + "\n\n记录读不出来：" + command.Escape(err.Error())
	}
	entries := make([]eatPeer, 0, len(document.Peers))
	for _, entry := range document.Peers {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].LastAt != entries[j].LastAt {
			return entries[i].LastAt > entries[j].LastAt
		}
		return entries[i].Peer < entries[j].Peer
	})
	if len(entries) > 20 {
		entries = entries[:20]
	}
	lines = append(lines, "", "<b>最近记录</b>（"+strconv.Itoa(len(entries))+" 条）")
	if len(entries) == 0 {
		lines = append(lines, "还没有记录。生成一次就会记下对方 id 和当时的头像号。")
	}
	for _, entry := range entries {
		current, known := p.currentPhoto(entry.Peer)
		state := "没换过"
		if entry.ChangedAt != 0 {
			state = "换过户 " + formatSince(entry.ChangedAt)
		}
		switch {
		case !known:
			state += "（当前头像号未知）"
		case entry.PhotoID != 0 && current != entry.PhotoID:
			state = "已换新头像，下次生成会重做"
		}
		lines = append(lines, "• "+command.Escape(entry.Name)+" <code>"+command.Escape(entry.Peer)+"</code>"+
			" 头像号 "+strconv.FormatInt(entry.PhotoID, 10)+
			"　生成 "+strconv.Itoa(entry.Renders)+" 次，命中 "+strconv.Itoa(entry.Hits)+" 次\n"+
			"　　"+command.Escape(state))
	}
	return strings.Join(lines, "\n")
}

// currentPhoto 按记录里的 id 反查当前头像号；重启后还没见过这个 peer 时 known 为 false。
func (p peerPhotos) currentPhoto(identity string) (int64, bool) {
	if p.peers == nil {
		return 0, false
	}
	if identity == "self" {
		photoID := p.selfPhoto()
		return photoID, photoID != 0
	}
	kind, rest, found := strings.Cut(identity, ":")
	id, err := strconv.ParseInt(rest, 10, 64)
	if !found || err != nil {
		return 0, false
	}
	switch kind {
	case "user":
		if info, known := p.peers.User(id); known && info.PhotoID != 0 {
			return info.PhotoID, true
		}
	case "channel":
		if info, known := p.peers.Channel(id); known && info.PhotoID != 0 {
			return info.PhotoID, true
		}
	case "chat":
		if info, known := p.peers.Chat(id); known && info.PhotoID != 0 {
			return info.PhotoID, true
		}
	}
	return 0, false
}

// eatPeer 是记录下来的一个对象：最后一次生成时看到的头像号，加上有没有换过、用了几次。
type eatPeer struct {
	Peer      string `json:"peer"`
	Name      string `json:"name,omitempty"`
	PhotoID   int64  `json:"photo_id"`
	FirstAt   int64  `json:"first_at"`
	LastAt    int64  `json:"last_at"`
	ChangedAt int64  `json:"changed_at,omitempty"`
	Renders   int    `json:"renders"`
	Hits      int    `json:"hits"`
}

type eatPeers struct {
	Peers map[string]eatPeer `json:"peers"`
}

// prunePeers 把记录控制在上限内：超了就丢最久没用过的。
func prunePeers(document *eatPeers) {
	if len(document.Peers) <= peersMaxEntries {
		return
	}
	entries := make([]eatPeer, 0, len(document.Peers))
	for _, entry := range document.Peers {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].LastAt != entries[j].LastAt {
			return entries[i].LastAt < entries[j].LastAt
		}
		return entries[i].Peer < entries[j].Peer
	})
	for _, entry := range entries[:len(entries)-peersMaxEntries] {
		delete(document.Peers, entry.Peer)
	}
}

// renderKey 拼出一次生成的规范身份。任何一侧认不出头像号时返回 false——
// 这一次既不读也不写缓存，绝不能把别人的脸当成这个人的。
func renderKey(parts ...string) (string, bool) {
	for _, part := range parts {
		if part == "" {
			return "", false
		}
	}
	return strings.Join(parts, "|"), true
}

// key 给出 peer 当前头像的身份：id 加上 Telegram 给的 photo id。
// 换头像就是换 photo id，键随之改变，所以旧的成品不可能被误用，失效由键本身完成。
// 和 .yvlu 的 avatarKey 同源（yvlu.go:983），多了旧式普通群这一支。
// 拿不到 photo id（重启后还没见过这个 peer、或对方没有公开头像）时返回空串，
// 调用方既不读也不写缓存，和 yvlu 的处理一致。
func (p peerPhotos) key(peer tg.InputPeerClass) string {
	identity, photoID, _ := p.identity(peer)
	if identity == "" || photoID == 0 {
		return ""
	}
	return identity + ":" + strconv.FormatInt(photoID, 10)
}

// identity 给出 peer 的 id、当前头像号和显示名。头像号为 0 表示这次没看到它的头像。
func (p peerPhotos) identity(peer tg.InputPeerClass) (string, int64, string) {
	if p.peers == nil || peer == nil {
		return "", 0, ""
	}
	switch value := peer.(type) {
	case *tg.InputPeerSelf:
		return "self", p.selfPhoto(), "本账号"
	case *tg.InputPeerUser:
		identity := "user:" + strconv.FormatInt(value.UserID, 10)
		if info, known := p.peers.User(value.UserID); known {
			return identity, info.PhotoID, info.DisplayName()
		}
		return identity, 0, ""
	case *tg.InputPeerChannel:
		identity := "channel:" + strconv.FormatInt(value.ChannelID, 10)
		if info, known := p.peers.Channel(value.ChannelID); known {
			return identity, info.PhotoID, info.Title
		}
		return identity, 0, ""
	case *tg.InputPeerChat:
		identity := "chat:" + strconv.FormatInt(value.ChatID, 10)
		if info, known := p.peers.Chat(value.ChatID); known {
			return identity, info.PhotoID, info.Title
		}
		return identity, 0, ""
	}
	return "", 0, ""
}

// selfPhoto 是本账号当前的头像号。它来自建连时固化的 self，
// 所以机主自己换头像要等重连或 .restart 才认得出来。
func (p peerPhotos) selfPhoto() int64 {
	if p.self == nil {
		return 0
	}
	photo, ok := p.self.GetPhoto()
	if !ok {
		return 0
	}
	if current, ok := photo.(*tg.UserProfilePhoto); ok {
		return current.PhotoID
	}
	return 0
}

// repliedMediaKey 认出被回复消息里那张图的身份（.eat2 用）。媒体 id 不会变，
// 所以它是一份永久有效的身份；认不出来时返回 ""，这一次不进缓存。
func repliedMediaKey(message *bot.Message) string {
	if message == nil || message.Raw == nil {
		return ""
	}
	source := message.Raw
	if wrapped, ok := webPageMedia(message.Raw); ok {
		source = wrapped
	}
	content, ok := source.GetMedia()
	if !ok {
		return ""
	}
	prefix := "media:" + message.ChatID + ":" + strconv.Itoa(source.ID) + ":"
	switch value := content.(type) {
	case *tg.MessageMediaPhoto:
		if photo, ok := value.Photo.(*tg.Photo); ok {
			return prefix + "photo" + strconv.FormatInt(photo.ID, 10)
		}
	case *tg.MessageMediaDocument:
		if document, ok := value.Document.(*tg.Document); ok {
			return prefix + "doc" + strconv.FormatInt(document.ID, 10)
		}
	}
	return ""
}

// renderPaths 是成品和元数据在 data/<directory>/render/ 下的两个路径：同一个键只哈希一次。
func renderPaths(a *app.App, directory, canonical, ext string) (blob, meta string) {
	digest := sha256.Sum256([]byte(canonical))
	base := hex.EncodeToString(digest[:])
	dir := filepath.Join(a.DataDir(), directory, "render")
	return filepath.Join(dir, base+ext), filepath.Join(dir, base+".json")
}

// writeCacheFile 原子地写一个缓存文件：同目录的临时文件改名到位，
// 同时执行的两条命令绝不能读到写了一半的内容。
func writeCacheFile(cache string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(cache), 0o700); err != nil {
		return err
	}
	temporary := cache + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, cache)
}

// touchRender 把命中过的成品对挪回「最新」：mtime 就是淘汰的时钟。
func touchRender(paths ...string) {
	now := time.Now()
	for _, path := range paths {
		_ = os.Chtimes(path, now, now)
	}
}

// digestBytes 给出内容的摘要前 16 个十六进制位，够长也够短。
func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

// renderPair 是磁盘上的一对：成品、元数据、体积和最近使用时间。
type renderPair struct {
	files []string
	size  int64
	mod   time.Time
}

// scanRender 列出 data/<directory>/render 下的成品对。
func scanRender(a *app.App, directory string) []renderPair {
	dir := filepath.Join(a.DataDir(), directory, "render")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	byBase := map[string]*renderPair{}
	var order []string
	for _, entry := range entries {
		name := entry.Name()
		info, err := entry.Info()
		if err != nil || entry.IsDir() {
			continue
		}
		// 改名失败时留下的临时文件，扫到就顺手清掉。
		if strings.HasSuffix(name, ".tmp") {
			_ = os.Remove(filepath.Join(dir, name))
			continue
		}
		base := name
		if index := strings.Index(name, "."); index > 0 {
			base = name[:index]
		}
		pair, ok := byBase[base]
		if !ok {
			pair = &renderPair{mod: info.ModTime()}
			byBase[base] = pair
			order = append(order, base)
		}
		pair.files = append(pair.files, filepath.Join(dir, name))
		// 同名的两个文件里，成品远大于元数据，取大的就是这一对的体积。
		if info.Size() > pair.size {
			pair.size = info.Size()
		}
		if info.ModTime().After(pair.mod) {
			pair.mod = info.ModTime()
		}
	}
	sort.Slice(order, func(i, j int) bool {
		left, right := byBase[order[i]], byBase[order[j]]
		if !left.mod.Equal(right.mod) {
			return left.mod.Before(right.mod)
		}
		return order[i] < order[j]
	})
	pairs := make([]renderPair, 0, len(order))
	for _, base := range order {
		pairs = append(pairs, *byBase[base])
	}
	return pairs
}

// pruneRender 把最久没用的成品删掉，直到条数、体积和年龄都回到上限内。
// 成对删除：删 json 时同名的成品一起删。
//
// scanRender 已经按 mtime 从旧到新排好，所以从最新那端往回留：一旦某一条塞不进
// 上限，比它更旧的每一条也塞不进（数量、体积、年龄都单调），整段删掉就够，
// 不用回头再排一次。
func pruneRender(a *app.App, directory string) int {
	pairs := scanRender(a, directory)
	removed := 0
	var total int64
	kept := 0
	for index := len(pairs) - 1; index >= 0; index-- {
		pair := pairs[index]
		// 孤儿元数据：成品没写成（或者被手动删了），留着也没用。
		if len(pair.files) == 1 && strings.HasSuffix(pair.files[0], ".json") {
			_ = os.Remove(pair.files[0])
			removed++
			continue
		}
		if kept+1 > renderMaxEntries || total+pair.size > renderMaxBytes || time.Since(pair.mod) > renderMaxAge {
			for _, older := range pairs[:index+1] {
				for _, path := range older.files {
					_ = os.Remove(path)
				}
				removed++
			}
			return removed
		}
		kept++
		total += pair.size
	}
	return removed
}

// renderUsage 报告某个目录下成品的对数和总体积。
func renderUsage(a *app.App, directory string) (int, int64) {
	var total int64
	pairs := scanRender(a, directory)
	for _, pair := range pairs {
		total += pair.size
	}
	return len(pairs), total
}

// stickerAttributes 是发一张贴纸要带的属性：贴纸属性、图片尺寸，动图再多一个视频属性。
// 和现场生成时发出去的那一份逐字段一致，所以命中和未命中在 Telegram 看来没有区别。
func stickerAttributes(meta renderMeta) []tg.DocumentAttributeClass {
	attributes := []tg.DocumentAttributeClass{
		&tg.DocumentAttributeSticker{Alt: meta.Alt, Stickerset: &tg.InputStickerSetEmpty{}},
		&tg.DocumentAttributeImageSize{W: meta.Width, H: meta.Height},
	}
	if meta.Seconds > 0 {
		// 原插件经 teleproto 按 .webm 文件发送，它会自动加上视频属性；
		// Telegram 自己的客户端发视频贴纸也带这一项，宽高和时长都是实际值。
		attributes = append(attributes, &tg.DocumentAttributeVideo{W: meta.Width, H: meta.Height, Duration: meta.Seconds})
	}
	return attributes
}

// sendSticker 用元数据里的参数原样发一份成品。
func sendSticker(ctx context.Context, client *bot.Client, peer tg.InputPeerClass, data []byte, meta renderMeta, replyTo int) error {
	return client.SendDocumentWith(ctx, peer, data, bot.DocumentOptions{
		Name: meta.Name, MimeType: meta.MimeType, ReplyTo: replyTo, Attributes: stickerAttributes(meta),
	})
}

// clearCacheDir 清空一个素材目录，但保留 peers.json：那是「谁换过头像」的记录，
// 不是下次能重新下载回来的素材。
func clearCacheDir(root string) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == peersFileName {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// formatBytes 把字节数说成人话。
func formatBytes(value int64) string {
	switch {
	case value >= 1<<20:
		return strconv.FormatFloat(float64(value)/(1<<20), 'f', 1, 64) + " MiB"
	case value >= 1<<10:
		return strconv.FormatFloat(float64(value)/(1<<10), 'f', 1, 64) + " KiB"
	default:
		return strconv.FormatInt(value, 10) + " B"
	}
}

// formatSince 把「多少时间以前」说成 3 天前、2 小时前这样的说法。
func formatSince(millis int64) string {
	value := time.Since(time.UnixMilli(millis))
	switch {
	case value >= 24*time.Hour:
		return strconv.Itoa(int(value.Hours()/24)) + " 天前"
	case value >= time.Hour:
		return strconv.Itoa(int(value.Hours())) + " 小时前"
	case value >= time.Minute:
		return strconv.Itoa(int(value.Minutes())) + " 分钟前"
	default:
		return "刚刚"
	}
}
