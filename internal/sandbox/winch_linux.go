package sandbox

import (
	"os"
	"syscall"
	"time"
	"unsafe"
)

type winsize struct{ row, col, xpixel, ypixel uint16 }

func getWinsize(fd int) (winsize, bool) {
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	return ws, errno == 0
}

// resizeTTY returns an inherited terminal to watch for size changes, or -1.
func resizeTTY() int {
	for _, fd := range []int{1, 0, 2} {
		if _, ok := getWinsize(fd); ok {
			return fd
		}
	}
	return -1
}

// relayResizes polls the terminal's size and signals the command when it
// changes. Polling is the only option: this process is in srt's detached
// session too, so no SIGWINCH reaches it either.
func relayResizes(p *os.Process, fd int) {
	last, _ := getWinsize(fd)
	for range time.Tick(250 * time.Millisecond) {
		if ws, ok := getWinsize(fd); ok && (ws.row != last.row || ws.col != last.col) {
			last = ws
			p.Signal(syscall.SIGWINCH)
		}
	}
}
