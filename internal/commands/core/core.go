// Package core 是基础命令：.ping .version .memory .status .sysinfo .help。
package core

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/OrionG-hub/laowangbot/internal/app"
	"github.com/OrionG-hub/laowangbot/internal/brand"
	"github.com/OrionG-hub/laowangbot/internal/command"
	"github.com/OrionG-hub/laowangbot/internal/commands/kit"
	"github.com/OrionG-hub/laowangbot/internal/httpx"
	"github.com/OrionG-hub/laowangbot/internal/sysinfo"
)

// Register 注册 ping、version、memory、status、sysinfo 和 help。
func Register(a *app.App) {
	registry := a.Registry
	registry.Register(
		&command.Command{Name: "ping", Description: "测试 Telegram 或某个网站的延迟", Usage: "[域名]", Help: pingHelp, Handle: func(ctx context.Context, inv *command.Invocation) error {
			switch target := inv.Arg(0); {
			case target == "help" || target == "h":
				return inv.Edit(ctx, pingHelp(inv.Prefix))
			case target != "":
				if dc, ok := parseDCTarget(target); ok {
					return inv.Edit(ctx, dcReport(ctx, dc))
				}
				return inv.Edit(ctx, probe(ctx, target))
			}
			elapsed, err := inv.Client.Ping(ctx)
			if err != nil {
				return inv.EditText(ctx, "Telegram 延迟测试失败")
			}
			editing := time.Now()
			if err := inv.EditText(ctx, "Pong!"); err != nil {
				return err
			}
			return inv.EditText(ctx, fmt.Sprintf("Pong!\nTelegram API: %d ms\n消息编辑: %d ms", elapsed.Milliseconds(), time.Since(editing).Milliseconds()))
		}},
		&command.Command{Name: "version", Description: "查看版本信息", Handle: func(ctx context.Context, inv *command.Invocation) error {
			return inv.Edit(ctx, versionText(a))
		}},
		&command.Command{Name: "ver", Description: "version 的简写", Hidden: true, Handle: func(ctx context.Context, inv *command.Invocation) error {
			return inv.Edit(ctx, versionText(a))
		}},
		&command.Command{Name: "memory", Description: "查看内存状态", Handle: func(ctx context.Context, inv *command.Invocation) error {
			return inv.Edit(ctx, memoryReport())
		}},
		&command.Command{Name: "status", Description: "查看运行状态卡片", Help: statusHelp, Handle: func(ctx context.Context, inv *command.Invocation) error {
			return status(ctx, a, registry, inv)
		}},
		&command.Command{Name: "sysinfo", Description: "查看详细系统信息", Handle: func(ctx context.Context, inv *command.Invocation) error {
			machine := sysinfo.ReadHost()
			process := sysinfo.Read()
			lines := []string{
				"<b>系统信息</b>", "",
				"主机: " + command.Code(machine.Hostname),
				"系统: " + command.Code(machine.Platform),
				"内核: " + command.Code(machine.KernelRelease),
				"运行时间: " + command.Code(formatUptime(machine.Uptime.Seconds())),
				"负载: " + command.Code(fmt.Sprintf("%.2f / %.2f / %.2f", machine.LoadAverage[0], machine.LoadAverage[1], machine.LoadAverage[2])),
				"CPU: " + command.Code(fmt.Sprintf("%d 核", machine.CPUs)),
				"系统内存: " + command.Code(fmt.Sprintf("%.2f / %.2f MB", sysinfo.Megabytes(machine.TotalMemory-machine.FreeMemory), sysinfo.Megabytes(machine.TotalMemory))),
				"",
				"<b>" + command.Escape(brand.Get()) + " 进程</b>",
				"Go: " + command.Code(runtime.Version()),
				"PID: " + command.Code(fmt.Sprint(os.Getpid())),
				"RSS: " + command.Code(fmt.Sprintf("%.2f MB", sysinfo.Megabytes(process.RSS))),
				"Heap: " + command.Code(fmt.Sprintf("%.2f / %.2f MB", sysinfo.Megabytes(process.HeapAlloc), sysinfo.Megabytes(process.HeapSys))),
				"Sys: " + command.Code(fmt.Sprintf("%.2f MB", sysinfo.Megabytes(process.Sys))),
			}
			return inv.Edit(ctx, strings.Join(lines, "\n"))
		}},
		&command.Command{Name: "help", Description: "查看命令列表或单条命令说明", Usage: "[命令]", Handle: func(ctx context.Context, inv *command.Invocation) error {
			if name := inv.Arg(0); name != "" {
				return inv.EditHelp(ctx, renderCommandHelp(registry, inv.Prefix, name))
			}
			return inv.EditHelp(ctx, renderHelpList(registry, inv.Prefix))
		}},
		&command.Command{Name: "h", Description: "help 的简写", Hidden: true, Handle: func(ctx context.Context, inv *command.Invocation) error {
			if name := inv.Arg(0); name != "" {
				return editHelpRich(ctx, inv, helpRichSingle(registry, inv.Prefix, name), renderCommandHelp(registry, inv.Prefix, name))
			}
			return editHelpRich(ctx, inv, helpRichList(registry, inv.Prefix), renderHelpList(registry, inv.Prefix))
		}},
	)
}

