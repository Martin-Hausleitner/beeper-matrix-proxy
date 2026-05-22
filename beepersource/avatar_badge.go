package beepersource

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/draw"
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
	logo, badgeColor, ok := platformBadgeGlyph(chat)
	if !ok {
		return matrixMediaWithBody(avatar, body, avatar.ContentHash, avatar.MimeType, avatar.FileName), nil
	}

	canvas := image.NewRGBA(image.Rect(0, 0, avatarBadgeSize, avatarBadgeSize))
	drawCover(canvas, src)
	drawPlatformBadge(canvas, logo, badgeColor)

	var out bytes.Buffer
	if err := png.Encode(&out, canvas); err != nil {
		return nil, err
	}
	fileName := strings.TrimSuffix(firstNonEmpty(avatar.FileName, "beeper-avatar"), filepath.Ext(avatar.FileName)) + "-badged.png"
	assetID := avatar.AssetID
	if assetID != "" {
		assetID = "avatar-badge-v2:" + avatar.AssetID + ":" + platformBadgeIconHash(chat)
	}
	return matrixMediaWithBody(
		matrixMediaWithAssetID(avatar, assetID),
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

func matrixMediaWithAssetID(template *MatrixMedia, assetID string) *MatrixMedia {
	copy := *template
	copy.AssetID = assetID
	return &copy
}

func generatedContactAvatarMedia(chat Chat) *MatrixMedia {
	platform := PlatformDisplayName(chat)
	canvas := image.NewRGBA(image.Rect(0, 0, avatarBadgeSize, avatarBadgeSize))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: contactFallbackColor(chat)}, image.Point{}, draw.Src)
	drawPersonSilhouette(canvas)
	if logo, badgeColor, ok := platformBadgeGlyph(chat); ok {
		drawPlatformBadge(canvas, logo, badgeColor)
	}
	var out bytes.Buffer
	_ = png.Encode(&out, canvas)
	body := out.Bytes()
	contentHash := fmt.Sprintf("%x", sha256.Sum256(body))
	return &MatrixMedia{
		AssetID:     "avatar-fallback-v2:" + firstNonEmpty(chat.ID, chat.Name, chat.AccountID, "unknown") + ":" + brandIconKey(platform) + ":" + platformBadgeIconHash(chat),
		ContentHash: contentHash,
		Content:     bytes.NewReader(body),
		FileName:    strings.ToLower(strings.ReplaceAll(platform, " ", "-")) + "-contact-avatar.png",
		MimeType:    "image/png",
		SizeBytes:   int64(len(body)),
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

func drawPlatformBadge(dst *image.RGBA, logo image.Image, bg color.RGBA) {
	cx, cy := 210, 210
	outerR := 40
	innerR := 28

	blendCircle(dst, cx+2, cy+4, outerR+2, color.RGBA{R: 0, G: 0, B: 0, A: 95})
	blendCircle(dst, cx, cy, outerR+2, color.RGBA{R: 255, G: 255, B: 255, A: 110})
	fillCircle(dst, cx, cy, outerR, bg)
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
			blendAt(dst, dx, dy, scaled.At(x+innerR, y+innerR))
		}
	}
}

func platformBadgeGlyph(chat Chat) (image.Image, color.RGBA, bool) {
	icon, ok := brandIconByKey(PlatformDisplayName(chat))
	if !ok {
		icon, ok = brandIconByKey(chat.AccountID)
	}
	if ok {
		body, ok := brandIconPNGByKey(icon.Key)
		if ok {
			img, err := png.Decode(bytes.NewReader(body))
			if err == nil {
				return img, parseHexColor(firstNonEmpty(icon.BrandColor, PlatformColor(chat))), true
			}
		}
	}
	if pngBytes, ok := platformLogoPNG(PlatformDisplayName(chat), PlatformColor(chat)); ok {
		img, err := png.Decode(bytes.NewReader(pngBytes))
		if err == nil {
			return img, parseHexColor(PlatformColor(chat)), true
		}
	}
	return nil, color.RGBA{}, false
}

func platformBadgeIconHash(chat Chat) string {
	if icon, ok := brandIconByKey(PlatformDisplayName(chat)); ok && len(icon.SHA256) >= 12 {
		return icon.SHA256[:12]
	}
	if icon, ok := brandIconByKey(chat.AccountID); ok && len(icon.SHA256) >= 12 {
		return icon.SHA256[:12]
	}
	return "generated"
}

func drawPersonSilhouette(dst *image.RGBA) {
	fillCircle(dst, 128, 99, 42, color.RGBA{R: 255, G: 255, B: 255, A: 235})
	fillCircle(dst, 128, 220, 82, color.RGBA{R: 255, G: 255, B: 255, A: 220})
	fillCircle(dst, 93, 93, 16, color.RGBA{R: 255, G: 255, B: 255, A: 60})
	fillCircle(dst, 163, 93, 16, color.RGBA{R: 255, G: 255, B: 255, A: 60})
}

func contactFallbackColor(chat Chat) color.RGBA {
	sum := sha256.Sum256([]byte(firstNonEmpty(chat.ID, chat.Name, chat.AccountID, "beeper")))
	palette := []string{"#2F343D", "#3F3656", "#334B46", "#563D45", "#4C4633", "#36435D", "#4A3C58"}
	return parseHexColor(palette[int(sum[0])%len(palette)])
}

func blendCircle(img *image.RGBA, cx, cy, r int, c color.RGBA) {
	rr := r * r
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= rr && image.Pt(x, y).In(img.Bounds()) {
				blendAt(img, x, y, c)
			}
		}
	}
}

func blendAt(img *image.RGBA, x, y int, src color.Color) {
	sr16, sg16, sb16, sa16 := src.RGBA()
	if sa16 == 0 {
		return
	}
	dr16, dg16, db16, da16 := img.At(x, y).RGBA()
	sa := float64(sa16) / 65535
	da := float64(da16) / 65535
	outA := sa + da*(1-sa)
	if outA == 0 {
		img.SetRGBA(x, y, color.RGBA{})
		return
	}
	out := func(s, d uint32) uint8 {
		v := (float64(s)/65535*sa + float64(d)/65535*da*(1-sa)) / outA
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		return uint8(v*255 + 0.5)
	}
	img.SetRGBA(x, y, color.RGBA{
		R: out(sr16, dr16),
		G: out(sg16, dg16),
		B: out(sb16, db16),
		A: uint8(outA*255 + 0.5),
	})
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
