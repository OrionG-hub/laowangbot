// Package sticker 实现 .sticker：把别人的贴纸存进自己的贴纸包。
//
// MiBox 的同名插件新建贴纸包走接口，往已有的包里加贴纸却是模拟私聊 @Stickers
// 机器人：发 /addsticker、包名、转发贴纸、emoji、/done，每步之间干等一两秒，
// 再靠匹配机器人回复里的英文句子判断成没成功。这里全部改用接口
// （stickers.addStickerToSet），不跟机器人对话，也不在你和它的聊天里留下记录。
package sticker

import (
	"context"
	"errors"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/OrionG-hub/laowangbot/internal/app"
	"github.com/OrionG-hub/laowangbot/internal/bot"
	"github.com/OrionG-hub/laowangbot/internal/command"
	"github.com/OrionG-hub/laowangbot/internal/commands/kit"
	"github.com/OrionG-hub/laowangbot/internal/store"
)

// settings 存在 data/sticker.json，字段名照 MiBox，--import-mibox 原样搬过来就能用。
type settings struct {
	DefaultPack string `json:"sticker_default_pack,omitempty"`
}

// fallbackEmojis 在贴纸没带 emoji 时随机挑一个，贴纸包里每张贴纸都得有。
var fallbackEmojis = []string{"😀", "😁", "😂", "🤣", "😊", "😇", "🙂", "😉", "😋", "😎", "😍", "😘", "😜", "🤗", "🤔", "😴", "😌", "😅", "😆", "😄"}

// autoPacks 是自动命名时最多试到第几个包。
const autoPacks = 50

var errNotOwner = kit.Fail("目标贴纸包不是你创建的")

// kind 识别源贴纸格式；目标收藏包统一存放全部格式。
type kind struct{ suffix, label string }

var (
	staticKind   = kind{"_static", "静态"}
	animatedKind = kind{"_animated", "动态"}
	videoKind    = kind{"_video", "视频"}
)

// found 是被回复的那张贴纸。
type found struct {
	document *tg.InputDocument
	source   tg.InputStickerSetClass
	emoji    string
	kind     kind
}

// validShortName 按贴纸包短名的规则检查：字母开头，只有字母、数字和下划线。
func validShortName(name string) bool {
	if name == "" || len(name) > 64 || strings.Contains(name, "__") {
		return false
	}
	for index, r := range name {
		letter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if index == 0 && !letter {
			return false
		}
		if !letter && !(r >= '0' && r <= '9') && r != '_' {
			return false
		}
	}
	return true
}

// stickerOf 从被回复的消息里取出贴纸；不是贴纸就返回 false。
func stickerOf(message *tg.Message) (*found, bool) {
	content, ok := message.GetMedia()
	if !ok {
		return nil, false
	}
	media, ok := content.(*tg.MessageMediaDocument)
	if !ok {
		return nil, false
	}
	raw, ok := media.Document.(*tg.Document)
	if !ok {
		return nil, false
	}
	document := &tg.InputDocument{ID: raw.ID, AccessHash: raw.AccessHash, FileReference: raw.FileReference}
	var attribute *tg.DocumentAttributeSticker
	for _, value := range raw.Attributes {
		if sticker, ok := value.(*tg.DocumentAttributeSticker); ok {
			attribute = sticker
		}
	}
	if attribute == nil {
		return nil, false
	}
	result := &found{document: document, source: attribute.Stickerset, emoji: strings.TrimSpace(attribute.Alt), kind: staticKind}
	if result.emoji == "" {
		result.emoji = fallbackEmojis[rand.IntN(len(fallbackEmojis))]
	}
	switch raw.MimeType {
	case "application/x-tgsticker":
		result.kind = animatedKind
	case "video/webm":
		result.kind = videoKind
	}
	return result, true
}

// ownUsername 是账号的用户名，自动命名贴纸包要用；没有就返回空。
func ownUsername(self *tg.User) string {
	if self.Username != "" {
		return self.Username
	}
	for _, name := range self.Usernames {
		if name.Active {
			return name.Username
		}
	}
	return ""
}

