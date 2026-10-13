//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func ListenIPC(name string) (net.Listener, error) {
	return winio.ListenPipe(`\\.\pipe\`+name, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;IU)", InputBufferSize: 65536, OutputBufferSize: 65536})
}
func IsService() bool { v, _ := svc.IsWindowsService(); return v }

type handler struct{ run func(context.Context) error }

func (h *handler) Execute(_ []string, r <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			status <- svc.Status{State: svc.StopPending}
			if err != nil {
				return true, 1
			}
			return false, 0
		case req := <-r:
			switch req.Cmd {
			case svc.Interrogate:
				status <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(20 * time.Second):
				}
				return false, 0
			}
		}
	}
}
func RunService(name string, run func(context.Context) error) error {
	return svc.Run(name, &handler{run: run})
}
func ServiceAction(action, name, configPath, pipe, role string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if action == "firewall" {
		// An explicit per-program block wins over an allow rule. Remove only local
		// UDP block rules for this exact EXE, never global rules or domain policy.
		quoted := strings.ReplaceAll(exe, "'", "''")
		script := `$ErrorActionPreference='Stop';[Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false);$exe='` + quoted + `';
  $local=@(Get-NetFirewallRule -PolicyStore PersistentStore -Direction Inbound | Where-Object {($_ | Get-NetFirewallApplicationFilter).Program -ieq $exe});
  foreach($r in $local){$protocol=($r | Get-NetFirewallPortFilter).Protocol;if(($r.Action -eq 'Block') -and ($protocol -in @('UDP','17'))){$r | Remove-NetFirewallRule};if(($r.DisplayName -eq 'NorthLink Direct UDP') -and ($r.Action -eq 'Allow')){$r | Remove-NetFirewallRule}};
  New-NetFirewallRule -DisplayName 'NorthLink Direct UDP' -Direction Inbound -Action Allow -Program $exe -Protocol UDP -Profile Any | Out-Null;
  $remaining=@(Get-NetFirewallRule -PolicyStore ActiveStore -Enabled True -Direction Inbound -Action Block | Where-Object {($_ | Get-NetFirewallApplicationFilter).Program -ieq $exe} | Where-Object {($_ | Get-NetFirewallPortFilter).Protocol -in @('UDP','17','Any','256')});
  if($remaining.Count -gt 0){throw 'An effective block policy still applies to this EXE. Ask your Windows administrator to allow its UDP traffic.'};Write-Output 'UDP rule ready. Firewall remains enabled.'`
		ps := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		cmd := exec.Command(ps, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		out, e := cmd.CombinedOutput()
		if e != nil {
			return fmt.Errorf("放行本 EXE 的 UDP 失败：%w (%s)；未关闭防火墙或修改其他程序规则", e, string(out))
		}
		return nil
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	if action == "install" {
		if s, e := m.OpenService(name); e == nil {
			s.Close()
			return errors.New("服务已安装，请先移除或直接使用现有服务")
		}
		s, err := m.CreateService(name, exe, mgr.Config{DisplayName: "NorthLink 提供端 (" + name + ")", Description: "Redis 信令 / ICE 打洞 / QUIC 直连，无中转。", StartType: mgr.StartAutomatic, DelayedAutoStart: true}, "--service", "--config", configPath, "--pipe", pipe, "--role", role)
		if err != nil {
			return err
		}
		defer s.Close()
		_ = s.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 10 * time.Second}, {Type: mgr.ServiceRestart, Delay: 30 * time.Second}}, 86400)
		return s.Start()
	}
	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()
	switch action {
	case "remove":
		_, _ = s.Control(svc.Stop)
		deadline := time.Now().Add(25 * time.Second)
		for time.Now().Before(deadline) {
			st, e := s.Query()
			if e != nil || st.State == svc.Stopped {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		return s.Delete()
	case "start":
		if st, e := s.Query(); e == nil && st.State == svc.Running {
			return nil
		}
		return s.Start()
	case "stop":
		_, err = s.Control(svc.Stop)
		return err
	default:
		return errors.New("未知服务操作")
	}
}
