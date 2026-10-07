//go:build !linux

package sandbox

import "os"

// srt on macOS does not detach the sandbox from the terminal, so resizes
// arrive on their own.
func resizeTTY() int { return -1 }

func relayResizes(*os.Process, int) {}
