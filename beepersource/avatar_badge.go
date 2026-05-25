package beepersource

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	_ "image/gif"
	_ "image/jpeg"
)

const avatarBadgeSize = 256
const avatarBadgeCacheVersion = "avatar-badge-v9"
const contactFallbackCacheVersion = "avatar-fallback-v17"
const uiAvatarFontSize = 0.4375

type avatarBadgeOptions struct {
	position                   string
	layout                     string
	shape                      string
	sizePercent                int
	insetPercent               int
	shadow                     bool
	groupAvatarStyle           string
	groupAvatarMaxParticipants int
	groupAvatarOverlapPercent  int
	groupAvatarExcludeSelf     bool
	groupAvatarSelfIDs         []string
}

func (s *Service) badgePortalAvatar(chat Chat, avatar *MatrixMedia) (*MatrixMedia, error) {
	if avatar == nil || !s.cfg.Matrix.AvatarBadges {
		return avatar, nil
	}
	badged, err := addPlatformBadgeToAvatarWithOptions(chat, avatar, s.avatarBadgeOptions())
	if err != nil {
		return avatar, nil
	}
	return badged, nil
}

func addPlatformBadgeToAvatar(chat Chat, avatar *MatrixMedia) (*MatrixMedia, error) {
	return addPlatformBadgeToAvatarWithOptions(chat, avatar, defaultAvatarBadgeOptions())
}

