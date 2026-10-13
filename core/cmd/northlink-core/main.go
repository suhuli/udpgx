package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/suhuli/udpgx/core/internal/config"
	"github.com/suhuli/udpgx/core/internal/ipc"
	"github.com/suhuli/udpgx/core/internal/platform"
)

func main() {
	role := flag.String("role", "provider", "provider or client")
	path := flag.String("config", "", "configuration JSON path")
	pipe := flag.String("pipe", "northlink-provider", "local named pipe")
	svcMode := flag.Bool("service", false, "run under Windows SCM")
	action := flag.String("service-action", "", "install, remove, start, stop, firewall")
	flag.Parse()
	if *path == "" {
		fmt.Fprintln(os.Stderr, "--config is required")
		os.Exit(2)
	}
	abs, err := filepath.Abs(*path)
	if err != nil {
		log.Fatal(err)
	}
	*path = abs
	_ = os.MkdirAll(filepath.Dir(*path), 0700)
	logPath := filepath.Join(filepath.Dir(*path), "core.log")
	f, err := newRollingLog(logPath)
	if err == nil {
		defer f.Close()
		log.SetOutput(f)
	}
	name := "NorthLink-" + *pipe
	if *action != "" {
		if err = platform.ServiceAction(*action, name, *path, *pipe, *role); err != nil {
			log.Print(err)
			_ = os.WriteFile(filepath.Join(filepath.Dir(*path), "operation-error.txt"), []byte(err.Error()), 0600)
			os.Exit(1)
		}
		_ = os.Remove(filepath.Join(filepath.Dir(*path), "operation-error.txt"))
		return
	}
	run := func(parent context.Context) error {
		cfg, err := config.Load(*path, *role)
		if err != nil {
			return err
		}
		if cfg.Role != *role {
			return fmt.Errorf("配置角色与启动角色不一致")
		}
		ctx, cancel := context.WithCancel(parent)
		defer cancel()
		l, err := platform.ListenIPC(*pipe)
		if err != nil {
			return err
		}
		defer l.Close()
		controller := ipc.NewController(ctx, *path, cfg, *svcMode)
		defer controller.Shutdown()
		go controller.AutoLoop(ctx)
		return ipc.Serve(ctx, l, controller, cancel)
	}
	if *svcMode {
		if !platform.IsService() {
			log.Fatal("请通过图形界面安装无人值守服务，不要直接使用 --service")
		}
		err = platform.RunService(name, run)
	} else {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		err = run(ctx)
	}
	if err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
