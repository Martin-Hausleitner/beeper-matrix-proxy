package beepersource

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"unicode"
)

func DeterministicTxnID(chatID, messageID, mutationType, version string) string {
	sum := sha256.Sum256([]byte(chatID + "\x00" + messageID + "\x00" + mutationType + "\x00" + version))
	return "bmp_" + base64.RawURLEncoding.EncodeToString(sum[:18])
}

func MatrixGhostLocalpart(beeperSenderID string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return unicode.ToLower(r)
		default:
			return '_'
		}
	}, beeperSenderID)
	clean = strings.Trim(clean, "_")
	if len(clean) > 32 {
		clean = clean[:32]
	}
	sum := sha256.Sum256([]byte(beeperSenderID))
	return "beeper_" + clean + "_" + base64.RawURLEncoding.EncodeToString(sum[:6])
}

func MessageVersion(msg Message) string {
	if msg.IsDeleted {
		return "deleted"
	}
	if msg.EditedTimestamp != nil {
		return msg.EditedTimestamp.UTC().Format("20060102T150405.000000000Z")
	}
	if !msg.Timestamp.IsZero() {
		return msg.Timestamp.UTC().Format("20060102T150405.000000000Z")
	}
	return "initial"
}