func addPlatformBadgeToAvatarWithOptions(chat Chat, avatar *MatrixMedia, opts avatarBadgeOptions) (*MatrixMedia, error) {
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
	drawPlatformBadgeWithOptions(canvas, logo, badgeColor, opts.normalized())

	var out bytes.Buffer
	if err := png.Encode(&out, canvas); err != nil {
		return nil, err
	}
	fileName := strings.TrimSuffix(firstNonEmpty(avatar.FileName, "beeper-avatar"), filepath.Ext(avatar.FileName)) + "-badged.png"
	assetID := avatar.AssetID
	if assetID != "" {
		assetID = avatarBadgeCacheVersion + ":" + avatar.AssetID + ":" + platformBadgeIconHash(chat) + ":" + opts.normalized().cacheKey()
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
	return generatedContactAvatarMediaWithOptions(chat, defaultAvatarBadgeOptions())
}

func generatedContactAvatarMediaWithOptions(chat Chat, opts avatarBadgeOptions) *MatrixMedia {
	platform := PlatformDisplayName(chat)
	canvas := image.NewRGBA(image.Rect(0, 0, avatarBadgeSize, avatarBadgeSize))
	opts = opts.normalized()
	if participant, ok := singleParticipantGroupFallback(chat, opts); ok {
		drawSingleParticipantGroupFallbackBase(canvas, participant)
	} else if shouldDrawGroupFallback(chat, opts) {
		drawGroupFallbackBase(canvas, chat, opts)
	} else {
		drawContactFallbackBase(canvas, chat)
	}
	if logo, badgeColor, ok := platformBadgeGlyph(chat); ok {
		drawPlatformBadgeWithOptions(canvas, logo, badgeColor, opts)
	}
	var out bytes.Buffer
	_ = png.Encode(&out, canvas)
	body := out.Bytes()
	contentHash := fmt.Sprintf("%x", sha256.Sum256(body))
	return &MatrixMedia{
		AssetID:     generatedContactAvatarAssetIDWithOptions(chat, opts),
		ContentHash: contentHash,
		Content:     bytes.NewReader(body),
		FileName:    strings.ToLower(strings.ReplaceAll(platform, " ", "-")) + "-contact-avatar.png",
		MimeType:    "image/png",
		SizeBytes:   int64(len(body)),
	}
}

func generatedContactAvatarAssetID(chat Chat) string {
	return generatedContactAvatarAssetIDWithOptions(chat, defaultAvatarBadgeOptions())
}

func generatedContactAvatarAssetIDWithOptions(chat Chat, opts avatarBadgeOptions) string {
	platform := PlatformDisplayName(chat)
	return contactFallbackCacheVersion + ":" + firstNonEmpty(chat.ID, chat.Name, chat.AccountID, "unknown") + ":" + groupAvatarSignature(chat, opts.normalized()) + ":" + brandIconKey(platform) + ":" + platformBadgeIconHash(chat) + ":" + initialsFontHash() + ":" + opts.normalized().cacheKey()
}

func badgedAvatarAssetID(chat Chat, avatarAssetID string) string {
	return badgedAvatarAssetIDWithOptions(chat, avatarAssetID, defaultAvatarBadgeOptions())
}

func badgedAvatarAssetIDWithOptions(chat Chat, avatarAssetID string, opts avatarBadgeOptions) string {
	if strings.TrimSpace(avatarAssetID) == "" {
		return ""
	}
	return avatarBadgeCacheVersion + ":" + avatarAssetID + ":" + platformBadgeIconHash(chat) + ":" + opts.normalized().cacheKey()
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

func (s *Service) avatarBadgeOptions() avatarBadgeOptions {
	return avatarBadgeOptions{
		position:                   s.cfg.Matrix.AvatarBadgePosition,
		layout:                     s.cfg.Matrix.AvatarBadgeLayout,
		shape:                      s.cfg.Matrix.AvatarBadgeShape,
		sizePercent:                s.cfg.Matrix.AvatarBadgeSizePercent,
		insetPercent:               s.cfg.Matrix.AvatarBadgeInsetPercent,
		shadow:                     s.cfg.Matrix.AvatarBadgeShadow,
		groupAvatarStyle:           s.cfg.Matrix.GroupAvatarStyle,
		groupAvatarMaxParticipants: s.cfg.Matrix.GroupAvatarMaxParticipants,
		groupAvatarOverlapPercent:  s.cfg.Matrix.GroupAvatarOverlapPercent,
		groupAvatarExcludeSelf:     s.cfg.Matrix.GroupAvatarExcludeSelf,
		groupAvatarSelfIDs:         append(append([]string{}, s.cfg.Matrix.GroupAvatarSelfIDs...), s.cfg.Matrix.UserID),
	}.normalized()
}

func defaultAvatarBadgeOptions() avatarBadgeOptions {
	return avatarBadgeOptions{
		position:                   "bottom-right",
		layout:                     "circle-safe",
		shape:                      "rounded",
		sizePercent:                22,
		insetPercent:               0,
		shadow:                     false,
		groupAvatarStyle:           "auto",
		groupAvatarMaxParticipants: 4,
		groupAvatarOverlapPercent:  34,
		groupAvatarExcludeSelf:     true,
	}
}

func (opts avatarBadgeOptions) normalized() avatarBadgeOptions {
	out := opts
	out.position = strings.ToLower(strings.TrimSpace(out.position))
	out.layout = strings.ToLower(strings.TrimSpace(out.layout))
	out.shape = strings.ToLower(strings.TrimSpace(out.shape))
	out.groupAvatarStyle = strings.ToLower(strings.TrimSpace(out.groupAvatarStyle))
	out.groupAvatarSelfIDs = normalizeStringList(out.groupAvatarSelfIDs)
	if out.position == "" {
		out.position = "bottom-right"
	}
	switch out.position {
	case "bottom-right", "bottom-left", "top-right", "top-left":
	default:
		out.position = "bottom-right"
	}
	if out.layout == "" {
		out.layout = "edge"
	}
	switch out.layout {
	case "edge", "corner", "compact":
		out.layout = "edge"
	case "circle-safe", "cinnysafe", "cinny-safe", "floating":
		out.layout = "circle-safe"
	default:
		out.layout = "edge"
	}
	if out.shape == "" {
		out.shape = "rounded"
	}
	switch out.shape {
	case "rounded", "app", "squircle":
		out.shape = "rounded"
	case "circle", "round":
		out.shape = "circle"
	default:
		out.shape = "rounded"
	}
	if out.sizePercent <= 0 {
		out.sizePercent = 31
	}
	if out.sizePercent < 18 {
		out.sizePercent = 18
	}
	if out.sizePercent > 42 {
		out.sizePercent = 42
	}
	if out.insetPercent < 0 {
		out.insetPercent = 0
	}
	if out.insetPercent > 16 {
		out.insetPercent = 16
	}
	if out.groupAvatarStyle == "" {
		out.groupAvatarStyle = "auto"
	}
	switch out.groupAvatarStyle {
	case "auto", "bubbles", "cluster", "beeper":
		out.groupAvatarStyle = "auto"
	case "initials", "single", "off", "none":
		out.groupAvatarStyle = "initials"
	default:
		out.groupAvatarStyle = "auto"
	}
	if out.groupAvatarMaxParticipants <= 0 {
		out.groupAvatarMaxParticipants = 4
	}
	if out.groupAvatarMaxParticipants < 2 {
		out.groupAvatarMaxParticipants = 2
	}
	if out.groupAvatarMaxParticipants > 10 {
		out.groupAvatarMaxParticipants = 10
	}
	if out.groupAvatarOverlapPercent < 0 {
		out.groupAvatarOverlapPercent = 0
	}
	if out.groupAvatarOverlapPercent > 42 {
		out.groupAvatarOverlapPercent = 42
	}
	return out
}

func (opts avatarBadgeOptions) cacheKey() string {
	opts = opts.normalized()
	shadow := "noshadow"
	if opts.shadow {
		shadow = "shadow"
	}
	self := "includeself"
	if opts.groupAvatarExcludeSelf {
		self = "excludeself"
	}
	selfHash := "noselfids"
	if len(opts.groupAvatarSelfIDs) > 0 {
		sum := sha256.Sum256([]byte(strings.Join(opts.groupAvatarSelfIDs, "\x00")))
		selfHash = fmt.Sprintf("%x", sum[:4])
	}
	return fmt.Sprintf("%s:%s:%s:%d:%d:%s:group-%s-%d-%d-%s-%s", opts.position, opts.layout, opts.shape, opts.sizePercent, opts.insetPercent, shadow, opts.groupAvatarStyle, opts.groupAvatarMaxParticipants, opts.groupAvatarOverlapPercent, self, selfHash)
}

func drawPlatformBadge(dst *image.RGBA, logo image.Image, badgeColor color.RGBA) {
	drawPlatformBadgeWithOptions(dst, logo, badgeColor, defaultAvatarBadgeOptions())
}

func drawPlatformBadgeWithOptions(dst *image.RGBA, logo image.Image, _ color.RGBA, opts avatarBadgeOptions) {
	opts = opts.normalized()
	x, y, size := avatarBadgeRect(opts)
	radius := size / 4
	if opts.shape == "circle" {
		radius = size / 2
	}

	if opts.shadow {
		drawSoftRoundedShadow(dst, x, y+1, size, size, radius)
	}
	drawNormalizedIcon(dst, logo, x, y, size, size, radius)
}

func avatarBadgeRect(opts avatarBadgeOptions) (x, y, size int) {
	opts = opts.normalized()
	size = avatarBadgeSize * opts.sizePercent / 100
	inset := avatarBadgeSize * opts.insetPercent / 100
	if opts.layout == "circle-safe" {
		// Keep the whole badge inside a hard circular Matrix/Cinny mask. For
		// rounded app tiles the limiting radius is the half diagonal, not the
		// half width, otherwise the tile corner gets clipped by circular clients.
		radius := avatarBadgeSize / 2
		badgeRadius := size / 2
		limitRadius := float64(badgeRadius)
		if opts.shape != "circle" {
			limitRadius = float64(size) * 0.707
		}
		centerOffset := int((float64(radius)-limitRadius)*0.707 + 0.5)
		cx := radius + centerOffset
		cy := radius + centerOffset
		x = cx - badgeRadius
		y = cy - badgeRadius
	} else {
		x = avatarBadgeSize - size - inset
		y = avatarBadgeSize - size - inset
	}
	if strings.HasSuffix(opts.position, "left") {
		x = avatarBadgeSize - size - x
	}
	if strings.HasPrefix(opts.position, "top") {
		y = avatarBadgeSize - size - y
	}
	return x, y, size
}

func drawSoftRoundedShadow(dst *image.RGBA, x, y, w, h, radius int) {
	layers := []struct {
		dx, dy int
		spread int
		alpha  uint8
	}{
		{dx: 0, dy: 1, spread: 3, alpha: 2},
		{dx: 0, dy: 1, spread: 2, alpha: 3},
		{dx: 0, dy: 1, spread: 1, alpha: 4},
	}
	for _, layer := range layers {
		fillRounded(
			dst,
			x+layer.dx-layer.spread,
			y+layer.dy-layer.spread,
			w+layer.spread*2,
			h+layer.spread*2,
			radius+layer.spread,
			color.RGBA{R: 0, G: 0, B: 0, A: layer.alpha},
		)
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
	if body, ok := brandIconPNGByKey(PlatformDisplayName(chat)); ok {
		sum := fmt.Sprintf("%x", sha256.Sum256(body))
		return sum[:12]
	}
	if body, ok := brandIconPNGByKey(chat.AccountID); ok {
		sum := fmt.Sprintf("%x", sha256.Sum256(body))
		return sum[:12]
	}
	return "generated"
}

func drawContactFallbackBase(dst *image.RGBA, chat Chat) {
	style := uiAvatarStyle(firstNonEmpty(chat.Name, chat.ID, chat.AccountID, "?"))
	drawInitialsGradient(dst, style.gradient)
	drawInitialsWithColor(dst, contactInitials(chat), avatarInitialsTextColor())
}

func shouldDrawGroupFallback(chat Chat, opts avatarBadgeOptions) bool {
	opts = opts.normalized()
	if opts.groupAvatarStyle == "initials" {
		return false
	}
	return chat.IsGroup && len(groupAvatarParticipantLabels(chat, opts)) >= 2
}

func singleParticipantGroupFallback(chat Chat, opts avatarBadgeOptions) (groupAvatarParticipant, bool) {
	opts = opts.normalized()
	if !chat.IsGroup || opts.groupAvatarStyle == "initials" {
		return groupAvatarParticipant{}, false
	}
	participants := groupAvatarParticipants(chat, opts)
	if len(participants) != 1 {
		return groupAvatarParticipant{}, false
	}
	return participants[0], true
}

type groupAvatarBubble struct {
	label        string
	messageCount int
	cx           int
	cy           int
	r            int
	index        int
}

type groupAvatarParticipant struct {
	label        string
	messageCount int
}

func drawGroupFallbackBase(dst *image.RGBA, chat Chat, opts avatarBadgeOptions) {
	opts = opts.normalized()
	drawInitialsGradient(dst, contactFallbackGradient(chat))
	fillRounded(dst, 0, 0, avatarBadgeSize, avatarBadgeSize, avatarBadgeSize/4, color.RGBA{R: 0, G: 0, B: 0, A: 34})
	participants := groupAvatarParticipants(chat, opts)
	bubbles := groupAvatarBubbleLayoutForParticipantsWithOptions(participants, opts.groupAvatarOverlapPercent, opts)
	for _, bubble := range bubbles {
		drawGradientCircle(dst, bubble.cx, bubble.cy, bubble.r, groupBubbleGradient(bubble.label, bubble.index))
	}
	for _, bubble := range bubbles {
		drawInitialsInCircleWithColor(dst, initialsFromLabel(bubble.label), bubble.cx, bubble.cy, bubble.r, avatarInitialsTextColor())
	}
}

func drawSingleParticipantGroupFallbackBase(dst *image.RGBA, participant groupAvatarParticipant) {
	label := firstNonEmpty(participant.label, "?")
	style := uiAvatarStyle(label)
	drawInitialsGradient(dst, style.gradient)
	drawInitialsWithColor(dst, initialsFromLabel(label), avatarInitialsTextColor())
}

func groupAvatarParticipantLabels(chat Chat, opts avatarBadgeOptions) []string {
	participants := groupAvatarParticipants(chat, opts)
	labels := make([]string, 0, len(participants))
	for _, participant := range participants {
		labels = append(labels, participant.label)
	}
	return labels
}

func groupAvatarParticipants(chat Chat, opts avatarBadgeOptions) []groupAvatarParticipant {
	opts = opts.normalized()
	maxParticipants := opts.groupAvatarMaxParticipants
	if maxParticipants <= 0 {
		maxParticipants = 4
	}
	seen := map[string]bool{}
	participants := make([]groupAvatarParticipant, 0, maxParticipants+1)
	add := func(label string, messageCount int) {
		label = strings.TrimSpace(label)
		if label == "" {
			return
		}
		key := strings.ToLower(label)
		if seen[key] {
			return
		}
		seen[key] = true
		participants = append(participants, groupAvatarParticipant{label: label, messageCount: messageCount})
	}
	for _, participant := range chat.Participants {
		if isSelfParticipant(participant, opts) {
			continue
		}
		add(firstNonEmpty(participant.DisplayName, participant.ID), participant.MessageCount)
	}
	if len(participants) == 0 {
		for _, part := range strings.FieldsFunc(chat.Name, func(r rune) bool {
			return r == ',' || r == '&' || r == '+' || r == '/' || r == '|'
		}) {
			add(part, 0)
		}
	}
	if hasMessageCounts(participants) {
		sort.SliceStable(participants, func(i, j int) bool {
			return participants[i].messageCount > participants[j].messageCount
		})
	}
	if len(participants) > maxParticipants && maxParticipants >= 2 {
		hidden := len(participants) - (maxParticipants - 1)
		participants = append(append([]groupAvatarParticipant{}, participants[:maxParticipants-1]...), groupAvatarParticipant{
			label:        fmt.Sprintf("+%d", hidden),
			messageCount: 1,
		})
	}
	if len(participants) > maxParticipants {
		participants = participants[:maxParticipants]
	}
	return participants
}

func hasMessageCounts(participants []groupAvatarParticipant) bool {
	for _, participant := range participants {
		if participant.messageCount > 0 {
			return true
		}
	}
	return false
}

func isSelfParticipant(participant Sender, opts avatarBadgeOptions) bool {
	if !opts.groupAvatarExcludeSelf {
		return false
	}
	candidates := []string{participant.ID, participant.DisplayName}
	commonSelfLabels := map[string]bool{
		"me": true, "you": true, "self": true, "ich": true, "du": true, "mich": true,
	}
	for _, candidate := range candidates {
		key := strings.ToLower(strings.TrimSpace(candidate))
		if key == "" {
			continue
		}
		if commonSelfLabels[key] {
			return true
		}
		for _, selfID := range opts.groupAvatarSelfIDs {
			if key == strings.ToLower(strings.TrimSpace(selfID)) {
				return true
			}
		}
	}
	return false
}

func groupAvatarBubbleLayout(labels []string, overlapPercent int) []groupAvatarBubble {
	participants := make([]groupAvatarParticipant, 0, len(labels))
	for _, label := range labels {
		participants = append(participants, groupAvatarParticipant{label: label, messageCount: 1})
	}
	return groupAvatarBubbleLayoutForParticipants(participants, overlapPercent)
}

func groupAvatarBubbleLayoutForParticipants(participants []groupAvatarParticipant, overlapPercent int) []groupAvatarBubble {
	return groupAvatarBubbleLayoutForParticipantsWithOptions(participants, overlapPercent, defaultAvatarBadgeOptions())
}

func groupAvatarBubbleLayoutForParticipantsWithOptions(participants []groupAvatarParticipant, overlapPercent int, opts avatarBadgeOptions) []groupAvatarBubble {
	labels := make([]string, 0, len(participants))
	for _, participant := range participants {
		labels = append(labels, participant.label)
	}
	count := len(labels)
	if count == 0 {
		return nil
	}
	if count > 10 {
		count = 10
		labels = labels[:count]
	}
	gap := groupAvatarBubbleGap(overlapPercent)
	r := groupAvatarBaseRadius(count)
	radii := make([]int, count)
	maxWeight := 1
	for _, participant := range participants[:count] {
		if participant.messageCount > maxWeight {
			maxWeight = participant.messageCount
		}
	}
	for i := 0; i < count; i++ {
		weight := participants[i].messageCount
		if weight <= 0 {
			weight = 1
		}
		bubbleR := r
		if maxWeight > 1 {
			ratio := math.Sqrt(float64(weight) / float64(maxWeight))
			if ratio < 0 {
				ratio = 0
			}
			scale := 0.78 + 0.22*ratio
			bubbleR = int(float64(r)*scale + 0.5)
			minR := 18
			if count <= 4 {
				minR = 26
			}
			if bubbleR < minR {
				bubbleR = minR
			}
		}
		radii[i] = bubbleR
	}
	coords := groupAvatarBubbleCenters(radii, gap)
	bubbles := make([]groupAvatarBubble, 0, count)
	for i := 0; i < count; i++ {
		bubbles = append(bubbles, groupAvatarBubble{
			label:        labels[i],
			messageCount: participants[i].messageCount,
			cx:           int(math.Round(coords[i][0])),
			cy:           int(math.Round(coords[i][1])),
			r:            radii[i],
			index:        i,
		})
	}
	return enforceGroupBubblePadding(bubbles, gap, opts.normalized())
}

func groupAvatarBubbleCenters(radii []int, gap int) [][2]float64 {
	count := len(radii)
	points := [][][2]float64{
		{{128, 128}},
		{{140, 78}, {76, 166}},
		{{72, 76}, {162, 82}, {82, 164}},
		{{70, 74}, {156, 82}, {78, 160}, {132, 136}},
	}
	if count <= 4 {
		return append([][2]float64{}, points[count-1]...)
	}
	if count >= 5 {
		return normalizeGroupBubbleCenters(groupAvatarFixedCenters(count), radii)
	}

	r := func(i int) float64 { return float64(radii[i] + gap) }
	coords := make([][2]float64, count)
	coords[0] = [2]float64{96, 78}
	coords[1] = [2]float64{coords[0][0] + r(0) + float64(radii[1]), coords[0][1]}
	coords[2] = [2]float64{
		coords[1][0] + (r(1)+float64(radii[2]))*math.Cos(math.Pi/5),
		coords[1][1] + (r(1)+float64(radii[2]))*math.Sin(math.Pi/5),
	}
	coords[3] = tangentCircleCenter(coords[0], r(0)+float64(radii[3]), coords[1], r(1)+float64(radii[3]), 1)
	coords[4] = tangentCircleCenter(coords[0], r(0)+float64(radii[4]), coords[3], r(3)+float64(radii[4]), 1)
	if count == 5 {
		return normalizeGroupBubbleCenters(coords[:5], radii)
	}
	coords[5] = tangentCircleCenter(coords[3], r(3)+float64(radii[5]), coords[4], r(4)+float64(radii[5]), -1)
	return normalizeGroupBubbleCenters(coords, radii)
}

func groupAvatarFixedCenters(count int) [][2]float64 {
	layouts := map[int][][2]float64{
		5:  {{76, 72}, {150, 74}, {198, 100}, {70, 146}, {128, 132}},
		6:  {{70, 66}, {142, 66}, {200, 98}, {106, 124}, {54, 144}, {96, 194}},
		7:  {{70, 60}, {124, 56}, {178, 76}, {86, 104}, {140, 104}, {44, 132}, {98, 150}},
		8:  {{70, 60}, {124, 56}, {178, 76}, {86, 104}, {140, 104}, {44, 132}, {98, 150}, {60, 190}},
		9:  {{70, 60}, {124, 56}, {178, 76}, {86, 104}, {140, 104}, {44, 132}, {98, 150}, {60, 190}, {100, 218}},
		10: {{68, 58}, {120, 54}, {172, 74}, {84, 100}, {136, 100}, {42, 128}, {94, 146}, {58, 184}, {112, 184}, {82, 220}},
	}
	if centers, ok := layouts[count]; ok {
		return append([][2]float64{}, centers...)
	}
	return append([][2]float64{}, layouts[10]...)
}

func groupAvatarBaseRadius(count int) int {
	switch {
	case count <= 1:
		return 56
	case count == 2:
		return 36
	case count == 3:
		return 39
	case count == 4:
		return 37
	case count == 5:
		return 32
	case count == 6:
		return 29
	case count == 7:
		return 26
	case count == 8:
		return 24
	case count == 9:
		return 22
	default:
		return 20
	}
}

func tangentCircleCenter(a [2]float64, ar float64, b [2]float64, br float64, sign float64) [2]float64 {
	dx := b[0] - a[0]
	dy := b[1] - a[1]
	d := math.Hypot(dx, dy)
	if d == 0 {
		return [2]float64{a[0], a[1] + ar}
	}
	x := (ar*ar - br*br + d*d) / (2 * d)
	h := math.Sqrt(math.Max(0, ar*ar-x*x))
	mx := a[0] + x*dx/d
	my := a[1] + x*dy/d
	return [2]float64{
		mx + sign*h*(-dy/d),
		my + sign*h*(dx/d),
	}
}

func normalizeGroupBubbleCenters(coords [][2]float64, radii []int) [][2]float64 {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for i, point := range coords {
		r := float64(radii[i])
		minX = math.Min(minX, point[0]-r)
		minY = math.Min(minY, point[1]-r)
		maxX = math.Max(maxX, point[0]+r)
		maxY = math.Max(maxY, point[1]+r)
	}
	targetCX, targetCY := 124.0, 112.0
	if len(coords) >= 6 {
		targetCY = 114
	}
	currentCX := (minX + maxX) / 2
	currentCY := (minY + maxY) / 2
	dx := targetCX - currentCX
	dy := targetCY - currentCY
	out := make([][2]float64, len(coords))
	for i, point := range coords {
		out[i] = [2]float64{point[0] + dx, point[1] + dy}
	}
	return out
}

func enforceGroupBubblePadding(bubbles []groupAvatarBubble, gap int, opts avatarBadgeOptions) []groupAvatarBubble {
	if len(bubbles) == 0 {
		return bubbles
	}
	badgeX, badgeY, badgeSize := avatarBadgeRect(opts)
	badgeCX := badgeX + badgeSize/2
	badgeCY := badgeY + badgeSize/2
	badgeR := int(float64(badgeSize)*0.707 + 0.5)
	badgeGap := groupAvatarBadgeGap(gap)
	outerGap := groupAvatarOuterGap()
	avatarCX, avatarCY := float64(avatarBadgeSize)/2, float64(avatarBadgeSize)/2
	avatarR := float64(avatarBadgeSize)/2 - float64(outerGap)

	scale := groupAvatarMaxScale(len(bubbles))
	for _, bubble := range bubbles {
		if bubble.r <= 0 {
			continue
		}
		limits := []int{
			bubble.cx - gap,
			bubble.cy - gap,
			avatarBadgeSize - bubble.cx - gap,
			avatarBadgeSize - bubble.cy - gap,
		}
		for _, limit := range limits {
			if limit <= 0 {
				scale = 0
				continue
			}
			scale = math.Min(scale, float64(limit)/float64(bubble.r))
		}
		circleLimit := avatarR - math.Hypot(float64(bubble.cx)-avatarCX, float64(bubble.cy)-avatarCY)
		if circleLimit <= 0 {
			scale = 0
		} else {
			scale = math.Min(scale, circleLimit/float64(bubble.r))
		}
		dx := bubble.cx - badgeCX
		dy := bubble.cy - badgeCY
		distance := math.Sqrt(float64(dx*dx + dy*dy))
		limit := distance - float64(badgeR) - float64(badgeGap)
		if limit <= 0 {
			scale = 0
		} else {
			scale = math.Min(scale, limit/float64(bubble.r))
		}
	}
	for i := 0; i < len(bubbles); i++ {
		for j := i + 1; j < len(bubbles); j++ {
			dx := bubbles[i].cx - bubbles[j].cx
			dy := bubbles[i].cy - bubbles[j].cy
			distance := math.Sqrt(float64(dx*dx + dy*dy))
			limit := distance - float64(gap)
			if limit <= 0 {
				scale = 0
				continue
			}
			scale = math.Min(scale, limit/float64(bubbles[i].r+bubbles[j].r))
		}
	}
	if scale <= 0 {
		scale = 0.35
	}
	for i := range bubbles {
		bubbles[i].r = maxInt(12, int(math.Floor(float64(bubbles[i].r)*scale)))
	}
	return bubbles
}

func groupAvatarBubbleGap(overlapPercent int) int {
	return 2 + clampInt(overlapPercent, 0, 42)/21
}

func groupAvatarBadgeGap(bubbleGap int) int {
	return maxInt(12, bubbleGap+8)
}

func groupAvatarOuterGap() int {
	return 5
}

func groupAvatarMaxScale(count int) float64 {
	switch {
	case count <= 2:
		return 1.45
	case count == 3:
		return 1.25
	case count == 4:
		return 1.15
	case count <= 6:
		return 1.12
	default:
		return 1.08
	}
}

func groupBubbleGradient(label string, index int) avatarGradient {
	gradient := uiAvatarStyle(label).gradient
	if index%3 == 1 {
		gradient.top = shiftColor(gradient.top, 6)
		gradient.bottom = shiftColor(gradient.bottom, -6)
	}
	if index%3 == 2 {
		gradient.top = shiftColor(gradient.top, -4)
		gradient.bottom = shiftColor(gradient.bottom, 8)
	}
	return gradient
}

func drawGradientCircle(dst *image.RGBA, cx, cy, r int, gradient avatarGradient) {
	rr := r * r
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			if !image.Pt(x, y).In(dst.Bounds()) {
				continue
			}
			dx := x - cx
			dy := y - cy
			if dx*dx+dy*dy > rr {
				continue
			}
			t := (float64(dx+r)/float64(maxInt(2*r, 1)) + float64(dy+r)/float64(maxInt(2*r, 1))) / 2
			blendAt(dst, x, y, color.RGBA{
				R: lerpByte(gradient.top.R, gradient.bottom.R, t),
				G: lerpByte(gradient.top.G, gradient.bottom.G, t),
				B: lerpByte(gradient.top.B, gradient.bottom.B, t),
				A: 255,
			})
		}
	}
}

func drawInitialsInCircle(dst *image.RGBA, initials string, cx, cy, r int) {
	drawInitialsInCircleWithColor(dst, initials, cx, cy, r, color.RGBA{R: 255, G: 255, B: 255, A: 245})
}

func drawInitialsInCircleWithColor(dst *image.RGBA, initials string, cx, cy, r int, textColor color.RGBA) {
	letters := []rune(initials)
	if len(letters) == 0 {
		letters = []rune{'?'}
	}
	if len(letters) > 2 {
		letters = letters[:2]
	}
	if drawInitialsWithSystemFontAt(dst, string(letters), cx, cy, r, textColor) {
		return
	}
	cell := maxInt(r/9, 3)
	if len(letters) == 1 {
		cell = maxInt(r/8, 4)
	}
	spacing := cell / 2
	totalW := len(letters)*5*cell + (len(letters)-1)*spacing
	totalH := 7 * cell
	startX := cx - totalW/2
	startY := cy - totalH/2
	for i, letter := range letters {
		drawGlyph(dst, normalizeInitialRune(letter), startX+i*(5*cell+spacing), startY, cell, textColor)
	}
}

func initialsFromLabel(label string) string {
	if strings.HasPrefix(strings.TrimSpace(label), "+") {
		return strings.TrimSpace(label)
	}
	return contactInitials(Chat{Name: label})
}

func groupAvatarSignature(chat Chat, opts avatarBadgeOptions) string {
	opts = opts.normalized()
	participants := groupAvatarParticipants(chat, opts)
	if !chat.IsGroup || opts.groupAvatarStyle == "initials" || len(participants) == 0 {
		return "single"
	}
	parts := make([]string, 0, len(participants))
	for _, participant := range participants {
		parts = append(parts, fmt.Sprintf("%s:%d", participant.label, participant.messageCount))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("group-%d-%x", len(participants), sum[:6])
}

type avatarGradient struct {
	top    color.RGBA
	bottom color.RGBA
}

type uiAvatarRenderStyle struct {
	background color.RGBA
	textColor  color.RGBA
	gradient   avatarGradient
}

func uiAvatarStyle(name string) uiAvatarRenderStyle {
	background := beeperPastelBackground(name)
	return uiAvatarRenderStyle{
		background: background,
		textColor:  avatarInitialsTextColor(),
		gradient: avatarGradient{
			top:    shiftColor(background, 16),
			bottom: shiftColor(background, -22),
		},
	}
}

func beeperPastelBackground(name string) color.RGBA {
	hue := float64(crc32.ChecksumIEEE([]byte(name)) % 360)
	return hslToRGB(hue, 0.62, 0.58)
}

func avatarInitialsTextColor() color.RGBA {
	return color.RGBA{R: 255, G: 255, B: 255, A: 255}
}

func uiAvatarAutoBackground(name string, saturationPercent int, luminancePercent int) color.RGBA {
	hue := float64(crc32.ChecksumIEEE([]byte(name))%360) / 360
	return uiAvatarHSLToRGB(hue, float64(saturationPercent)/100, float64(luminancePercent)/100)
}

func uiAvatarHSLToRGB(h, s, l float64) color.RGBA {
	red := l
	green := l
	blue := l
	v := l + s - l*s
	if l <= 0.5 {
		v = l * (1.0 + s)
	}
	if v > 0 {
		m := l + l - v
		sv := (v - m) / v
		h *= 6.0
		sextant := math.Floor(h)
		fract := h - sextant
		vsf := v * sv * fract
		mid1 := m + vsf
		mid2 := v - vsf
		switch int(sextant) {
		case 0:
			red, green, blue = v, mid1, m
		case 1:
			red, green, blue = mid2, v, m
		case 2:
			red, green, blue = m, v, mid1
		case 3:
			red, green, blue = m, mid2, v
		case 4:
			red, green, blue = mid1, m, v
		default:
			red, green, blue = v, m, mid2
		}
	}
	return color.RGBA{
		R: clampByte(int(red*255 + 0.5)),
		G: clampByte(int(green*255 + 0.5)),
		B: clampByte(int(blue*255 + 0.5)),
		A: 255,
	}
}

func uiAvatarContrastColor(background color.RGBA) color.RGBA {
	l1 := 0.2126*math.Pow(float64(background.R)/255, 2.2) +
		0.7152*math.Pow(float64(background.G)/255, 2.2) +
		0.0722*math.Pow(float64(background.B)/255, 2.2)
	contrastRatio := int((l1 + 0.05) / 0.05)
	if contrastRatio > 5 {
		return color.RGBA{R: 0, G: 0, B: 0, A: 245}
	}
	return color.RGBA{R: 255, G: 255, B: 255, A: 245}
}

var contactFallbackGradients = []avatarGradient{
	{top: parseHexColor("#FF4D8D"), bottom: parseHexColor("#B5179E")},
	{top: parseHexColor("#7C5CFF"), bottom: parseHexColor("#243BDE")},
	{top: parseHexColor("#20D6C7"), bottom: parseHexColor("#008E9B")},
	{top: parseHexColor("#35D07F"), bottom: parseHexColor("#0B8F4D")},
	{top: parseHexColor("#FFB340"), bottom: parseHexColor("#E65A2E")},
	{top: parseHexColor("#36A8FF"), bottom: parseHexColor("#1455D9")},
	{top: parseHexColor("#C7F043"), bottom: parseHexColor("#53B800")},
	{top: parseHexColor("#FF6B6B"), bottom: parseHexColor("#C92A5A")},
	{top: parseHexColor("#00C2FF"), bottom: parseHexColor("#0072FF")},
	{top: parseHexColor("#A66CFF"), bottom: parseHexColor("#6B2CCF")},
	{top: parseHexColor("#20E3B2"), bottom: parseHexColor("#0D7E83")},
	{top: parseHexColor("#F54EA2"), bottom: parseHexColor("#7A4DFF")},
	{top: parseHexColor("#FF7A59"), bottom: parseHexColor("#D7263D")},
	{top: parseHexColor("#FFD166"), bottom: parseHexColor("#F77F00")},
	{top: parseHexColor("#06D6A0"), bottom: parseHexColor("#118AB2")},
	{top: parseHexColor("#EF476F"), bottom: parseHexColor("#7209B7")},
	{top: parseHexColor("#4CC9F0"), bottom: parseHexColor("#4361EE")},
	{top: parseHexColor("#F72585"), bottom: parseHexColor("#3A0CA3")},
	{top: parseHexColor("#80ED99"), bottom: parseHexColor("#2D6A4F")},
	{top: parseHexColor("#F9844A"), bottom: parseHexColor("#F94144")},
	{top: parseHexColor("#90BE6D"), bottom: parseHexColor("#277DA1")},
	{top: parseHexColor("#B8F2E6"), bottom: parseHexColor("#5E60CE")},
	{top: parseHexColor("#FFAFCC"), bottom: parseHexColor("#C9184A")},
	{top: parseHexColor("#BDE0FE"), bottom: parseHexColor("#4361EE")},
	{top: parseHexColor("#CAFFBF"), bottom: parseHexColor("#38B000")},
	{top: parseHexColor("#FDFFB6"), bottom: parseHexColor("#FF9F1C")},
	{top: parseHexColor("#9BF6FF"), bottom: parseHexColor("#0096C7")},
	{top: parseHexColor("#FFC6FF"), bottom: parseHexColor("#9D4EDD")},
	{top: parseHexColor("#A0C4FF"), bottom: parseHexColor("#3A86FF")},
	{top: parseHexColor("#FFADAD"), bottom: parseHexColor("#E63946")},
	{top: parseHexColor("#D0F4DE"), bottom: parseHexColor("#2A9D8F")},
	{top: parseHexColor("#FCF6BD"), bottom: parseHexColor("#F4A261")},
}

func drawInitialsGradient(dst *image.RGBA, gradient avatarGradient) {
	bounds := dst.Bounds()
	w := maxInt(bounds.Dx()-1, 1)
	h := maxInt(bounds.Dy()-1, 1)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			t := (float64(x-bounds.Min.X)/float64(w) + float64(y-bounds.Min.Y)/float64(h)) / 2
			dst.SetRGBA(x, y, color.RGBA{
				R: lerpByte(gradient.top.R, gradient.bottom.R, t),
				G: lerpByte(gradient.top.G, gradient.bottom.G, t),
				B: lerpByte(gradient.top.B, gradient.bottom.B, t),
				A: 255,
			})
		}
	}
}

