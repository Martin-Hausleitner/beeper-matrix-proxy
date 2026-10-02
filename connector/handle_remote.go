package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// File where you can put all the events from the upstream network
// For example when you receive a new message
// This file is responsible for bridging those upstream things to matrix
//
// Some things you can do here:
// - Connect to an upstream websocket
// - Poll for messages

// QueueRemoteMessage shows the preferred Remote -> Matrix flow using the bridge event queue.
func (nc *MyNetworkClient) QueueRemoteMessage(ctx context.Context, portalID networkid.PortalID, body string) {
	evt := &simplevent.Message[string]{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventMessage,
			PortalKey: networkid.PortalKey{ID: portalID, Receiver: nc.login.ID},
			Sender: bridgev2.EventSender{
				Sender:   networkid.UserID("example-ghost"),
				IsFromMe: false,
			},
			CreatePortal: true,
			Timestamp:    time.Now(),
		},
		Data: body,
		ID:   networkid.MessageID(fmt.Sprintf("remote-%s-%d", portalID, time.Now().UnixNano())),
		ConvertMessageFunc: func(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, data string) (*bridgev2.ConvertedMessage, error) {
			return &bridgev2.ConvertedMessage{
				Parts: []*bridgev2.ConvertedMessagePart{{
					Type: event.EventMessage,
					Content: &event.MessageEventContent{
						MsgType: event.MsgText,
						Body:    data,
					},
				}},
			}, nil
		},
	}

	nc.bridge.QueueRemoteEvent(nc.login, evt)
}

func (nc *MyNetworkClient) handleLocalMatrixEvent(ctx context.Context, evt *event.Event) {
	if evt == nil || (evt.Type != event.EventMessage && evt.Type != event.EventSticker) || evt.RoomID == "" || evt.ID == "" {
		return
	}
	if nc.consumeSentEvent(evt.ID) {
		// Messages sent from Beeper to the remote Matrix account echo back in /sync.
		return
	}
	content := cloneMessageContent(messageContentFromEventContent(evt.Content))
	sender, isFromMe := nc.senderForLocalMatrixEvent(ctx, evt, content)
	if replaceID := content.OptionalGetRelatesTo().GetReplaceID(); replaceID != "" {
		nc.handleLocalMatrixEdit(ctx, evt, content, replaceID)
		return
	}
	portalID := networkid.PortalID(evt.RoomID)
	remote := &simplevent.Message[*event.MessageEventContent]{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventMessage,
			PortalKey: networkid.PortalKey{ID: portalID, Receiver: nc.login.ID},
			Sender: bridgev2.EventSender{
				Sender:   sender,
				IsFromMe: isFromMe,
			},
			CreatePortal: true,
			Timestamp:    eventTimestamp(evt),
		},
		Data: content,
		ID:   networkid.MessageID(evt.ID),
		ConvertMessageFunc: func(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, data *event.MessageEventContent) (*bridgev2.ConvertedMessage, error) {
			content := cloneMessageContent(data)
			replyTo, threadRoot := remoteRelationTargets(content)
			removeRemoteRelationTargets(content)
			if err := nc.reuploadContentToBeeper(ctx, intent, content); err != nil {
				return nil, err
			}
			return &bridgev2.ConvertedMessage{
				ReplyTo:    replyTo,
				ThreadRoot: threadRoot,
				Parts: []*bridgev2.ConvertedMessagePart{{
					Type:    evt.Type,
					Content: content,
				}},
			}, nil
		},
		HandleExistingFunc: func(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, existing []*database.Message, data *event.MessageEventContent) (bridgev2.UpsertResult, error) {
			return bridgev2.UpsertResult{}, nil
		},
	}
	nc.bridge.QueueRemoteEvent(nc.login, remote)
}

func (nc *MyNetworkClient) senderForLocalMatrixEvent(ctx context.Context, evt *event.Event, content *event.MessageEventContent) (networkid.UserID, bool) {
	sender := networkid.UserID(evt.Sender)
	isFromMe := evt.Sender == nc.mx.UserID
	profile := content.BeeperPerMessageProfile
	if profile == nil || profile.ID == "" || profile.Displayname == "" {
		return sender, isFromMe
	}
	content.RemovePerMessageProfileFallback()
	content.BeeperPerMessageProfile = nil
	profileSender := networkid.UserID(profile.ID)
	nc.rememberRemoteProfile(ctx, profileSender, profile)
	return profileSender, false
}

