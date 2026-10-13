//go:build !windows

package platform

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
)

func ListenIPC(name string) (net.Listener, error) {
	p := filepath.Join(os.TempDir(), name+".sock")
	if c, e := net.Dial("unix", p); e == nil {
		c.Close()
		return nil, errors.New("core already running")
	}
	_ = os.Remove(p)
	l, e := net.Listen("unix", p)
	if e == nil {
		_ = os.Chmod(p, 0600)
	}
	return l, e
}
func IsService() bool                                          { return false }
func RunService(_ string, _ func(context.Context) error) error { return errors.New("Windows only") }
func ServiceAction(_, _, _, _, _ string) error                 { return errors.New("Windows only") }