func contactFallbackGradient(chat Chat) avatarGradient {
	sum := sha256.Sum256([]byte(firstNonEmpty(chat.ID, chat.Name, chat.AccountID, "beeper")))
	gradient := contactFallbackGradients[int(sum[0])%len(contactFallbackGradients)]
	if sum[1]%2 == 1 {
		gradient.top, gradient.bottom = gradient.bottom, gradient.top
	}
	return gradient
}

func contactInitials(chat Chat) string {
	return uiAvatarInitials(firstNonEmpty(chat.Name, chat.ID, chat.AccountID, "?"))
}

func uiAvatarInitials(label string) string {
	label = strings.TrimSpace(label)
	if strings.HasPrefix(label, "+") {
		return label
	}
	parts := strings.FieldsFunc(label, func(r rune) bool {
		return unicode.IsSpace(r) || r == '-' || r == '_' || r == '.' || r == ':' || r == '/' || r == '@'
	})
	cleanParts := make([][]rune, 0, len(parts))
	for _, part := range parts {
		letters := make([]rune, 0, len(part))
		for _, r := range part {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				letters = append(letters, unicode.ToUpper(r))
			}
		}
		if len(letters) > 0 {
			cleanParts = append(cleanParts, letters)
		}
	}
	if len(cleanParts) == 0 {
		return "?"
	}
	if len(cleanParts) == 1 {
		initials := cleanParts[0]
		if len(initials) > 2 {
			initials = initials[:2]
		}
		return string(initials)
	}
	initials := []rune{cleanParts[0][0], cleanParts[len(cleanParts)-1][0]}
	return string(initials)
}

