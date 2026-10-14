package connector

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Ensure MyNetworkClient implements NetworkAPI.
var _ bridgev2.NetworkAPI = (*MyNetworkClient)(nil)

const (
	remoteReconnectBaseDelay            = 30 * time.Second
	remoteReconnectMaxDelay             = 5 * time.Minute
	maxSentEventCacheSize               = 4096
	defaultLocalMatrixSyncTimelineLimit = 100
)

// MyNetworkClient implements the bridgev2.NetworkAPI for interacting
// with the simple network on behalf of a specific user login.
type MyNetworkClient struct {
	log                 zerolog.Logger
	bridge              *bridgev2.Bridge
	login               *bridgev2.UserLogin
	connector           *MyConnector
	metadata            *loginMetadataStore
	mx                  *mautrix.Client
	connectMu           sync.Mutex
	cancel              context.CancelFunc
	syncGeneration      uint64
	reconnectGeneration uint64
	syncHandlersReady   bool
	reconnectScheduled  bool
	reconnectAttempts   int
	retired             bool
	loggedIn            bool
	sentMu              sync.Mutex
	sentEvents          map[id.EventID]struct{}
	sentEventOrder      []id.EventID
	avatarMu            sync.Mutex
	badAvatars          map[id.ContentURIString]struct{}
	fallbackAvatars     map[id.ContentURIString]*bridgev2.Avatar
	mediaMu             sync.RWMutex
	localMaxUploadSize  int64
	typingMu            sync.Mutex
	remoteTyping        map[id.RoomID]map[id.UserID]struct{}
	reactionMu          sync.Mutex
	remoteReactions     map[id.EventID]remoteReaction
}

func (nc *MyNetworkClient) Connect(ctx context.Context) {
	nc.connect(ctx, nil)
}

// connect starts the remote sync. A non-nil expected generation is used by
// delayed retry workers to avoid reviving a client that was replaced while it
// was waiting to retry.
func (nc *MyNetworkClient) connect(ctx context.Context, expectedReconnectGeneration *uint64) {
	nc.connectMu.Lock()
	if expectedReconnectGeneration != nil && *expectedReconnectGeneration != nc.reconnectGeneration {
		nc.connectMu.Unlock()
		return
	}
	if nc.retired {
		nc.connectMu.Unlock()
		return
	}
	if nc.mx == nil {
		nc.loggedIn = false
		nc.log.Error().Msg("Remote Matrix client missing")
		nc.connectMu.Unlock()
		return
	}
	if nc.cancel != nil {
		nc.loggedIn = true
		nc.log.Debug().Msg("Remote Matrix client already connected")
		nc.connectMu.Unlock()
		return
	}
	if err := nc.remoteMatrixPreflight(ctx); err != nil {
		nc.loggedIn = false
		login := nc.login
		shouldReconnect := !nc.reconnectScheduled
		if shouldReconnect {
			nc.reconnectScheduled = true
		}
		nc.connectMu.Unlock()
		nc.sendTransientDisconnect(login, "REMOTE_MATRIX_UNREACHABLE", err)
		nc.log.Err(err).Msg("Remote Matrix preflight failed")
		if shouldReconnect {
			go nc.reconnectAfterRemoteSyncFailure()
		}
		return
	}
	nc.loggedIn = true
	nc.reconnectScheduled = false
	nc.reconnectAttempts = 0
	nc.login.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateConnected,
		RemoteID:   nc.login.ID,
	})
	nc.refreshLocalMediaConfig(ctx)
	nc.configureLocalMatrixSyncer()
	syncCtx, cancel := context.WithCancel(context.Background())
	nc.syncGeneration++
	generation := nc.syncGeneration
	nc.cancel = cancel
	go nc.syncRooms(syncCtx)
	go func() {
		nc.handleSyncExit(generation, nc.mx.SyncWithContext(syncCtx))
	}()
	nc.connectMu.Unlock()
}

