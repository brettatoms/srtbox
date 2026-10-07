package broker

import (
	"bytes"
	"os"
	"syscall"
	"unsafe"
)

// openPTY returns a new pseudo-terminal's controlling and terminal ends.
func openPTY() (master, tty *os.File, err error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	if err := ioctl(m.Fd(), syscall.TIOCPTYGRANT, 0); err != nil {
		m.Close()
		return nil, nil, err
	}
	if err := ioctl(m.Fd(), syscall.TIOCPTYUNLK, 0); err != nil {
		m.Close()
		return nil, nil, err
	}
	name := make([]byte, 128)
	if err := ioctl(m.Fd(), syscall.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))); err != nil {
		m.Close()
		return nil, nil, err
	}
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	t, err := os.OpenFile(string(name), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		return nil, nil, err
	}
	return m, t, nil
}