func drawInitials(dst *image.RGBA, initials string) {
	drawInitialsWithColor(dst, initials, color.RGBA{R: 255, G: 255, B: 255, A: 255})
}

func drawInitialsWithColor(dst *image.RGBA, initials string, textColor color.RGBA) {
	letters := []rune(initials)
	if len(letters) == 0 {
		letters = []rune{'?'}
	}
	if len(letters) > 2 {
		letters = letters[:2]
	}
	if drawInitialsWithSystemFont(dst, string(letters), textColor) {
		return
	}
	cell := 16
	if len(letters) == 1 {
		cell = 22
	}
	spacing := cell
	totalW := len(letters)*5*cell + (len(letters)-1)*spacing
	totalH := 7 * cell
	startX := (avatarBadgeSize - totalW) / 2
	startY := (avatarBadgeSize-totalH)/2 - 4
	for i, r := range letters {
		drawGlyph(dst, normalizeInitialRune(r), startX+i*(5*cell+spacing), startY, cell, textColor)
	}
}

func drawInitialsWithSystemFont(dst *image.RGBA, initials string, textColor color.RGBA) bool {
	size := float64(avatarBadgeSize) * uiAvatarFontSize
	if len([]rune(initials)) == 1 {
		size = 112
	}
	face, ok := initialsFontFaceWithSize(size)
	if !ok {
		return false
	}
	d := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(textColor),
		Face: face,
	}
	bounds, _ := d.BoundString(initials)
	width := (bounds.Max.X - bounds.Min.X).Ceil()
	height := (bounds.Max.Y - bounds.Min.Y).Ceil()
	x := (avatarBadgeSize-width)/2 - bounds.Min.X.Ceil()
	y := (avatarBadgeSize-height)/2 - bounds.Min.Y.Ceil() - 2
	d.Dot = fixed.P(x, y)
	d.DrawString(initials)
	return true
}