func (nc *MyNetworkClient) configureLocalMatrixSyncer() {
	if nc.syncHandlersReady {
		return
	}
	syncer, ok := nc.mx.Syncer.(*mautrix.DefaultSyncer)
	if !ok {
		return
	}
	syncer.FilterJSON = localMatrixSyncFilter()
	syncer.OnSync(dropInitialTimelineEvents)
	syncer.OnEventType(event.EventMessage, func(ctx context.Context, evt *event.Event) {
		nc.handleLocalMatrixEvent(ctx, evt)
	})
	syncer.OnEventType(event.EventReaction, func(ctx context.Context, evt *event.Event) {
		nc.handleLocalMatrixReaction(ctx, evt)
	})
	syncer.OnEventType(event.EventRedaction, func(ctx context.Context, evt *event.Event) {
		nc.handleLocalMatrixRedaction(ctx, evt)
	})
	syncer.OnEventType(event.EventUnstablePollStart, func(ctx context.Context, evt *event.Event) {
		nc.handleLocalMatrixPoll(ctx, evt)
	})
	syncer.OnEventType(event.EventUnstablePollResponse, func(ctx context.Context, evt *event.Event) {
		nc.handleLocalMatrixPollResponse(ctx, evt)
	})
	syncer.OnEventType(event.CallInvite, func(ctx context.Context, evt *event.Event) {
		nc.handleLocalMatrixCallInvite(ctx, evt)
	})
	syncer.OnEventType(event.EphemeralEventTyping, func(ctx context.Context, evt *event.Event) {
		nc.handleLocalMatrixTyping(ctx, evt)
	})
	syncer.OnEventType(event.EphemeralEventReceipt, func(ctx context.Context, evt *event.Event) {
		nc.handleLocalMatrixReceipt(ctx, evt)
	})
	syncer.OnEventType(event.StateRoomName, func(ctx context.Context, evt *event.Event) {
		nc.resyncLocalRoomInfo(ctx, evt.RoomID)
	})
	syncer.OnEventType(event.StateRoomAvatar, func(ctx context.Context, evt *event.Event) {
		nc.resyncLocalRoomInfo(ctx, evt.RoomID)
	})
	syncer.OnEventType(event.StateTopic, func(ctx context.Context, evt *event.Event) {
		nc.resyncLocalRoomInfo(ctx, evt.RoomID)
	})
	nc.syncHandlersReady = true
}

func (nc *MyNetworkClient) handleSyncExit(generation uint64, err error) {
	nc.connectMu.Lock()
	if generation != nc.syncGeneration {
		nc.connectMu.Unlock()
		return
	}
	nc.cancel = nil
	if err == nil || errors.Is(err, context.Canceled) {
		nc.connectMu.Unlock()
		return
	}
	nc.loggedIn = false
	login := nc.login
	if errors.Is(err, mautrix.MUnknownToken) {
		nc.reconnectScheduled = false
		nc.connectMu.Unlock()
		nc.sendBadCredentials(login, err)
		nc.log.Err(err).Msg("Remote Matrix sync token was rejected")
		return
	}
	shouldReconnect := !nc.reconnectScheduled
	if shouldReconnect {
		nc.reconnectScheduled = true
	}
	nc.connectMu.Unlock()
	nc.sendTransientDisconnect(login, "REMOTE_MATRIX_SYNC_STOPPED", err)
	nc.log.Err(err).Msg("Remote Matrix sync stopped")
	if shouldReconnect {
		go nc.reconnectAfterRemoteSyncFailure()
	}
}

func (nc *MyNetworkClient) remoteMatrixPreflight(ctx context.Context) error {
	preflightCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := nc.mx.Versions(preflightCtx); err != nil {
		return fmt.Errorf("remote Matrix /versions failed: %w", err)
	}
	return nil
}

func (nc *MyNetworkClient) sendTransientDisconnect(login *bridgev2.UserLogin, code string, err error) {
	if login == nil {
		return
	}
	reason := ""
	if err != nil {
		reason = err.Error()
	}
	login.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateTransientDisconnect,
		RemoteID:   login.ID,
		Error:      status.BridgeStateErrorCode(code),
		Reason:     reason,
	})
}