// friendly 把贴纸相关的接口错误翻成看得懂的话。
func friendly(err error) error {
	for code, text := range map[string]string{
		"STICKERS_TOO_MUCH":             "贴纸包已满",
		"STICKERPACK_STICKERS_TOO_MUCH": "贴纸包已满",
		"STICKER_VIDEO_LONG":            "视频贴纸不能超过 3 秒",
		"STICKER_PNG_DIMENSIONS":        "静态贴纸要有一边正好 512 像素",
		"STICKERSET_INVALID":            "贴纸包名无效、已被别人占用，或者不是你建的",
		"SHORTNAME_OCCUPY_FAILED":       "贴纸包名已被占用",
		"STICKER_EMOJI_INVALID":         "这张贴纸的 emoji 无效",
		"PACK_SHORT_NAME_INVALID":       "贴纸包名无效（字母开头，只能有字母、数字和下划线）",
		"PACK_SHORT_NAME_OCCUPIED":      "贴纸包名已被占用",
	} {
		if tgerr.Is(err, code) {
			return kit.Fail(text)
		}
	}
	if wait, ok := bot.FloodWait(err); ok {
		return kit.Failf("请求太频繁，%d 秒后再试", int(wait.Seconds()))
	}
	return err
}

// full 判断错误是不是「包满了」。Telegram 文档里这两个码都用来表示满。
func full(err error) bool {
	return tgerr.Is(err, "STICKERS_TOO_MUCH") || tgerr.Is(err, "STICKERPACK_STICKERS_TOO_MUCH")
}

// lookup 读取目标包的所有权。包满没满不在这里查：上限以 Telegram 为准，
// 加贴纸时它会回满了的错误，见 full。
func lookup(ctx context.Context, api *tg.Client, name string) (*tg.MessagesStickerSet, error) {
	result, err := api.MessagesGetStickerSet(ctx, &tg.MessagesGetStickerSetRequest{Stickerset: &tg.InputStickerSetShortName{ShortName: name}})
	if err == nil {
		set, ok := result.(*tg.MessagesStickerSet)
		if !ok {
			return nil, kit.Fail("未获取到贴纸包信息，请重试")
		}
		return set, nil
	}
	if tgerr.Is(err, "STICKERSET_INVALID") {
		return nil, nil
	}
	return nil, err
}

// add 把贴纸加进名为 name 的包，包不存在就用 title 新建。返回是否新建了。
func add(ctx context.Context, api *tg.Client, name, title string, sticker *found) (bool, error) {
	created, err := addOnce(ctx, api, name, title, sticker)
	if !tgerr.Is(err, "FILE_REFERENCE_EXPIRED", "FILE_REFERENCE_INVALID", "FILE_REFERENCE_EMPTY") {
		return created, err
	}
	if sticker.source == nil {
		return created, err
	}
	if _, empty := sticker.source.(*tg.InputStickerSetEmpty); empty {
		return created, err
	}
	result, refreshErr := api.MessagesGetStickerSet(ctx, &tg.MessagesGetStickerSetRequest{Stickerset: sticker.source})
	if refreshErr != nil {
		return created, refreshErr
	}
	if set, ok := result.(*tg.MessagesStickerSet); ok {
		for _, entry := range set.Documents {
			if doc, ok := entry.(*tg.Document); ok && doc.ID == sticker.document.ID {
				sticker.document = &tg.InputDocument{ID: doc.ID, AccessHash: doc.AccessHash, FileReference: doc.FileReference}
				return addOnce(ctx, api, name, title, sticker)
			}
		}
	}
	return created, err
}