func drawInitialsWithSystemFontAt(dst *image.RGBA, initials string, cx, cy, r int, textColor color.RGBA) bool {
	size := float64(r*2) * uiAvatarFontSize
	if len([]rune(initials)) == 1 {
		size = float64(r) * 0.82
	}
	face, ok := initialsFontFaceWithSize(size)
	if !ok {
		return false
	}
	d := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(textColor),
		Face: face,
	}
	bounds, _ := d.BoundString(initials)
	width := (bounds.Max.X - bounds.Min.X).Ceil()
	height := (bounds.Max.Y - bounds.Min.Y).Ceil()
	x := cx - width/2 - bounds.Min.X.Ceil()
	y := cy - height/2 - bounds.Min.Y.Ceil() - 1
	d.Dot = fixed.P(x, y)
	d.DrawString(initials)
	return true
}

func initialsFontFace(letterCount int) (font.Face, bool) {
	size := 92.0
	if letterCount == 1 {
		size = 112
	}
	return initialsFontFaceWithSize(size)
}

func initialsFontFaceWithSize(size float64) (font.Face, bool) {
	ft, ok := loadInitialsFont()
	if !ok {
		return nil, false
	}
	face, err := opentype.NewFace(ft, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, false
	}
	return face, true
}

var (
	initialsFontOnce sync.Once
	initialsFont     *opentype.Font
)

