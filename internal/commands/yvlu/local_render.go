package yvlu

import (
	"context"
	"encoding/base64"
	"errors"
	"image"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"github.com/OrionG-hub/laowangbot/internal/command"
	"github.com/OrionG-hub/laowangbot/internal/imaging"
	"github.com/OrionG-hub/laowangbot/internal/media"
	"github.com/OrionG-hub/laowangbot/internal/quote/render"
)

// errLocalUnsupported 表示这条 payload 超出本地渲染 v1 的范围，
// 调用方应回退远程服务，而不是把消息画残。
var errLocalUnsupported = errors.New("local render does not support this payload")

// renderLocal 用本地渲染器画语录：image/stories 出 PNG，quote 出 PNG
// 后再转 WebP 贴纸。含媒体（照片、语音、文件、音频）的消息 v1 不画，
// 直接回退远程，避免照片被丢掉。
func (s *yvluService) renderLocal(ctx context.Context, inv *command.Invocation, payload *quotePayload) ([]byte, string, error) {
	for _, item := range payload.Messages {
		if item.Media != nil || item.Voice != nil || item.Document != nil || item.Audio != nil {
			return nil, "", errLocalUnsupported
		}
	}
	images, err := s.customEmojiImages(ctx, inv, payload)
	if err != nil {
		return nil, "", err
	}
	messages := make([]render.Message, 0, len(payload.Messages))
	for _, item := range payload.Messages {
		messages = append(messages, toRenderMessage(item, images))
	}
	png, err := render.Render(messages, render.Options{Format: payload.Type, BubbleColor: payload.BackgroundColor})
	if err != nil {
		return nil, "", err
	}
	if payload.Type == "quote" {
		dir, err := os.MkdirTemp("", "laowangbot-yvlu-local-")
		if err != nil {
			return nil, "", err
		}
		defer os.RemoveAll(dir)
		webp, err := media.StickerWebP(ctx, dir, png)
		if err != nil {
			return nil, "", err
		}
		return webp, "webp", nil
	}
	return png, "png", nil
}

// toRenderMessage 把远程 payload 的消息换成本地渲染器的输入：
// 头像 data URL 解回图片，表情状态与行内表情换成已下载好的图。
func toRenderMessage(item quoteMessage, images map[string]image.Image) render.Message {
	msg := render.Message{
		From: render.Author{
			ID:          item.From.ID,
			Name:        item.From.Name,
			EmojiStatus: images[item.From.EmojiStatus],
		},
		Text:       item.Text,
		Entities:   convertRenderEntities(item.Entities, images),
		ShowAvatar: item.Avatar,
		Tag:        item.SenderTag,
		TagRole:    item.SenderTagRole,
	}
	if item.From.Photo != nil {
		msg.From.Avatar = decodeDataURL(item.From.Photo.URL)
	}
	if item.Forward != nil {
		msg.Forward = item.Forward.Label
	}
	if item.ReplyMessage != nil {
		msg.Reply = &render.Reply{
			Name:     item.ReplyMessage.Name,
			Text:     item.ReplyMessage.Text,
			Entities: convertRenderEntities(item.ReplyMessage.Entities, images),
		}
	}
	return msg
}

// convertRenderEntities 把远程 entity 换成本地格式；行内表情换成图，
// 没下载到的退回按原字符画（渲染器里 CustomEmoji 为 nil 时走这条路）。
func convertRenderEntities(entities []quoteEntity, images map[string]image.Image) []render.Entity {
	out := make([]render.Entity, 0, len(entities))
	for _, e := range entities {
		re := render.Entity{Offset: e.Offset, Length: e.Length, Type: e.Type, URL: e.URL}
		if e.Type == "custom_emoji" && e.CustomEmojiID != "" {
			re.CustomEmoji = images[e.CustomEmojiID]
		}
		out = append(out, re)
	}
	return out
}