func addOnce(ctx context.Context, api *tg.Client, name, title string, sticker *found) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	item := tg.InputStickerSetItem{Document: sticker.document, Emoji: sticker.emoji}
	set, err := lookup(ctx, api, name)
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if set != nil {
		if !set.Set.Creator {
			return false, errNotOwner
		}
		_, err := api.StickersAddStickerToSet(ctx, &tg.StickersAddStickerToSetRequest{
			Stickerset: &tg.InputStickerSetShortName{ShortName: name}, Sticker: item})
		if tgerr.Is(err, "STICKERSET_INVALID") {
			// Only a confirmed ownership change permits another destination.
			if latest, checkErr := lookup(ctx, api, name); checkErr == nil && latest != nil && !latest.Set.Creator {
				return false, errNotOwner
			}
		}
		return false, err
	}
	_, err = api.StickersCreateStickerSet(ctx, &tg.StickersCreateStickerSetRequest{
		UserID: &tg.InputUserSelf{}, Title: title, ShortName: name, Stickers: []tg.InputStickerSetItem{item}})
	return true, err
}

// save 按指定名称或 用户名_stickers_序号 寻找可写的包，满包或重名时换后缀。
func save(ctx context.Context, client *bot.Client, target string, sticker *found) (string, bool, error) {
	if target != "" && !validShortName(target) {
		return "", false, kit.Fail("贴纸包名无效（字母开头，最多 64 个字母、数字或下划线，不能连续下划线）")
	}
	api := client.API()
	username := ownUsername(client.Self())
	hasUsername := username != ""
	if username == "" {
		username = "user" + strconv.FormatInt(client.SelfID(), 10)
	}
	title := "@" + username + " 的收藏"
	if target != "" && !hasUsername {
		title = target
	}
	for index := 1; index <= autoPacks; index++ {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		name := username + "_stickers_" + strconv.Itoa(index)
		if target != "" {
			name = target
			if index > 1 {
				suffix := "_" + strconv.Itoa(index)
				prefix := strings.TrimRight(target[:min(len(target), 64-len(suffix))], "_")
				name = prefix + suffix
				if strings.EqualFold(name, target) {
					name = strings.TrimRight(prefix[:len(prefix)-1], "_") + suffix
				}
			}
		}
		created, err := add(ctx, api, name, title, sticker)
		if full(err) || errors.Is(err, errNotOwner) || tgerr.Is(err, "PACK_SHORT_NAME_OCCUPIED", "SHORTNAME_OCCUPY_FAILED") {
			continue
		}
		return name, created, friendly(err)
	}
	return "", false, kit.Failf("自动命名的 %d 个贴纸包均已满或被占用，用 .sticker to 包名 存到自己的其他包", autoPacks)
}

func help(prefix string) string {
	p := command.Escape(prefix)
	return "⭐ <b>收藏贴纸</b>\n\n把别人的贴纸存进你自己的贴纸包，静态、动态、视频贴纸都行。\n\n" +
		"• 回复一个贴纸发 <code>" + p + "sticker</code> 存到默认包；没设默认包就存到自动命名的 " +
		"<code>用户名_stickers_1</code> 这类包里，静态、动态、视频统一收藏，满了自动换下一个\n" +
		"• 回复贴纸发 <code>" + p + "sticker to 包名</code> 这一次存到指定的包\n" +
		"• 默认包或指定包已满、被占用时，依次尝试 包名_2、包名_3，最多 50 个；过长的包名会截短保留序号，默认设置不变\n" +
		"• <code>" + p + "sticker 包名</code> 设默认包，包不存在的话第一次存时新建\n" +
		"• <code>" + p + "sticker cancel</code> 取消默认包\n" +
		"• 不回复发 <code>" + p + "sticker</code> 看当前设置\n\n" +
		"包名最多 64 个字符，字母开头，仅字母、数字和下划线，不能连续下划线。没有用户名时用 user账号ID 自动命名。贴纸没带 emoji 时随机配一个。"
}

func packLink(name string) string {
	return `<a href="https://t.me/addstickers/` + command.Escape(name) + `">` + command.Escape(name) + `</a>`
}