func loadInitialsFont() (*opentype.Font, bool) {
	initialsFontOnce.Do(func() {
		for _, path := range initialsFontPaths() {
			if path == "" {
				continue
			}
			body, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			ft, err := opentype.Parse(body)
			if err == nil {
				initialsFont = ft
				return
			}
		}
	})
	return initialsFont, initialsFont != nil
}

func initialsFontPaths() []string {
	paths := []string{
		strings.TrimSpace(os.Getenv("BEEPER_MATRIX_PROXY_INITIALS_FONT_FILE")),
		filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "matrix-archive-sync", "fonts", "Poppins.ttf"),
	}
	paths = append(paths,
		"/System/Library/Fonts/Supplemental/Arial Bold.ttf",
		"/System/Library/Fonts/SFNS.ttf",
	)
	return paths
}

func initialsFontHash() string {
	for _, path := range initialsFontPaths() {
		if path == "" {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(body))
		return sum[:12]
	}
	return "font-fallback"
}

func normalizeStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func normalizeInitialRune(r rune) rune {
	switch unicode.ToUpper(r) {
	case 'Ä':
		return 'A'
	case 'Ö':
		return 'O'
	case 'Ü':
		return 'U'
	default:
		return unicode.ToUpper(r)
	}
}

func drawGlyph(dst *image.RGBA, r rune, x, y, cell int, c color.RGBA) {
	rows, ok := glyph5x7[r]
	if !ok {
		rows = glyph5x7['?']
	}
	for yy, row := range rows {
		for xx, bit := range row {
			if bit != '1' {
				continue
			}
			draw.Draw(dst, image.Rect(x+xx*cell, y+yy*cell, x+(xx+1)*cell-2, y+(yy+1)*cell-2), &image.Uniform{C: c}, image.Point{}, draw.Src)
		}
	}
}