func (nc *MyNetworkClient) rememberRemoteProfile(ctx context.Context, sender networkid.UserID, profile *event.BeeperPerMessageProfile) {
	if sender == "" || profile == nil || profile.Displayname == "" {
		return
	}
	name := profile.Displayname
	info := &bridgev2.UserInfo{Name: &name}
	if profile.AvatarURL != nil && string(*profile.AvatarURL) != "" {
		info.Avatar = nc.avatarFromMXC(ctx, *profile.AvatarURL)
	}
	nc.profileMu.Lock()
	if nc.remoteProfiles == nil {
		nc.remoteProfiles = make(map[networkid.UserID]*bridgev2.UserInfo)
	}
	nc.remoteProfiles[sender] = info
	nc.profileMu.Unlock()
}

func (nc *MyNetworkClient) cachedRemoteProfile(sender networkid.UserID) (*bridgev2.UserInfo, bool) {
	nc.profileMu.RLock()
	defer nc.profileMu.RUnlock()
	info, ok := nc.remoteProfiles[sender]
	if !ok || info == nil {
		return nil, false
	}
	clone := &bridgev2.UserInfo{}
	if info.Name != nil {
		name := *info.Name
		clone.Name = &name
	}
	clone.Avatar = info.Avatar
	return clone, true
}

func (nc *MyNetworkClient) handleLocalMatrixEdit(ctx context.Context, evt *event.Event, content *event.MessageEventContent, target id.EventID) {
	if evt == nil || evt.RoomID == "" || evt.ID == "" || target == "" {
		return
	}
	if nc.consumeSentEvent(evt.ID) {
		return
	}
	portalID := networkid.PortalID(evt.RoomID)
	sender, isFromMe := nc.senderForLocalMatrixEvent(ctx, evt, content)
	remote := &simplevent.Message[*event.MessageEventContent]{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventEdit,
			PortalKey: networkid.PortalKey{ID: portalID, Receiver: nc.login.ID},
			Sender: bridgev2.EventSender{
				Sender:   sender,
				IsFromMe: isFromMe,
			},
			CreatePortal: true,
			Timestamp:    eventTimestamp(evt),
		},
		Data:          content,
		ID:            networkid.MessageID(evt.ID),
		TargetMessage: networkid.MessageID(target),
		ConvertEditFunc: func(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, existing []*database.Message, data *event.MessageEventContent) (*bridgev2.ConvertedEdit, error) {
			data = cleanEditContentForBeeper(data)
			if err := nc.reuploadContentToBeeper(ctx, intent, data); err != nil {
				return nil, err
			}
			if len(existing) == 0 {
				return &bridgev2.ConvertedEdit{
					AddedParts: &bridgev2.ConvertedMessage{Parts: []*bridgev2.ConvertedMessagePart{{
						Type:    event.EventMessage,
						Content: data,
					}}},
				}, nil
			}
			return &bridgev2.ConvertedEdit{
				ModifiedParts: []*bridgev2.ConvertedEditPart{{
					Part:    existing[0],
					Type:    event.EventMessage,
					Content: data,
				}},
			}, nil
		},
	}
	nc.bridge.QueueRemoteEvent(nc.login, remote)
}

