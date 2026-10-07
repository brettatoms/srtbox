//go:build linux || darwin

package broker

import (
	"syscall"
	"unsafe"
)

type winsize struct{ rows, cols, x, y uint16 }

func ioctl(fd, req, arg uintptr) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg); e != 0 {
		return e
	}
	return nil
}

// termSize reports the size of the terminal on fd; ok is false when fd is
// not a terminal.
func termSize(fd uintptr) (rows, cols uint16, ok bool) {
	var ws winsize
	if ioctl(fd, syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws))) != nil {
		return 0, 0, false
	}
	return ws.rows, ws.cols, true
}

func setTermSize(fd uintptr, rows, cols uint16) error {
	ws := winsize{rows: rows, cols: cols}
	return ioctl(fd, syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))
}
