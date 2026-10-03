// Package eatgif 实现 .eatgif 和 .eat / .eat2：把头像合成表情贴纸，前者是动图，
// 后者是静态图。两者的素材都来自 TeleBox 插件仓库，头像的摆放规则也一样。
package eatgif

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tg"

	"github.com/OrionG-hub/laowangbot/internal/app"
	"github.com/OrionG-hub/laowangbot/internal/bot"
	"github.com/OrionG-hub/laowangbot/internal/command"
	"github.com/OrionG-hub/laowangbot/internal/commands/kit"
	"github.com/OrionG-hub/laowangbot/internal/httpx"
	"github.com/OrionG-hub/laowangbot/internal/imaging"
	"github.com/OrionG-hub/laowangbot/internal/media"
)

// 动画素材放在 TeleBox 插件仓库里：一份按名字列出各个动画的目录；每个动画
// 有一份列出各帧的 JSON 定义；每一帧有一张底图（PNG 或 JPEG），外加两个头像
// 贴上去时要套的遮罩。图片第一次用过之后就一直缓存在磁盘上；目录和定义会更新，
// 缓存超过 docRefresh 就重新下载。
const eatgifRoot = "https://raw.githubusercontent.com/TeleBoxOrg/TeleBox-Plugins/main/eatgif/"

// eatgifMaxFrames 是一个动画最多接受的帧数。现有素材最多的 ddw 有 88 帧；
// 帧是逐张合成、逐张写盘的，内存里同时只有一帧，所以上限只用来挡住异常的定义。
const eatgifMaxFrames = 200

// docRefresh 是素材目录和动画定义在本地缓存的有效期。原插件每次启动重新读目录、
// 每次生成重新读定义；这里一天刷新一次，新增的动画最迟第二天出现，
// .eatgif clear 可以立刻刷新。
const docRefresh = 24 * time.Hour

// eatgifRole 描述一个头像在某一帧里的摆放方式。
type eatgifRole struct {
	X          int      `json:"x"`
	Y          int      `json:"y"`
	Mask       string   `json:"mask"`
	Rotate     *float64 `json:"rotate,omitempty"`
	Brightness *float64 `json:"brightness,omitempty"`
}

type eatgifFrame struct {
	URL   string      `json:"url"`
	Delay *int        `json:"delay,omitempty"`
	Me    *eatgifRole `json:"me,omitempty"`
	You   *eatgifRole `json:"you,omitempty"`
}

type eatgifSpec struct {
	Width  int           `json:"width"`
	Height int           `json:"height"`
	Frames []eatgifFrame `json:"res"`
}

type eatgifEntry struct {
	URL  string `json:"url"`
	Desc string `json:"desc"`
}

type eatgifService struct {
	a       *app.App
	lane    *renderLane
	mu      sync.Mutex
	catalog map[string]eatgifEntry
	// catalogAt 是 catalog 读进内存的时间，超过 docRefresh 就重读。
	catalogAt time.Time
}

// safeRelative 拒绝可能跑出缓存目录或素材根路径的素材路径。这里的每个路径
// 都来自远程的 JSON 文档，属于不可信输入。
func safeRelative(value string) (string, error) {
	if value == "" || strings.Contains(value, "\\") || strings.Contains(value, "://") {
		return "", kit.Fail("素材路径无效")
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return "", kit.Fail("素材路径无效")
		}
	}
	return value, nil
}

// asset 获取远程素材，磁盘上已有副本时直接复用。
func (s *eatgifService) asset(ctx context.Context, relative string, limit int64) ([]byte, error) {
	clean, err := safeRelative(relative)
	if err != nil {
		return nil, err
	}
	return fetchCached(ctx, s.a, "eatgif", clean, eatgifRoot+clean, limit)
}