func (nc *MyNetworkClient) handleLocalMatrixReaction(ctx context.Context, evt *event.Event) {
	if evt == nil || evt.Type != event.EventReaction || evt.RoomID == "" || evt.ID == "" {
		return
	}
	if nc.consumeSentEvent(evt.ID) {
		return
	}
	content, ok := evt.Content.Parsed.(*event.ReactionEventContent)
	if !ok {
		parsed := event.ReactionEventContent{}
		if len(evt.Content.VeryRaw) > 0 && json.Unmarshal(evt.Content.VeryRaw, &parsed) == nil {
			content = &parsed
		}
	}
	if content == nil {
		return
	}
	emoji := content.RelatesTo.GetAnnotationKey()
	target := content.RelatesTo.GetAnnotationID()
	if emoji == "" || target == "" {
		return
	}
	nc.bridge.QueueRemoteEvent(nc.login, &simplevent.Reaction{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventReaction,
			PortalKey: networkid.PortalKey{ID: networkid.PortalID(evt.RoomID), Receiver: nc.login.ID},
			Sender: bridgev2.EventSender{
				Sender:   networkid.UserID(evt.Sender),
				IsFromMe: evt.Sender == nc.mx.UserID,
			},
			CreatePortal: true,
			Timestamp:    eventTimestamp(evt),
		},
		TargetMessage: networkid.MessageID(target),
		EmojiID:       networkid.EmojiID(emoji),
		Emoji:         emoji,
		ReactionDBMeta: &ReactionMetadata{
			RemoteEventID: string(evt.ID),
		},
	})
	nc.rememberRemoteReaction(ctx, evt.ID, remoteReaction{
		RoomID:        evt.RoomID,
		TargetMessage: networkid.MessageID(target),
		Sender:        networkid.UserID(evt.Sender),
		IsFromMe:      evt.Sender == nc.mx.UserID,
		EmojiID:       networkid.EmojiID(emoji),
		Emoji:         emoji,
		Timestamp:     eventTimestamp(evt),
	})
}

func (nc *MyNetworkClient) handleLocalMatrixRedaction(ctx context.Context, evt *event.Event) {
	if evt == nil || evt.Type != event.EventRedaction || evt.RoomID == "" || evt.ID == "" {
		return
	}
	if nc.consumeSentEvent(evt.ID) {
		return
	}
	target := evt.Redacts
	if target == "" {
		content := evt.Content.AsRedaction()
		target = content.Redacts
	}
	if target == "" {
		return
	}
	if reaction, ok := nc.popRemoteReaction(ctx, target); ok {
		nc.bridge.QueueRemoteEvent(nc.login, &simplevent.Reaction{
			EventMeta: simplevent.EventMeta{
				Type:      bridgev2.RemoteEventReactionRemove,
				PortalKey: networkid.PortalKey{ID: networkid.PortalID(reaction.RoomID), Receiver: nc.login.ID},
				Sender: bridgev2.EventSender{
					Sender:   reaction.Sender,
					IsFromMe: reaction.IsFromMe,
				},
				CreatePortal: true,
				Timestamp:    eventTimestamp(evt),
			},
			TargetMessage: reaction.TargetMessage,
			EmojiID:       reaction.EmojiID,
			Emoji:         reaction.Emoji,
		})
		return
	}
	nc.bridge.QueueRemoteEvent(nc.login, &simplevent.MessageRemove{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventMessageRemove,
			PortalKey: networkid.PortalKey{ID: networkid.PortalID(evt.RoomID), Receiver: nc.login.ID},
			Sender: bridgev2.EventSender{
				Sender:   networkid.UserID(evt.Sender),
				IsFromMe: evt.Sender == nc.mx.UserID,
			},
			CreatePortal: true,
			Timestamp:    eventTimestamp(evt),
		},
		TargetMessage: networkid.MessageID(target),
	})
}

type remoteReaction struct {
	RoomID        id.RoomID
	TargetMessage networkid.MessageID
	Sender        networkid.UserID
	IsFromMe      bool
	EmojiID       networkid.EmojiID
	Emoji         string
	Timestamp     time.Time
}

func (nc *MyNetworkClient) rememberRemoteReaction(ctx context.Context, eventID id.EventID, reaction remoteReaction) {
	if eventID == "" {
		return
	}
	nc.reactionMu.Lock()
	defer nc.reactionMu.Unlock()
	if nc.remoteReactions == nil {
		nc.remoteReactions = make(map[id.EventID]remoteReaction)
	}
	nc.remoteReactions[eventID] = reaction
	nc.persistRemoteReaction(ctx, eventID, reaction)
}

func (nc *MyNetworkClient) popRemoteReaction(ctx context.Context, eventID id.EventID) (remoteReaction, bool) {
	if eventID == "" {
		return remoteReaction{}, false
	}
	nc.reactionMu.Lock()
	defer nc.reactionMu.Unlock()
	if nc.remoteReactions != nil {
		reaction, ok := nc.remoteReactions[eventID]
		if ok {
			delete(nc.remoteReactions, eventID)
			nc.forgetRemoteReaction(ctx, eventID)
			return reaction, true
		}
	}
	var reaction remoteReaction
	var ok bool
	if reaction, ok = nc.loadRemoteReaction(eventID); ok {
		nc.forgetRemoteReaction(ctx, eventID)
		return reaction, true
	}
	return remoteReaction{}, false
}

