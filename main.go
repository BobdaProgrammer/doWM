package main //nolint:revive

import (
	"log/slog"

	"github.com/BobdaProgrammer/doWM/wm"
)

func main() {
	WM, err := wm.Create()
	if err != nil {
		slog.Error("Couldn't initialise window manager", "error:", err)
		return
	}
	defer WM.Close()

	WM.Run()
}