// cachePath 是 key 在 data/<directory>/ 下的缓存文件：文件名由 key 的哈希加上
// key 的扩展名构成。
func cachePath(a *app.App, directory, key string) string {
	digest := sha256.Sum256([]byte(key))
	return filepath.Join(a.DataDir(), directory, hex.EncodeToString(digest[:])+filepath.Ext(key))
}

// readCache 读缓存文件，不存在、为空或超过 limit 时返回 false。
func readCache(cache string, limit int64) ([]byte, bool) {
	data, err := os.ReadFile(cache)
	if err != nil || len(data) == 0 || int64(len(data)) > limit {
		return nil, false
	}
	return data, true
}

// fetchCached 下载 url 并缓存；磁盘上已有就直接用，不再联网。用于不会改动的图片素材。
func fetchCached(ctx context.Context, a *app.App, directory, key, url string, limit int64) ([]byte, error) {
	cache := cachePath(a, directory, key)
	if data, ok := readCache(cache, limit); ok {
		return data, nil
	}
	return download(ctx, cache, url, limit)
}

// fetchFresh 和 fetchCached 一样缓存，但磁盘副本超过 maxAge 就重新下载。用于会更新的
// 目录和定义。下载失败时退回旧副本：GitHub 一时连不上，不该让已经能用的动画也用不了。
func fetchFresh(ctx context.Context, a *app.App, directory, key, url string, limit int64, maxAge time.Duration) ([]byte, error) {
	cache := cachePath(a, directory, key)
	if info, err := os.Stat(cache); err == nil && time.Since(info.ModTime()) < maxAge {
		if data, ok := readCache(cache, limit); ok {
			return data, nil
		}
	}
	data, err := download(ctx, cache, url, limit)
	if err == nil {
		return data, nil
	}
	if stale, ok := readCache(cache, limit); ok {
		return stale, nil
	}
	return nil, err
}

// download 下载 url 并写进缓存文件 cache。
func download(ctx context.Context, cache, url string, limit int64) ([]byte, error) {
	response, err := httpx.Do(ctx, httpx.Request{URL: url, Timeout: 30 * time.Second, MaxBytes: limit})
	if err != nil {
		return nil, err
	}
	if !response.OK() {
		return nil, &httpx.StatusError{Status: response.Status}
	}
	// 落盘失败不影响这一次：内容已经在手上了，下次再试着缓存。
	_ = writeCacheFile(cache, response.Body)
	return response.Body, nil
}

// assetDocument 读取一份会更新的素材，反序列化到 out，并给出这次实际用到的内容的摘要。
func (s *eatgifService) assetDocument(ctx context.Context, relative string, out any) (string, error) {
	clean, err := safeRelative(relative)
	if err != nil {
		return "", err
	}
	data, err := fetchFresh(ctx, s.a, "eatgif", clean, eatgifRoot+clean, 1<<20, docRefresh)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return "", kit.Fail("素材配置无效")
	}
	return digestBytes(data), nil
}

// assetJSON 读取目录或动画定义。它们会随仓库更新，所以缓存有有效期，见 docRefresh。
func (s *eatgifService) assetJSON(ctx context.Context, relative string, out any) error {
	_, err := s.assetDocument(ctx, relative, out)
	return err
}

func (s *eatgifService) getCatalog(ctx context.Context) (map[string]eatgifEntry, error) {
	s.mu.Lock()
	cached, loaded := s.catalog, s.catalogAt
	s.mu.Unlock()
	if cached != nil && time.Since(loaded) < docRefresh {
		return cached, nil
	}
	catalog := map[string]eatgifEntry{}
	if err := s.assetJSON(ctx, "config.json", &catalog); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.catalog, s.catalogAt = catalog, time.Now()
	s.mu.Unlock()
	return catalog, nil
}

// frameCanvas 解码一帧底图，得到可以往上贴头像的画布。底图有 PNG 也有 JPEG
// （ddw、jbff 两款是 JPEG），按内容判断格式。
func frameCanvas(data []byte) (*image.RGBA, error) {
	decoded, err := imaging.Decode(data)
	if err != nil {
		return nil, err
	}
	return imaging.ToRGBA(decoded), nil
}