func (nc *MyNetworkClient) persistRemoteReaction(ctx context.Context, eventID id.EventID, reaction remoteReaction) {
	if nc.metadata == nil {
		return
	}
	_ = nc.metadata.update(ctx, func(meta *LoginMetadata) bool {
		if meta.RemoteReactions == nil {
			meta.RemoteReactions = make(map[string]StoredRemoteReaction)
		}
		meta.RemoteReactions[string(eventID)] = reaction.store()
		return true
	})
}

func (nc *MyNetworkClient) loadRemoteReaction(eventID id.EventID) (remoteReaction, bool) {
	if nc.metadata == nil || eventID == "" {
		return remoteReaction{}, false
	}
	meta := nc.metadata.snapshot()
	stored, ok := meta.RemoteReactions[string(eventID)]
	if !ok {
		return remoteReaction{}, false
	}
	return stored.remote(), true
}

func (nc *MyNetworkClient) forgetRemoteReaction(ctx context.Context, eventID id.EventID) {
	if nc.metadata == nil || eventID == "" {
		return
	}
	_ = nc.metadata.update(ctx, func(meta *LoginMetadata) bool {
		if meta.RemoteReactions == nil {
			return false
		}
		if _, ok := meta.RemoteReactions[string(eventID)]; !ok {
			return false
		}
		delete(meta.RemoteReactions, string(eventID))
		if len(meta.RemoteReactions) == 0 {
			meta.RemoteReactions = nil
		}
		return true
	})
}

func (reaction remoteReaction) store() StoredRemoteReaction {
	return StoredRemoteReaction{
		RoomID:        string(reaction.RoomID),
		TargetMessage: string(reaction.TargetMessage),
		Sender:        string(reaction.Sender),
		IsFromMe:      reaction.IsFromMe,
		EmojiID:       string(reaction.EmojiID),
		Emoji:         reaction.Emoji,
		Timestamp:     reaction.Timestamp,
	}
}

func (stored StoredRemoteReaction) remote() remoteReaction {
	return remoteReaction{
		RoomID:        id.RoomID(stored.RoomID),
		TargetMessage: networkid.MessageID(stored.TargetMessage),
		Sender:        networkid.UserID(stored.Sender),
		IsFromMe:      stored.IsFromMe,
		EmojiID:       networkid.EmojiID(stored.EmojiID),
		Emoji:         stored.Emoji,
		Timestamp:     stored.Timestamp,
	}
}

func (nc *MyNetworkClient) handleLocalMatrixPoll(ctx context.Context, evt *event.Event) {
	if evt == nil || evt.Type != event.EventUnstablePollStart || evt.RoomID == "" || evt.ID == "" {
		return
	}
	if nc.consumeSentEvent(evt.ID) {
		return
	}
	poll, ok := evt.Content.Parsed.(*event.PollStartEventContent)
	if !ok || poll == nil {
		return
	}
	raw := rawContentFromEvent(evt.Content, poll)
	normalizePollStartRaw(raw)
	nc.queueLocalMatrixRawEvent(ctx, evt, event.EventUnstablePollStart, raw)
}

func (nc *MyNetworkClient) handleLocalMatrixPollResponse(ctx context.Context, evt *event.Event) {
	if evt == nil || evt.Type != event.EventUnstablePollResponse || evt.RoomID == "" || evt.ID == "" {
		return
	}
	if nc.consumeSentEvent(evt.ID) {
		return
	}
	resp, ok := evt.Content.Parsed.(*event.PollResponseEventContent)
	if !ok || resp == nil {
		return
	}
	nc.queueLocalMatrixRawEvent(ctx, evt, event.EventUnstablePollResponse, rawContentFromParsed(resp, nil))
}