func (nc *MyNetworkClient) sendBadCredentials(login *bridgev2.UserLogin, err error) {
	if login == nil {
		return
	}
	reason := ""
	if err != nil {
		reason = err.Error()
	}
	login.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateBadCredentials,
		RemoteID:   login.ID,
		Error:      status.BridgeStateErrorCode("REMOTE_MATRIX_BAD_CREDENTIALS"),
		Reason:     reason,
	})
}

func (nc *MyNetworkClient) reconnectAfterRemoteSyncFailure() {
	nc.reconnectAfterRemoteSyncFailureWithDelay(remoteReconnectDelay)
}

func (nc *MyNetworkClient) reconnectAfterRemoteSyncFailureWithDelay(delayFunc func(int) time.Duration) {
	nc.connectMu.Lock()
	attempt := nc.reconnectAttempts
	reconnectGeneration := nc.reconnectGeneration
	if nc.reconnectAttempts < 10 {
		nc.reconnectAttempts++
	}
	nc.connectMu.Unlock()
	time.Sleep(delayFunc(attempt))
	nc.connectMu.Lock()
	if reconnectGeneration != nc.reconnectGeneration {
		nc.connectMu.Unlock()
		return
	}
	shouldReconnect := nc.cancel == nil && nc.mx != nil && nc.reconnectScheduled
	if shouldReconnect {
		nc.reconnectScheduled = false
	}
	nc.connectMu.Unlock()
	if !shouldReconnect {
		return
	}
	nc.log.Info().Msg("Retrying remote Matrix sync after transient failure")
	nc.connect(context.Background(), &reconnectGeneration)
}

func remoteReconnectDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	multiplier := math.Pow(2, float64(attempt))
	delay := time.Duration(float64(remoteReconnectBaseDelay) * multiplier)
	if delay > remoteReconnectMaxDelay {
		return remoteReconnectMaxDelay
	}
	return delay
}

func (nc *MyNetworkClient) refreshLocalMediaConfig(ctx context.Context) {
	cfg, err := nc.mx.GetMediaConfig(ctx)
	if err != nil {
		nc.log.Warn().Err(err).Msg("Failed to fetch remote Matrix media config")
		return
	}
	if cfg == nil || cfg.UploadSize <= 0 {
		return
	}
	nc.mediaMu.Lock()
	nc.localMaxUploadSize = cfg.UploadSize
	nc.mediaMu.Unlock()
	nc.log.Info().
		Int64("max_upload_size", cfg.UploadSize).
		Msg("Fetched remote Matrix media config")
}

func (nc *MyNetworkClient) getLocalMaxUploadSize() int64 {
	override := envInt64("LOCAL_MATRIX_MAX_UPLOAD_SIZE", 0)
	nc.mediaMu.RLock()
	maxSize := nc.localMaxUploadSize
	nc.mediaMu.RUnlock()
	if override > 0 && maxSize > 0 && override > maxSize {
		return maxSize
	} else if override > 0 {
		return override
	}
	if maxSize > 0 {
		return maxSize
	}
	return 50 * 1024 * 1024
}

func localMatrixSyncFilter() *mautrix.Filter {
	timelineLimit := localMatrixSyncTimelineLimit()
	return &mautrix.Filter{
		EventFormat: mautrix.EventFormatClient,
		Presence:    &mautrix.FilterPart{NotTypes: []event.Type{event.NewEventType("*")}},
		AccountData: &mautrix.FilterPart{Limit: 1},
		Room: &mautrix.RoomFilter{
			AccountData: &mautrix.FilterPart{Limit: 1},
			Ephemeral: &mautrix.FilterPart{
				Limit: 20,
				Types: []event.Type{
					event.EphemeralEventTyping,
					event.EphemeralEventReceipt,
				},
			},
			State: &mautrix.FilterPart{
				Limit:           20,
				LazyLoadMembers: true,
				Types: []event.Type{
					event.StateRoomName,
					event.StateRoomAvatar,
					event.StateTopic,
					event.StateMember,
				},
			},
			Timeline: &mautrix.FilterPart{
				Limit: timelineLimit,
				Types: []event.Type{
					event.EventMessage,
					event.EventSticker,
					event.EventReaction,
					event.EventRedaction,
					event.EventUnstablePollStart,
					event.EventUnstablePollResponse,
					event.EventUnstablePollEnd,
					event.CallInvite,
					event.StateRoomName,
					event.StateRoomAvatar,
					event.StateTopic,
				},
			},
		},
	}
}

