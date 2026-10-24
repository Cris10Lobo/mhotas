package tui

import (
	"strings"

	"github.com/mbndr/figlet4go"
)

func renderBanner() (string, error) {
	ascii := figlet4go.NewAsciiRender()
	opt := figlet4go.NewRenderOptions()
	opt.FontName = "larry3d"
	s, err := ascii.RenderOpts("mHOTAS", opt)
	if err != nil {
		return "", err
	}
	// The font pads blank trailing rows.
	return strings.TrimRight(s, " \t\n"), nil
}
