//go:build windows

package nativeui

import "unsafe"

func unsafePointer(p *uint16) unsafe.Pointer { return unsafe.Pointer(p) }
