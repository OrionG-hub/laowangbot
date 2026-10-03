package monitor

import (
	"context"
	"fmt"
	"html"
	"sort"
	"strings"
)

func stateLabel(v bool) string {
	if v {
		return "开启"
	}
	return "关闭"
}
func (m *Monitor) details(_ context.Context) string {
	s := m.state.Settings
	var lines []string
	text := func(label, value string) {
		if value == "" {
			value = "未设置"
		}
		lines = append(lines, html.EscapeString(label+"："+value))
	}
	cmd := func(value string) { lines = append(lines, "<code>.monitor "+html.EscapeString(value)+"</code>") }
	title := func(value string) { lines = append(lines, "", "<b>"+html.EscapeString(value)+"</b>") }
	title("Monitor · 当前配置")
	for _, v := range []struct {
		label, key string
		on         bool
	}{{"总开关", "global", s.IsGlobalEnabled}, {"监听全部群", "set monitor_all_groups", s.MonitorAllGroups}, {"24 小时去重", "set dedup", s.EnableDedup}, {"管理员消息", "set monitor_admins_messages", s.MonitorAdminsMessages}, {"普通用户消息", "set monitor_users_messages", s.MonitorUsersMessages}, {"机器人消息（旧字段 on 表示监控）", "set ignore_bot_messages", !s.IgnoreBotMessages}} {
		text(v.label, stateLabel(v.on))
		next := "on"
		if v.on {
			next = "off"
		}
		cmd(v.key + " " + next)
	}
	list := func(label, key string, values []string) {
		title(fmt.Sprintf("%s · %d 项", label, len(values)))
		if len(values) == 0 {
			lines = append(lines, "暂无")
		}
		for i, v := range values {
			text(fmt.Sprintf("%d", i+1), v)
			cmd("set " + key + " del " + fmt.Sprint(i+1))
		}
	}
	list("监听群", "monitor_group", s.EnabledGroups)
	list("排除群（优先于监听）", "exclude_group", s.ExcludedGroups)
	list("全局关键词", "keyword", s.Keywords)
	title(fmt.Sprintf("通知目标 · %d 项", len(s.TargetGroups)))
	if len(s.TargetGroups) == 0 {
		lines = append(lines, "尚未设置通知目标")
	}
	for i, t := range s.TargetGroups {
		text(fmt.Sprint(i+1), t.ID+" "+t.Name)
		if t.URL != "" {
			text("链接", t.URL)
		}
		cmd("set target del " + fmt.Sprint(i+1))
	}
	for _, table := range []struct {
		label, key string
		values     map[string][]string
	}{{"分群关键词", "group_keyword", s.GroupKeywords}, {"分群关注用户", "group_user", s.GroupUsers}} {
		title(table.label)
		keys := make([]string, 0, len(table.values))
		for k := range table.values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			lines = append(lines, "暂无")
		}
		for _, group := range keys {
			text("群", group)
			for i, v := range table.values[group] {
				text(fmt.Sprint(i+1), v)
				cmd("set " + table.key + " del " + group + " " + fmt.Sprint(i+1))
			}
			cmd("set " + table.key + " clear " + group)
		}
	}
	title("通知身份与权限")
	token := "未设置（账号发送，无 Bot 按钮）"
	if s.BotToken != "" {
		token = "已设置（隐藏）"
	}
	text("Bot Token", token)
	text("通知机器人 ID", s.BotID)
	text("可信 Leader", s.TrustedLeaderID)
	text("去重记录", fmt.Sprint(len(m.state.Dedup)))
	text("待处理作业", fmt.Sprint(len(m.state.Jobs)))
	title("操作说明")
	lines = append(lines, "上方开关命令切换到相反状态；删除／清空命令立即生效，请先核对编号。", "添加规则、通知目标及使用示例：")
	cmd("help")
	return strings.Join(lines, "\n")
}
