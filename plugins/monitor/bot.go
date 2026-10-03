package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	api "github.com/OrionG-hub/laowangbot/pkg/pluginapi"
	"io"
	"net/http"
	"strings"
	"time"
)

type Callback struct {
	ID   string `json:"id"`
	Data string `json:"data"`
	From struct {
		ID json.Number `json:"id"`
	} `json:"from"`
	Message *struct {
		ID   int `json:"message_id"`
		Chat struct {
			ID json.Number `json:"id"`
		} `json:"chat"`
		Markup struct {
			Keyboard [][]map[string]any `json:"inline_keyboard"`
		} `json:"reply_markup"`
	} `json:"message"`
}
type botResponse struct {
	OK          bool            `json:"ok"`
	Code        int             `json:"error_code"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

func (m *Monitor) bot(ctx context.Context, method string, payload any) (botResponse, error) {
	var result botResponse
	b, e := json.Marshal(payload)
	if e != nil {
		return result, e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, m.botBase+"/bot"+m.state.Settings.BotToken+"/"+method, bytes.NewReader(b))
	if e != nil {
		return result, fmt.Errorf("Bot API request creation failed")
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	res, e := client.Do(req)
	if e != nil {
		return result, fmt.Errorf("Bot API %s network failure", method)
	}
	defer res.Body.Close()
	e = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&result)
	if e != nil {
		return result, fmt.Errorf("Bot API %s invalid response", method)
	}
	if !result.OK {
		return result, fmt.Errorf("Bot API %s failed (%d)", method, result.Code)
	}
	return result, nil
}
func (m *Monitor) poll(ctx context.Context) error {
	now := time.Now().UnixMilli()
	m.nextPoll = now + 1000
	r, e := m.bot(ctx, "getUpdates", map[string]any{"offset": m.state.UpdateID + 1, "timeout": 0, "allowed_updates": []string{"callback_query"}})
	if e != nil {
		m.nextPoll = now + 5000
		if r.Code == 409 {
			m.conflicts++
			m.nextPoll = now + int64(min(60000, m.conflicts*5000))
		}
		return e
	}
	m.conflicts = 0
	var updates []struct {
		ID       int64     `json:"update_id"`
		Callback *Callback `json:"callback_query"`
	}
	if e = json.Unmarshal(r.Result, &updates); e != nil {
		return e
	}
	for _, u := range updates {
		if u.ID <= m.state.UpdateID {
			continue
		}
		if u.Callback != nil {
			if e = m.callback(ctx, *u.Callback); e != nil {
				return e
			}
		}
		m.state.UpdateID = max(m.state.UpdateID, u.ID)
	}
	return nil
}
func (m *Monitor) callback(ctx context.Context, cb Callback) error {
	if !strings.HasPrefix(cb.Data, "send_") {
		return nil
	}
	alert := "❌ 该指令已执行过或缓存已清空。"
	if err := m.ensureOwner(ctx); err != nil {
		return err
	}
	if cb.From.ID.String() != m.owner {
		alert = "❌ 权限拒绝：这是机主专属按钮！"
	} else if a, ok := m.state.Pending[strings.TrimPrefix(cb.Data, "send_")]; !ok {
		alert = "❌ 该指令已执行过或缓存已清空。"
	} else if time.Now().UnixMilli()-a.Time > callbackFreshMS {
		// Polling is stopped while nothing is pending, so a press can surface
		// long after it was made; the 代发 must not fire on it.
		alert = "❌ 该按钮已超过 10 分钟，未执行代发。"
	} else {
		if cb.Message == nil || cb.Message.Chat.ID == "" {
			alert = "❌ 无法获取群组信息。"
		} else {
			chat := cb.Message.Chat.ID.String()
			key := taskKey(a.TargetChatID, a.SourceChatID, a.Keyword)
			m.state.Suppress[key] = time.Now().UnixMilli()
			if err := m.save(); err != nil {
				return err
			}
			r, e := m.call(ctx, api.Call{Method: "send", Target: chat, Text: ".monitor sync " + a.TargetChatID + " " + a.SourceChatID + " " + a.Keyword})
			if e != nil {
				delete(m.state.Suppress, key)
				_ = m.save()
				alert = "❌ 代发失败，请重试"
			} else {
				delete(m.state.Pending, strings.TrimPrefix(cb.Data, "send_"))
				m.queueSync(a.TargetChatID, a.SourceChatID, a.Keyword, chat, true, r.MessageID)
				if err := m.save(); err != nil {
					return err
				}
				alert = "✅ 同步指令已发出！集群正在执行..."
				keyboard := [][]map[string]any{}
				for _, row := range cb.Message.Markup.Keyboard {
					newRow := []map[string]any{}
					for _, b := range row {
						data, _ := b["callback_data"].(string)
						if !strings.HasPrefix(data, "send_") {
							newRow = append(newRow, b)
						}
					}
					if len(newRow) > 0 {
						keyboard = append(keyboard, newRow)
					}
				}
				_, _ = m.bot(ctx, "editMessageReplyMarkup", map[string]any{"chat_id": chat, "message_id": cb.Message.ID, "reply_markup": map[string]any{"inline_keyboard": keyboard}})
			}
		}
	}
	_, err := m.bot(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": cb.ID, "text": alert, "show_alert": true})
	return err
}
