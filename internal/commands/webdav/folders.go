package webdav

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func validChatFolder(folder string) bool {
	return folder != "" && folder != "." && folder != ".." && !strings.ContainsAny(folder, "/\\\x00\r\n")
}

func (d *davClient) collectionExists(ctx context.Context, folder string) (bool, error) {
	res, err := d.request(ctx, "PROPFIND", folder+"/", map[string]string{"Depth": "0"}, "", 0)
	if err != nil {
		return false, err
	}
	if res.status == 404 {
		return false, nil
	}
	if res.status != 207 || !hasCollection(res.body) {
		return false, fmt.Errorf("会话目录检查失败：HTTP %d；请检查目录和访问权限", res.status)
	}
	return true, nil
}

// Each destination has its own rename journal; an interrupted MOVE can be
// reconciled on the next upload even if its response or local commit was lost.
func (s *service) resumeDirectoryRename(ctx context.Context, d *davClient, key string, pending DirectoryRename) error {
	if !validChatFolder(pending.From) || !validChatFolder(pending.To) || pending.From == pending.To {
		return errors.New("保存的会话目录重命名状态无效")
	}
	from, err := d.collectionExists(ctx, pending.From)
	if err != nil {
		return err
	}
	to, err := d.collectionExists(ctx, pending.To)
	if err != nil {
		return err
	}
	if from && to {
		return errors.New("新群名目录已存在，已停止重命名；不覆盖或合并目录")
	}
	if !from && !to {
		return errors.New("会话目录重命名未确认：新旧目录均不存在")
	}
	if from {
		res, err := d.request(ctx, "MOVE", pending.From+"/", map[string]string{"Destination": d.url(pending.To + "/"), "Overwrite": "F"}, "", 0)
		if err != nil {
			return err
		}
		if res.status != 201 && res.status != 204 {
			return fmt.Errorf("会话目录重命名失败：HTTP %d；不覆盖或合并目录，请重试 .dav", res.status)
		}
		from, err = d.collectionExists(ctx, pending.From)
		if err != nil {
			return err
		}
		to, err = d.collectionExists(ctx, pending.To)
		if err != nil {
			return err
		}
		if from || !to {
			return errors.New("会话目录重命名未确认；请重试 .dav，不会继续上传")
		}
	}
	if err := s.records.Update(func(db *Records) error {
		if db.Chats == nil {
			db.Chats = map[string]string{}
		}
		db.Chats[pending.ChatID] = pending.To
		for i := range db.Uploads {
			row := &db.Uploads[i]
			if row.Target == pending.Target && row.ChatID == pending.ChatID && strings.HasPrefix(row.RemotePath, pending.From+"/") {
				row.RemotePath = pending.To + strings.TrimPrefix(row.RemotePath, pending.From)
				row.ChatName = pending.Name
			}
		}
		delete(db.Renames, key)
		return nil
	}); err != nil {
		return errors.New("远端目录已重命名，但本地路径保存失败；请重试 .dav 恢复，不会继续上传")
	}
	return nil
}

func (s *service) chatFolder(ctx context.Context, d *davClient, chatID, name, target string, rename bool) (string, Records, error) {
	db, err := s.records.Read()
	if err != nil {
		return "", db, errors.New("无法读取 WebDAV 记录")
	}
	key := target + ":" + chatID
	if pending, ok := db.Renames[key]; ok {
		if pending.Target != target || pending.ChatID != chatID {
			return "", db, errors.New("保存的会话目录重命名状态无效")
		}
		if err := s.resumeDirectoryRename(ctx, d, key, pending); err != nil {
			return "", db, err
		}
		db, err = s.records.Read()
		if err != nil {
			return "", db, errors.New("无法读取 WebDAV 记录")
		}
	}
	folder := db.Chats[chatID]
	// The shared Chats mapping may refer to another server. Successful records
	// identify the actual old root for this destination without changing others.
	for i := len(db.Uploads) - 1; i >= 0; i-- {
		row := db.Uploads[i]
		if row.Target == target && row.ChatID == chatID {
			folder, _, _ = strings.Cut(row.RemotePath, "/")
			break
		}
	}
	if folder != "" && !validChatFolder(folder) {
		return "", db, errors.New("保存的 WebDAV 会话目录无效")
	}
	if name == "" {
		if folder != "" {
			return folder, db, nil
		}
		name = "会话"
	}
	desired := safeName(name, 55) + " (" + chatID + ")"
	if folder != "" && !rename {
		desired = folder
	}
	if folder != "" && folder != desired {
		from, err := d.collectionExists(ctx, folder)
		if err != nil {
			return "", db, err
		}
		to, err := d.collectionExists(ctx, desired)
		if err != nil {
			return "", db, err
		}
		if to {
			return "", db, errors.New("新群名目录已存在，已停止重命名；不覆盖或合并目录")
		}
		if from {
			pending := DirectoryRename{Target: target, ChatID: chatID, From: folder, To: desired, Name: name}
			if err := s.records.Update(func(db *Records) error {
				if db.Renames == nil {
					db.Renames = map[string]DirectoryRename{}
				}
				db.Renames[key] = pending
				return nil
			}); err != nil {
				return "", db, errors.New("无法保存会话目录重命名状态；未移动目录")
			}
			if err := s.resumeDirectoryRename(ctx, d, key, pending); err != nil {
				return "", db, err
			}
		} else {
			for _, row := range db.Uploads {
				if row.Target == target && row.ChatID == chatID && strings.HasPrefix(row.RemotePath, folder+"/") {
					return "", db, errors.New("原会话目录不存在，无法确认重命名；未修改历史路径")
				}
			}
		}
	}
	if err := s.records.Update(func(db *Records) error {
		if db.Chats == nil {
			db.Chats = map[string]string{}
		}
		db.Chats[chatID] = desired
		for i := range db.Uploads {
			row := &db.Uploads[i]
			if row.Target == target && row.ChatID == chatID && strings.HasPrefix(row.RemotePath, desired+"/") {
				row.ChatName = name
			}
		}
		return nil
	}); err != nil {
		return "", db, errors.New("无法保存 WebDAV 会话目录")
	}
	db, err = s.records.Read()
	if err != nil {
		return "", db, errors.New("无法读取 WebDAV 记录")
	}
	return desired, db, nil
}