func localMatrixSyncTimelineLimit() int {
	limit := envInt("LOCAL_MATRIX_SYNC_TIMELINE_LIMIT", defaultLocalMatrixSyncTimelineLimit)
	if limit < defaultLocalMatrixSyncTimelineLimit {
		return defaultLocalMatrixSyncTimelineLimit
	}
	return limit
}

func dropInitialTimelineEvents(_ context.Context, resp *mautrix.RespSync, since string) bool {
	if since != "" || resp == nil {
		return true
	}
	for _, roomData := range resp.Rooms.Join {
		if roomData != nil {
			roomData.Timeline.Events = nil
		}
	}
	return true
}

func (nc *MyNetworkClient) resyncLocalRoomInfo(ctx context.Context, roomID id.RoomID) {
	if roomID == "" {
		return
	}
	nc.connector.resyncRoom(ctx, nc.login, roomID, nc.buildChatInfo(ctx, roomID))
}

func (nc *MyNetworkClient) Disconnect() {
	nc.disconnect(false)
}

func (nc *MyNetworkClient) retire() {
	nc.disconnect(true)
}

func (nc *MyNetworkClient) disconnect(retire bool) {
	nc.connectMu.Lock()
	defer nc.connectMu.Unlock()
	if retire {
		nc.retired = true
	}
	nc.syncGeneration++
	nc.reconnectGeneration++
	nc.reconnectScheduled = false
	nc.reconnectAttempts = 0
	nc.loggedIn = false
	if nc.login != nil && nc.login.BridgeState != nil {
		nc.login.BridgeState.StopUnknownErrorReconnect()
	}
	if nc.cancel != nil {
		nc.cancel()
		nc.cancel = nil
	}
	nc.login.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateLoggedOut,
		RemoteID:   nc.login.ID,
		Reason:     "Disconnected from remote Matrix bridge",
	})
}

func (nc *MyNetworkClient) markSentEvent(eventID id.EventID) {
	if eventID == "" {
		return
	}
	nc.sentMu.Lock()
	defer nc.sentMu.Unlock()
	if nc.sentEvents == nil {
		nc.sentEvents = make(map[id.EventID]struct{})
	}
	if _, ok := nc.sentEvents[eventID]; !ok {
		nc.sentEventOrder = append(nc.sentEventOrder, eventID)
	}
	nc.sentEvents[eventID] = struct{}{}
	nc.pruneSentEventsLocked()
}

func (nc *MyNetworkClient) consumeSentEvent(eventID id.EventID) bool {
	if eventID == "" {
		return false
	}
	nc.sentMu.Lock()
	defer nc.sentMu.Unlock()
	if nc.sentEvents == nil {
		return false
	}
	if _, ok := nc.sentEvents[eventID]; !ok {
		return false
	}
	delete(nc.sentEvents, eventID)
	return true
}

func (nc *MyNetworkClient) pruneSentEventsLocked() {
	for len(nc.sentEvents) > maxSentEventCacheSize && len(nc.sentEventOrder) > 0 {
		oldest := nc.sentEventOrder[0]
		nc.sentEventOrder = nc.sentEventOrder[1:]
		delete(nc.sentEvents, oldest)
	}
	if len(nc.sentEventOrder) <= maxSentEventCacheSize*2 {
		return
	}
	compacted := nc.sentEventOrder[:0]
	for _, eventID := range nc.sentEventOrder {
		if _, ok := nc.sentEvents[eventID]; ok {
			compacted = append(compacted, eventID)
		}
	}
	nc.sentEventOrder = compacted
}