// decodeDataURL 把 avatar() 生成的 data URL 解回图片，解不开返回 nil
// （渲染器此时画渐变首字头像，不影响生成）。
func decodeDataURL(url string) image.Image {
	index := strings.Index(url, ",")
	if index < 0 {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(url[index+1:])
	if err != nil {
		return nil
	}
	img, err := imaging.Decode(raw)
	if err != nil {
		return nil
	}
	return img
}

// customEmojiImages 一次取回 payload 里全部自定义表情文档并解成图：
// 表情状态与行内表情的 document ID 先去重，一次 RPC 全取回来，
// 再逐个下载。下载失败、解码失败的 ID 不进 map，渲染时退回原字符。
func (s *yvluService) customEmojiImages(ctx context.Context, inv *command.Invocation, payload *quotePayload) (map[string]image.Image, error) {
	seen := map[string]bool{}
	var ids []int64
	collect := func(docID string) {
		if docID == "" || seen[docID] {
			return
		}
		seen[docID] = true
		if id, err := strconv.ParseInt(docID, 10, 64); err == nil && id != 0 {
			ids = append(ids, id)
		}
	}
	for _, item := range payload.Messages {
		collect(item.From.EmojiStatus)
		for _, e := range item.Entities {
			if e.Type == "custom_emoji" {
				collect(e.CustomEmojiID)
			}
		}
		if item.ReplyMessage != nil {
			for _, e := range item.ReplyMessage.Entities {
				if e.Type == "custom_emoji" {
					collect(e.CustomEmojiID)
				}
			}
		}
	}
	images := map[string]image.Image{}
	if len(ids) == 0 {
		return images, nil
	}
	docs, err := inv.Client.API().MessagesGetCustomEmojiDocuments(ctx, ids)
	if err != nil {
		// 表情文档拿不到不影响正文生成：全部退回原字符。
		return images, nil
	}
	for _, entry := range docs {
		doc, ok := entry.(*tg.Document)
		if !ok {
			continue
		}
		if img := s.customEmojiDocumentImage(ctx, inv, doc); img != nil {
			images[strconv.FormatInt(doc.ID, 10)] = img
		}
	}
	return images, nil
}

// customEmojiDocumentImage 把一个自定义表情文档解成静态图：
// 光栅格式（WebP/PNG/JPEG）直接下载解码；TGS 动画 v1 不渲染，
// 但文档自带的静态缩略图能用就用缩略图，实在没有才跳过。
func (s *yvluService) customEmojiDocumentImage(ctx context.Context, inv *command.Invocation, doc *tg.Document) image.Image {
	mime := strings.ToLower(strings.TrimSpace(doc.MimeType))
	if mime == "image/webp" || mime == "image/png" || mime == "image/jpeg" {
		if img := s.downloadDocumentImage(ctx, inv, doc, ""); img != nil {
			return img
		}
	}
	// TGS 或光栅下载失败时退回缩略图（与 ai/images.go 的 documentThumb 同一路）。
	if data := s.documentThumb(ctx, inv, doc); len(data) > 0 {
		if img, err := imaging.Decode(data); err == nil {
			return img
		}
	}
	return nil
}

// downloadDocumentImage 下载整个文档并解码；thumbSize 为空表示原图。
func (s *yvluService) downloadDocumentImage(ctx context.Context, inv *command.Invocation, doc *tg.Document, thumbSize string) image.Image {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	location := &tg.InputDocumentFileLocation{ID: doc.ID, AccessHash: doc.AccessHash, FileReference: doc.FileReference, ThumbSize: thumbSize}
	data, err := inv.Client.DownloadFile(ctx, location, doc.DCID, 1<<20)
	if err != nil || len(data) == 0 {
		return nil
	}
	img, err := imaging.Decode(data)
	if err != nil {
		return nil
	}
	return img
}

// documentThumb 下载文档最大的缩略图：内联字节直接用，否则按类型下载。
func (s *yvluService) documentThumb(ctx context.Context, inv *command.Invocation, doc *tg.Document) []byte {
	kind, inline := thumbChoice(doc.Thumbs)
	if inline != nil {
		return inline
	}
	if kind == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	location := &tg.InputDocumentFileLocation{ID: doc.ID, AccessHash: doc.AccessHash, FileReference: doc.FileReference, ThumbSize: kind}
	data, err := inv.Client.DownloadFile(ctx, location, doc.DCID, 1<<20)
	if err != nil {
		return nil
	}
	return data
}

// thumbChoice 从缩略图里挑面积最大的一个：内联字节优先于类型。
func thumbChoice(thumbs []tg.PhotoSizeClass) (kind string, inline []byte) {
	area := 0
	for _, entry := range thumbs {
		switch size := entry.(type) {
		case *tg.PhotoSize:
			if size.W*size.H > area {
				kind, inline, area = size.Type, nil, size.W*size.H
			}
		case *tg.PhotoSizeProgressive:
			if size.W*size.H > area {
				kind, inline, area = size.Type, nil, size.W*size.H
			}
		case *tg.PhotoCachedSize:
			if size.W*size.H > area {
				kind, inline, area = "", size.Bytes, size.W*size.H
			}
		}
	}
	return kind, inline
}
