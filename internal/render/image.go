package render

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func RenderImageFile(path string, proto ImageProtocol, inTmux bool, maxCols, maxRows int) (string, error) {
	if proto == ProtoNone {
		return fmt.Sprintf("[Prévia desativada] Arquivo salvo em: %s", path), nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	switch proto {
	case ProtoKitty:
		b64 := base64.StdEncoding.EncodeToString(raw)
		seq := fmt.Sprintf("\x1b_Gf=100,a=T,c=%d,r=%d;%s\x1b\\", maxCols, maxRows, b64)
		if inTmux {
			seq = wrapTmuxPassthrough(seq)
		}
		return seq, nil

	case ProtoITerm2:
		b64 := base64.StdEncoding.EncodeToString(raw)
		seq := fmt.Sprintf("\x1b]1337;File=inline=1;width=%d;height=%d;preserveAspectRatio=1:%s\a", maxCols, maxRows, b64)
		if inTmux {
			seq = wrapTmuxPassthrough(seq)
		}
		return seq, nil

	case ProtoSixel, ProtoBlocks:
		img, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			return fmt.Sprintf("[Imagem salva em %s]", path), nil
		}
		return renderHalfBlocks(img, maxCols, maxRows), nil

	default:
		return fmt.Sprintf("Arquivo salvo em: %s", path), nil
	}
}

func wrapTmuxPassthrough(seq string) string {
	escaped := strings.ReplaceAll(seq, "\x1b", "\x1b\x1b")
	return "\x1bPtmux;" + escaped + "\x1b\\"
}

func renderHalfBlocks(img image.Image, maxCols, maxRows int) string {
	if maxCols < 10 {
		maxCols = 40
	}
	if maxRows < 6 {
		maxRows = 16
	}
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	if w == 0 || h == 0 {
		return ""
	}

	targetW := maxCols
	targetH := maxRows * 2
	if targetW > 64 {
		targetW = 64
	}
	if targetH > 32 {
		targetH = 32
	}

	var sb strings.Builder
	for y := 0; y < targetH; y += 2 {
		for x := 0; x < targetW; x++ {
			sx := bounds.Min.X + (x*w)/targetW
			sy1 := bounds.Min.Y + (y*h)/targetH
			sy2 := bounds.Min.Y + ((y+1)*h)/targetH
			if sy2 >= bounds.Max.Y {
				sy2 = bounds.Max.Y - 1
			}
			r1, g1, b1, _ := img.At(sx, sy1).RGBA()
			r2, g2, b2, _ := img.At(sx, sy2).RGBA()
			fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀",
				r1>>8, g1>>8, b1>>8,
				r2>>8, g2>>8, b2>>8,
			)
		}
		sb.WriteString("\x1b[0m\n")
	}
	return sb.String()
}

func OpenExternally(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}