// paste 给一个头像套上它的遮罩，再按定义里指定的位置合成到画布上。
func (s *eatgifService) paste(ctx context.Context, canvas *image.RGBA, role *eatgifRole, face image.Image) error {
	maskData, err := s.asset(ctx, role.Mask, 5<<20)
	if err != nil {
		return err
	}
	return pasteWithMask(canvas, role, face, maskData)
}

// pasteWithMask 把头像缩放到遮罩大小，按需旋转、调亮度，套上遮罩后贴到 (X, Y)。
func pasteWithMask(canvas *image.RGBA, role *eatgifRole, face image.Image, maskData []byte) error {
	mask, err := imaging.DecodePNG(maskData)
	if err != nil {
		return err
	}
	bounds := mask.Bounds()
	if bounds.Dx() > 512 || bounds.Dy() > 512 {
		return kit.Fail("素材遮罩尺寸异常")
	}
	shaped := image.Image(imaging.Resize(face, bounds.Dx(), bounds.Dy()))
	if role.Rotate != nil && *role.Rotate != 0 {
		shaped = imaging.Rotate(shaped, clampFloat(*role.Rotate, -360, 360))
	}
	if role.Brightness != nil && *role.Brightness != 1 {
		shaped = imaging.Brightness(shaped, clampFloat(*role.Brightness, 0.1, 2))
	}
	imaging.Composite(canvas, imaging.ApplyMask(shaped, mask), role.X, role.Y)
	return nil
}