func versionText(a *app.App) string {
	return strings.Join([]string{
		"<b>" + command.Escape(brand.Get()) + " 版本</b>", "",
		command.Escape(brand.Get()) + ": " + command.Code(kit.Version(a)),
		"Go: " + command.Code(runtime.Version()),
		"平台: " + command.Code(runtime.GOOS+" "+runtime.GOARCH),
		"PID: " + command.Code(fmt.Sprint(os.Getpid())),
	}, "\n")
}

func memoryReport() string {
	current := sysinfo.Read()
	lines := []string{"<b>内存状态</b>", ""}
	if current.RSS > 0 {
		lines = append(lines, "RSS（进程总占用）: "+command.Code(fmt.Sprintf("%.2f MB", sysinfo.Megabytes(current.RSS))))
	} else {
		lines = append(lines, "RSS（进程总占用）: "+command.Code("不可用"))
	}
	lines = append(lines,
		"Heap（Go 已用 / 预留）: "+command.Code(fmt.Sprintf("%.2f / %.2f MB", sysinfo.Megabytes(current.HeapAlloc), sysinfo.Megabytes(current.HeapSys))),
		"Sys（运行时预留）: "+command.Code(fmt.Sprintf("%.2f MB", sysinfo.Megabytes(current.Sys))),
		"Goroutine: "+command.Code(fmt.Sprint(current.Goroutines)),
	)
	return strings.Join(lines, "\n")
}

func formatUptime(seconds float64) string {
	total := int64(seconds)
	return fmt.Sprintf("%d天 %d小时 %d分钟", total/86400, (total/3600)%24, (total/60)%60)
}

func renderHelpList(registry *command.Registry, prefix string) string {
	lines := []string{"<b>命令列表</b>", ""}
	for _, cmd := range registry.Commands() {
		usage := prefix + cmd.Name
		if cmd.Usage != "" {
			usage += " " + cmd.Usage
		}
		lines = append(lines, command.Code(usage)+" — "+command.Escape(cmd.Description))
	}
	quoted := make([]string, 0, len(registry.Prefixes()))
	for _, value := range registry.Prefixes() {
		quoted = append(quoted, command.Code(value))
	}
	lines = append(lines, "", "前缀: "+strings.Join(quoted, " "), "用 "+command.Code(prefix+"help 命令")+" 查看单条说明。")
	return strings.Join(lines, "\n")
}

func renderCommandHelp(registry *command.Registry, prefix, name string) string {
	cmd, ok := lookupForHelp(registry, name)
	if !ok {
		return "未知命令: " + command.Code(name)
	}
	return cmd.HelpText(prefix)
}

// lookupForHelp 找 .help 后面写的那条命令，照 MiBox 放宽：可以带前缀（.help .ping）、
// 大小写不同、写的是别名（.help 测速 找到别名指向的命令）。
func lookupForHelp(registry *command.Registry, name string) (*command.Command, bool) {
	for _, candidate := range registry.Prefixes() {
		if trimmed, ok := strings.CutPrefix(name, candidate); ok && trimmed != "" {
			name = trimmed
			break
		}
	}
	if cmd, ok := registry.Lookup(name); ok {
		return cmd, true
	}
	if cmd, ok := registry.Lookup(strings.ToLower(name)); ok {
		return cmd, true
	}
	if expansion, ok := registry.Aliases()[name]; ok {
		if fields := strings.Fields(expansion); len(fields) > 0 {
			return registry.Lookup(fields[0])
		}
	}
	return nil, false
}

