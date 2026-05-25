package beepersource

import "strings"

func PlatformDisplayName(chat Chat) string {
	if name := strings.TrimSpace(chat.Network); name != "" {
		return name
	}
	account := strings.TrimSpace(chat.AccountID)
	if account == "" {
		return "Beeper"
	}
	base := strings.TrimPrefix(account, "local-")
	if idx := strings.IndexByte(base, '_'); idx >= 0 {
		base = base[:idx]
	}
	switch strings.ToLower(base) {
	case "whatsapp", "wa":
		return "WhatsApp"
	case "signal":
		return "Signal"
	case "telegram", "tg":
		return "Telegram"
	case "discord", "discordgo":
		return "Discord"
	case "slack":
		return "Slack"
	case "facebook", "messenger", "meta":
		return "Messenger"
	case "instagram", "ig":
		return "Instagram"
	case "imessage", "bluebubbles":
		return "iMessage"
	case "twitter", "x":
		return "X"
	case "linkedin":
		return "LinkedIn"
	case "matrix":
		return "Matrix"
	case "creatorhero":
		return "CreatorHero"
	case "onlyfans":
		return "OnlyFans"
	case "fansly":
		return "Fansly"
	case "fanvue":
		return "Fanvue"
	case "mym", "mymfans":
		return "MYM.fans"
	case "fancentro":
		return "FanCentro"
	case "slushy":
		return "Slushy"
	case "uncove":
		return "Uncove"
	case "subscribestar":
		return "SubscribeStar"
	case "maloum":
		return "Maloum"
	case "dfans":
		return "dFans"
	case "manyvids":
		return "ManyVids"
	case "unlockd":
		return "Unlockd"
	case "sospoilt":
		return "SoSpoilt"
	case "xpanded":
		return "Xpanded"
	case "revealme":
		return "RevealMe"
	case "admireme":
		return "AdmireMe"
	case "camsoda":
		return "CamSoda"
	case "stacked":
		return "Stacked"
	case "fanview":
		return "Fanview"
	default:
		return titleAccount(base)
	}
}

func PlatformInitials(platform string) string {
	words := strings.Fields(strings.ReplaceAll(platform, "-", " "))
	if len(words) == 0 {
		return "B"
	}
	if strings.EqualFold(platform, "WhatsApp") {
		return "WA"
	}
	if strings.EqualFold(platform, "iMessage") {
		return "iM"
	}
	if len(words) == 1 {
		runes := []rune(words[0])
		if len(runes) == 1 {
			return strings.ToUpper(string(runes[0]))
		}
		return strings.ToUpper(string(runes[:min(2, len(runes))]))
	}
	return strings.ToUpper(string([]rune(words[0])[0]) + string([]rune(words[1])[0]))
}

func PlatformColor(chat Chat) string {
	switch strings.ToLower(PlatformDisplayName(chat)) {
	case "whatsapp":
		return "#25D366"
	case "signal":
		return "#3A76F0"
	case "telegram":
		return "#229ED9"
	case "discord":
		return "#5865F2"
	case "slack":
		return "#4A154B"
	case "messenger":
		return "#0084FF"
	case "instagram":
		return "#C13584"
	case "imessage":
		return "#34C759"
	case "x":
		return "#111111"
	case "linkedin":
		return "#0A66C2"
	case "matrix":
		return "#000000"
	case "creatorhero":
		return "#F2F0EC"
	case "onlyfans":
		return "#00AFF0"
	case "fansly":
		return "#2799F6"
	case "fanvue":
		return "#2027D2"
	case "mym.fans":
		return "#181A20"
	case "fancentro":
		return "#FF6B2A"
	case "slushy":
		return "#EF3B7D"
	case "uncove":
		return "#6C5CE7"
	case "subscribestar":
		return "#F59E0B"
	case "maloum":
		return "#111827"
	case "dfans":
		return "#7C3AED"
	case "manyvids":
		return "#E1007A"
	case "unlockd":
		return "#111111"
	case "sospoilt":
		return "#D946EF"
	case "xpanded":
		return "#166534"
	case "revealme":
		return "#0EA5E9"
	case "admireme":
		return "#EC4899"
	case "camsoda":
		return "#8B5CF6"
	case "stacked":
		return "#111827"
	case "fanview":
		return "#0098C7"
	default:
		return "#5662F6"
	}
}

func titleAccount(account string) string {
	parts := strings.FieldsFunc(account, func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	})
	for i, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(strings.ToLower(part))
		runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
		parts[i] = string(runes)
	}
	if len(parts) == 0 {
		return "Beeper"
	}
	return strings.Join(parts, " ")
}
