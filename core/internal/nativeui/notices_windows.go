//go:build windows

package nativeui

import (
	_ "embed"
	"github.com/lxn/win"
	"os"
	"path/filepath"
	"syscall"
)

//go:embed NOTICE.txt
var licenseNotices string

func (u *ui) showLicenses() {
	p := filepath.Join(filepath.Dir(u.opts.ConfigPath), "open-source-licenses.txt")
	if err := os.WriteFile(p, []byte(licenseNotices), 0600); err != nil {
		u.failure(err)
		return
	}
	_ = win.ShellExecute(u.mw.Handle(), syscall.StringToUTF16Ptr("open"), syscall.StringToUTF16Ptr(p), nil, nil, win.SW_SHOWNORMAL)
}