func pingHelp(prefix string) string {
	p := command.Escape(prefix)
	return "🏓 <b>延迟测试</b>\n\n• <code>" + p + "ping</code> 测 Telegram 接口和消息编辑的延迟\n" +
		"• <code>" + p + "ping dc</code> 测试 Telegram 五个数据中心\n" +
		"• <code>" + p + "ping dc1</code> 测试单个数据中心\n" +
		"• <code>" + p + "ping example.com</code> 对 https://example.com 发一次 HEAD 请求，显示状态码和耗时（5 秒超时）"
}

var dcTargets = []string{"149.154.175.53", "149.154.167.51", "149.154.175.100", "149.154.167.91", "91.108.56.130"}

var dcLocations = []string{"Miami", "Amsterdam", "Miami", "Amsterdam", "Singapore"}

type dcTarget struct {
	all   bool
	index int
}

func parseDCTarget(target string) (dcTarget, bool) {
	if target == "dc" || target == "all" {
		return dcTarget{all: true}, true
	}
	if len(target) == 3 && target[:2] == "dc" && target[2] >= '1' && target[2] <= '5' {
		return dcTarget{index: int(target[2] - '1')}, true
	}
	return dcTarget{}, false
}

type dcProbeResult struct {
	port int
	ms   int64
	err  error
}

func probeDCWith(ctx context.Context, address string, dial func(context.Context, string, string) (net.Conn, error), fallback func(context.Context, string) (int64, error)) dcProbeResult {
	for _, port := range []int{443, 80} {
		started := time.Now()
		conn, err := dial(ctx, "tcp", fmt.Sprintf("%s:%d", address, port))
		if err == nil {
			_ = conn.Close()
			return dcProbeResult{port: port, ms: time.Since(started).Milliseconds()}
		}
	}
	ms, err := fallback(ctx, address)
	return dcProbeResult{ms: ms, err: err}
}

func probeDC(ctx context.Context, address string) dcProbeResult {
	return probeDCWith(ctx, address, (&net.Dialer{}).DialContext, systemPing)
}

func systemPing(ctx context.Context, address string) (int64, error) {
	started := time.Now()
	name := "ping"
	args := []string{"-c", "1", "-W", "2", address}
	if runtime.GOOS == "windows" {
		args = []string{"-n", "1", "-w", "2000", address}
	}
	if err := exec.CommandContext(ctx, name, args...).Run(); err != nil {
		return 0, err
	}
	return time.Since(started).Milliseconds(), nil
}

func dcReport(ctx context.Context, target dcTarget) string {
	indices := []int{target.index}
	if target.all {
		indices = []int{0, 1, 2, 3, 4}
	}
	lines := make([]string, 0, len(indices)+1)
	lines = append(lines, "<b>Telegram 数据中心延迟</b>")
	for _, index := range indices {
		result := probeDC(ctx, dcTargets[index])
		lines = append(lines, formatDCLine(index, result))
	}
	return strings.Join(lines, "\n")
}

func formatDCLine(index int, result dcProbeResult) string {
	if result.err != nil {
		return fmt.Sprintf("🌐 <b>DC%d (%s):</b> <code>超时</code>", index+1, dcLocations[index])
	}
	return fmt.Sprintf("🌐 <b>DC%d (%s):</b> <code>%dms</code>", index+1, dcLocations[index], result.ms)
}

// pingTarget 是 .ping 能测的目标：和 MiBox 一样只允许字母、数字和 . _ : -，最长 253 字符。
var pingTarget = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,253}$`)

// probe 对 https://target 发一次 HEAD 请求，报告状态码和耗时。
func probe(ctx context.Context, target string) string {
	if !pingTarget.MatchString(target) {
		return "❌ 无效的目标：只能是域名或 IP"
	}
	started := time.Now()
	response, err := httpx.Do(ctx, httpx.Request{Method: "HEAD", URL: "https://" + target, Timeout: 5 * time.Second, MaxBytes: 1})
	if err != nil {
		return "❌ 网络测试失败或目标不可达"
	}
	return command.Code(fmt.Sprintf("%s: HTTP %d，%d ms", target, response.Status, time.Since(started).Milliseconds()))
}

func statusHelp(prefix string) string {
	return "📊 <b>运行状态</b>\n\n<code>" + command.Escape(prefix) + "status</code> 发一张状态卡片：健康状态、在线时长，" +
		"CPU、内存、磁盘、Swap 的占用；卡片下面附进程和主机的文字说明。CPU 要采样约 160 毫秒。"
}