func (nc *MyNetworkClient) LogoutRemote(ctx context.Context) {
	nc.Disconnect()
}

// IsThisUser checks if the given remote network user ID belongs to this client instance.
func (nc *MyNetworkClient) IsThisUser(ctx context.Context, userID networkid.UserID) bool {
	return string(userID) == string(nc.mx.UserID)
}

func (nc *MyNetworkClient) IsLoggedIn() bool {
	nc.connectMu.Lock()
	defer nc.connectMu.Unlock()
	return nc.loggedIn
}

func (nc *MyNetworkClient) syncRooms(ctx context.Context) {
	rooms, err := nc.mx.JoinedRooms(ctx)
	if err != nil {
		nc.log.Err(err).Msg("Failed to fetch joined remote Matrix rooms")
		return
	}
	for _, roomID := range rooms.JoinedRooms {
		info := nc.buildChatInfo(ctx, roomID)
		nc.connector.resyncRoom(ctx, nc.login, roomID, info)
		nc.backfillRecent(ctx, roomID)
	}
}

func (nc *MyNetworkClient) buildChatInfo(ctx context.Context, roomID id.RoomID) *bridgev2.ChatInfo {
	name := string(roomID)
	var nameContent event.RoomNameEventContent
	if err := nc.mx.StateEvent(ctx, roomID, event.StateRoomName, "", &nameContent); err == nil && nameContent.Name != "" {
		name = nameContent.Name
	}
	topic := ""
	var topicContent event.TopicEventContent
	if err := nc.mx.StateEvent(ctx, roomID, event.StateTopic, "", &topicContent); err == nil {
		topic = topicContent.Topic
	}
	var avatar *bridgev2.Avatar
	var avatarContent event.RoomAvatarEventContent
	if err := nc.mx.StateEvent(ctx, roomID, event.StateRoomAvatar, "", &avatarContent); err == nil {
		avatar = nc.avatarFromMXC(ctx, avatarContent.URL)
	}
	return &bridgev2.ChatInfo{
		Name:        &name,
		Topic:       &topic,
		Avatar:      avatar,
		CanBackfill: true,
		Members:     nc.buildMemberList(ctx, roomID),
	}
}

func (nc *MyNetworkClient) buildMemberList(ctx context.Context, roomID id.RoomID) *bridgev2.ChatMemberList {
	memberResp, err := nc.mx.JoinedMembers(ctx, roomID)
	if err != nil {
		nc.log.Warn().Err(err).Str("room_id", string(roomID)).Msg("Failed to fetch joined Matrix members")
		return &bridgev2.ChatMemberList{Members: []bridgev2.ChatMember{nc.ownChatMember()}}
	}
	members := make([]bridgev2.ChatMember, 0, len(memberResp.Joined))
	for userID, member := range memberResp.Joined {
		displayName := member.DisplayName
		if displayName == "" {
			displayName = userID.String()
		}
		isFromMe := userID == nc.mx.UserID
		chatMember := bridgev2.ChatMember{
			EventSender: bridgev2.EventSender{
				Sender:   networkid.UserID(userID),
				IsFromMe: isFromMe,
			},
			Membership: event.MembershipJoin,
			UserInfo: &bridgev2.UserInfo{
				Name:   &displayName,
				Avatar: nc.avatarFromMXC(ctx, id.ContentURIString(member.AvatarURL)),
			},
		}
		if isFromMe {
			chatMember.SenderLogin = nc.login.ID
		}
		members = append(members, chatMember)
	}
	return &bridgev2.ChatMemberList{
		IsFull:                     true,
		ExcludeChangesFromTimeline: true,
		TotalMemberCount:           len(members),
		Members:                    members,
	}
}