func (nc *MyNetworkClient) handleLocalMatrixCallInvite(ctx context.Context, evt *event.Event) {
	if evt == nil || evt.Type != event.CallInvite || evt.RoomID == "" || evt.ID == "" {
		return
	}
	if nc.consumeSentEvent(evt.ID) {
		return
	}
	body := callNoticeBody(evt.RoomID)
	nc.bridge.QueueRemoteEvent(nc.login, &simplevent.Message[string]{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventMessage,
			PortalKey: networkid.PortalKey{ID: networkid.PortalID(evt.RoomID), Receiver: nc.login.ID},
			Sender: bridgev2.EventSender{
				Sender:   networkid.UserID(evt.Sender),
				IsFromMe: evt.Sender == nc.mx.UserID,
			},
			CreatePortal: true,
			Timestamp:    eventTimestamp(evt),
		},
		Data: body,
		ID:   networkid.MessageID("call-notice:" + string(evt.ID)),
		ConvertMessageFunc: func(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, data string) (*bridgev2.ConvertedMessage, error) {
			return &bridgev2.ConvertedMessage{
				Parts: []*bridgev2.ConvertedMessagePart{{
					Type: event.EventMessage,
					Content: &event.MessageEventContent{
						MsgType: event.MsgNotice,
						Body:    data,
					},
				}},
			}, nil
		},
	})
}

func callNoticeBody(roomID id.RoomID) string {
	return fmt.Sprintf("Matrix call started in %s. Open this room in your Matrix client or Element Call to join.", roomID)
}

func cleanEditContentForBeeper(content *event.MessageEventContent) *event.MessageEventContent {
	if content == nil {
		return nil
	}
	source := content
	if content.NewContent != nil {
		source = content.NewContent
	}
	cleaned := cloneMessageContent(source)
	if cleaned == nil {
		return nil
	}
	cleaned.Body = stripMatrixEditFallbackPrefix(cleaned.Body)
	if cleaned.FormattedBody != "" {
		cleaned.FormattedBody = stripMatrixEditFallbackPrefix(cleaned.FormattedBody)
	}
	cleaned.NewContent = nil
	cleaned.RelatesTo = nil
	return cleaned
}

func remoteRelationTargets(content *event.MessageEventContent) (*networkid.MessageOptionalPartID, *networkid.MessageID) {
	if content == nil || content.RelatesTo == nil {
		return nil, nil
	}
	var replyTo *networkid.MessageOptionalPartID
	if replyID := content.RelatesTo.GetReplyTo(); replyID != "" {
		replyTo = &networkid.MessageOptionalPartID{MessageID: networkid.MessageID(replyID)}
	}
	var threadRoot *networkid.MessageID
	if content.RelatesTo.Type == event.RelThread && content.RelatesTo.EventID != "" {
		root := networkid.MessageID(content.RelatesTo.EventID)
		threadRoot = &root
	}
	return replyTo, threadRoot
}

func removeRemoteRelationTargets(content *event.MessageEventContent) {
	if content == nil || content.RelatesTo == nil {
		return
	}
	if content.RelatesTo.GetReplyTo() == "" && !(content.RelatesTo.Type == event.RelThread && content.RelatesTo.EventID != "") {
		return
	}
	content.RelatesTo = nil
}

func stripMatrixEditFallbackPrefix(body string) string {
	for strings.HasPrefix(body, "* ") {
		body = strings.TrimPrefix(body, "* ")
	}
	return body
}

func (nc *MyNetworkClient) queueLocalMatrixRawEvent(ctx context.Context, evt *event.Event, evtType event.Type, raw map[string]any) {
	if evt == nil || evt.RoomID == "" || evt.ID == "" {
		return
	}
	portalID := networkid.PortalID(evt.RoomID)
	sender := networkid.UserID(evt.Sender)
	nc.bridge.QueueRemoteEvent(nc.login, &simplevent.Message[map[string]any]{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventMessage,
			PortalKey: networkid.PortalKey{ID: portalID, Receiver: nc.login.ID},
			Sender: bridgev2.EventSender{
				Sender:   sender,
				IsFromMe: evt.Sender == nc.mx.UserID,
			},
			CreatePortal: true,
			Timestamp:    eventTimestamp(evt),
		},
		Data: raw,
		ID:   networkid.MessageID(evt.ID),
		ConvertMessageFunc: func(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, data map[string]any) (*bridgev2.ConvertedMessage, error) {
			data = cloneRawMap(data)
			if evtType == event.EventUnstablePollResponse {
				remapRawRelationTarget(ctx, portal, data)
			}
			return &bridgev2.ConvertedMessage{
				Parts: []*bridgev2.ConvertedMessagePart{{
					Type: evtType,
					Content: &event.MessageEventContent{
						MsgType: event.MsgNotice,
						Body:    rawEventFallbackBody(evtType, data),
					},
					Extra: data,
				}},
			}, nil
		},
	})
}