// Register 注册 .sticker。
func Register(a *app.App) {
	saved := kit.NewStore(a, "sticker.json", func() settings { return settings{} })
	a.Registry.Register(&command.Command{Name: "sticker", Description: "把贴纸存进自己的贴纸包", Usage: "[to 包名|包名|cancel]", Help: help, Timeout: 2 * time.Minute,
		Handle: func(ctx context.Context, inv *command.Invocation) error {
			sub := strings.ToLower(inv.Arg(0))
			if sub == "help" || sub == "h" {
				return inv.Edit(ctx, help(inv.Prefix))
			}
			reply, err := inv.Client.GetReply(ctx, inv.Message)
			if err != nil {
				return err
			}
			var sticker *found
			if reply != nil && reply.Raw != nil {
				sticker, _ = stickerOf(reply.Raw)
			}
			if sticker == nil {
				if reply != nil && sub == "" {
					return inv.EditText(ctx, "❌ 回复的不是贴纸")
				}
				return configure(ctx, inv, saved)
			}
			current, err := saved.Read()
			if err != nil {
				return err
			}
			target := current.DefaultPack
			if sub == "to" {
				target = inv.Arg(1)
				if !validShortName(target) {
					return inv.EditText(ctx, "❌ 包名只能用字母、数字和下划线，字母开头")
				}
			}
			if err := inv.EditText(ctx, "⏳ 正在收藏…"); err != nil {
				return err
			}
			name, created, err := save(ctx, inv.Client, target, sticker)
			if err != nil {
				if text, ok := kit.IsUserError(err); ok {
					return inv.EditText(ctx, "❌ "+text)
				}
				return err
			}
			verb := "已存进"
			if created {
				verb = "新建了贴纸包并存进"
			}
			if err := inv.Edit(ctx, "✅ "+verb+" "+packLink(name)); err != nil {
				return err
			}
			// 和 MiBox 一样，成功的提示 5 秒后删掉，不在对话里留痕迹。
			if kit.Sleep(ctx, 5*time.Second) == nil {
				_ = inv.Client.DeleteMessage(ctx, inv.Message)
			}
			return nil
		}})
}

// configure 处理不回复贴纸时的子命令：看设置、设默认包、取消默认包。
func configure(ctx context.Context, inv *command.Invocation, saved *store.Store[settings]) error {
	argument := inv.Arg(0)
	switch {
	case argument == "":
		current, err := saved.Read()
		if err != nil {
			return err
		}
		text := "⭐ <b>收藏贴纸</b>\n\n"
		switch username := ownUsername(inv.Client.Self()); {
		case current.DefaultPack != "":
			text += "默认贴纸包：" + packLink(current.DefaultPack)
		case username != "":
			text += "没设默认包，会存进 " + command.Code(username+"_stickers_1") + " 这类自动命名的包"
		default:
			text += "没设默认包，会存进 " + command.Code("user"+strconv.FormatInt(inv.Client.SelfID(), 10)+"_stickers_1") + " 这类自动命名的包"
		}
		return inv.Edit(ctx, text+"\n\n回复一个贴纸发 "+command.Code(inv.Prefix+"sticker")+" 收藏。")
	case strings.EqualFold(argument, "cancel"):
		if err := saved.Update(func(value *settings) error { value.DefaultPack = ""; return nil }); err != nil {
			return err
		}
		return inv.EditText(ctx, "✅ 已取消默认贴纸包")
	case strings.EqualFold(argument, "to"):
		return inv.EditText(ctx, "❌ 要回复一个贴纸再用 to")
	case !validShortName(argument):
		return inv.EditText(ctx, "❌ 包名只能用字母、数字和下划线，字母开头")
	}
	set, err := lookup(ctx, inv.Client.API(), argument)
	if err != nil {
		return inv.EditText(ctx, "❌ 查不了这个贴纸包："+command.Brief(err))
	}
	if err := saved.Update(func(value *settings) error { value.DefaultPack = argument; return nil }); err != nil {
		return err
	}
	note := "，第一次收藏时新建"
	if set != nil {
		note = ""
		if !set.Set.Creator {
			note = "，此名称已被占用，收藏时自动寻找可用的后缀包"
		}
	}
	return inv.Edit(ctx, "✅ 默认贴纸包设为 "+command.Code(argument)+note)
}
