package beepersource

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type MatrixClientSink struct {
	cfg         Config
	store       *Store
	client      *mautrix.Client
	accessToken string
}

type MatrixRateLimitError struct {
	RetryAfter time.Duration
	StatusCode int
	ErrCode    string
	Message    string
}

func (e *MatrixRateLimitError) Error() string {
	if e == nil {
		return ""
	}
	detail := strings.TrimSpace(e.Message)
	if detail == "" {
		detail = "Matrix rate limit exceeded"
	}
	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s (retry after %s)", detail, e.RetryAfter)
	}
	return detail
}

func NewMatrixClientSink(cfg Config, store *Store, accessToken string) (*MatrixClientSink, error) {
	cli, err := mautrix.NewClient(cfg.Matrix.HomeserverURL, id.UserID(cfg.Matrix.UserID), accessToken)
	if err != nil {
		return nil, err
	}
	cli.DefaultHTTPRetries = 2
	cli.DefaultHTTPBackoff = 500 * time.Millisecond
	if cfg.Matrix.InsecureSkipTLS {
		cli.Client = &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // Local self-signed cert opt-in.
		}
	} else {
		cli.Client = &http.Client{Timeout: 30 * time.Second}
	}
	return &MatrixClientSink{cfg: cfg, store: store, client: cli, accessToken: accessToken}, nil
}

func (m *MatrixClientSink) EnsurePortal(ctx context.Context, chat Chat, avatar *MatrixMedia) (string, error) {
	if roomID, ok, err := m.store.PortalRoomID(ctx, chat.ID); err != nil {
		return "", err
	} else if ok {
		if err := m.updateRoomMetadata(ctx, roomID, chat); err != nil {
			return "", err
		}
		if err := m.updateRoomAvatar(ctx, roomID, avatar); err != nil {
			return "", err
		}
		return roomID, nil
	}

	name, topic, profileValue := portalProfileSyncValue(m.cfg, chat)
	req := &mautrix.ReqCreateRoom{
		Name:     name,
		Topic:    topic,
		Preset:   "private_chat",
		IsDirect: !chat.IsGroup,
	}
	if m.cfg.Matrix.InviteUserID != "" {
		req.Invite = []id.UserID{id.UserID(m.cfg.Matrix.InviteUserID)}
	}
	if avatar != nil {
		avatarURL, info, err := m.uploadAvatar(ctx, avatar)
		if err != nil {
			return "", err
		}
		if avatarURL != "" {
			req.InitialState = append(req.InitialState, &event.Event{
				Type: event.StateRoomAvatar,
				Content: event.Content{Parsed: &event.RoomAvatarEventContent{
					URL:  avatarURL,
					Info: info,
				}},
			})
		}
	} else if avatarURL, info, err := m.uploadLocalAvatar(ctx, chat.AvatarURL); err == nil && avatarURL != "" {
		req.InitialState = append(req.InitialState, &event.Event{
			Type: event.StateRoomAvatar,
			Content: event.Content{Parsed: &event.RoomAvatarEventContent{
				URL:  avatarURL,
				Info: info,
			}},
		})
	}
	resp, err := m.createRoom(ctx, req)
	if err != nil {
		return "", err
	}
	if err := m.store.SetValue(ctx, portalProfileSyncKey(chat.ID), profileValue); err != nil {
		return "", err
	}
	return resp.RoomID.String(), nil
}

