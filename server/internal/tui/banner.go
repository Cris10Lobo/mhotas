package tui

import (
	"strings"

	"github.com/mbndr/figlet4go"
)

// renderBanner draws "mHOTAS" as 3D block-letter ASCII art — FIGlet's
// "larry3d" font, bundled with figlet4go as embedded font data, so this
// needs no network access or font file at runtime, the same way a prod CLI
// tool's splash banner would ship its own art rather than fetching it.
func renderBanner() (string, error) {
	ascii := figlet4go.NewAsciiRender()
	opt := figlet4go.NewRenderOptions()
	opt.FontName = "larry3d"
	s, err := ascii.RenderOpts("mHOTAS", opt)
	if err != nil {
		return "", err
	}
	// The font pads a couple of fully-blank trailing rows onto every
	// render; trimmed here once rather than re-trimming on every View().
	return strings.TrimRight(s, " \t\n"), nil
}
