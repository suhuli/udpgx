//go:build windows

package nativeui

import (
	"fmt"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/suhuli/udpgx/core/internal/config"
	"strings"
)

func (u *ui) editSelected() {
	i := u.services.CurrentIndex()
	if i < 0 {
		return
	}
	if u.opts.Role == "provider" {
		u.editService(i)
	} else {
		u.editMapping(i)
	}
}
func (u *ui) deleteSelected() {
	i := u.services.CurrentIndex()
	if i < 0 {
		return
	}
	if walk.MsgBox(u.mw, "删除所选", "删除后需要保存并应用才会生效。继续？", walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
		return
	}
	if u.opts.Role == "provider" && i < len(u.cfg.Services) {
		u.cfg.Services = append(u.cfg.Services[:i], u.cfg.Services[i+1:]...)
	} else if i < len(u.cfg.Mappings) {
		u.cfg.Mappings = append(u.cfg.Mappings[:i], u.cfg.Mappings[i+1:]...)
	}
	u.updateServices()
	u.say("已删除草稿中的条目，保存并应用后生效。")
}
func (u *ui) editService(index int) {
	s := config.Service{ID: config.NewID(), Name: "新服务", Host: "127.0.0.1", Port: 8080, Enabled: true}
	if index >= 0 && index < len(u.cfg.Services) {
		s = u.cfg.Services[index]
	}
	var d *walk.Dialog
	var name, host *walk.LineEdit
	var port *walk.NumberEdit
	var enabled *walk.CheckBox
	var ok, cancel *walk.PushButton
	result, err := (Dialog{AssignTo: &d, Title: "编辑 TCP 服务", Icon: u.icon, Size: Size{Width: 480, Height: 280}, MinSize: Size{Width: 450, Height: 260}, Font: Font{Family: "Microsoft YaHei UI", PointSize: 10}, Layout: VBox{}, DefaultButton: &ok, CancelButton: &cancel, Children: []Widget{
		Composite{Layout: Grid{Columns: 2}, Children: []Widget{Label{Text: "服务名称"}, LineEdit{AssignTo: &name, Text: s.Name}, Label{Text: "目标 IP / 主机"}, LineEdit{AssignTo: &host, Text: s.Host}, Label{Text: "目标 TCP 端口"}, NumberEdit{AssignTo: &port, MinValue: 1, MaxValue: 65535, Decimals: 0, Value: float64(s.Port)}, Label{Text: ""}, CheckBox{AssignTo: &enabled, Text: "启用此服务", Checked: s.Enabled}}},
		Label{Text: "可填写本机 127.0.0.1，也可以填写本机能访问的内网地址。"},
		Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{HSpacer{}, PushButton{AssignTo: &ok, Text: "确定", OnClicked: func() {
			if strings.TrimSpace(name.Text()) == "" || strings.TrimSpace(host.Text()) == "" {
				walk.MsgBox(d, "输入不完整", "请填写名称和目标地址。", walk.MsgBoxIconWarning)
				return
			}
			s.Name = strings.TrimSpace(name.Text())
			s.Host = strings.TrimSpace(host.Text())
			s.Port = int(port.Value())
			s.Enabled = enabled.Checked()
			d.Accept()
		}}, PushButton{AssignTo: &cancel, Text: "取消", OnClicked: func() { d.Cancel() }}}},
	}}).Run(u.mw)
	if err != nil {
		u.failure(err)
		return
	}
	if result != walk.DlgCmdOK {
		return
	}
	if index < 0 {
		u.cfg.Services = append(u.cfg.Services, s)
	} else {
		u.cfg.Services[index] = s
	}
	u.updateServices()
	u.say("服务草稿已更新，保存并应用后生效。")
}
func (u *ui) addMapping() {
	i, j := u.providerCombo.CurrentIndex(), u.serviceCombo.CurrentIndex()
	if i < 0 || i >= len(u.providers) || j < 0 || j >= len(u.providers[i].Services) {
		u.failure(fmt.Errorf("请先启动连接，等待在线提供端出现，再选择一项服务。"))
		return
	}
	d, s := u.providers[i], u.providers[i].Services[j]
	port := s.Port
	if port == 80 {
		port = 18080
	} else if port == 3389 {
		port = 13389
	}
	used := map[int]bool{}
	for _, m := range u.cfg.Mappings {
		used[m.LocalPort] = true
	}
	for used[port] && port < 65535 {
		port++
	}
	m := config.Mapping{ID: config.NewID(), Name: s.Name, DeviceID: d.ID, DeviceName: d.Name, ServiceID: s.ID, LocalHost: "127.0.0.1", LocalPort: port, Enabled: true}
	u.cfg.Mappings = append(u.cfg.Mappings, m)
	u.updateServices()
	_ = u.services.SetCurrentIndex(len(u.cfg.Mappings) - 1)
	u.say(fmt.Sprintf("已添加草稿：127.0.0.1:%d → %s / %s。保存并应用后开始监听。", port, d.Name, s.Name))
}
func (u *ui) editMapping(index int) {
	if index < 0 || index >= len(u.cfg.Mappings) {
		return
	}
	m := u.cfg.Mappings[index]
	var d *walk.Dialog
	var name, host *walk.LineEdit
	var port *walk.NumberEdit
	var enabled *walk.CheckBox
	var ok, cancel *walk.PushButton
	result, err := (Dialog{AssignTo: &d, Title: "编辑本地映射", Icon: u.icon, Size: Size{Width: 490, Height: 280}, MinSize: Size{Width: 450, Height: 260}, Font: Font{Family: "Microsoft YaHei UI", PointSize: 10}, Layout: VBox{}, DefaultButton: &ok, CancelButton: &cancel, Children: []Widget{
		Label{Text: "提供端：" + m.DeviceName + "（目标服务保持不变）"},
		Composite{Layout: Grid{Columns: 2}, Children: []Widget{Label{Text: "映射名称"}, LineEdit{AssignTo: &name, Text: m.Name}, Label{Text: "本地监听 IP"}, LineEdit{AssignTo: &host, Text: m.LocalHost}, Label{Text: "本地端口"}, NumberEdit{AssignTo: &port, MinValue: 1, MaxValue: 65535, Decimals: 0, Value: float64(m.LocalPort)}, Label{Text: ""}, CheckBox{AssignTo: &enabled, Text: "启用此映射", Checked: m.Enabled}}},
		Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{HSpacer{}, PushButton{AssignTo: &ok, Text: "确定", OnClicked: func() {
			m.Name = strings.TrimSpace(name.Text())
			m.LocalHost = strings.TrimSpace(host.Text())
			m.LocalPort = int(port.Value())
			m.Enabled = enabled.Checked()
			copy := u.cfg
			copy.Mappings = []config.Mapping{m}
			if err := config.Validate(copy, false); err != nil {
				walk.MsgBox(d, "输入有误", err.Error(), walk.MsgBoxIconWarning)
				return
			}
			d.Accept()
		}}, PushButton{AssignTo: &cancel, Text: "取消", OnClicked: func() { d.Cancel() }}}},
	}}).Run(u.mw)
	if err != nil {
		u.failure(err)
		return
	}
	if result != walk.DlgCmdOK {
		return
	}
	u.cfg.Mappings[index] = m
	u.updateServices()
	u.say("映射草稿已更新，保存并应用后生效。")
}