func (m *MatrixClientSink) createRoom(ctx context.Context, req *mautrix.ReqCreateRoom) (*mautrix.RespCreateRoom, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := m.requestContext(ctx)
	defer cancel()
	url := strings.TrimRight(m.cfg.Matrix.HomeserverURL, "/") + "/_matrix/client/v3/createRoom"
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+m.accessToken)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if rateErr := parseMatrixRateLimit(resp.StatusCode, resp.Header, body); rateErr != nil {
			return nil, rateErr
		}
		return nil, fmt.Errorf("create room failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out struct {
		RoomID string `json:"room_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.RoomID == "" {
		return nil, fmt.Errorf("create room failed: missing room_id")
	}
	return &mautrix.RespCreateRoom{RoomID: id.RoomID(out.RoomID)}, nil
}

func (m *MatrixClientSink) PortalAccessible(ctx context.Context, roomID string) (bool, error) {
	reqCtx, cancel := m.requestContext(ctx)
	defer cancel()
	url := strings.TrimRight(m.cfg.Matrix.HomeserverURL, "/") +
		"/_matrix/client/v3/rooms/" + url.PathEscape(roomID) + "/state/m.room.create/"
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+m.accessToken)
	resp, err := m.client.Client.Do(httpReq)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusForbidden, http.StatusNotFound:
		return false, nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if rateErr := parseMatrixRateLimit(resp.StatusCode, resp.Header, body); rateErr != nil {
		return false, rateErr
	}
	return false, fmt.Errorf("check room state failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
}

func (m *MatrixClientSink) EnsurePortalSpaces(ctx context.Context, chats []Chat) error {
	if len(chats) == 0 {
		return nil
	}
	rootID, err := m.ensureSpace(ctx, "matrix_space:root", "All Chats", "Beeper bridge workspace", platformAvatarMedia(Chat{Network: "Beeper"}))
	if err != nil {
		return err
	}
	eligibleChats := make([]Chat, 0, len(chats))
	byPlatform := make(map[string][]Chat)
	for _, chat := range chats {
		if _, ok, err := m.store.PortalRoomID(ctx, chat.ID); err != nil {
			return err
		} else if ok {
			eligibleChats = append(eligibleChats, chat)
			platform := PlatformDisplayName(chat)
			byPlatform[platform] = append(byPlatform[platform], chat)
		}
	}
	switch matrixSpaceGrouping(m.cfg.Matrix.SpaceGrouping) {
	case "none":
		return m.linkChatsIntoSpace(ctx, rootID, eligibleChats)
	case "account":
		return m.ensureAccountSpaces(ctx, rootID, eligibleChats)
	case "platform-account":
		return m.ensurePlatformAccountSpaces(ctx, rootID, byPlatform)
	}
	platforms := make([]string, 0, len(byPlatform))
	for platform := range byPlatform {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	for _, platform := range platforms {
		platformChats := byPlatform[platform]
		spaceID, err := m.ensureSpace(ctx, "matrix_space:platform:"+platform, platform, platform+" chats", platformAvatarMedia(platformChats[0]))
		if err != nil {
			return err
		}
		if err := m.linkSpaceChild(ctx, rootID, spaceID, platform, true); err != nil {
			return err
		}
		if err := m.clearSpaceParent(ctx, spaceID, rootID); err != nil {
			return err
		}
		sort.Slice(platformChats, func(i, j int) bool {
			return roomDisplayName(m.cfg, platformChats[i]) < roomDisplayName(m.cfg, platformChats[j])
		})
		if err := m.linkChatsIntoSpace(ctx, spaceID, platformChats); err != nil {
			return err
		}
	}
	return nil
}

func matrixSpaceGrouping(raw string) string {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "", "platform", "messenger", "service":
		return "platform"
	case "platform-account", "platform_account", "team", "hierarchy", "hikiri", "hikiris":
		return "platform-account"
	case "account", "login", "channel":
		return "account"
	case "none", "flat", "off":
		return "none"
	default:
		return "platform"
	}
}

func (m *MatrixClientSink) ensurePlatformAccountSpaces(ctx context.Context, rootID string, byPlatform map[string][]Chat) error {
	platforms := make([]string, 0, len(byPlatform))
	for platform := range byPlatform {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	for _, platform := range platforms {
		platformChats := byPlatform[platform]
		platformSpaceID, err := m.ensureSpace(ctx, "matrix_space:platform:"+platform, platform, platform+" chats", platformAvatarMedia(platformChats[0]))
		if err != nil {
			return err
		}
		if err := m.linkSpaceChild(ctx, rootID, platformSpaceID, platform, true); err != nil {
			return err
		}
		if err := m.clearSpaceParent(ctx, platformSpaceID, rootID); err != nil {
			return err
		}
		if err := m.ensureAccountSpaces(ctx, platformSpaceID, platformChats); err != nil {
			return err
		}
	}
	return nil
}

func (m *MatrixClientSink) ensureAccountSpaces(ctx context.Context, parentID string, chats []Chat) error {
	byAccount := make(map[string][]Chat)
	for _, chat := range chats {
		key := accountSpaceKey(chat)
		byAccount[key] = append(byAccount[key], chat)
	}
	accounts := make([]string, 0, len(byAccount))
	for account := range byAccount {
		accounts = append(accounts, account)
	}
	sort.Strings(accounts)
	for _, account := range accounts {
		accountChats := byAccount[account]
		spaceName := accountSpaceDisplayName(accountChats[0])
		spaceID, err := m.ensureSpace(ctx, "matrix_space:account:"+account, spaceName, accountSpaceTopic(accountChats[0]), accountSpaceAvatarMedia(accountChats[0]))
		if err != nil {
			return err
		}
		if err := m.linkSpaceChild(ctx, parentID, spaceID, spaceName, true); err != nil {
			return err
		}
		if err := m.clearSpaceParent(ctx, spaceID, parentID); err != nil {
			return err
		}
		sort.Slice(accountChats, func(i, j int) bool {
			return roomDisplayName(m.cfg, accountChats[i]) < roomDisplayName(m.cfg, accountChats[j])
		})
		if err := m.linkChatsIntoSpace(ctx, spaceID, accountChats); err != nil {
			return err
		}
	}
	return nil
}

func (m *MatrixClientSink) linkChatsIntoSpace(ctx context.Context, spaceID string, chats []Chat) error {
	sort.Slice(chats, func(i, j int) bool {
		return roomDisplayName(m.cfg, chats[i]) < roomDisplayName(m.cfg, chats[j])
	})
	for _, chat := range chats {
		roomID, ok, err := m.store.PortalRoomID(ctx, chat.ID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := m.linkSpaceChild(ctx, spaceID, roomID, roomDisplayName(m.cfg, chat), false); err != nil {
			return err
		}
		if err := m.linkSpaceParent(ctx, roomID, spaceID, true); err != nil {
			return err
		}
	}
	return nil
}

func (m *MatrixClientSink) ensureSpace(ctx context.Context, key string, name string, topic string, avatar *MatrixMedia) (string, error) {
	if roomID, err := m.store.GetValue(ctx, key); err != nil {
		return "", err
	} else if roomID != "" {
		if err := m.updateSpaceMetadata(ctx, roomID, name, topic, avatar); err != nil {
			return "", err
		}
		return roomID, nil
	}
	req := &mautrix.ReqCreateRoom{
		Name:            name,
		Topic:           topic,
		Preset:          "private_chat",
		CreationContent: map[string]interface{}{"type": string(event.RoomTypeSpace)},
	}
	if m.cfg.Matrix.InviteUserID != "" {
		req.Invite = []id.UserID{id.UserID(m.cfg.Matrix.InviteUserID)}
	}
	if avatar != nil {
		avatarURL, info, err := m.uploadAvatar(ctx, avatar)
		if err != nil {
			return "", err
		}
		if avatarURL != "" {
			req.InitialState = append(req.InitialState, &event.Event{
				Type: event.StateRoomAvatar,
				Content: event.Content{Parsed: &event.RoomAvatarEventContent{
					URL:  avatarURL,
					Info: info,
				}},
			})
		}
	}
	resp, err := m.createRoom(ctx, req)
	if err != nil {
		return "", err
	}
	roomID := resp.RoomID.String()
	if err := m.store.SetValue(ctx, key, roomID); err != nil {
		return "", err
	}
	return roomID, nil
}

func (m *MatrixClientSink) updateSpaceMetadata(ctx context.Context, roomID string, name string, topic string, avatar *MatrixMedia) error {
	if _, err := m.sendStateEvent(ctx, id.RoomID(roomID), event.StateRoomName, "", &event.RoomNameEventContent{Name: name}); err != nil {
		return err
	}
	if _, err := m.sendStateEvent(ctx, id.RoomID(roomID), event.StateTopic, "", &event.TopicEventContent{Topic: topic}); err != nil {
		return err
	}
	return m.updateRoomAvatar(ctx, roomID, avatar)
}

func (m *MatrixClientSink) linkSpaceChild(ctx context.Context, parentRoomID string, childRoomID string, order string, suggested bool) error {
	_, err := m.sendStateEvent(ctx, id.RoomID(parentRoomID), event.StateSpaceChild, childRoomID, &event.SpaceChildEventContent{
		Via:       []string{m.matrixServerName()},
		Order:     spaceOrder(order),
		Suggested: suggested,
	})
	return err
}

func (m *MatrixClientSink) linkSpaceParent(ctx context.Context, childRoomID string, parentRoomID string, canonical bool) error {
	_, err := m.sendStateEvent(ctx, id.RoomID(childRoomID), event.StateSpaceParent, parentRoomID, &event.SpaceParentEventContent{
		Via:       []string{m.matrixServerName()},
		Canonical: canonical,
	})
	return err
}

func (m *MatrixClientSink) clearSpaceParent(ctx context.Context, childRoomID string, parentRoomID string) error {
	_, err := m.sendStateEvent(ctx, id.RoomID(childRoomID), event.StateSpaceParent, parentRoomID, map[string]any{})
	return err
}

func (m *MatrixClientSink) matrixServerName() string {
	if _, server, ok := strings.Cut(m.cfg.Matrix.UserID, ":"); ok && server != "" {
		return server
	}
	if parsed, err := url.Parse(m.cfg.Matrix.HomeserverURL); err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return "localhost"
}

func spaceOrder(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "0"
	}
	value = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, value)
	if len(value) > 50 {
		value = value[:50]
	}
	return value
}

func parseMatrixRateLimit(statusCode int, headers http.Header, body []byte) *MatrixRateLimitError {
	var matrixErr struct {
		ErrCode      string `json:"errcode"`
		Message      string `json:"error"`
		RetryAfterMS int64  `json:"retry_after_ms"`
	}
	_ = json.Unmarshal(body, &matrixErr)
	if statusCode != http.StatusTooManyRequests && matrixErr.ErrCode != "M_LIMIT_EXCEEDED" {
		return nil
	}
	retryAfter := time.Duration(matrixErr.RetryAfterMS) * time.Millisecond
	if retryAfter <= 0 {
		retryAfter = retryAfterHeader(headers.Get("Retry-After"))
	}
	return &MatrixRateLimitError{
		RetryAfter: retryAfter,
		StatusCode: statusCode,
		ErrCode:    matrixErr.ErrCode,
		Message:    matrixErr.Message,
	}
}

func retryAfterHeader(raw string) time.Duration {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		if delay := time.Until(retryAt); delay > 0 {
			return delay
		}
	}
	return 0
}

func (m *MatrixClientSink) updateRoomMetadata(ctx context.Context, roomID string, chat Chat) error {
	key := portalProfileSyncKey(chat.ID)
	name, topic, value := portalProfileSyncValue(m.cfg, chat)
	last, err := m.store.GetValue(ctx, key)
	if err != nil || last == value {
		return err
	}
	if _, err = m.sendStateEvent(ctx, id.RoomID(roomID), event.StateRoomName, "", &event.RoomNameEventContent{Name: name}); err != nil {
		return err
	}
	if _, err = m.sendStateEvent(ctx, id.RoomID(roomID), event.StateTopic, "", &event.TopicEventContent{Topic: topic}); err != nil {
		return err
	}
	return m.store.SetValue(ctx, key, value)
}

func portalProfileSyncKey(chatID string) string {
	return "portal_profile:" + chatID
}

func portalProfileSyncValue(cfg Config, chat Chat) (name string, topic string, value string) {
	name = strings.TrimSpace(cfg.Matrix.RoomNamePrefix + roomDisplayName(cfg, chat))
	topic = fmt.Sprintf("Beeper source chat %s from %s (%s)", chat.ID, PlatformDisplayName(chat), chat.AccountID)
	return name, topic, name + "\x00" + topic
}

func (m *MatrixClientSink) updateRoomAvatar(ctx context.Context, roomID string, avatar *MatrixMedia) error {
	avatarURL, info, err := m.uploadAvatar(ctx, avatar)
	if err != nil || avatarURL == "" {
		return err
	}
	_, err = m.sendStateEvent(ctx, id.RoomID(roomID), event.StateRoomAvatar, "", &event.RoomAvatarEventContent{
		URL:  avatarURL,
		Info: info,
	})
	return err
}

func (m *MatrixClientSink) sendStateEvent(ctx context.Context, roomID id.RoomID, eventType event.Type, stateKey string, content any) (*mautrix.RespSendEvent, error) {
	reqCtx, cancel := m.requestContext(ctx)
	defer cancel()
	return m.client.SendStateEvent(reqCtx, roomID, eventType, stateKey, content)
}

func (m *MatrixClientSink) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := time.Duration(m.cfg.Sync.PortalTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 75 * time.Second
	}
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	return context.WithTimeout(ctx, timeout)
}

func (m *MatrixClientSink) uploadAvatar(ctx context.Context, avatar *MatrixMedia) (id.ContentURIString, *event.FileInfo, error) {
	if avatar == nil {
		return "", nil, nil
	}
	if avatar.AssetID != "" {
		cached, ok, err := m.store.MediaByAssetID(ctx, avatar.AssetID)
		if err != nil {
			return "", nil, err
		}
		if ok && avatarCacheReusable(cached, avatar) {
			return id.ContentURIString(cached.CachedMXC), &event.FileInfo{
				MimeType: cached.MimeType,
				Size:     int(cached.SizeBytes),
			}, nil
		}
	}
	if avatar.Content == nil {
		return "", nil, nil
	}
	upload, err := m.client.UploadMedia(ctx, mautrix.ReqUploadMedia{
		Content:       avatar.Content,
		ContentLength: avatar.SizeBytes,
		ContentType:   avatar.MimeType,
		FileName:      avatar.FileName,
	})
	if err != nil {
		return "", nil, err
	}
	if err := m.store.UpsertMediaCache(ctx, *avatar, string(upload.ContentURI.CUString())); err != nil {
		return "", nil, err
	}
	return upload.ContentURI.CUString(), &event.FileInfo{
		MimeType: avatar.MimeType,
		Size:     int(avatar.SizeBytes),
	}, nil
}

func avatarCacheReusable(cached MatrixMedia, avatar *MatrixMedia) bool {
	if avatar == nil || avatar.ContentHash == "" {
		return true
	}
	return cached.ContentHash == avatar.ContentHash
}

func (m *MatrixClientSink) EnsurePuppet(ctx context.Context, sender Sender) (string, error) {
	if sender.ID == "" {
		return "", nil
	}
	matrixUserID := "@" + MatrixGhostLocalpart(sender.ID) + ":beeper-source.local"
	if err := m.store.UpsertPuppet(ctx, sender, matrixUserID); err != nil {
		return "", err
	}
	return matrixUserID, nil
}

func (m *MatrixClientSink) SendMessage(ctx context.Context, outbound MatrixOutbound) (string, error) {
	content, err := m.messageContent(ctx, outbound)
	if err != nil {
		return "", err
	}
	req := mautrix.ReqSendEvent{TransactionID: outbound.TransactionID}
	if !outbound.Timestamp.IsZero() {
		req.Timestamp = outbound.Timestamp.UnixMilli()
	}
	resp, err := m.client.SendMessageEvent(ctx, id.RoomID(outbound.RoomID), event.EventMessage, contentPayload(content, outbound), req)
	if err != nil {
		return "", err
	}
	return resp.EventID.String(), nil
}

func (m *MatrixClientSink) EditMessage(ctx context.Context, outbound MatrixOutbound, targetEventID string) (string, error) {
	newContent, err := m.messageContent(ctx, outbound)
	if err != nil {
		return "", err
	}
	editContent := *newContent
	editContent.NewContent = newContent
	editContent.RelatesTo = &event.RelatesTo{
		Type:    event.RelReplace,
		EventID: id.EventID(targetEventID),
	}
	editContent.Body = "* " + newContent.Body
	if newContent.FormattedBody != "" {
		editContent.FormattedBody = "* " + newContent.FormattedBody
	}
	req := mautrix.ReqSendEvent{TransactionID: outbound.TransactionID}
	if !outbound.Timestamp.IsZero() {
		req.Timestamp = outbound.Timestamp.UnixMilli()
	}
	resp, err := m.client.SendMessageEvent(ctx, id.RoomID(outbound.RoomID), event.EventMessage, contentPayload(&editContent, outbound), req)
	if err != nil {
		return "", err
	}
	return resp.EventID.String(), nil
}

func (m *MatrixClientSink) RedactMessage(ctx context.Context, roomID string, eventID string, txnID string, reason string) (string, error) {
	resp, err := m.client.RedactEvent(ctx, id.RoomID(roomID), id.EventID(eventID), mautrix.ReqRedact{
		Reason: reason,
		TxnID:  txnID,
	})
	if err != nil {
		return "", err
	}
	return resp.EventID.String(), nil
}

func (m *MatrixClientSink) messageContent(ctx context.Context, outbound MatrixOutbound) (*event.MessageEventContent, error) {
	profile := &event.BeeperPerMessageProfile{
		ID:          outbound.SenderID,
		Displayname: outbound.SenderName,
		HasFallback: true,
	}
	if outbound.SenderAvatar != nil {
		avatarURL, _, err := m.uploadAvatar(ctx, outbound.SenderAvatar)
		if err == nil && avatarURL != "" {
			profile.AvatarURL = &avatarURL
		}
	}
	content := &event.MessageEventContent{
		MsgType:                 event.MessageType(outbound.MsgType),
		Body:                    outbound.Body,
		BeeperPerMessageProfile: profile,
	}
	if outbound.Media != nil {
		upload, err := m.client.UploadMedia(ctx, mautrix.ReqUploadMedia{
			Content:       outbound.Media.Content,
			ContentLength: outbound.Media.SizeBytes,
			ContentType:   outbound.Media.MimeType,
			FileName:      outbound.Media.FileName,
		})
		if err != nil {
			return nil, err
		}
		content.URL = upload.ContentURI.CUString()
		content.FileName = outbound.Media.FileName
		content.Info = &event.FileInfo{
			MimeType:   outbound.Media.MimeType,
			Width:      outbound.Media.Width,
			Height:     outbound.Media.Height,
			Duration:   outbound.Media.DurationMS,
			Size:       int(outbound.Media.SizeBytes),
			MauGIF:     outbound.Media.IsGIF,
			IsAnimated: outbound.Media.IsGIF,
		}
		if outbound.Media.IsVoiceNote {
			content.MSC3245Voice = &event.MSC3245Voice{}
			content.MSC1767Audio = &event.MSC1767Audio{Duration: outbound.Media.DurationMS}
		}
	}
	if outbound.ReplyToEvent != "" {
		content.GetRelatesTo().SetReplyTo(id.EventID(outbound.ReplyToEvent))
	}
	if m.cfg.Matrix.PrefixSender && outbound.SenderName != "" {
		content.Body = outbound.SenderName + ": " + content.Body
	}
	if outbound.HTML != "" {
		content.Format = event.FormatHTML
		content.FormattedBody = outbound.HTML
		if m.cfg.Matrix.PrefixSender && outbound.SenderName != "" {
			content.FormattedBody = "<strong>" + html.EscapeString(outbound.SenderName) + "</strong>: " + outbound.HTML
		}
	}
	return content, nil
}

func contentPayload(content *event.MessageEventContent, outbound MatrixOutbound) map[string]any {
	data, err := json.Marshal(content)
	if err != nil {
		return map[string]any{
			"msgtype": string(content.MsgType),
			"body":    content.Body,
		}
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return map[string]any{
			"msgtype": string(content.MsgType),
			"body":    content.Body,
		}
	}
	source := map[string]any{
		"message_id": outbound.MessageID,
		"chat_id":    outbound.ChatID,
		"account_id": outbound.AccountID,
	}
	if outbound.SortKey != "" {
		source["sort_key"] = outbound.SortKey
	}
	if outbound.IsHidden {
		source["is_hidden"] = outbound.IsHidden
	}
	if outbound.IsSender {
		source["is_sender"] = outbound.IsSender
	}
	if outbound.IsUnread {
		source["is_unread"] = outbound.IsUnread
	}
	if len(outbound.Mentions) > 0 {
		source["mentions"] = outbound.Mentions
	}
	if outbound.AttachmentID != "" {
		source["attachment_id"] = outbound.AttachmentID
	}
	if outbound.AttachmentIdx > 0 {
		source["attachment_index"] = outbound.AttachmentIdx
	}
	payload["com.openclaw.beeper.source"] = source
	if outbound.VoiceAI != nil {
		payload["com.openclaw.voice_ai"] = outbound.VoiceAI
	}
	return payload
}

func roomDisplayName(cfg Config, chat Chat) string {
	account := PlatformDisplayName(chat)
	name := strings.TrimSpace(chat.Name)
	if !cfg.Matrix.RoomNameIncludePlatform {
		name = stripLegacyRoomNamePlatform(name, account)
		if name != "" {
			return name
		}
		return account
	}
	if account == "" {
		return name
	}
	if name == "" {
		return "[" + account + "]"
	}
	return "[" + account + "] " + name
}

func stripLegacyRoomNamePlatform(name, account string) string {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, "Beeper:") {
		name = strings.TrimSpace(strings.TrimPrefix(name, "Beeper:"))
	}
	if account != "" {
		prefix := "[" + account + "]"
		if strings.HasPrefix(name, prefix) {
			name = strings.TrimSpace(strings.TrimPrefix(name, prefix))
		}
	}
	return name
}

func (m *MatrixClientSink) uploadLocalAvatar(ctx context.Context, rawURL string) (id.ContentURIString, *event.FileInfo, error) {
	path := strings.TrimSpace(rawURL)
	if path == "" {
		return "", nil, nil
	}
	if strings.HasPrefix(path, "file://") {
		parsed, err := url.Parse(path)
		if err != nil {
			return "", nil, err
		}
		path = parsed.Path
	}
	if strings.Contains(path, "://") {
		return "", nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return "", nil, err
	}
	mimeType := mime.TypeByExtension(filepath.Ext(path))
	if mimeType == "" {
		head := make([]byte, 512)
		n, _ := file.Read(head)
		mimeType = http.DetectContentType(head[:n])
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return "", nil, err
		}
	}
	upload, err := m.client.UploadMedia(ctx, mautrix.ReqUploadMedia{
		Content:       file,
		ContentLength: stat.Size(),
		ContentType:   mimeType,
		FileName:      filepath.Base(path),
	})
	if err != nil {
		return "", nil, err
	}
	return upload.ContentURI.CUString(), &event.FileInfo{
		MimeType: mimeType,
		Size:     int(stat.Size()),
	}, nil
}
