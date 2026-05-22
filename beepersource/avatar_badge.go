package beepersource

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"path/filepath"
	"strings"

	_ "image/gif"
	_ "image/jpeg"
)

const avatarBadgeSize = 256

func (s *Service) badgePortalAvatar(chat Chat, avatar *MatrixMedia) (*MatrixMedia, error) {
	if avatar == nil || !s.cfg.Matrix.AvatarBadges {
		return avatar, nil
	}
	badged, err := addPlatformBadgeToAvatar(chat, avatar)
	if err != nil {
		return avatar, nil
	}
	return badged, nil
}

func addPlatformBadgeToAvatar(chat Chat, avatar *MatrixMedia) (*MatrixMedia, error) {
	if avatar == nil || avatar.Content == nil {
		return avatar, nil
	}
	body, err := io.ReadAll(avatar.Content)
	if avatar.Close != nil {
		_ = avatar.Close()
	}
	if err != nil {
		return nil, err
	}

	src, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return matrixMediaWithBody(avatar, body, avatar.ContentHash, avatar.MimeType, avatar.FileName), nil
	}
	logoBytes, ok := platformLogoPNG(PlatformDisplayName(chat), PlatformColor(chat))
	if !ok {
		return matrixMediaWithBody(avatar, body, avatar.ContentHash, avatar.MimeType, avatar.FileName), nil
	}
	logo, err := png.Decode(bytes.NewReader(logoBytes))
	if err != nil {
		return matrixMediaWithBody(avatar, body, avatar.ContentHash, avatar.MimeType, avatar.FileName), nil
	}

	canvas := image.NewRGBA(image.Rect(0, 0, avatarBadgeSize, avatarBadgeSize))
	drawCover(canvas, src)
	drawPlatformBadge(canvas, logo)

	var out bytes.Buffer
	if err := png.Encode(&out, canvas); err != nil {
		return nil, err
	}
	fileName := strings.TrimSuffix(firstNonEmpty(avatar.FileName, "beeper-avatar"), filepath.Ext(avatar.FileName)) + "-badged.png"
	return matrixMediaWithBody(
		avatar,
		out.Bytes(),
		fmt.Sprintf("%x", sha256.Sum256(out.Bytes())),
		"image/png",
		fileName,
	), nil
}

func matrixMediaWithBody(template *MatrixMedia, body []byte, contentHash string, mimeType string, fileName string) *MatrixMedia {
	return &MatrixMedia{
		AssetID:     template.AssetID,
		ContentHash: contentHash,
		CachedMXC:   template.CachedMXC,
		Content:     bytes.NewReader(body),
		FileName:    fileName,
		MimeType:    mimeType,
		SizeBytes:   int64(len(body)),
		Width:       template.Width,
		Height:      template.Height,
		DurationMS:  template.DurationMS,
		IsGIF:       template.IsGIF,
		IsVoiceNote: template.IsVoiceNote,
	}
}

func drawCover(dst *image.RGBA, src image.Image) {
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	dstW := dst.Bounds().Dx()
	dstH := dst.Bounds().Dy()
	if srcW <= 0 || srcH <= 0 || dstW <= 0 || dstH <= 0 {
		return
	}
	scaleX := float64(srcW) / float64(dstW)
	scaleY := float64(srcH) / float64(dstH)
	scale := scaleX
	if scaleY < scaleX {
		scale = scaleY
	}
	cropW := float64(dstW) * scale
	cropH := float64(dstH) * scale
	offsetX := float64(srcBounds.Min.X) + (float64(srcW)-cropW)/2
	offsetY := float64(srcBounds.Min.Y) + (float64(srcH)-cropH)/2

	for y := 0; y < dstH; y++ {
		for x := 0; x < dstW; x++ {
			sx := int(offsetX + float64(x)*scale)
			sy := int(offsetY + float64(y)*scale)
			dst.Set(x, y, src.At(sx, sy))
		}
	}
}

func drawPlatformBadge(dst *image.RGBA, logo image.Image) {
	cx, cy := 205, 205
	outerR := 45
	innerR := 36

	fillCircle(dst, cx+2, cy+3, outerR, color.RGBA{R: 0, G: 0, B: 0, A: 95})
	fillCircle(dst, cx, cy, outerR, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	scaled := image.NewRGBA(image.Rect(0, 0, innerR*2, innerR*2))
	drawFit(scaled, logo)
	for y := -innerR; y < innerR; y++ {
		for x := -innerR; x < innerR; x++ {
			if x*x+y*y > innerR*innerR {
				continue
			}
			dx := cx + x
			dy := cy + y
			if !image.Pt(dx, dy).In(dst.Bounds()) {
				continue
			}
			dst.Set(dx, dy, scaled.At(x+innerR, y+innerR))
		}
	}
}

func drawFit(dst *image.RGBA, src image.Image) {
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	dstW := dst.Bounds().Dx()
	dstH := dst.Bounds().Dy()
	if srcW <= 0 || srcH <= 0 || dstW <= 0 || dstH <= 0 {
		return
	}
	for y := 0; y < dstH; y++ {
		for x := 0; x < dstW; x++ {
			sx := srcBounds.Min.X + x*srcW/dstW
			sy := srcBounds.Min.Y + y*srcH/dstH
			dst.Set(x, y, src.At(sx, sy))
		}
	}
}
