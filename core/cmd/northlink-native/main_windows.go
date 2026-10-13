//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/suhuli/udpgx/core/internal/config"
	"github.com/suhuli/udpgx/core/internal/nativeui"
	"github.com/suhuli/udpgx/core/internal/platform"
)

// Each download has a compiled-in role. Renaming the EXE doesn't change it.
var defaultRole = "provider"

func main() {
	runtime.LockOSThread()
	role := flag.String("role", defaultRole, "provider or client")
	dataDir := flag.String("data-dir", "", "optional configuration directory")
	minimized := flag.Bool("minimized", false, "start in tray")
	firewall := flag.Bool("firewall", false, "add a UDP Windows firewall rule")
	smokeReport := flag.String("smoke-report", "", "development GUI smoke report")
	smokeSeconds := flag.Int("smoke-seconds", 8, "development GUI smoke duration")
	flag.Parse()
	if *role != "provider" && *role != "client" {
		nativeui.FatalMessage("角色无效", "角色只能为 provider 或 client。")
		return
	}
	base := *dataDir
	if base == "" {
		base = filepath.Join(os.Getenv("LOCALAPPDATA"), "NorthLink", *role)
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		nativeui.FatalMessage("无法创建配置目录", err.Error())
		return
	}
	logPath := filepath.Join(base, "native.log")
	if st, e := os.Stat(logPath); e == nil && st.Size() > 4<<20 {
		_ = os.Remove(logPath + ".1")
		_ = os.Rename(logPath, logPath+".1")
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err == nil {
		defer f.Close()
		log.SetOutput(f)
		_ = debug.SetCrashOutput(f, debug.CrashOptions{})
	}
	defer func() {
		if p := recover(); p != nil {
			log.Printf("FATAL %v\n%s", p, debug.Stack())
			nativeui.FatalMessage("NorthLink 启动异常", fmt.Sprintf("%v\n\n日志：%s", p, logPath))
		}
	}()
	exe, _ := os.Executable()
	log.Printf("NorthLink %s native %s role=%s exe=%s", config.Version, runtime.GOARCH, *role, exe)
	if *firewall {
		err := platform.ServiceAction("firewall", "", filepath.Join(base, "config.json"), "", *role)
		if err != nil {
			log.Print(err)
			nativeui.FatalMessage("防火墙设置失败", err.Error())
		} else {
			nativeui.InfoMessage("防火墙设置完成", "已放行当前 EXE 的 UDP 入站。网络或路由器策略仍需允许对端 UDP。")
		}
		return
	}
	cfg, err := config.Load(filepath.Join(base, "config.json"), *role)
	if err != nil {
		nativeui.FatalMessage("配置读取失败", err.Error()+"\n\n文件："+filepath.Join(base, "config.json")+"\n原配置没有被覆盖。可备份后移走此文件，再重新运行。")
		return
	}
	if cfg.Role != *role {
		nativeui.FatalMessage("配置角色不一致", "请使用对应的提供端或客户端程序。")
		return
	}
	options := nativeui.Options{Role: *role, ConfigPath: filepath.Join(base, "config.json"), LogPath: logPath, Minimized: *minimized, SmokeReport: *smokeReport, SmokeDuration: time.Duration(*smokeSeconds) * time.Second}
	if err = nativeui.Run(context.Background(), cfg, options); err != nil {
		log.Printf("UI ERROR: %v", err)
		nativeui.FatalMessage("NorthLink 无法启动", strings.SplitN(err.Error(), "\n", 2)[0]+"\n\n日志："+logPath)
	}
}