func (nc *MyNetworkClient) ownChatMember() bridgev2.ChatMember {
	return bridgev2.ChatMember{
		EventSender: bridgev2.EventSender{
			Sender:      networkid.UserID(nc.mx.UserID),
			SenderLogin: nc.login.ID,
			IsFromMe:    true,
		},
		Membership: event.MembershipJoin,
		UserInfo: &bridgev2.UserInfo{
			Name: &nc.login.RemoteName,
		},
	}
}

func (nc *MyNetworkClient) backfillRecent(ctx context.Context, roomID id.RoomID) {
	limit := envInt("LOCAL_MATRIX_INITIAL_BACKFILL_LIMIT", 0)
	if limit <= 0 {
		return
	}
	resp, err := nc.mx.Messages(ctx, roomID, "", "", mautrix.DirectionBackward, nil, limit)
	if err != nil {
		nc.log.Err(err).Str("room_id", string(roomID)).Msg("Failed to fetch recent messages")
		return
	}
	for i := len(resp.Chunk) - 1; i >= 0; i-- {
		nc.handleLocalMatrixEvent(ctx, resp.Chunk[i])
	}
}

func (nc *MyNetworkClient) avatarFromMXC(ctx context.Context, uri id.ContentURIString) *bridgev2.Avatar {
	if uri == "" || nc.mx == nil {
		return nil
	}
	nc.avatarMu.Lock()
	if _, failed := nc.badAvatars[uri]; failed {
		nc.avatarMu.Unlock()
		return nc.generatedFallbackAvatarFromMXC(uri)
	}
	nc.avatarMu.Unlock()
	parsed, err := uri.Parse()
	if err != nil {
		nc.markBadAvatar(uri, err)
		return nil
	}
	data, err := nc.downloadMatrixMedia(ctx, parsed)
	if err != nil {
		return nc.fallbackAvatarFromMXC(uri, err)
	}
	return &bridgev2.Avatar{
		ID: networkid.AvatarID(uri),
		Get: func(ctx context.Context) ([]byte, error) {
			return append([]byte(nil), data...), nil
		},
	}
}

func (nc *MyNetworkClient) fallbackAvatarFromMXC(uri id.ContentURIString, err error) *bridgev2.Avatar {
	nc.markBadAvatar(uri, err)
	return nc.generatedFallbackAvatarFromMXC(uri)
}

func (nc *MyNetworkClient) generatedFallbackAvatarFromMXC(uri id.ContentURIString) *bridgev2.Avatar {
	nc.avatarMu.Lock()
	if avatar, ok := nc.fallbackAvatars[uri]; ok {
		nc.avatarMu.Unlock()
		return avatar
	}
	nc.avatarMu.Unlock()

	data, genErr := generateFallbackAvatarPNG(string(uri))
	if genErr != nil {
		nc.log.Warn().
			Err(genErr).
			Str("mxc", string(uri)).
			Msg("Failed to generate fallback Matrix avatar")
		return nil
	}
	avatar := &bridgev2.Avatar{
		ID: networkid.AvatarID("fallback:" + string(uri)),
		Get: func(ctx context.Context) ([]byte, error) {
			return append([]byte(nil), data...), nil
		},
	}
	nc.avatarMu.Lock()
	if nc.fallbackAvatars == nil {
		nc.fallbackAvatars = make(map[id.ContentURIString]*bridgev2.Avatar)
	}
	nc.fallbackAvatars[uri] = avatar
	nc.avatarMu.Unlock()
	return avatar
}

func (nc *MyNetworkClient) markBadAvatar(uri id.ContentURIString, err error) {
	nc.avatarMu.Lock()
	if nc.badAvatars == nil {
		nc.badAvatars = make(map[id.ContentURIString]struct{})
	}
	_, alreadyLogged := nc.badAvatars[uri]
	nc.badAvatars[uri] = struct{}{}
	nc.avatarMu.Unlock()
	if !alreadyLogged {
		nc.log.Warn().
			Err(err).
			Str("mxc", string(uri)).
			Msg("Skipping Matrix avatar because the media could not be downloaded")
	}
}

func envInt(name string, fallback int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt64(name string, fallback int64) int64 {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}