func shiftColor(c color.RGBA, delta int) color.RGBA {
	return color.RGBA{
		R: clampByte(int(c.R) + delta),
		G: clampByte(int(c.G) + delta),
		B: clampByte(int(c.B) + delta),
		A: c.A,
	}
}

func lerpByte(a, b uint8, t float64) uint8 {
	return clampByte(int(float64(a)*(1-t) + float64(b)*t + 0.5))
}

func clampByte(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func hslToRGB(h, s, l float64) color.RGBA {
	c := (1 - absFloat(2*l-1)) * s
	x := c * (1 - absFloat(modFloat(h/60, 2)-1))
	m := l - c/2
	var r1, g1, b1 float64
	switch {
	case h < 60:
		r1, g1, b1 = c, x, 0
	case h < 120:
		r1, g1, b1 = x, c, 0
	case h < 180:
		r1, g1, b1 = 0, c, x
	case h < 240:
		r1, g1, b1 = 0, x, c
	case h < 300:
		r1, g1, b1 = x, 0, c
	default:
		r1, g1, b1 = c, 0, x
	}
	return color.RGBA{
		R: clampByte(int((r1 + m) * 255)),
		G: clampByte(int((g1 + m) * 255)),
		B: clampByte(int((b1 + m) * 255)),
		A: 255,
	}
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func modFloat(a, b float64) float64 {
	for a >= b {
		a -= b
	}
	for a < 0 {
		a += b
	}
	return a
}

var glyph5x7 = map[rune][7]string{
	'?': {"01110", "10001", "00001", "00010", "00100", "00000", "00100"},
	'0': {"01110", "10001", "10011", "10101", "11001", "10001", "01110"},
	'1': {"00100", "01100", "00100", "00100", "00100", "00100", "01110"},
	'2': {"01110", "10001", "00001", "00010", "00100", "01000", "11111"},
	'3': {"11110", "00001", "00001", "01110", "00001", "00001", "11110"},
	'4': {"00010", "00110", "01010", "10010", "11111", "00010", "00010"},
	'5': {"11111", "10000", "10000", "11110", "00001", "00001", "11110"},
	'6': {"01110", "10000", "10000", "11110", "10001", "10001", "01110"},
	'7': {"11111", "00001", "00010", "00100", "01000", "01000", "01000"},
	'8': {"01110", "10001", "10001", "01110", "10001", "10001", "01110"},
	'9': {"01110", "10001", "10001", "01111", "00001", "00001", "01110"},
	'A': {"01110", "10001", "10001", "11111", "10001", "10001", "10001"},
	'B': {"11110", "10001", "10001", "11110", "10001", "10001", "11110"},
	'C': {"01111", "10000", "10000", "10000", "10000", "10000", "01111"},
	'D': {"11110", "10001", "10001", "10001", "10001", "10001", "11110"},
	'E': {"11111", "10000", "10000", "11110", "10000", "10000", "11111"},
	'F': {"11111", "10000", "10000", "11110", "10000", "10000", "10000"},
	'G': {"01111", "10000", "10000", "10011", "10001", "10001", "01110"},
	'H': {"10001", "10001", "10001", "11111", "10001", "10001", "10001"},
	'I': {"01110", "00100", "00100", "00100", "00100", "00100", "01110"},
	'J': {"00111", "00010", "00010", "00010", "00010", "10010", "01100"},
	'K': {"10001", "10010", "10100", "11000", "10100", "10010", "10001"},
	'L': {"10000", "10000", "10000", "10000", "10000", "10000", "11111"},
	'M': {"10001", "11011", "10101", "10101", "10001", "10001", "10001"},
	'N': {"10001", "11001", "10101", "10011", "10001", "10001", "10001"},
	'O': {"01110", "10001", "10001", "10001", "10001", "10001", "01110"},
	'P': {"11110", "10001", "10001", "11110", "10000", "10000", "10000"},
	'Q': {"01110", "10001", "10001", "10001", "10101", "10010", "01101"},
	'R': {"11110", "10001", "10001", "11110", "10100", "10010", "10001"},
	'S': {"01111", "10000", "10000", "01110", "00001", "00001", "11110"},
	'T': {"11111", "00100", "00100", "00100", "00100", "00100", "00100"},
	'U': {"10001", "10001", "10001", "10001", "10001", "10001", "01110"},
	'V': {"10001", "10001", "10001", "10001", "10001", "01010", "00100"},
	'W': {"10001", "10001", "10001", "10101", "10101", "10101", "01010"},
	'X': {"10001", "10001", "01010", "00100", "01010", "10001", "10001"},
	'Y': {"10001", "10001", "01010", "00100", "00100", "00100", "00100"},
	'Z': {"11111", "00001", "00010", "00100", "01000", "10000", "11111"},
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

func fillRounded(img *image.RGBA, x, y, w, h, radius int, c color.RGBA) {
	for yy := 0; yy < h; yy++ {
		for xx := 0; xx < w; xx++ {
			if !inRoundedRect(xx, yy, w, h, radius) {
				continue
			}
			dx := x + xx
			dy := y + yy
			if image.Pt(dx, dy).In(img.Bounds()) {
				blendAt(img, dx, dy, c)
			}
		}
	}
}

func drawNormalizedIcon(dst *image.RGBA, src image.Image, x, y, w, h, radius int) {
	content := visibleBounds(src)
	if iconLooksLikeCompleteAppIcon(src, content) {
		content = src.Bounds()
	}
	scaled := image.NewRGBA(image.Rect(0, 0, w, h))
	if iconLooksLikeCompleteAppIcon(src, content) {
		drawContainFromBounds(scaled, src, content)
	} else {
		drawCoverFromBounds(scaled, src, content)
	}
	for yy := 0; yy < h; yy++ {
		for xx := 0; xx < w; xx++ {
			if !inRoundedRect(xx, yy, w, h, radius) {
				continue
			}
			dx := x + xx
			dy := y + yy
			if image.Pt(dx, dy).In(dst.Bounds()) {
				blendAt(dst, dx, dy, scaled.At(xx, yy))
			}
		}
	}
}

func iconLooksLikeCompleteAppIcon(src image.Image, visible image.Rectangle) bool {
	bounds := src.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 || visible.Dx() <= 0 || visible.Dy() <= 0 {
		return false
	}
	coverageX := float64(visible.Dx()) / float64(bounds.Dx())
	coverageY := float64(visible.Dy()) / float64(bounds.Dy())
	if coverageX >= 0.82 && coverageY >= 0.82 {
		return true
	}
	return false
}

func visibleBounds(src image.Image) image.Rectangle {
	bounds := src.Bounds()
	minX, minY := bounds.Max.X, bounds.Max.Y
	maxX, maxY := bounds.Min.X, bounds.Min.Y
	found := false
	corners := []color.Color{
		src.At(bounds.Min.X, bounds.Min.Y),
		src.At(bounds.Max.X-1, bounds.Min.Y),
		src.At(bounds.Min.X, bounds.Max.Y-1),
		src.At(bounds.Max.X-1, bounds.Max.Y-1),
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if !isVisibleIconPixel(src.At(x, y), corners) {
				continue
			}
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x+1 > maxX {
				maxX = x + 1
			}
			if y+1 > maxY {
				maxY = y + 1
			}
			found = true
		}
	}
	if !found {
		return bounds
	}
	rect := image.Rect(minX, minY, maxX, maxY)
	if rect.Dx() < bounds.Dx()/4 || rect.Dy() < bounds.Dy()/4 {
		return bounds
	}
	return rect
}

func isVisibleIconPixel(pixel color.Color, backgrounds []color.Color) bool {
	r16, g16, b16, a16 := pixel.RGBA()
	if a16 < 12000 {
		return false
	}
	r, g, b := int(r16>>8), int(g16>>8), int(b16>>8)
	for _, bg := range backgrounds {
		br16, bg16, bb16, ba16 := bg.RGBA()
		if ba16 < 12000 {
			continue
		}
		distance := absInt(r-int(br16>>8)) + absInt(g-int(bg16>>8)) + absInt(b-int(bb16>>8))
		if distance < 34 {
			return false
		}
	}
	return true
}

func drawCoverFromBounds(dst *image.RGBA, src image.Image, srcBounds image.Rectangle) {
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
	if scaleY > scaleX {
		scale = scaleY
	}
	drawW := int(float64(srcW)/scale + 0.5)
	drawH := int(float64(srcH)/scale + 0.5)
	offsetX := (dstW - drawW) / 2
	offsetY := (dstH - drawH) / 2
	for y := 0; y < drawH; y++ {
		for x := 0; x < drawW; x++ {
			sx := srcBounds.Min.X + int(float64(x)*scale)
			sy := srcBounds.Min.Y + int(float64(y)*scale)
			dst.Set(offsetX+x, offsetY+y, src.At(sx, sy))
		}
	}
}

func drawContainFromBounds(dst *image.RGBA, src image.Image, srcBounds image.Rectangle) {
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
	if scaleY > scaleX {
		scale = scaleY
	}
	drawW := int(float64(srcW)/scale + 0.5)
	drawH := int(float64(srcH)/scale + 0.5)
	offsetX := (dstW - drawW) / 2
	offsetY := (dstH - drawH) / 2
	for y := 0; y < drawH; y++ {
		for x := 0; x < drawW; x++ {
			sx := srcBounds.Min.X + int(float64(x)*scale)
			sy := srcBounds.Min.Y + int(float64(y)*scale)
			dx := offsetX + x
			dy := offsetY + y
			if image.Pt(dx, dy).In(dst.Bounds()) {
				dst.Set(dx, dy, src.At(sx, sy))
			}
		}
	}
}

func inRoundedRect(x, y, w, h, radius int) bool {
	if x < 0 || y < 0 || x >= w || y >= h {
		return false
	}
	if radius <= 0 {
		return true
	}
	cx := radius
	if x >= w-radius {
		cx = w - radius - 1
	} else if x >= radius {
		return true
	}
	cy := radius
	if y >= h-radius {
		cy = h - radius - 1
	} else if y >= radius {
		return true
	}
	dx := x - cx
	dy := y - cy
	return dx*dx+dy*dy <= radius*radius
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
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
