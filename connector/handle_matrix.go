package connector

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/util/jsontime"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// HandleMatrixMessage handles incoming messages from Matrix for this user.
func (nc *MyNetworkClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	log := nc.log.With().
		Str("portal_id", string(msg.Portal.ID)).
		Str("sender_mxid", string(msg.Event.Sender)).
		Str("event_id", string(msg.Event.ID)).
		Logger()
	ctx = log.WithContext(ctx)

	log.Info().Msg("HandleMatrixMessage called")

	if nc.mx == nil {
		return nil, fmt.Errorf("remote Matrix client is not connected")
	}

	roomID := id.RoomID(msg.Portal.ID)
	content := cloneMessageContent(msg.Content)
	if content == nil {
		content = messageContentFromEventContent(msg.Event.Content)
	}
	rewriteContentRelationsForLocalMatrix(content, msg.ReplyTo, msg.ThreadRoot)
	if err := nc.reuploadContentToLocalMatrix(ctx, content); err != nil {
		return nil, err
	}
	resp, err := nc.mx.SendMessageEvent(ctx, roomID, event.EventMessage, content)
	if err != nil {
		return nil, err
	}
	nc.markSentEvent(resp.EventID)

	return &bridgev2.MatrixMessageResponse{
		DB: &database.Message{
			ID:       networkid.MessageID(resp.EventID),
			PartID:   networkid.PartID(""),
			SenderID: networkid.UserID(nc.mx.UserID),
		},
	}, nil
}

func rewriteContentRelationsForLocalMatrix(content *event.MessageEventContent, replyTo, threadRoot *database.Message) {
	if content == nil {
		return
	}
	rel := content.OptionalGetRelatesTo()
	if rel == nil && (replyTo != nil || threadRoot != nil) {
		rel = content.GetRelatesTo()
	}
	if rel == nil {
		return
	}
	if replyTo != nil && replyTo.ID != "" {
		rel.SetReplyTo(id.EventID(replyTo.ID))
	}
	if threadRoot != nil && threadRoot.ID != "" {
		fallback := rel.GetReplyTo()
		rel.SetThread(id.EventID(threadRoot.ID), fallback)
	}
}

// GetUserInfo is not implemented for this simple connector.
func (nc *MyNetworkClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	if info, ok := nc.cachedRemoteProfile(networkid.UserID(ghost.ID)); ok {
		return info, nil
	}
	userID := id.UserID(ghost.ID)
	name := userID.String()
	info := &bridgev2.UserInfo{Name: &name}
	profile, err := nc.mx.GetProfile(ctx, userID)
	if err == nil && profile != nil {
		if profile.DisplayName != "" {
			info.Name = &profile.DisplayName
		}
		if !profile.AvatarURL.IsEmpty() {
			info.Avatar = nc.avatarFromMXC(ctx, profile.AvatarURL.CUString())
		}
	}
	return info, nil
}

func (nc *MyNetworkClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	if nc.mx == nil {
		return nil, fmt.Errorf("remote Matrix client is not connected")
	}
	return nc.buildChatInfo(ctx, id.RoomID(portal.ID)), nil
}

// GetCapabilities returns the supported features for chats handled by this client.
func (nc *MyNetworkClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	maxMediaSize := nc.getLocalMaxUploadSize()
	media := func(mimeTypes ...string) *event.FileFeatures {
		supportedMimeTypes := make(map[string]event.CapabilitySupportLevel, len(mimeTypes))
		for _, mimeType := range mimeTypes {
			supportedMimeTypes[mimeType] = event.CapLevelFullySupported
		}
		return &event.FileFeatures{
			MimeTypes:        supportedMimeTypes,
			Caption:          event.CapLevelFullySupported,
			MaxCaptionLength: 65536,
			MaxSize:          maxMediaSize,
		}
	}
	voice := media("audio/ogg", "audio/mpeg", "audio/mp4", "audio/x-m4a", "audio/wav", "audio/webm")
	maxVoiceDuration := jsontime.S(30 * time.Minute)
	voice.MaxDuration = &maxVoiceDuration
	editMaxAge := jsontime.S(24 * time.Hour)
	deleteMaxAge := jsontime.S(24 * time.Hour)
	return &event.RoomFeatures{
		MaxTextLength: 65536,
		File: event.FileFeatureMap{
			event.MsgImage:      media("image/png", "image/jpeg", "image/gif", "image/webp"),
			event.MsgVideo:      media("video/mp4", "video/quicktime", "video/webm"),
			event.MsgAudio:      media("audio/mpeg", "audio/mp4", "audio/ogg", "audio/wav", "audio/x-m4a", "audio/webm"),
			event.MsgFile:       media("*/*"),
			event.CapMsgGIF:     media("image/gif", "video/mp4", "video/webm"),
			event.CapMsgVoice:   voice,
			event.CapMsgSticker: media("image/png", "image/jpeg", "image/webp", "image/gif"),
		},
		Formatting: event.FormattingFeatureMap{
			event.FmtBold:          event.CapLevelFullySupported,
			event.FmtItalic:        event.CapLevelFullySupported,
			event.FmtUnderline:     event.CapLevelFullySupported,
			event.FmtStrikethrough: event.CapLevelFullySupported,
			event.FmtInlineCode:    event.CapLevelFullySupported,
			event.FmtCodeBlock:     event.CapLevelFullySupported,
			event.FmtBlockquote:    event.CapLevelFullySupported,
			event.FmtInlineLink:    event.CapLevelFullySupported,
			event.FmtUserLink:      event.CapLevelFullySupported,
			event.FmtRoomLink:      event.CapLevelFullySupported,
			event.FmtEventLink:     event.CapLevelFullySupported,
			event.FmtUnorderedList: event.CapLevelFullySupported,
			event.FmtOrderedList:   event.CapLevelFullySupported,
		},
		LocationMessage:        event.CapLevelFullySupported,
		Poll:                   event.CapLevelFullySupported,
		Edit:                   event.CapLevelFullySupported,
		EditMaxCount:           100,
		EditMaxAge:             &editMaxAge,
		Delete:                 event.CapLevelFullySupported,
		DeleteMaxAge:           &deleteMaxAge,
		Reply:                  event.CapLevelFullySupported,
		Thread:                 event.CapLevelFullySupported,
		Reaction:               event.CapLevelFullySupported,
		ReactionCount:          1,
		CustomEmojiReactions:   true,
		ReadReceipts:           true,
		TypingNotifications:    true,
		PerMessageProfileRelay: true,
	}
}
