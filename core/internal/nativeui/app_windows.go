//go:build windows

package nativeui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"github.com/suhuli/udpgx/core/internal/config"
	"github.com/suhuli/udpgx/core/internal/engine"
	"github.com/suhuli/udpgx/core/internal/ipc"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type Options struct {
	Role, ConfigPath, LogPath, SmokeReport string
	Minimized                              bool
	SmokeDuration                          time.Duration
}
type ui struct {
	mw                                               *walk.MainWindow
	tabs                                             *walk.TabWidget
	tray                                             *walk.NotifyIcon
	icon                                             *walk.Icon
	ctl                                              *ipc.Controller
	ctx                                              context.Context
	cancel                                           context.CancelFunc
	cfg                                              config.Config
	opts                                             Options
	state                                            engine.State
	desired, busy                                    bool
	closing                                          atomic.Bool
	trayReady                                        bool
	start, stop, save                                *walk.PushButton
	signalText, peerText, trafficText, hint, footer  *walk.Label
	redisHost, redisUser, redisPassword, name, group *walk.LineEdit
	redisPort, redisDB, timeout                      *walk.NumberEdit
	tls, autoConnect, startup                        *walk.CheckBox
	stun                                             *walk.TextEdit
	peers, services, logs                            *walk.TableView
	peerModel, serviceModel, logModel                *rowsModel
	providerCombo, serviceCombo                      *walk.ComboBox
	providers                                        []engine.Device
	providerSig, logSig, serviceSig                  string
	lastSent, lastReceived                           uint64
	lastSample                                       time.Time
	balloon                                          bool
	instance                                         windows.Handle
}

var msgBox = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")

func box(title, message string, flags uintptr) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(message)
	_, _, _ = msgBox.Call(0, uintptr(unsafePointer(m)), uintptr(unsafePointer(t)), flags)
}
func FatalMessage(title, message string) { box(title, message, 0x10|0x10000) }
func InfoMessage(title, message string)  { box(title, message, 0x40|0x10000) }