func rawContentFromParsed(parsed any, fallback map[string]any) map[string]any {
	if parsed == nil {
		return fallback
	}
	raw, err := json.Marshal(parsed)
	if err != nil {
		return fallback
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return fallback
	}
	return out
}

func rawContentFromEvent(content event.Content, parsed any) map[string]any {
	if len(content.Raw) > 0 {
		return cloneRawMap(content.Raw)
	}
	if len(content.VeryRaw) > 0 {
		var raw map[string]any
		if json.Unmarshal(content.VeryRaw, &raw) == nil {
			return raw
		}
	}
	return rawContentFromParsed(parsed, nil)
}

func cloneRawMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneRawValue(value)
	}
	return out
}

func cloneRawValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneRawMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneRawValue(item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(typed))
		for i, item := range typed {
			out[i] = cloneRawMap(item)
		}
		return out
	default:
		return value
	}
}

func remapRawRelationTarget(ctx context.Context, portal *bridgev2.Portal, raw map[string]any) {
	rel, ok := raw["m.relates_to"].(map[string]any)
	if !ok {
		return
	}
	target, _ := rel["event_id"].(string)
	if target == "" {
		return
	}
	msg, err := portal.Bridge.DB.Message.GetFirstPartByID(ctx, portal.Receiver, networkid.MessageID(target))
	if err != nil || msg == nil || msg.MXID == "" {
		return
	}
	rel["event_id"] = string(msg.MXID)
}

func rawEventFallbackBody(evtType event.Type, raw map[string]any) string {
	if evtType == event.EventUnstablePollStart {
		if start, ok := raw["org.matrix.msc3381.poll.start"].(map[string]any); ok {
			if question, ok := start["question"].(map[string]any); ok {
				if text := pollText(question); text != "" {
					return "[Poll] " + text
				}
			}
		}
		return "Poll"
	}
	return ""
}

func normalizePollStartRaw(raw map[string]any) {
	if raw == nil {
		return
	}
	start, ok := raw["org.matrix.msc3381.poll.start"].(map[string]any)
	if !ok {
		return
	}
	if question, ok := start["question"].(map[string]any); ok {
		normalizePollTextObject(question)
	}
	switch answers := start["answers"].(type) {
	case []any:
		for _, item := range answers {
			if answer, ok := item.(map[string]any); ok {
				normalizePollTextObject(answer)
			}
		}
	case []map[string]any:
		for _, answer := range answers {
			normalizePollTextObject(answer)
		}
	}
}

func normalizePollTextObject(raw map[string]any) {
	if raw == nil {
		return
	}
	if _, ok := raw["org.matrix.msc1767.text"]; ok {
		return
	}
	if text := pollText(raw); text != "" {
		raw["org.matrix.msc1767.text"] = text
	}
}

func pollText(raw map[string]any) string {
	for _, key := range []string{"org.matrix.msc1767.text", "body", "text"} {
		if text, ok := raw[key].(string); ok && text != "" {
			return text
		}
	}
	if messages, ok := raw["org.matrix.msc1767.message"].([]any); ok {
		for _, item := range messages {
			msg, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := msg["body"].(string); ok && text != "" {
				return text
			}
		}
	}
	return ""
}

func messageContentFromEventContent(content event.Content) *event.MessageEventContent {
	if parsed, ok := content.Parsed.(*event.MessageEventContent); ok {
		return parsed
	}
	if parsed, ok := content.Parsed.(event.MessageEventContent); ok {
		return &parsed
	}
	var msg event.MessageEventContent
	if len(content.VeryRaw) > 0 {
		if err := json.Unmarshal(content.VeryRaw, &msg); err == nil && msg.Body != "" {
			return &msg
		}
	}
	if rawBody, ok := content.Raw["body"].(string); ok {
		msg.Body = rawBody
	}
	if rawType, ok := content.Raw["msgtype"].(string); ok {
		msg.MsgType = event.MessageType(rawType)
	}
	if msg.MsgType == "" {
		msg.MsgType = event.MsgText
	}
	return &msg
}

func eventTimestamp(evt *event.Event) time.Time {
	if evt.Timestamp > 0 {
		return time.UnixMilli(evt.Timestamp)
	}
	return time.Now()
}
