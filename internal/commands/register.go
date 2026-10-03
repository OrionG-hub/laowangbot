// Package commands 把各命令接入应用。每个子目录是一个命令，或共用一套数据的一组命令
// （如 aban、ids、sudo）；kit 是它们共用的小工具。
package commands

import (
	"github.com/OrionG-hub/laowangbot/internal/app"
	"github.com/OrionG-hub/laowangbot/internal/commands/aban"
	"github.com/OrionG-hub/laowangbot/internal/commands/acn"
	"github.com/OrionG-hub/laowangbot/internal/commands/ai"
	"github.com/OrionG-hub/laowangbot/internal/commands/alias"
	"github.com/OrionG-hub/laowangbot/internal/commands/bf"
	"github.com/OrionG-hub/laowangbot/internal/commands/bin"
	"github.com/OrionG-hub/laowangbot/internal/commands/calc"
	"github.com/OrionG-hub/laowangbot/internal/commands/core"
	"github.com/OrionG-hub/laowangbot/internal/commands/da"
	"github.com/OrionG-hub/laowangbot/internal/commands/dme"
	"github.com/OrionG-hub/laowangbot/internal/commands/eatgif"
	"github.com/OrionG-hub/laowangbot/internal/commands/gt"
	"github.com/OrionG-hub/laowangbot/internal/commands/ids"
	"github.com/OrionG-hub/laowangbot/internal/commands/ip"
	"github.com/OrionG-hub/laowangbot/internal/commands/log"
	"github.com/OrionG-hub/laowangbot/internal/commands/prefix"
	"github.com/OrionG-hub/laowangbot/internal/commands/privacy"
	"github.com/OrionG-hub/laowangbot/internal/commands/rate"
	"github.com/OrionG-hub/laowangbot/internal/commands/re"
	"github.com/OrionG-hub/laowangbot/internal/commands/restart"
	"github.com/OrionG-hub/laowangbot/internal/commands/save"
	"github.com/OrionG-hub/laowangbot/internal/commands/speedtest"
	"github.com/OrionG-hub/laowangbot/internal/commands/sticker"
	"github.com/OrionG-hub/laowangbot/internal/commands/sudo"
	"github.com/OrionG-hub/laowangbot/internal/commands/sum"
	"github.com/OrionG-hub/laowangbot/internal/commands/tr"
	"github.com/OrionG-hub/laowangbot/internal/commands/tts"
	"github.com/OrionG-hub/laowangbot/internal/commands/update"
	"github.com/OrionG-hub/laowangbot/internal/commands/webdav"
	"github.com/OrionG-hub/laowangbot/internal/commands/whois"
	"github.com/OrionG-hub/laowangbot/internal/commands/yvlu"
)

// RegisterAll 把所有命令组接入应用。
func RegisterAll(a *app.App) {
	core.Register(a)
	restart.Register(a)
	update.Register(a)
	calc.Register(a)
	rate.Register(a)
	whois.Register(a)
	webdav.Register(a)
	ai.Register(a)
	gt.Register(a)
	tr.Register(a)
	speedtest.Register(a)
	log.Register(a)
	bf.Register(a)
	save.Register(a)
	ip.Register(a)
	bin.Register(a)
	ids.Register(a)
	sum.Register(a)
	re.Register(a)
	dme.Register(a)
	da.Register(a)
	aban.Register(a)
	acn.Register(a)
	yvlu.Register(a)
	eatgif.Register(a)
	sticker.Register(a)
	tts.Register(a)
	privacy.Register(a)
	prefix.Register(a)
	alias.Register(a)
	sudo.Register(a)
}