func Run(parent context.Context, cfg config.Config, opts Options) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	u := &ui{ctx: ctx, cancel: cancel, cfg: cfg, opts: opts, lastSample: time.Now(), peerModel: &rowsModel{}, serviceModel: &rowsModel{}, logModel: &rowsModel{}}
	title := "NorthLink 0.3 · 提供端"
	if opts.Role == "client" {
		title = "NorthLink 0.3 · 客户端"
	}
	token := windows.GetCurrentProcessToken()
	usr, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("读取本机用户信息失败: %w", err)
	}
	hash := sha256.Sum256([]byte(usr.User.Sid.String()))
	userHash := hex.EncodeToString(hash[:6])
	mutexName, _ := windows.UTF16PtrFromString(`Local\NorthLink-native-` + opts.Role + "-" + userHash)
	u.instance, err = windows.CreateMutex(nil, false, mutexName)
	if err == windows.ERROR_ALREADY_EXISTS {
		if u.instance != 0 {
			windows.CloseHandle(u.instance)
		}
		h := win.FindWindow(nil, syscall.StringToUTF16Ptr(title))
		if h != 0 {
			win.ShowWindow(h, win.SW_RESTORE)
			win.SetForegroundWindow(h)
		} else {
			InfoMessage("NorthLink 已在运行", "程序已启动或在托盘中，请检查右下角托盘；也可稍后重试。")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("建立单实例标记失败: %w", err)
	}
	defer windows.CloseHandle(u.instance)
	// Never run simultaneously with the previous separate-core package using the
	// same device ID. The new UI still opens, instead of silently failing.
	legacyPipe := "NorthLink-" + opts.Role + "-" + userHash
	legacy := legacyRunning(legacyPipe)
	initial := cfg
	if legacy || opts.SmokeReport != "" {
		initial.AutoConnect = false
	}
	u.ctl = ipc.NewController(ctx, opts.ConfigPath, initial, false)
	defer u.ctl.Shutdown()
	exe, _ := os.Executable()
	u.icon, err = walk.NewIconExtractedFromFileWithSize(exe, 0, 32)
	if err != nil {
		u.icon = walk.IconApplication()
	}
	u.serviceModel.onCheck = func(i int, v bool) {
		if opts.Role == "provider" {
			if i < len(u.cfg.Services) {
				u.cfg.Services[i].Enabled = v
			}
		} else if i < len(u.cfg.Mappings) {
			u.cfg.Mappings[i].Enabled = v
		}
		u.say("已修改草稿。点击“保存并应用”生效。")
	}
	if err = u.create(title); err != nil {
		return err
	}
	defer u.mw.Dispose()
	u.setupTray()
	u.fillConfig()
	u.updateServices()
	u.mw.Closing().Attach(func(cancelClose *bool, _ walk.CloseReason) {
		if u.closing.Load() {
			return
		}
		*cancelClose = true
		if u.trayReady {
			u.mw.Hide()
			if !u.balloon {
				_ = u.tray.ShowInfo("NorthLink 仍在运行", "双击/单击托盘图标恢复窗口；右键可以配置、停止或退出。")
				u.balloon = true
			}
		} else {
			u.quit()
		}
	})
	if legacy {
		u.say("检测到旧版后台。请先从旧版托盘“停止并退出”；如装过旧版服务，请停止它后再启动新版连接。")
	}
	go u.ctl.AutoLoop(ctx)
	go u.watch()
	if cfg.Redis.Host == "" {
		_ = u.tabs.SetCurrentIndex(2)
		if config.BuiltinRedisAvailable() {
			u.say("Redis 地址留空即使用私有构建的内置信令；填写后使用自定义配置。")
		} else {
			u.say("开源版不预置公共 Redis。请先配置自己的信令服务，两端地址、DB 和连接组须相同。")
		}
	}
	u.mw.Show()
	u.restore()
	log.Printf("WINDOW_READY role=%s arch=%s", opts.Role, runtime.GOARCH)
	if opts.Minimized && opts.SmokeReport == "" && u.trayReady {
		u.mw.Hide()
	}
	if opts.SmokeReport != "" {
		go u.smoke()
	}
	code := u.mw.Run()
	log.Printf("WINDOW_EXIT %d", code)
	if u.tray != nil {
		u.tray.Dispose()
	}
	return nil
}
func legacyRunning(name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	c, e := winio.DialPipeContext(ctx, `\\.\pipe\`+name)
	if e == nil {
		c.Close()
		return true
	}
	return false
}
func (u *ui) create(title string) error {
	return (MainWindow{
		AssignTo: &u.mw, Title: title, Icon: u.icon, Size: Size{Width: 1080, Height: 720}, MinSize: Size{Width: 880, Height: 620}, Font: Font{Family: "Microsoft YaHei UI", PointSize: 10}, Layout: VBox{Margins: Margins{Left: 16, Top: 14, Right: 16, Bottom: 10}, Spacing: 10},
		Children: []Widget{
			Composite{Layout: HBox{MarginsZero: true, Spacing: 16}, Children: []Widget{
				Composite{Layout: VBox{MarginsZero: true, Spacing: 3}, StretchFactor: 1, Children: []Widget{Label{Text: "NorthLink  /  原生单文件版", Font: Font{Family: "Microsoft YaHei UI", PointSize: 19, Bold: true}, TextColor: walk.RGB(20, 92, 80)}, Label{Text: "无需安装 · 无需 .NET · 界面与网络核心已合并", TextColor: walk.RGB(92, 106, 112)}}},
				PushButton{AssignTo: &u.start, Text: "启动连接", MinSize: Size{Width: 100, Height: 34}, MaxSize: Size{Width: 115, Height: 34}, OnClicked: u.startClick}, PushButton{AssignTo: &u.stop, Text: "停止", MinSize: Size{Width: 76, Height: 34}, MaxSize: Size{Width: 80, Height: 34}, OnClicked: u.stopClick}, PushButton{Text: "退出", MinSize: Size{Width: 70, Height: 34}, MaxSize: Size{Width: 80, Height: 34}, OnClicked: u.quit},
			}},
			TabWidget{AssignTo: &u.tabs, StretchFactor: 1, Pages: []TabPage{u.overviewPage(), u.servicesPage(), u.settingsPage(), u.logsPage()}},
			Label{AssignTo: &u.hint, Text: "准备就绪。关闭窗口隐藏到托盘，点击“退出”完全结束程序。", TextColor: walk.RGB(130, 76, 16), MinSize: Size{Width: 0, Height: 34}},
			Label{AssignTo: &u.footer, Text: "Redis 仅信令 / ICE + QUIC 直连 / 不使用中转", TextColor: walk.RGB(90, 108, 110)},
		},
	}).Create()
}
func (u *ui) overviewPage() TabPage {
	return TabPage{Title: "连接状态", Layout: VBox{Margins: Margins{Left: 14, Top: 14, Right: 14, Bottom: 14}, Spacing: 12}, Children: []Widget{
		Composite{Layout: HBox{MarginsZero: true, Spacing: 12}, Children: []Widget{
			GroupBox{Title: "Redis 信令", StretchFactor: 1, Layout: VBox{}, Children: []Widget{Label{AssignTo: &u.signalText, Text: "未启动", Font: Font{PointSize: 17, Bold: true}}, Label{Text: "只交换设备信息和连接信令"}}},
			GroupBox{Title: "P2P 直连", StretchFactor: 1, Layout: VBox{}, Children: []Widget{Label{AssignTo: &u.peerText, Text: "0 台 / 0 条 TCP", Font: Font{PointSize: 17, Bold: true}}, Label{Text: "每个客户端使用独立直连会话"}}},
			GroupBox{Title: "实时传输", StretchFactor: 1, Layout: VBox{}, Children: []Widget{Label{AssignTo: &u.trafficText, Text: "↑ 0 B/s   ↓ 0 B/s", Font: Font{PointSize: 14, Bold: true}}, Label{Text: "业务数据不经过 Redis / STUN"}}},
		}},
		TableView{AssignTo: &u.peers, Model: u.peerModel, StretchFactor: 1, AlternatingRowBG: true, LastColumnStretched: false, NotSortableByHeaderClick: true, Columns: []TableViewColumn{{Title: "对端设备", Width: 170}, {Title: "连接状态", Width: 170}, {Title: "直连路径", Width: 120}, {Title: "RTT", Width: 90}, {Title: "TCP", Width: 60}, {Title: "地址 / 错误详情", Width: 290}}, OnItemActivated: func() {
			i := u.peers.CurrentIndex()
			if i >= 0 && i < len(u.state.Peers) {
				p := u.state.Peers[i]
				walk.MsgBox(u.mw, p.Name, fmt.Sprintf("状态：%s\n路径：%s\n本地：%s\n对端：%s\n上次建连：%.2f 秒\n\n实时诊断：%s\n\n最近错误：%s", p.State, p.Path, p.Local, p.Remote, float64(p.SetupMS)/1000, p.Diagnostic, p.LastError), walk.MsgBoxIconInformation)
			}
		}},
		Label{Text: "两端都请在“连接配置”放行本 EXE 的 UDP。信令在线不等于打洞成功；不会回退中转。", TextColor: walk.RGB(79, 99, 103)},
	}}
}
func (u *ui) servicesPage() TabPage {
	title := "发布服务"
	help := "发布本机或本机可访问的 TCP 目标。勾选启用，双击行修改；保存后生效。"
	var children []Widget
	if u.opts.Role == "client" {
		title = "端口映射"
		help = "启动连接后选择在线提供端和服务。网站默认本地 18080，远程桌面默认 13389。"
		children = append(children, Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
			Label{Text: "提供端"}, ComboBox{AssignTo: &u.providerCombo, StretchFactor: 1, Model: []string{}, OnCurrentIndexChanged: u.providerChanged}, Label{Text: "服务"}, ComboBox{AssignTo: &u.serviceCombo, StretchFactor: 1, Model: []string{}}, PushButton{Text: "添加所选映射", OnClicked: u.addMapping},
		}})
	}
	children = append(children, Label{Text: help})
	var actions []Widget
	if u.opts.Role == "provider" {
		actions = append(actions, PushButton{Text: "添加服务", OnClicked: func() { u.editService(-1) }})
	}
	actions = append(actions, PushButton{Text: "编辑所选", OnClicked: u.editSelected}, PushButton{Text: "删除所选", OnClicked: u.deleteSelected}, HSpacer{}, PushButton{Text: "保存并应用", OnClicked: u.saveClick})
	children = append(children, Composite{Layout: HBox{MarginsZero: true}, Children: actions})
	columns := []TableViewColumn{{Title: "启用 / 服务名称", Width: 220}, {Title: "目标地址", Width: 250}, {Title: "端口", Width: 100}, {Title: "类型", Width: 120}}
	if u.opts.Role == "client" {
		columns = []TableViewColumn{{Title: "启用 / 服务名称", Width: 190}, {Title: "提供端", Width: 190}, {Title: "本地监听", Width: 200}, {Title: "最近连接结果", Width: 160}, {Title: "详情", Width: 260}}
	}
	children = append(children, TableView{AssignTo: &u.services, Model: u.serviceModel, CheckBoxes: true, StretchFactor: 1, AlternatingRowBG: true, LastColumnStretched: false, NotSortableByHeaderClick: true, Columns: columns, OnItemActivated: u.editSelected})
	children = append(children, Label{Text: "应用修改会重建链路，已有会话会断开。目标服务必须已启动；本工具不会自动开启远程桌面。"})
	return TabPage{Title: title, Layout: VBox{Margins: Margins{Left: 14, Top: 14, Right: 14, Bottom: 14}, Spacing: 10}, Children: children}
}
func (u *ui) settingsPage() TabPage {
	return TabPage{Title: "连接配置", Layout: VBox{Margins: Margins{Left: 14, Top: 12, Right: 14, Bottom: 12}, Spacing: 10}, Children: []Widget{
		Composite{Layout: HBox{MarginsZero: true, Spacing: 14}, Children: []Widget{
			GroupBox{Title: redisPanelTitle(), StretchFactor: 1, Layout: Grid{Columns: 2}, Children: []Widget{
				Label{Text: "主机"}, LineEdit{AssignTo: &u.redisHost, CueBanner: redisHostHint()},
				Label{Text: "端口 / DB"}, Composite{Layout: HBox{MarginsZero: true, Spacing: 8}, Children: []Widget{NumberEdit{AssignTo: &u.redisPort, MinValue: 1, MaxValue: 65535, Decimals: 0, Value: float64(6379), MinSize: Size{Width: 90, Height: 0}}, Label{Text: "DB"}, NumberEdit{AssignTo: &u.redisDB, MinValue: 0, MaxValue: 65535, Decimals: 0, Value: float64(0), MinSize: Size{Width: 65, Height: 0}}}},
				Label{Text: "用户名"}, LineEdit{AssignTo: &u.redisUser, CueBanner: "可留空"},
				Label{Text: "密码"}, LineEdit{AssignTo: &u.redisPassword, PasswordMode: true},
				CheckBox{AssignTo: &u.tls, ColumnSpan: 2, Text: "Redis 使用 TLS"},
			}},
			GroupBox{Title: "本机设置", StretchFactor: 1, Layout: Grid{Columns: 2}, Children: []Widget{
				Label{Text: "设备名称"}, LineEdit{AssignTo: &u.name}, Label{Text: "连接组"}, LineEdit{AssignTo: &u.group}, Label{Text: "打洞超时 / 秒"}, NumberEdit{AssignTo: &u.timeout, MinValue: 10, MaxValue: 120, Decimals: 0, Value: float64(30)},
				CheckBox{AssignTo: &u.autoConnect, Text: "软件启动后自动连接", ColumnSpan: 2}, CheckBox{AssignTo: &u.startup, Text: "登录 Windows 时启动并缩到托盘", ColumnSpan: 2},
			}},
		}},
		GroupBox{Title: "STUN 地址发现（每行一个，可编辑端口）", StretchFactor: 1, Layout: VBox{Spacing: 7}, Children: []Widget{
			TextEdit{AssignTo: &u.stun, MinSize: Size{Width: 0, Height: 86}, VScroll: true, Font: Font{Family: "Consolas", PointSize: 10}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{PushButton{Text: "恢复默认 STUN", OnClicked: func() { _ = u.stun.SetText(strings.Join(config.Default(u.opts.Role).STUN, "\r\n")) }}, PushButton{Text: "放行本 EXE 的 UDP", OnClicked: u.firewall}, Label{Text: "仅此操作需要管理员确认；不会安装服务。", TextColor: walk.RGB(99, 111, 119)}}},
		}},
		Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{PushButton{AssignTo: &u.save, Text: "保存并应用", OnClicked: u.saveClick}, PushButton{Text: "导入配置", OnClicked: u.importConfig}, PushButton{Text: "导出配置", OnClicked: u.exportConfig}, PushButton{Text: "打开配置 / 日志目录", OnClicked: u.openFolder}, HSpacer{}}},
		Label{Text: "免安装：不会安装 .NET 或系统服务。只保存本地配置和日志；导出配置包含 Redis 密码。", TextColor: walk.RGB(85, 109, 111)},
	}}
}
func (u *ui) logsPage() TabPage {
	return TabPage{Title: "运行日志", Layout: VBox{Margins: Margins{Left: 14, Top: 14, Right: 14, Bottom: 14}}, Children: []Widget{
		Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{Label{Text: "最近 250 条事件。启动异常也记录到 native.log。"}, HSpacer{}, PushButton{Text: "查看选中", OnClicked: u.viewLog}, PushButton{Text: "导出日志", OnClicked: u.exportLogs}, PushButton{Text: "开源许可", OnClicked: u.showLicenses}}},
		TableView{AssignTo: &u.logs, Model: u.logModel, StretchFactor: 1, AlternatingRowBG: true, LastColumnStretched: false, NotSortableByHeaderClick: true, Columns: []TableViewColumn{{Title: "时间", Width: 110}, {Title: "级别", Width: 70}, {Title: "事件（双击查看完整内容）", Width: 820}}, OnItemActivated: u.viewLog},
	}}
}
func (u *ui) setupTray() {
	ni, err := walk.NewNotifyIcon(u.mw)
	if err != nil {
		log.Printf("tray: %v", err)
		return
	}
	u.tray = ni
	if err = ni.SetIcon(u.icon); err != nil {
		log.Printf("tray icon: %v", err)
		return
	}
	_ = ni.SetToolTip("NorthLink 0.3 · " + u.roleName())
	ni.MouseDown().Attach(func(_, _ int, b walk.MouseButton) {
		if b == walk.LeftButton {
			u.restore()
		}
	})
	for _, item := range []struct {
		name string
		fn   func()
	}{{"打开主界面", u.restore}, {"连接配置", func() { u.restore(); _ = u.tabs.SetCurrentIndex(2) }}, {"启动连接", u.startClick}, {"停止连接", u.stopClick}, {"停止并退出", u.quit}} {
		a := walk.NewAction()
		_ = a.SetText(item.name)
		a.Triggered().Attach(item.fn)
		_ = ni.ContextMenu().Actions().Add(a)
	}
	if err = ni.SetVisible(true); err == nil {
		u.trayReady = true
	} else {
		log.Printf("tray visible: %v", err)
	}
}
func (u *ui) roleName() string {
	if u.opts.Role == "client" {
		return "客户端"
	}
	return "提供端"
}
func (u *ui) restore() {
	u.mw.Show()
	win.ShowWindow(u.mw.Handle(), win.SW_RESTORE)
	win.SetForegroundWindow(u.mw.Handle())
}
func (u *ui) say(s string) { _ = u.hint.SetText(s) }
func (u *ui) failure(err error) {
	log.Printf("UI: %v", err)
	message := strings.SplitN(err.Error(), "\n\nStack:", 2)[0]
	u.say(message)
	walk.MsgBox(u.mw, "NorthLink", message, walk.MsgBoxIconWarning)
}
func (u *ui) fillConfig() {
	c := u.cfg
	_ = u.redisHost.SetText(c.Redis.Host)
	_ = u.redisPort.SetValue(float64(c.Redis.Port))
	_ = u.redisDB.SetValue(float64(c.Redis.DB))
	_ = u.redisUser.SetText(c.Redis.Username)
	_ = u.redisPassword.SetText(c.Redis.Password)
	u.tls.SetChecked(c.Redis.TLS)
	_ = u.name.SetText(c.Name)
	_ = u.group.SetText(c.Group)
	_ = u.timeout.SetValue(float64(c.ConnectTimeoutSeconds))
	u.autoConnect.SetChecked(c.AutoConnect)
	u.startup.SetChecked(c.StartWithWindows)
	_ = u.stun.SetText(strings.Join(c.STUN, "\r\n"))
}
func (u *ui) capture() (config.Config, error) {
	c := u.cfg
	c.Redis.Host = strings.TrimSpace(u.redisHost.Text())
	c.Redis.Port = int(u.redisPort.Value())
	c.Redis.DB = int(u.redisDB.Value())
	c.Redis.Username = strings.TrimSpace(u.redisUser.Text())
	c.Redis.Password = u.redisPassword.Text()
	c.Redis.TLS = u.tls.Checked()
	c.Name = strings.TrimSpace(u.name.Text())
	c.Group = strings.TrimSpace(u.group.Text())
	c.ConnectTimeoutSeconds = int(u.timeout.Value())
	c.AutoConnect = u.autoConnect.Checked()
	c.StartWithWindows = u.startup.Checked()
	c.STUN = []string{}
	for _, s := range strings.Split(strings.ReplaceAll(u.stun.Text(), "\r", ""), "\n") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.HasPrefix(s, "stun:") {
			s = "stun:" + s
			if strings.Count(s, ":") == 1 {
				s += ":3478"
			}
		}
		c.STUN = append(c.STUN, s)
	}
	c = config.SanitizeRedisChoice(c)
	if err := config.Validate(c, false); err != nil {
		return c, err
	}
	// UI drafts must never share slices with a live Engine.
	b, _ := json.Marshal(c)
	var clone config.Config
	_ = json.Unmarshal(b, &clone)
	return clone, nil
}
func (u *ui) do(label string, work func() error) {
	if u.busy || u.closing.Load() {
		return
	}
	u.busy = true
	u.buttons()
	u.say(label)
	go func() {
		err := work()
		if u.closing.Load() {
			return
		}
		u.mw.Synchronize(func() {
			u.busy = false
			u.buttons()
			if err != nil {
				u.failure(err)
			} else {
				u.say("操作完成。应用中的映射状态和详细结果可在连接状态 / 日志中查看。")
			}
		})
	}()
}
func (u *ui) buttons() {
	if u.start == nil {
		return
	}
	u.start.SetEnabled(!u.busy)
	u.stop.SetEnabled(!u.busy && u.desired)
	u.save.SetEnabled(!u.busy)
	if u.desired {
		_ = u.start.SetText("重新连接")
	} else {
		_ = u.start.SetText("启动连接")
	}
}
func (u *ui) saveClick() {
	c, e := u.capture()
	if e != nil {
		u.failure(e)
		return
	}
	u.cfg = c
	u.do("正在保存配置，运行中的链路会重建…", func() error {
		if err := u.ctl.Save(c); err != nil {
			return err
		}
		return u.setStartup(c.StartWithWindows)
	})
}
func (u *ui) startClick() {
	c, e := u.capture()
	if e == nil {
		e = config.Validate(c, true)
	}
	if e != nil {
		_ = u.tabs.SetCurrentIndex(2)
		u.failure(e)
		return
	}
	token := windows.GetCurrentProcessToken()
	usr, e := token.GetTokenUser()
	if e == nil {
		h := sha256.Sum256([]byte(usr.User.Sid.String()))
		if legacyRunning("NorthLink-" + u.opts.Role + "-" + hex.EncodeToString(h[:6])) {
			u.failure(fmt.Errorf("旧版后台仍在运行。请从旧版托盘停止并退出，或停止旧版 Windows 服务，再启动新版连接。"))
			return
		}
	}
	u.cfg = c
	u.do("正在连接 Redis 并准备直连…", func() error {
		if err := u.ctl.Save(c); err != nil {
			return err
		}
		if err := u.setStartup(c.StartWithWindows); err != nil {
			return err
		}
		return u.ctl.Start()
	})
	_ = u.tabs.SetCurrentIndex(0)
}
func (u *ui) stopClick() {
	u.do("正在停止连接并释放端口…", func() error { u.ctl.Stop(); return nil })
}
func (u *ui) quit() {
	if u.closing.Load() {
		return
	}
	if u.state.ActiveConnections > 0 && walk.MsgBox(u.mw, "停止并退出", "正在使用的连接将断开，确定退出？", walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
		return
	}
	u.closing.Store(true)
	u.cancel()
	u.say("正在停止并退出…")
	u.mw.SetEnabled(false)
	go func() {
		u.ctl.Shutdown()
		u.mw.Synchronize(func() {
			if u.tray != nil {
				_ = u.tray.SetVisible(false)
			}
			u.mw.Close()
			walk.App().Exit(0)
		})
	}()
}
func (u *ui) watch() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		v := u.ctl.State()
		s := v["state"].(engine.State)
		desired := v["desired"].(bool)
		if u.closing.Load() {
			return
		}
		u.mw.Synchronize(func() {
			if !u.closing.Load() {
				u.render(s, desired)
			}
		})
		select {
		case <-u.ctx.Done():
			return
		case <-t.C:
		}
	}
}
func (u *ui) render(s engine.State, desired bool) {
	u.state = s
	u.desired = desired
	status := "未启动"
	if s.SignalOnline {
		status = "已连接"
		if s.BuiltinSignal {
			status = "已连接（内置）"
		}
	} else if desired {
		status = "连接 / 重试中"
	}
	_ = u.signalText.SetText(status)
	count := 0
	rows := [][]any{}
	for _, p := range s.Peers {
		rtt := "—"
		if p.State == "已直连" {
			count++
			rtt = fmt.Sprintf("%.1f ms", p.RTTMs)
		}
		detail := p.Remote
		if p.LastError != "" {
			detail = p.LastError
		}
		rows = append(rows, []any{p.Name, p.State, p.Path, rtt, p.Streams, detail})
	}
	u.peerModel.reset(rows, nil)
	_ = u.peerText.SetText(fmt.Sprintf("%d 台 / %d 条 TCP", count, s.ActiveConnections))
	seconds := time.Since(u.lastSample).Seconds()
	if seconds < .1 {
		seconds = .1
	}
	var up, down float64
	if s.BytesSent >= u.lastSent {
		up = float64(s.BytesSent-u.lastSent) / seconds
	}
	if s.BytesReceived >= u.lastReceived {
		down = float64(s.BytesReceived-u.lastReceived) / seconds
	}
	_ = u.trafficText.SetText("↑ " + size(up) + "/s   ↓ " + size(down) + "/s")
	u.lastSent = s.BytesSent
	u.lastReceived = s.BytesReceived
	u.lastSample = time.Now()
	_ = u.footer.SetText(fmt.Sprintf("%s · %s · %s · ↑ %s / ↓ %s · %s", u.roleName(), config.Version, runtime.GOARCH, size(float64(s.BytesSent)), size(float64(s.BytesReceived)), s.SignalError))
	u.updateServices()
	signature := ""
	providers := []engine.Device{}
	for _, d := range s.Devices {
		if d.Role == "provider" {
			providers = append(providers, d)
			signature += d.ID + d.Name
			for _, v := range d.Services {
				signature += v.ID + v.Name + fmt.Sprint(v.Port)
			}
		}
	}
	if u.providerCombo != nil && signature != u.providerSig {
		previous := ""
		i := u.providerCombo.CurrentIndex()
		if i >= 0 && i < len(u.providers) {
			previous = u.providers[i].ID
		}
		u.providers = providers
		u.providerSig = signature
		names := []string{}
		selected := 0
		for i, d := range providers {
			names = append(names, d.Name+" · "+d.ID[:min(8, len(d.ID))])
			if d.ID == previous {
				selected = i
			}
		}
		_ = u.providerCombo.SetModel(names)
		if len(names) > 0 {
			_ = u.providerCombo.SetCurrentIndex(selected)
		} else {
			_ = u.serviceCombo.SetModel([]string{})
		}
	}
	sig := ""
	if len(s.Logs) > 0 {
		l := s.Logs[len(s.Logs)-1]
		sig = fmt.Sprint(len(s.Logs)) + l.Time + l.Message
	}
	if sig != u.logSig {
		u.logSig = sig
		lr := [][]any{}
		for _, l := range s.Logs {
			tm := l.Time
			if parsed, e := time.Parse(time.RFC3339, tm); e == nil {
				tm = parsed.Local().Format("15:04:05")
			}
			level := "信息"
			if l.Level == "warn" {
				level = "注意"
			} else if l.Level == "error" {
				level = "错误"
			}
			lr = append(lr, []any{tm, level, l.Message})
		}
		u.logModel.reset(lr, nil)
	}
	u.buttons()
}
func size(n float64) string {
	units := []string{"B", "KB", "MB", "GB"}
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", n, units[i])
}
func (u *ui) updateServices() {
	selected := -1
	if u.services != nil {
		selected = u.services.CurrentIndex()
	}
	rows := [][]any{}
	checks := []bool{}
	if u.opts.Role == "provider" {
		for _, s := range u.cfg.Services {
			rows = append(rows, []any{s.Name, s.Host, s.Port, "TCP"})
			checks = append(checks, s.Enabled)
		}
	} else {
		for _, m := range u.cfg.Mappings {
			state, detail := "未启动", ""
			for _, v := range u.state.Mappings {
				if v.ID == m.ID {
					state = v.State
					detail = v.LastError
					break
				}
			}
			rows = append(rows, []any{m.Name, m.DeviceName, fmt.Sprintf("%s:%d", m.LocalHost, m.LocalPort), state, detail})
			checks = append(checks, m.Enabled)
		}
	}
	signature, _ := json.Marshal(struct {
		Rows   [][]any
		Checks []bool
	}{rows, checks})
	if string(signature) == u.serviceSig {
		return
	}
	u.serviceSig = string(signature)
	u.serviceModel.reset(rows, checks)
	if u.services != nil && selected >= 0 && selected < len(rows) {
		_ = u.services.SetCurrentIndex(selected)
	}
}
func (u *ui) providerChanged() {
	if u.providerCombo == nil || u.serviceCombo == nil {
		return
	}
	i := u.providerCombo.CurrentIndex()
	names := []string{}
	if i >= 0 && i < len(u.providers) {
		for _, s := range u.providers[i].Services {
			names = append(names, fmt.Sprintf("%s · %s:%d", s.Name, s.Host, s.Port))
		}
	}
	_ = u.serviceCombo.SetModel(names)
	if len(names) > 0 {
		_ = u.serviceCombo.SetCurrentIndex(0)
	}
}
func (u *ui) setStartup(enabled bool) error {
	key := "NorthLink." + u.opts.Role
	if !enabled {
		k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
		if err == registry.ErrNotExist {
			return nil
		}
		if err != nil {
			return err
		}
		defer k.Close()
		err = k.DeleteValue(key)
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(key, `"`+exe+`" --minimized`)
}
func (u *ui) openFolder() {
	_ = win.ShellExecute(u.mw.Handle(), syscall.StringToUTF16Ptr("open"), syscall.StringToUTF16Ptr(filepath.Dir(u.opts.ConfigPath)), nil, nil, win.SW_SHOWNORMAL)
}
func (u *ui) firewall() {
	exe, e := os.Executable()
	if e != nil {
		u.failure(e)
		return
	}
	if !win.ShellExecute(u.mw.Handle(), syscall.StringToUTF16Ptr("runas"), syscall.StringToUTF16Ptr(exe), syscall.StringToUTF16Ptr("--firewall"), nil, win.SW_SHOWNORMAL) {
		u.failure(fmt.Errorf("未能完成管理员授权。普通启动不需要管理员；仅添加防火墙规则需要。"))
	}
}
func (u *ui) viewLog() {
	i := u.logs.CurrentIndex()
	if i >= 0 && i < len(u.state.Logs) {
		l := u.state.Logs[i]
		walk.MsgBox(u.mw, "运行日志", l.Time+"\n\n"+l.Message, walk.MsgBoxIconInformation)
	}
}
func (u *ui) exportLogs() {
	dlg := walk.FileDialog{Title: "导出日志", FilePath: "NorthLink-log.txt", Filter: "文本文件 (*.txt)|*.txt"}
	ok, err := dlg.ShowSave(u.mw)
	if err != nil {
		u.failure(err)
		return
	}
	if !ok {
		return
	}
	lines := []string{fmt.Sprintf("NorthLink %s / %s / %s", config.Version, u.opts.Role, runtime.GOARCH)}
	for _, p := range u.state.Peers {
		lines = append(lines, fmt.Sprintf("对端 %s：%s；%s；%s", p.Name, p.State, p.Diagnostic, p.LastError))
	}
	for _, l := range u.state.Logs {
		lines = append(lines, l.Time+" ["+l.Level+"] "+l.Message)
	}
	if err = os.WriteFile(dlg.FilePath, []byte(strings.Join(lines, "\r\n")), 0600); err != nil {
		u.failure(err)
	} else {
		u.say("日志已导出。")
	}
}
func (u *ui) importConfig() {
	dlg := walk.FileDialog{Title: "导入配置", Filter: "JSON 配置 (*.json)|*.json"}
	ok, err := dlg.ShowOpen(u.mw)
	if err != nil {
		u.failure(err)
		return
	}
	if !ok {
		return
	}
	b, err := os.ReadFile(dlg.FilePath)
	if err != nil {
		u.failure(err)
		return
	}
	var c config.Config
	if err = json.Unmarshal(b, &c); err == nil {
		if c.Role != u.opts.Role {
			err = fmt.Errorf("导入配置角色与当前程序不一致")
		} else {
			c.DeviceID = u.cfg.DeviceID
			err = config.Validate(c, false)
		}
	}
	if err != nil {
		u.failure(err)
		return
	}
	u.cfg = config.SanitizeRedisChoice(c)
	u.fillConfig()
	u.updateServices()
	u.say("已导入为草稿，并保留本机设备 ID。确认后点击保存并应用。")
}
func (u *ui) exportConfig() {
	c, err := u.capture()
	if err != nil {
		u.failure(err)
		return
	}
	if walk.MsgBox(u.mw, "导出配置", "导出的 JSON 包含 Redis 密码，请妥善保管。继续？", walk.MsgBoxYesNo|walk.MsgBoxIconWarning) != walk.DlgCmdYes {
		return
	}
	dlg := walk.FileDialog{Title: "导出配置", FilePath: "NorthLink-" + u.opts.Role + ".json", Filter: "JSON 配置 (*.json)|*.json"}
	ok, err := dlg.ShowSave(u.mw)
	if err != nil {
		u.failure(err)
		return
	}
	if !ok {
		return
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	if err = os.WriteFile(dlg.FilePath, b, 0600); err != nil {
		u.failure(err)
	} else {
		u.say("配置已导出。")
	}
}
func (u *ui) smoke() {
	select {
	case <-u.ctx.Done():
		return
	case <-time.After(u.opts.SmokeDuration):
	}
	u.mw.Synchronize(func() {
		tabsOK := true
		for i := 0; i < 4; i++ {
			if e := u.tabs.SetCurrentIndex(i); e != nil {
				tabsOK = false
			}
		}
		draft, captureErr := u.capture()
		saved := false
		if captureErr == nil {
			saved = config.Save(filepath.Join(filepath.Dir(u.opts.ConfigPath), "smoke-config.json"), draft) == nil
		}
		u.mw.Hide()
		hidden := !win.IsWindowVisible(u.mw.Handle())
		u.restore()
		restored := win.IsWindowVisible(u.mw.Handle())
		first := ""
		if len(u.cfg.STUN) > 0 {
			first = u.cfg.STUN[0]
		}
		result := map[string]any{"version": config.Version, "role": u.opts.Role, "architecture": runtime.GOARCH, "windowCreated": true, "tabCount": u.tabs.Pages().Len(), "tabsSwitch": tabsOK, "configCapture": captureErr == nil, "configSaved": saved, "hideRestore": hidden && restored, "trayAvailable": u.trayReady, "configLoaded": u.cfg.DeviceID != "", "stunFirst": first}
		b, _ := json.MarshalIndent(result, "", "  ")
		_ = os.WriteFile(u.opts.SmokeReport, b, 0600)
		u.quit()
	})
}

// Describe the actual build, never advertise a private account in public builds.
func redisPanelTitle() string {
	if config.BuiltinRedisAvailable() {
		return "Redis（留空使用私有内置；自定义时两端一致）"
	}
	return "Redis 信令（请配置自己的服务，两端一致）"
}
func redisHostHint() string {
	if config.BuiltinRedisAvailable() {
		return "留空使用私有内置；填写后使用自定义 Redis"
	}
	return "域名或 IP，可填写 host:port"
}
