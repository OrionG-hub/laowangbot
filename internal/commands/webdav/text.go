package webdav

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/OrionG-hub/laowangbot/internal/bot"
	"github.com/gotd/td/tg"
)

// Link previews still represent text; unsupported media must not silently lose
// their attachment by falling back to a text-only archive.
func textMessageSupported(message *bot.Message) bool {
	if message.Text == "" {
		return false
	}
	media, ok := message.Raw.GetMedia()
	if !ok {
		return true
	}
	switch media.(type) {
	case *tg.MessageMediaEmpty, *tg.MessageMediaWebPage:
		return true
	default:
		return false
	}
}

type archivedMessage struct {
	AuthorID      string   `json:"authorId"`
	ID            int      `json:"id"`
	MediaTypes    []string `json:"mediaTypes"`
	PeerID        string   `json:"peerId"`
	Text          string   `json:"text"`
	ThreadID      string   `json:"threadId,omitempty"`
	Timestamp     int      `json:"timestamp"`
	ReplyToID     int      `json:"replyToId,omitempty"`
	ReplyToPeerID string   `json:"replyToPeerId,omitempty"`
}

func messageJSON(message *bot.Message, source *bot.MediaSource) ([]byte, error) {
	types := []string{}
	if source != nil {
		kind := "document"
		switch {
		case source.Photo:
			kind = "photo"
		case strings.HasPrefix(source.MimeType, "video/"):
			kind = "video"
		case strings.HasPrefix(source.MimeType, "audio/"):
			kind = "audio"
		}
		types = append(types, kind)
	}
	record := archivedMessage{AuthorID: bot.PeerID(message.Sender), ID: message.ID, MediaTypes: types, PeerID: message.ChatID, Text: message.Text, Timestamp: message.Raw.Date, ReplyToID: message.ReplyToID, ReplyToPeerID: bot.PeerID(message.ReplyToPeer)}
	if message.TopicID > 0 {
		record.ThreadID = strconv.Itoa(message.TopicID)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