func clampFloat(value, low, high float64) float64 {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

// help 是 .eatgif 的帮助：用法，加上当前可用的全部动画。目录读不到时只给用法。
func (s *eatgifService) help(prefix string) string {
	p := command.Escape(prefix)
	text := "🎬 <b>头像动图表情</b>\n\n回复一条消息（用户或频道发的都可以），把双方头像合成为动画贴纸。\n\n" +
		"• 回复一条消息发 <code>" + p + "eatgif 名称</code> 生成\n• <code>" + p +
		"eatgif list</code> 列出全部可用动画\n• <code>" + p +
		"eatgif clear</code> 清空素材缓存\n• <code>" + p + "eatgif cache</code> 看缓存记录（只限本人）\n\n" +
		"同一个人同一款动画生成过一次就存下来，再发秒回；对方换了头像会自动重做一份。\n" +
		"命令发出去就删掉，不在聊天里显示中间状态，动图在后台跑完后回复到原消息上。\n\n"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	catalog, err := s.getCatalog(ctx)
	if err != nil {
		return text + "动画列表暂时读不到（" + command.Escape(httpx.Reason(err)) + "），稍后发 <code>" + p + "eatgif list</code> 查看。"
	}
	names := make([]string, 0, len(catalog))
	for name := range catalog {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := []string{"<b>全部动画</b>（" + strconv.Itoa(len(names)) + " 款）"}
	for _, name := range names {
		lines = append(lines, "• "+command.Code(name)+" "+command.Escape(catalog[name].Desc))
	}
	return text + strings.Join(lines, "\n") + "\n\n素材首次使用时从远程下载并缓存，需要主机装有 ffmpeg。"
}

// Register 注册 .eatgif、.eat 和 .eat2。三者共用一条后台队列和一份头像记录。
func Register(a *app.App) {
	service := &eatgifService{a: a}
	lane := newRenderLane(a)
	service.lane = lane
	lane.worker = service
	registerEat(a, lane)
	a.Registry.AddJob(lane.run)
	a.Registry.Register(&command.Command{Name: "eatgif", Description: "将双方头像合成为动画贴纸", Usage: "名称", Help: service.help, Timeout: 5 * time.Minute,
		Handle: func(ctx context.Context, inv *command.Invocation) error {
			sub := strings.ToLower(inv.Arg(0))
			if sub == "clear" {
				// 素材和成品一起清，但留下谁换过头像的记录：那是事实，不是可重下的素材。
				if err := clearCacheDir(filepath.Join(a.DataDir(), "eatgif")); err != nil {
					return err
				}
				service.mu.Lock()
				service.catalog, service.catalogAt = nil, time.Time{}
				service.mu.Unlock()
				return inv.EditText(ctx, "缓存已清理并将在下次请求时刷新")
			}
			catalog, err := service.getCatalog(ctx)
			if err != nil {
				return inv.Edit(ctx, "❌ 无法读取素材列表："+command.Escape(httpx.Reason(err)))
			}
			if sub == "cache" {
				// 放在读目录之后：万一远程真有一款叫 cache 的动画，让动画优先。
				// 这是只给机主看的诊断面板，少一个入口比吞掉一个大家在用的动画划算。
				if _, taken := catalog[sub]; !taken {
					return kit.SendPages(ctx, inv, command.HTMLPages(lane.cachePanel(facesOf(inv.Client)), 3800))
				}
			}
			if sub == "" || sub == "list" || sub == "ls" || sub == "help" || sub == "h" {
				names := make([]string, 0, len(catalog))
				for name := range catalog {
					names = append(names, name)
				}
				sort.Strings(names)
				lines := []string{"<b>头像动图表情</b>", command.Code(inv.Prefix+"eatgif 名称") + "（需回复目标）", ""}
				for _, name := range names {
					lines = append(lines, "• "+command.Code(name)+" - "+command.Escape(catalog[name].Desc))
				}
				return kit.SendPages(ctx, inv, command.HTMLPages(strings.Join(lines, "\n"), 3800))
			}
			selected, ok := catalog[sub]
			if !ok {
				return inv.Edit(ctx, "未找到："+command.Code(sub))
			}
			reply, err := inv.Client.GetReply(ctx, inv.Message)
			if err != nil {
				return err
			}
			if reply == nil {
				return inv.EditText(ctx, "请回复一条消息后再生成，用户或频道发的都可以")
			}
			me, you, err := facePeers(inv, reply)
			if err != nil {
				if text, ok := kit.IsUserError(err); ok {
					return inv.EditText(ctx, "❌ "+text)
				}
				return err
			}
			target := service.target(facesOf(inv.Client), me, you, selected)
			// 已经生成过就直接发出去：不跑 ffmpeg，也就犯不着排队，当场办完。
			if data, meta, ok := lane.lookup(target, service.freshSpecDigest(selected.URL)); ok {
				peer, err := inv.Client.InputPeer(inv.Message.Peer)
				if err != nil {
					return err
				}
				if err := sendSticker(ctx, inv.Client, peer, data, meta, inv.Message.ReplyToID); err != nil {
					return err
				}
				lane.note(facesOf(inv.Client), []tg.InputPeerClass{me, you}, false)
				_ = inv.Client.DeleteMessage(ctx, inv.Message)
				return nil
			}
			// 要生成就不在这儿跑：排进队列，立刻把这条指令删掉，稍后作为回复发出。
			// 「一次只跑一个 ffmpeg」的老规矩改由队列保证，命令不用干等，也不用再看中间状态。
			if !lane.enqueue(&renderJob{target: target, label: selected.Desc,
				chatID: inv.Message.ChatID, replyTo: inv.Message.ReplyToID, me: me, you: you}) {
				return inv.EditText(ctx, "已有一个动图在生成，队列也排满了，请稍候再发")
			}
			if err := inv.Client.DeleteMessage(ctx, inv.Message); err != nil {
				inv.Log.Warn("eatgif.delete_failed", slog.String("error", err.Error()))
			}
			return nil
		}})
}

// facePeers 找出两张头像该用谁的对象。
//
// 「对方」是被回复消息的发送者，用户、频道、群都可以：以频道身份发言、
// 频道推到讨论组的消息、匿名管理员，发送者都不是用户，以前一律被拒。
// 「自己」见 ownFace。
func facePeers(inv *command.Invocation, reply *bot.Message) (me, you tg.InputPeerClass, err error) {
	if reply.Sender == nil {
		return nil, nil, kit.Fail("看不出被回复的消息是谁发的")
	}
	peer, err := inv.Client.InputPeer(reply.Sender)
	if err != nil {
		return nil, nil, kit.Fail("无法解析对方的身份")
	}
	return ownFace(inv), peer, nil
}

// ownFace 是「自己」那张头像该用谁的：别人借用账号（.sudo、.sure）时用借用者的，
// 和 MiBox 一样；否则是发命令的身份。
func ownFace(inv *command.Invocation) tg.InputPeerClass {
	if inv.Trigger != nil && inv.Trigger.Sender != nil {
		if peer, err := inv.Client.InputPeer(inv.Trigger.Sender); err == nil {
			return peer
		}
	}
	return speakerOf(inv.Client, inv.Message)
}

// speakerOf 是一条自己发的消息以谁的身份发出：以频道身份发言时是那个频道，否则是本人。
func speakerOf(client *bot.Client, message *bot.Message) tg.InputPeerClass {
	if channel, ok := message.Sender.(*tg.PeerChannel); ok {
		if peer, err := client.InputPeer(channel); err == nil {
			return peer
		}
	}
	return &tg.InputPeerSelf{}
}

// loadFace 下载并解码一个头像。先下小图，下不了再下大图：小图拿不到的对象，往往还能拿到大图。
func loadFace(ctx context.Context, client *bot.Client, peer tg.InputPeerClass, who string) (image.Image, error) {
	data, err := client.DownloadProfilePhoto(ctx, peer, false, 2<<20)
	if err != nil || len(data) == 0 {
		data, err = client.DownloadProfilePhoto(ctx, peer, true, 2<<20)
	}
	if err != nil {
		return nil, kit.Failf("无法获取%s的头像", who)
	}
	if len(data) == 0 {
		return nil, kit.Failf("%s没有公开头像，合成不了", who)
	}
	decoded, err := imaging.Decode(data)
	if err != nil {
		return nil, kit.Failf("无法解析%s的头像", who)
	}
	return decoded, nil
}

// faces 下载并解码双方的头像。头像本身不缓存：换头像的代价就是一次下载，
// 真正贵的是下面几十帧的合成和一次 ffmpeg 编码，那部分才有成品缓存。
func (s *eatgifService) faces(ctx context.Context, client *bot.Client, me, you tg.InputPeerClass) (*avatarPair, error) {
	mine, err := loadFace(ctx, client, me, "你")
	if err != nil {
		return nil, err
	}
	theirs, err := loadFace(ctx, client, you, "对方")
	if err != nil {
		return nil, err
	}
	return &avatarPair{me: mine, you: theirs}, nil
}

// avatarPair 是一个动画要合成的两张头像。
type avatarPair struct{ me, you image.Image }

// spec 读出动画定义，并给出这次实际用到的那份的摘要：定义变了摘要就变，
// 用旧定义做出来的成品不会被当成新定义做出来的。
func (s *eatgifService) spec(ctx context.Context, relative string) (eatgifSpec, string, error) {
	var definition eatgifSpec
	digest, err := s.assetDocument(ctx, relative, &definition)
	if err != nil {
		return eatgifSpec{}, "", err
	}
	if definition.Width < 1 || definition.Height < 1 || definition.Width > 512 || definition.Height > 512 ||
		len(definition.Frames) < 1 || len(definition.Frames) > eatgifMaxFrames {
		return eatgifSpec{}, "", kit.Fail("动画定义无效")
	}
	return definition, digest, nil
}

// freshSpecDigest 只在磁盘上的定义副本还新鲜时给出摘要，缺失或过期时返回 ""。
// 那种时候无从判断定义变了没有，命令这一次就不探缓存，交给队列取回定义再精确比对。
func (s *eatgifService) freshSpecDigest(relative string) string {
	clean, err := safeRelative(relative)
	if err != nil {
		return ""
	}
	path := cachePath(s.a, "eatgif", clean)
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) >= docRefresh {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return digestBytes(data)
}

// target 拼出这次生成的缓存身份。me 也要进键：借用账号时贴的是借用者的头像，
// 漏掉它就会把机主的脸当成借用者的发出去。
func (s *eatgifService) target(p peerPhotos, me, you tg.InputPeerClass, selected eatgifEntry) renderTarget {
	meKey, youKey := p.key(me), p.key(you)
	canonical := ""
	if meKey != "" && youKey != "" {
		canonical, _ = renderKey("eatgif", renderFormat, "me="+meKey, "you="+youKey, "anim="+selected.URL)
	}
	return renderTarget{canonical: canonical, directory: "eatgif", ext: ".webm",
		specRel: selected.URL, peers: "me=" + meKey + "|you=" + youKey, limit: webmCacheLimit}
}

// render 逐帧合成再编码成动图贴纸，是队列里那一步的全部。
// 一次运行要解码几十帧，所以由队列保证同时只跑一个。
func (s *eatgifService) render(ctx context.Context, client *bot.Client, job *renderJob,
	definition eatgifSpec, digest string) ([]byte, renderMeta, error) {
	faces, err := s.faces(ctx, client, job.me, job.you)
	if err != nil {
		return nil, renderMeta{}, err
	}
	directory, err := os.MkdirTemp("", "mibot-eatgif-")
	if err != nil {
		return nil, renderMeta{}, err
	}
	defer os.RemoveAll(directory)

	frames := make([]media.Frame, 0, len(definition.Frames))
	var total time.Duration
	for index, entry := range definition.Frames {
		if err := ctx.Err(); err != nil {
			return nil, renderMeta{}, err
		}
		canvasData, err := s.asset(ctx, entry.URL, 5<<20)
		if err != nil {
			return nil, renderMeta{}, kit.Fail("素材下载失败：" + httpx.Reason(err))
		}
		canvas, err := frameCanvas(canvasData)
		if err != nil {
			return nil, renderMeta{}, kit.Fail("素材图片无效")
		}
		// 先贴被回复者的头像，再贴本账号的，与素材定义编写时的顺序一致。
		if entry.You != nil {
			if err := s.paste(ctx, canvas, entry.You, faces.you); err != nil {
				return nil, renderMeta{}, kit.Fail("合成失败：" + httpx.Reason(err))
			}
		}
		if entry.Me != nil {
			if err := s.paste(ctx, canvas, entry.Me, faces.me); err != nil {
				return nil, renderMeta{}, kit.Fail("合成失败：" + httpx.Reason(err))
			}
		}
		path := filepath.Join(directory, fmt.Sprintf("frame%04d.png", index))
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return nil, renderMeta{}, err
		}
		err = imaging.WritePNG(file, canvas)
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, renderMeta{}, err
		}
		delay := 100
		if entry.Delay != nil {
			delay = kit.Clamp(*entry.Delay, 20, 5000)
		}
		frames = append(frames, media.Frame{Path: path, Delay: time.Duration(delay) * time.Millisecond})
		total += time.Duration(delay) * time.Millisecond
	}

	webm, err := media.StickerWebM(ctx, directory, frames, definition.Width, definition.Height)
	if err != nil {
		return nil, renderMeta{}, kit.Fail("视频编码失败：" + command.Brief(err))
	}
	meta := renderMeta{Kind: "eatgif", Name: "sticker.webm", MimeType: "video/webm", Alt: "✨",
		Width: definition.Width, Height: definition.Height, Seconds: total.Seconds(),
		Digest: digest, Peers: job.target.peers, Bytes: int64(len(webm)), MadeAt: time.Now().UnixMilli()}
	return webm, meta, nil
}
