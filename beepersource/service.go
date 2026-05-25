package beepersource

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var ErrMatrixToBeeperDisabled = errors.New("matrix to beeper is disabled")

type BeeperAPI interface {
	Health(context.Context) error
	ListChats(context.Context) ([]Chat, error)
	ListMessages(ctx context.Context, chatID string, afterCursor string, limit int) ([]Message, string, error)
	ListMessagePage(ctx context.Context, chatID string, cursor string, direction string) (MessagePage, error)
	DownloadAsset(ctx context.Context, assetURL string) (*AssetStream, error)
	SendMessage(ctx context.Context, outbound BeeperOutbound) (string, error)
	UpdateMessage(ctx context.Context, chatID, messageID, text string) error
	DeleteMessage(ctx context.Context, chatID, messageID string, forEveryone bool) error
	AddReaction(ctx context.Context, chatID, messageID, reactionKey, txnID string) error
	RemoveReaction(ctx context.Context, chatID, messageID, reactionKey string) error
}

type HistoryBackfillResult struct {
	ChatsConsidered  int
	ChatsProcessed   int
	MessagesSeen     int
	MessagesMirrored int
	CompletedChats   int
	FailedChats      int
}

type MatrixSink interface {
	EnsurePortal(context.Context, Chat, *MatrixMedia) (string, error)
	EnsurePuppet(context.Context, Sender) (string, error)
	SendMessage(context.Context, MatrixOutbound) (string, error)
	EditMessage(context.Context, MatrixOutbound, string) (string, error)
	RedactMessage(context.Context, string, string, string, string) (string, error)
}

type MatrixPortalAccessChecker interface {
	PortalAccessible(context.Context, string) (bool, error)
}

type MatrixSpaceOrganizer interface {
	EnsurePortalSpaces(context.Context, []Chat) error
}

type Service struct {
	cfg    Config
	store  *Store
	api    BeeperAPI
	matrix MatrixSink
}

func NewService(cfg Config, store *Store, api BeeperAPI, matrix MatrixSink) *Service {
	return &Service{cfg: cfg, store: store, api: api, matrix: matrix}
}

func (s *Service) ReconcileOnce(ctx context.Context) error {
	if err := s.api.Health(ctx); err != nil {
		return err
	}
	chats, err := s.api.ListChats(ctx)
	if err != nil {
		return err
	}
	for _, chat := range chats {
		if !s.cfg.AllowsBeeperChatRecord(chat) {
			continue
		}
		roomID, err := s.ensurePortal(ctx, chat)
		if err != nil {
			return err
		}
		cursor, err := s.store.PortalCursor(ctx, chat.ID)
		if err != nil {
			return err
		}
		messages, nextCursor, err := s.api.ListMessages(ctx, chat.ID, cursor, 100)
		if err != nil {
			return fmt.Errorf("list Beeper messages for %s: %w", chat.ID, err)
		}
		for _, msg := range messages {
			if err := s.mirrorMessage(ctx, roomID, chat, msg); err != nil {
				return err
			}
		}
		if err := s.store.UpsertPortal(ctx, chat, roomID, nextCursor); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) BackfillHistoryOnce(ctx context.Context, chatLimit int) (HistoryBackfillResult, error) {
	var result HistoryBackfillResult
	if chatLimit <= 0 {
		chatLimit = 1
	}
	if err := s.api.Health(ctx); err != nil {
		return result, err
	}
	chats, err := s.api.ListChats(ctx)
	if err != nil {
		return result, err
	}
	var firstErr error
	for _, chat := range chats {
		if !s.cfg.AllowsBeeperChatRecord(chat) {
			continue
		}
		result.ChatsConsidered++
		done, err := s.store.PortalBackfillDone(ctx, chat.ID)
		if err != nil {
			return result, err
		}
		if done {
			continue
		}
		if result.ChatsProcessed >= chatLimit {
			continue
		}
		if err := s.backfillChatHistoryPage(ctx, chat, &result); err != nil {
			result.FailedChats++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		result.ChatsProcessed++
	}
	return result, firstErr
}

func (s *Service) backfillChatHistoryPage(ctx context.Context, chat Chat, result *HistoryBackfillResult) error {
	roomID, err := s.ensurePortal(ctx, chat)
	if err != nil {
		return err
	}
	cursor, done, err := s.store.PortalBackfillCursor(ctx, chat.ID)
	if err != nil {
		return err
	}
	if done {
		return nil
	}
	page, err := s.api.ListMessagePage(ctx, chat.ID, cursor, "before")
	if err != nil {
		return fmt.Errorf("list older Beeper messages for %s: %w", chat.ID, err)
	}
	result.MessagesSeen += len(page.Messages)
	for _, msg := range page.Messages {
		if err := s.mirrorMessage(ctx, roomID, chat, msg); err != nil {
			return err
		}
		result.MessagesMirrored++
	}
	nextCursor := page.OldestCursor
	completed := len(page.Messages) == 0 || !page.HasMore || nextCursor == ""
	if err := s.store.SetPortalBackfillState(ctx, chat.ID, nextCursor, completed); err != nil {
		return err
	}
	if completed {
		result.CompletedChats++
	}
	return nil
}

func (s *Service) ReconcilePortalsOnly(ctx context.Context) error {
	if err := s.api.Health(ctx); err != nil {
		return err
	}
	chats, err := s.api.ListChats(ctx)
	if err != nil {
		return err
	}
	allowedChats := make([]Chat, 0, len(chats))
	toEnsure := make([]Chat, 0, len(chats))
	for _, chat := range chats {
		if !s.cfg.AllowsBeeperChatRecord(chat) {
			continue
		}
		allowedChats = append(allowedChats, chat)
		if roomID, ok, err := s.store.PortalRoomID(ctx, chat.ID); err != nil {
			return err
		} else if ok {
			if s.cfg.Sync.PortalCheckAccess {
				checker, ok := s.matrix.(MatrixPortalAccessChecker)
				if !ok {
					toEnsure = append(toEnsure, chat)
					continue
				}
				accessible, err := checker.PortalAccessible(ctx, roomID)
				if err != nil {
					toEnsure = append(toEnsure, chat)
					continue
				}
				if !accessible {
					if err := s.store.DeletePortal(ctx, chat.ID); err != nil {
						return err
					}
				}
			}
			toEnsure = append(toEnsure, chat)
			continue
		}
		toEnsure = append(toEnsure, chat)
	}
	if len(toEnsure) > 0 {
		workers := minPositive(s.cfg.Sync.PortalWorkers, len(toEnsure))
		backpressure := newPortalBackpressure(workers)
		jobs := make(chan Chat)
		errs := make(chan error, len(toEnsure))
		var wg sync.WaitGroup
		timeout := time.Duration(s.cfg.Sync.PortalTimeoutSeconds) * time.Second
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for chat := range jobs {
					chatCtx, chatCancel := context.WithTimeout(ctx, timeout)
					err := s.ensurePortalWithBackoff(chatCtx, chat, backpressure)
					chatCancel()
					if err != nil {
						errs <- err
					}
				}
			}()
		}
		for _, chat := range toEnsure {
			select {
			case jobs <- chat:
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				return ctx.Err()
			}
		}
		close(jobs)
		wg.Wait()
		close(errs)
		var firstErr error
		failures := 0
		for err := range errs {
			failures++
			if firstErr == nil {
				firstErr = err
			}
		}
		if firstErr != nil {
			return fmt.Errorf("%d portal room(s) failed and can be retried by rerunning rooms-only: %w", failures, firstErr)
		}
	}
	if s.cfg.Matrix.Spaces {
		if organizer, ok := s.matrix.(MatrixSpaceOrganizer); ok {
			if err := organizer.EnsurePortalSpaces(ctx, allowedChats); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) ensurePortalWithBackoff(ctx context.Context, chat Chat, backpressure *portalBackpressure) error {
	attempt := 0
	for {
		release, err := backpressure.beforeAttempt(ctx)
		if err != nil {
			return err
		}
		_, err = s.ensurePortal(ctx, chat)
		if err == nil {
			release(true)
			return nil
		}
		var rateErr *MatrixRateLimitError
		if !errors.As(err, &rateErr) {
			release(false)
			return err
		}
		attempt++
		retryAfter := rateErr.RetryAfter
		if retryAfter <= 0 {
			retryAfter = adaptivePortalRetryDelay(attempt)
		}
		backpressure.noteRateLimit(retryAfter)
		release(false)
	}
}

type portalBackpressure struct {
	mu       sync.Mutex
	active   int
	limit    int
	maxLimit int
	resumeAt time.Time
}

func newPortalBackpressure(workers int) *portalBackpressure {
	workers = maxPositive(workers, 1)
	return &portalBackpressure{limit: workers, maxLimit: workers}
}

func (p *portalBackpressure) beforeAttempt(ctx context.Context) (func(bool), error) {
	for {
		p.mu.Lock()
		now := time.Now()
		if p.active < p.limit && !now.Before(p.resumeAt) {
			p.active++
			p.mu.Unlock()
			return func(success bool) {
				p.mu.Lock()
				if p.active > 0 {
					p.active--
				}
				if success && p.limit < p.maxLimit {
					p.limit++
				}
				p.mu.Unlock()
			}, nil
		}
		wait := 10 * time.Millisecond
		if now.Before(p.resumeAt) {
			wait = time.Until(p.resumeAt)
		}
		p.mu.Unlock()
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil, ctx.Err()
		}
	}
}

func (p *portalBackpressure) noteRateLimit(retryAfter time.Duration) {
	if retryAfter <= 0 {
		retryAfter = adaptivePortalRetryDelay(1)
	}
	p.mu.Lock()
	if p.limit > 1 {
		p.limit = maxPositive(1, p.limit/2)
	}
	next := time.Now().Add(retryAfter)
	if next.After(p.resumeAt) {
		p.resumeAt = next
	}
	p.mu.Unlock()
}

func adaptivePortalRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 6 {
		attempt = 6
	}
	delay := time.Duration(250*(1<<(attempt-1))) * time.Millisecond
	if delay > 5*time.Second {
		return 5 * time.Second
	}
	return delay
}

func minPositive(a, b int) int {
	if a <= 0 || (b > 0 && b < a) {
		return b
	}
	return a
}

func maxPositive(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *Service) ensurePortal(ctx context.Context, chat Chat) (string, error) {
	avatar, err := s.portalAvatar(ctx, chat)
	if err != nil {
		avatar = platformAvatarMedia(chat)
	}
	roomID, err := s.matrix.EnsurePortal(ctx, chat, avatar)
	if avatar != nil && avatar.Close != nil {
		_ = avatar.Close()
	}
	if err != nil {
		return "", fmt.Errorf("ensure Matrix portal for %s: %w", chat.ID, err)
	}
	cursor, err := s.store.PortalCursor(ctx, chat.ID)
	if err != nil {
		return "", err
	}
	if err := s.store.UpsertPortal(ctx, chat, roomID, cursor); err != nil {
		return "", err
	}
	if avatar != nil {
		syncValue := s.portalAvatarSyncValue(chat)
		if avatar.AssetID != "" {
			syncValue = avatar.AssetID
		}
		if err := s.store.SetValue(ctx, portalAvatarSyncKey(chat.ID), syncValue); err != nil {
			return "", err
		}
	}
	return roomID, nil
}

func (s *Service) portalAvatar(ctx context.Context, chat Chat) (*MatrixMedia, error) {
	if avatar, ok, err := s.contactOverrideAvatarForChat(chat); ok || err != nil {
		if err != nil {
			return avatar, err
		}
		return s.badgePortalAvatar(chat, avatar)
	}
	avatarURL, platformFallback := s.portalAvatarSource(chat)
	expectedSyncValue := s.portalAvatarSyncValue(chat)
	lastAvatar, err := s.store.GetValue(ctx, portalAvatarSyncKey(chat.ID))
	if err != nil {
		return nil, err
	}
	if platformFallback {
		if !s.cfg.Matrix.ForceAvatarSync && lastAvatar == expectedSyncValue {
			return nil, nil
		}
		return generatedContactAvatarMediaWithOptions(chat, s.avatarBadgeOptions()), nil
	}
	if avatar, ok, err := localAvatarMedia(avatarURL); ok || err != nil {
		if !s.cfg.Matrix.ForceAvatarSync && lastAvatar == expectedSyncValue {
			if avatar != nil && avatar.Close != nil {
				_ = avatar.Close()
			}
			return nil, err
		}
		if err != nil {
			return avatar, err
		}
		if avatar != nil {
			avatar.AssetID = avatarURL
		}
		return s.badgePortalAvatar(chat, avatar)
	}
	asset, err := s.api.DownloadAsset(ctx, avatarURL)
	if err != nil {
		return generatedContactAvatarMediaWithOptions(chat, s.avatarBadgeOptions()), nil
	}
	body, err := io.ReadAll(asset.Content)
	_ = asset.Content.Close()
	if err != nil {
		return generatedContactAvatarMediaWithOptions(chat, s.avatarBadgeOptions()), nil
	}
	fileName := firstNonEmpty(asset.FileName, "beeper-avatar")
	mimeType := firstNonEmpty(asset.MimeType, "application/octet-stream")
	return s.badgePortalAvatar(chat, &MatrixMedia{
		AssetID:     avatarURL,
		ContentHash: fmt.Sprintf("%x", sha256.Sum256(body)),
		Content:     bytes.NewReader(body),
		FileName:    fileName,
		MimeType:    mimeType,
		SizeBytes:   int64(len(body)),
	})
}

func (s *Service) portalAvatarSyncValue(chat Chat) string {
	avatarURL, platformFallback := s.portalAvatarSource(chat)
	if platformFallback {
		return generatedContactAvatarAssetIDWithOptions(chat, s.avatarBadgeOptions())
	}
	if !s.cfg.Matrix.AvatarBadges {
		return avatarURL
	}
	return badgedAvatarAssetIDWithOptions(chat, avatarURL, s.avatarBadgeOptions())
}

func (s *Service) portalAvatarSource(chat Chat) (string, bool) {
	if s.cfg.Matrix.PlatformAvatars {
		return platformAvatarSyncValue(chat), true
	}
	if s.cfg.Matrix.DMParticipantAvatars && !chat.IsGroup {
		if avatarURL := firstParticipantAvatarURL(chat); avatarURL != "" {
			return s.resolveBeeperAssetURL(avatarURL), false
		}
	}
	if avatarURL := strings.TrimSpace(chat.AvatarURL); avatarURL != "" {
		return s.resolveBeeperAssetURL(avatarURL), false
	}
	return platformAvatarSyncValue(chat), true
}

func firstParticipantAvatarURL(chat Chat) string {
	for _, participant := range chat.Participants {
		if avatarURL := strings.TrimSpace(participant.AvatarID); avatarURL != "" {
			return avatarURL
		}
	}
	return ""
}

func (s *Service) resolveBeeperAssetURL(rawURL string) string {
	return resolveBeeperAssetURL(s.cfg.Beeper.BaseURL, rawURL)
}

func resolveBeeperAssetURL(baseURL, rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err == nil && parsed.IsAbs() {
		return rawURL
	}
	if err != nil || !strings.HasPrefix(rawURL, "/") {
		return rawURL
	}
	if filepath.IsAbs(rawURL) && !looksLikeBeeperHTTPAssetPath(rawURL) {
		return rawURL
	}
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || !base.IsAbs() {
		return rawURL
	}
	return base.ResolveReference(parsed).String()
}

func looksLikeBeeperHTTPAssetPath(rawURL string) bool {
	return strings.HasPrefix(rawURL, "/_matrix/") ||
		strings.HasPrefix(rawURL, "/v1/") ||
		strings.HasPrefix(rawURL, "/media/") ||
		strings.HasPrefix(rawURL, "/assets/")
}

func platformAvatarMedia(chat Chat) *MatrixMedia {
	platform := PlatformDisplayName(chat)
	if icon, ok := brandIconByKey(platform); ok {
		if pngBytes, ok := brandIconPNGByKey(icon.Key); ok {
			return &MatrixMedia{
				AssetID:   platformAvatarSyncValue(chat),
				Content:   bytes.NewReader(pngBytes),
				FileName:  strings.ToLower(strings.ReplaceAll(platform, " ", "-")) + "-bridge.png",
				MimeType:  "image/png",
				SizeBytes: int64(len(pngBytes)),
			}
		}
	}
	if pngBytes, ok := platformLogoPNG(platform, PlatformColor(chat)); ok {
		return &MatrixMedia{
			AssetID:   platformAvatarSyncValue(chat),
			Content:   bytes.NewReader(pngBytes),
			FileName:  strings.ToLower(strings.ReplaceAll(platform, " ", "-")) + "-bridge.png",
			MimeType:  "image/png",
			SizeBytes: int64(len(pngBytes)),
		}
	}
	svg := platformLogoSVG(platform, PlatformColor(chat))
	return &MatrixMedia{
		AssetID:   platformAvatarSyncValue(chat),
		Content:   bytes.NewReader([]byte(svg)),
		FileName:  strings.ToLower(strings.ReplaceAll(platform, " ", "-")) + "-bridge.svg",
		MimeType:  "image/svg+xml",
		SizeBytes: int64(len(svg)),
	}
}

func platformLogoSVG(platform string, bg string) string {
	name := strings.TrimSpace(platform)
	switch strings.ToLower(name) {
	case "whatsapp":
		return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" data-platform="%s" width="256" height="256" viewBox="0 0 256 256"><rect width="256" height="256" rx="56" fill="%s"/><path fill="#fff" d="M128 50c-41.4 0-75 31.8-75 71 0 13.5 4 26.1 10.9 36.9L55 206l49.9-12.8A78.8 78.8 0 0 0 128 196c41.4 0 75-31.8 75-71s-33.6-75-75-75z"/><path fill="%s" d="M128 65c32.5 0 59 24.8 59 55.5S160.5 176 128 176c-7.9 0-15.4-1.5-22.3-4.2l-5.2-2.1-23 5.9 4.2-22.3-3.2-4.8a50 50 0 0 1-9.5-28C69 89.8 95.5 65 128 65z"/><path fill="#fff" d="M104 92c-3.9 0-10 8.8-10 16.7 0 19.8 23.8 49 48.8 55.5 9.1 2.4 17.2-6.4 19.2-12.1 1-3.1.5-5.4-1.6-6.5l-18.2-8.6c-2.1-1-4.5-.5-5.9 1.4l-5.4 7.1c-1.2 1.6-3.4 2.1-5.1 1.1-9.7-5.6-17-12.5-22.2-22-1-1.8-.5-4 1.2-5.2l6.6-4.9c1.8-1.3 2.4-3.8 1.4-5.8l-8-15c-.7-1.1-.5-1.7-.8-1.7z"/></svg>`, html.EscapeString(name), bg, bg)
	case "signal":
		return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" data-platform="%s" width="256" height="256" viewBox="0 0 256 256"><rect width="256" height="256" rx="56" fill="%s"/><circle cx="128" cy="128" r="62" fill="none" stroke="#fff" stroke-width="18" stroke-dasharray="20 13"/><path fill="#fff" d="M91 118c0-20.4 16.6-37 37-37s37 16.6 37 37-16.6 37-37 37h-21l-27 20 7-29a36.9 36.9 0 0 1 4-28z"/></svg>`, html.EscapeString(name), bg)
	case "telegram":
		return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" data-platform="%s" width="256" height="256" viewBox="0 0 256 256"><rect width="256" height="256" rx="56" fill="%s"/><path fill="#fff" d="M205 67 176 199c-2 9-8 11-16 7l-45-33-22 21c-2 2-4 4-9 4l3-47 86-78c4-3-1-5-6-2L61 138l-46-15c-10-3-10-10 2-14L196 40c8-3 15 2 9 27z"/></svg>`, html.EscapeString(name), bg)
	case "discord":
		return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" data-platform="%s" width="256" height="256" viewBox="0 0 256 256"><rect width="256" height="256" rx="56" fill="%s"/><path fill="#fff" d="M88 82c25-11 55-11 80 0 15 21 22 44 20 68-22 17-43 24-64 24s-42-7-64-24c-2-24 5-47 20-68zm26 57a12 12 0 1 0 0-24 12 12 0 0 0 0 24zm56 0a12 12 0 1 0 0-24 12 12 0 0 0 0 24zm-62 27c13 6 27 6 40 0"/></svg>`, html.EscapeString(name), bg)
	case "slack":
		return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" data-platform="%s" width="256" height="256" viewBox="0 0 256 256"><rect width="256" height="256" rx="56" fill="%s"/><path fill="#fff" d="M86 58a18 18 0 0 1 36 0v48H86V58zm48 0a18 18 0 0 1 36 0v48h-36V58zM58 86h48v36H58a18 18 0 0 1 0-36zm0 48h48v36H58a18 18 0 0 1 0-36zm28 16h36v48a18 18 0 0 1-36 0v-48zm48 0h36v48a18 18 0 0 1-36 0v-48zm16-64h48a18 18 0 0 1 0 36h-48V86zm0 48h48a18 18 0 0 1 0 36h-48v-36z"/></svg>`, html.EscapeString(name), bg)
	case "instagram":
		return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" data-platform="%s" width="256" height="256" viewBox="0 0 256 256"><rect width="256" height="256" rx="56" fill="%s"/><rect x="70" y="70" width="116" height="116" rx="30" fill="none" stroke="#fff" stroke-width="16"/><circle cx="128" cy="128" r="28" fill="none" stroke="#fff" stroke-width="16"/><circle cx="163" cy="93" r="10" fill="#fff"/></svg>`, html.EscapeString(name), bg)
	case "messenger":
		return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" data-platform="%s" width="256" height="256" viewBox="0 0 256 256"><rect width="256" height="256" rx="56" fill="%s"/><path fill="#fff" d="M128 60c-42 0-76 30-76 67 0 21 11 40 29 52v29l27-15c7 1 13 2 20 2 42 0 76-30 76-68s-34-67-76-67zm-8 90-20-21-39 21 44-47 21 21 38-21-44 47z"/></svg>`, html.EscapeString(name), bg)
	case "imessage":
		return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" data-platform="%s" width="256" height="256" viewBox="0 0 256 256"><rect width="256" height="256" rx="56" fill="%s"/><path fill="#fff" d="M128 61c42 0 76 28 76 63s-34 63-76 63c-8 0-16-1-24-3l-39 20 12-35c-16-12-25-28-25-45 0-35 34-63 76-63z"/></svg>`, html.EscapeString(name), bg)
	case "x":
		return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" data-platform="%s" width="256" height="256" viewBox="0 0 256 256"><rect width="256" height="256" rx="56" fill="%s"/><path fill="#fff" d="M73 62h34l29 41 36-41h25l-50 57 55 75h-34l-34-47-41 47H68l55-63L73 62zm25 18 79 96h-13L85 80h13z"/></svg>`, html.EscapeString(name), bg)
	case "linkedin":
		return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" data-platform="%s" width="256" height="256" viewBox="0 0 256 256"><rect width="256" height="256" rx="56" fill="%s"/><rect x="67" y="103" width="28" height="86" fill="#fff"/><circle cx="81" cy="76" r="16" fill="#fff"/><path fill="#fff" d="M115 103h27v12c6-9 16-15 30-15 29 0 34 20 34 46v43h-28v-39c0-18-4-27-17-27-13 0-18 9-18 27v39h-28v-86z"/></svg>`, html.EscapeString(name), bg)
	case "matrix", "beeper", "beeper (matrix)", "bridgev2":
		return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" data-platform="%s" width="256" height="256" viewBox="0 0 256 256"><rect width="256" height="256" rx="56" fill="%s"/><path fill="#fff" d="M72 60h18v136H72V60zm94 0h18v136h-18V60zM105 86h18v25h10V86h18v84h-18v-42h-10v42h-18V86z"/></svg>`, html.EscapeString(name), bg)
	default:
		initials := PlatformInitials(name)
		return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" data-platform="%s" width="256" height="256" viewBox="0 0 256 256"><rect width="256" height="256" rx="56" fill="%s"/><text x="128" y="148" text-anchor="middle" font-family="Arial, Helvetica, sans-serif" font-size="72" font-weight="700" fill="#ffffff">%s</text></svg>`, html.EscapeString(name), bg, html.EscapeString(initials))
	}
}

func platformAvatarSyncValue(chat Chat) string {
	return "platform-logo-v5:" + PlatformDisplayName(chat)
}

func accountSpaceAvatarMedia(chat Chat) *MatrixMedia {
	spaceChat := Chat{
		ID:        "account-space:" + accountSpaceKey(chat),
		AccountID: chat.AccountID,
		Network:   PlatformDisplayName(chat),
		Name:      accountSpaceDisplayName(chat),
	}
	return generatedContactAvatarMediaWithOptions(spaceChat, defaultAvatarBadgeOptions())
}

func accountSpaceKey(chat Chat) string {
	key := strings.TrimSpace(chat.AccountID)
	if key == "" {
		key = PlatformDisplayName(chat)
	}
	return brandIconKey(PlatformDisplayName(chat)) + ":" + sanitizeSpaceKey(key)
}

func accountSpaceDisplayName(chat Chat) string {
	platform := PlatformDisplayName(chat)
	login := accountLoginLabel(chat.AccountID, platform)
	if login == "" || strings.EqualFold(login, platform) {
		return platform + " Login"
	}
	return platform + " · " + login
}

func accountSpaceTopic(chat Chat) string {
	return "Beeper " + PlatformDisplayName(chat) + " login/channel " + accountLoginLabel(chat.AccountID, PlatformDisplayName(chat))
}

func accountLoginLabel(accountID string, platform string) string {
	label := strings.TrimSpace(accountID)
	if label == "" {
		return ""
	}
	label = strings.TrimPrefix(label, "local-")
	prefix := strings.ToLower(platform)
	prefix = strings.ReplaceAll(prefix, " ", "")
	prefix = strings.ReplaceAll(prefix, ".", "")
	normalized := strings.ToLower(label)
	normalized = strings.ReplaceAll(normalized, "-", "")
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, ".", "")
	if strings.HasPrefix(normalized, prefix) {
		for _, sep := range []string{"_", "-", "."} {
			if idx := strings.Index(label, sep); idx >= 0 && idx+1 < len(label) {
				label = label[idx+1:]
				break
			}
		}
	}
	label = strings.ReplaceAll(label, "_", " ")
	label = strings.ReplaceAll(label, "-", " ")
	label = strings.TrimSpace(label)
	if label == "" {
		return platform
	}
	return titleAccount(label)
}

func sanitizeSpaceKey(raw string) string {
	key := strings.ToLower(strings.TrimSpace(raw))
	key = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, key)
	key = strings.Trim(key, "-_.")
	if key == "" {
		return "default"
	}
	return key
}

func localAvatarMedia(rawPath string) (*MatrixMedia, bool, error) {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return nil, false, nil
	}
	if strings.HasPrefix(path, "file://") {
		parsed, err := url.Parse(path)
		if err != nil {
			return nil, true, err
		}
		path = parsed.Path
	}
	if decoded, err := url.PathUnescape(path); err == nil {
		path = decoded
	}
	if strings.Contains(path, "://") {
		return nil, false, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, true, err
	}
	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, true, err
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		_ = file.Close()
		return nil, true, err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, true, err
	}
	mimeType := mime.TypeByExtension(filepath.Ext(path))
	if mimeType == "" {
		head := make([]byte, 512)
		n, _ := file.Read(head)
		mimeType = http.DetectContentType(head[:n])
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			_ = file.Close()
			return nil, true, err
		}
	}
	return &MatrixMedia{
		ContentHash: fmt.Sprintf("%x", hasher.Sum(nil)),
		Content:     file,
		Close:       file.Close,
		FileName:    filepath.Base(path),
		MimeType:    mimeType,
		SizeBytes:   stat.Size(),
	}, true, nil
}

func senderForMessage(chat Chat, msg Message) Sender {
	sender := Sender{
		ID:          msg.SenderID,
		DisplayName: msg.SenderName,
	}
	for _, participant := range chat.Participants {
		if participant.ID != msg.SenderID {
			continue
		}
		if sender.DisplayName == "" {
			sender.DisplayName = participant.DisplayName
		}
		sender.AvatarID = participant.AvatarID
		break
	}
	return sender
}

func (s *Service) senderAvatarMedia(ctx context.Context, sender Sender) (*MatrixMedia, error) {
	if avatar, ok, err := s.contactOverrideAvatarForSender(sender); ok || err != nil {
		return avatar, err
	}
	avatarURL := s.resolveBeeperAssetURL(sender.AvatarID)
	if avatarURL == "" {
		return nil, nil
	}
	if cached, ok, err := s.store.MediaByAssetID(ctx, avatarURL); err != nil {
		return nil, err
	} else if ok {
		return &MatrixMedia{
			AssetID:   avatarURL,
			CachedMXC: cached.CachedMXC,
			FileName:  "beeper-sender-avatar",
			MimeType:  cached.MimeType,
			SizeBytes: cached.SizeBytes,
			Close:     func() error { return nil },
		}, nil
	}
	if avatar, ok, err := localAvatarMedia(avatarURL); ok || err != nil {
		if avatar != nil {
			avatar.AssetID = avatarURL
		}
		return avatar, err
	}
	asset, err := s.api.DownloadAsset(ctx, avatarURL)
	if err != nil {
		return nil, err
	}
	fileName := firstNonEmpty(asset.FileName, "beeper-sender-avatar")
	mimeType := firstNonEmpty(asset.MimeType, mime.TypeByExtension(filepath.Ext(fileName)), "application/octet-stream")
	return &MatrixMedia{
		AssetID:   avatarURL,
		Content:   asset.Content,
		Close:     asset.Content.Close,
		FileName:  fileName,
		MimeType:  mimeType,
		SizeBytes: asset.SizeBytes,
	}, nil
}

func portalAvatarSyncKey(chatID string) string {
	return "portal_avatar:" + chatID
}

func (s *Service) mirrorMessage(ctx context.Context, roomID string, chat Chat, msg Message) error {
	if err := s.store.UpsertBeeperMessageRaw(ctx, msg); err != nil {
		return err
	}
	sender := senderForMessage(chat, msg)
	existing, ok, err := s.store.MessageByBeeperID(ctx, msg.ID)
	if err != nil {
		return err
	}
	version := MessageVersion(msg)
	if ok {
		if existing.Version == version {
			return s.mirrorAdditionalAttachments(ctx, roomID, msg, sender, existing.MatrixEventID, version)
		}
		if msg.IsDeleted {
			txnID := DeterministicTxnID(msg.ChatID, msg.ID, MutationDelete, version)
			if _, err := s.matrix.RedactMessage(ctx, roomID, existing.MatrixEventID, txnID, "deleted in Beeper source"); err != nil {
				return err
			}
			now := time.Now().UTC()
			existing.Version = version
			existing.DeletedAt = &now
			if err := s.store.UpsertMessageMapping(ctx, existing); err != nil {
				return err
			}
			return s.redactAdditionalAttachments(ctx, roomID, msg, version)
		}
		edit := s.matrixOutboundForMessage(roomID, msg, sender, version)
		if avatar, err := s.senderAvatarMedia(ctx, sender); err == nil && avatar != nil {
			defer avatar.Close()
			edit.SenderAvatar = avatar
		}
		edit.TransactionID = DeterministicTxnID(msg.ChatID, msg.ID, MutationEdit, version)
		if media, err := s.matrixMedia(ctx, msg); err != nil {
			edit.MsgType = "m.notice"
			edit.Body = fmt.Sprintf("Unsupported or unavailable Beeper media: %s", err)
		} else if media != nil {
			defer media.Close()
			edit.Media = media
		}
		if edit.ReplyToEvent == "" && msg.LinkedMessageID != "" {
			replyTo, ok, err := s.store.MessageByBeeperID(ctx, msg.LinkedMessageID)
			if err != nil {
				return err
			}
			if ok {
				edit.ReplyToEvent = replyTo.MatrixEventID
			}
		}
		if _, err := s.matrix.EditMessage(ctx, edit, existing.MatrixEventID); err != nil {
			if edit.Media != nil {
				edit.Media = nil
				edit.MsgType = "m.notice"
				edit.Body = fmt.Sprintf("Beeper media could not be mirrored to Matrix: %v", err)
				if _, retryErr := s.matrix.EditMessage(ctx, edit, existing.MatrixEventID); retryErr == nil {
					err = nil
				} else {
					err = retryErr
				}
			}
			if err != nil {
				return err
			}
		}
		existing.Version = version
		existing.DeletedAt = nil
		if err := s.store.UpsertMessageMapping(ctx, existing); err != nil {
			return err
		}
		return s.mirrorAdditionalAttachments(ctx, roomID, msg, sender, existing.MatrixEventID, version)
	}
	body := messageBody(msg)
	if matrixEventID, ok, err := s.store.ConsumeOutboundEcho(ctx, msg.ChatID, body); err != nil {
		return err
	} else if ok {
		return s.store.UpsertMessageMapping(ctx, MessageMapping{
			BeeperMessageID: msg.ID,
			MatrixEventID:   matrixEventID,
			ChatID:          msg.ChatID,
			Version:         version,
		})
	}
	senderMXID, err := s.matrix.EnsurePuppet(ctx, sender)
	if err != nil {
		return err
	}
	outbound := s.matrixOutboundForMessage(roomID, msg, sender, version)
	outbound.SenderMXID = senderMXID
	if avatar, err := s.senderAvatarMedia(ctx, sender); err == nil && avatar != nil {
		defer avatar.Close()
		outbound.SenderAvatar = avatar
	}
	if msg.LinkedMessageID != "" {
		replyTo, ok, err := s.store.MessageByBeeperID(ctx, msg.LinkedMessageID)
		if err != nil {
			return err
		}
		if ok {
			outbound.ReplyToEvent = replyTo.MatrixEventID
		}
	}
	if media, err := s.matrixMedia(ctx, msg); err != nil {
		outbound.MsgType = "m.notice"
		outbound.Body = fmt.Sprintf("Unsupported or unavailable Beeper media: %s", err)
	} else if media != nil {
		defer media.Close()
		outbound.Media = media
	}
	eventID, err := s.matrix.SendMessage(ctx, outbound)
	if err != nil && outbound.Media != nil {
		outbound.Media = nil
		outbound.MsgType = "m.notice"
		outbound.Body = fmt.Sprintf("Beeper media could not be mirrored to Matrix: %v", err)
		eventID, err = s.matrix.SendMessage(ctx, outbound)
	}
	if err != nil {
		return err
	}
	if err := s.store.UpsertMessageMapping(ctx, MessageMapping{
		BeeperMessageID: msg.ID,
		MatrixEventID:   eventID,
		ChatID:          msg.ChatID,
		Version:         version,
	}); err != nil {
		return err
	}
	return s.mirrorAdditionalAttachments(ctx, roomID, msg, sender, eventID, version)
}

func (s *Service) matrixOutboundForMessage(roomID string, msg Message, sender Sender, version string) MatrixOutbound {
	return MatrixOutbound{
		RoomID:        roomID,
		MessageID:     msg.ID,
		AccountID:     msg.AccountID,
		ChatID:        msg.ChatID,
		SenderID:      sender.ID,
		SenderName:    sender.DisplayName,
		SortKey:       msg.SortKey,
		Body:          messageBody(msg),
		HTML:          msg.HTML,
		MsgType:       matrixMsgType(msg),
		Timestamp:     msg.Timestamp,
		TransactionID: DeterministicTxnID(msg.ChatID, msg.ID, MutationMessage, version),
		IsHidden:      msg.IsHidden,
		IsSender:      msg.IsSender,
		IsUnread:      msg.IsUnread,
		Mentions:      append([]string(nil), msg.Mentions...),
	}
}

func (s *Service) mirrorAdditionalAttachments(ctx context.Context, roomID string, msg Message, sender Sender, primaryEventID string, version string) error {
	if len(msg.Attachments) <= 1 {
		return nil
	}
	senderMXID, err := s.matrix.EnsurePuppet(ctx, sender)
	if err != nil {
		return err
	}
	for index := 1; index < len(msg.Attachments); index++ {
		mappingID := attachmentMessageID(msg, index)
		existing, exists, err := s.store.MessageByBeeperID(ctx, mappingID)
		if err != nil {
			return err
		}
		if exists && existing.Version == version {
			continue
		}
		att := msg.Attachments[index]
		outbound := MatrixOutbound{
			RoomID:        roomID,
			MessageID:     mappingID,
			AccountID:     msg.AccountID,
			ChatID:        msg.ChatID,
			SenderID:      sender.ID,
			SenderName:    sender.DisplayName,
			SenderMXID:    senderMXID,
			SortKey:       msg.SortKey,
			Body:          firstNonEmpty(att.FileName, msg.Text, "Beeper attachment"),
			MsgType:       matrixMsgTypeForAttachment(att, msg.Type),
			Timestamp:     msg.Timestamp,
			ReplyToEvent:  primaryEventID,
			TransactionID: DeterministicTxnID(msg.ChatID, mappingID, MutationMessage, version),
			IsHidden:      msg.IsHidden,
			IsSender:      msg.IsSender,
			IsUnread:      msg.IsUnread,
			Mentions:      append([]string(nil), msg.Mentions...),
			AttachmentID:  att.ID,
			AttachmentIdx: index,
		}
		if avatar, err := s.senderAvatarMedia(ctx, sender); err == nil && avatar != nil {
			defer avatar.Close()
			outbound.SenderAvatar = avatar
		}
		if media, err := s.matrixMediaForAttachment(ctx, att); err != nil {
			outbound.MsgType = "m.notice"
			outbound.Body = fmt.Sprintf("Unsupported or unavailable Beeper media: %s", err)
		} else if media != nil {
			defer media.Close()
			outbound.Media = media
		}
		if exists {
			outbound.TransactionID = DeterministicTxnID(msg.ChatID, mappingID, MutationEdit, version)
			if _, err := s.matrix.EditMessage(ctx, outbound, existing.MatrixEventID); err != nil {
				if outbound.Media != nil {
					outbound.Media = nil
					outbound.MsgType = "m.notice"
					outbound.Body = fmt.Sprintf("Beeper media could not be mirrored to Matrix: %v", err)
					if _, retryErr := s.matrix.EditMessage(ctx, outbound, existing.MatrixEventID); retryErr == nil {
						err = nil
					} else {
						err = retryErr
					}
				}
				if err != nil {
					return err
				}
			}
			existing.Version = version
			existing.DeletedAt = nil
			if err := s.store.UpsertMessageMapping(ctx, existing); err != nil {
				return err
			}
			continue
		}
		eventID, err := s.matrix.SendMessage(ctx, outbound)
		if err != nil && outbound.Media != nil {
			outbound.Media = nil
			outbound.MsgType = "m.notice"
			outbound.Body = fmt.Sprintf("Beeper media could not be mirrored to Matrix: %v", err)
			eventID, err = s.matrix.SendMessage(ctx, outbound)
		}
		if err != nil {
			return err
		}
		if err := s.store.UpsertMessageMapping(ctx, MessageMapping{
			BeeperMessageID: mappingID,
			MatrixEventID:   eventID,
			ChatID:          msg.ChatID,
			Version:         version,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) redactAdditionalAttachments(ctx context.Context, roomID string, msg Message, version string) error {
	mappings, err := s.store.MessageMappingsByChat(ctx, msg.ChatID)
	if err != nil {
		return err
	}
	prefix := msg.ID + "/attachment/"
	for _, existing := range mappings {
		if !strings.HasPrefix(existing.BeeperMessageID, prefix) || existing.DeletedAt != nil {
			continue
		}
		txnID := DeterministicTxnID(msg.ChatID, existing.BeeperMessageID, MutationDelete, version)
		if _, err := s.matrix.RedactMessage(ctx, roomID, existing.MatrixEventID, txnID, "deleted in Beeper source"); err != nil {
			return err
		}
		now := time.Now().UTC()
		existing.Version = version
		existing.DeletedAt = &now
		if err := s.store.UpsertMessageMapping(ctx, existing); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) matrixMedia(ctx context.Context, msg Message) (*MatrixMedia, error) {
	if len(msg.Attachments) == 0 {
		return nil, nil
	}
	return s.matrixMediaForAttachment(ctx, msg.Attachments[0])
}

func (s *Service) matrixMediaForAttachment(ctx context.Context, att Attachment) (*MatrixMedia, error) {
	if att.URL == "" {
		return nil, nil
	}
	if s.cfg.Media.MaxUploadBytes > 0 && att.SizeBytes > s.cfg.Media.MaxUploadBytes {
		return nil, fmt.Errorf("%s is %d bytes, over configured Matrix upload limit %d", att.FileName, att.SizeBytes, s.cfg.Media.MaxUploadBytes)
	}
	asset, err := s.api.DownloadAsset(ctx, att.URL)
	if err != nil {
		return nil, err
	}
	fileName := firstNonEmpty(att.FileName, asset.FileName, "beeper-asset")
	mimeType := firstNonEmpty(att.MimeType, asset.MimeType, "application/octet-stream")
	sizeBytes := att.SizeBytes
	if sizeBytes == 0 {
		sizeBytes = asset.SizeBytes
	}
	if s.cfg.Media.MaxUploadBytes > 0 && sizeBytes > s.cfg.Media.MaxUploadBytes {
		_ = asset.Content.Close()
		return nil, fmt.Errorf("%s is %d bytes, over configured Matrix upload limit %d", fileName, sizeBytes, s.cfg.Media.MaxUploadBytes)
	}
	return &MatrixMedia{
		Content:     asset.Content,
		Close:       asset.Content.Close,
		FileName:    fileName,
		MimeType:    mimeType,
		SizeBytes:   sizeBytes,
		Width:       att.Width,
		Height:      att.Height,
		DurationMS:  att.DurationMS,
		IsGIF:       att.IsGIF,
		IsVoiceNote: att.IsVoiceNote,
	}, nil
}

func attachmentMessageID(msg Message, index int) string {
	if index <= 0 {
		return msg.ID
	}
	return fmt.Sprintf("%s/attachment/%d", msg.ID, index)
}

func (s *Service) matrixToBeeperDisabled() bool {
	return s.cfg.Safety.DisableMatrixToBeeper || s.cfg.Sync.Mode == SyncModeReadOnly
}

func (s *Service) HandleMatrixMessage(ctx context.Context, inbound MatrixInbound) error {
	if s.matrixToBeeperDisabled() {
		return ErrMatrixToBeeperDisabled
	}
	version := inbound.MatrixEventID
	if version == "" {
		version = inbound.Body
	}
	txnID := DeterministicTxnID(inbound.ChatID, inbound.MatrixEventID, MutationMessage, version)
	replyToID := ""
	if inbound.ReplyToEvent != "" {
		replyTo, ok, err := s.store.MessageByMatrixEventID(ctx, inbound.ReplyToEvent)
		if err != nil {
			return err
		}
		if ok {
			replyToID = replyTo.BeeperMessageID
		}
	}
	beeperMessageID, err := s.api.SendMessage(ctx, BeeperOutbound{
		ChatID:      inbound.ChatID,
		Text:        inbound.Body,
		HTML:        inbound.HTML,
		ReplyToID:   replyToID,
		ClientTxnID: txnID,
		Attachment:  inbound.Attachment,
	})
	if err != nil {
		return err
	}
	if beeperMessageID != "" && inbound.MatrixEventID != "" {
		if err := s.store.UpsertMessageMapping(ctx, MessageMapping{
			BeeperMessageID: beeperMessageID,
			MatrixEventID:   inbound.MatrixEventID,
			ChatID:          inbound.ChatID,
			Version:         firstNonEmpty(inbound.MatrixEventID, inbound.Body),
		}); err != nil {
			return err
		}
	}
	return s.store.RememberOutboundEcho(ctx, inbound.ChatID, inbound.Body, inbound.MatrixEventID, 10*time.Minute)
}

func (s *Service) HandleMatrixEdit(ctx context.Context, chatID, matrixTargetEventID, text string) error {
	if s.matrixToBeeperDisabled() {
		return ErrMatrixToBeeperDisabled
	}
	mapping, ok, err := s.store.MessageByMatrixEventID(ctx, matrixTargetEventID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return s.api.UpdateMessage(ctx, chatID, mapping.BeeperMessageID, text)
}

func (s *Service) HandleMatrixRedaction(ctx context.Context, chatID, matrixTargetEventID string) error {
	if s.matrixToBeeperDisabled() {
		return ErrMatrixToBeeperDisabled
	}
	reaction, ok, err := s.store.ReactionByMatrixEventID(ctx, matrixTargetEventID)
	if err != nil {
		return err
	}
	if ok {
		if err := s.api.RemoveReaction(ctx, chatID, reaction.BeeperMessageID, reaction.ReactionKey); err != nil {
			return err
		}
		return s.store.DeleteReactionMapping(ctx, matrixTargetEventID)
	}
	mapping, ok, err := s.store.MessageByMatrixEventID(ctx, matrixTargetEventID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return s.api.DeleteMessage(ctx, chatID, mapping.BeeperMessageID, true)
}

func (s *Service) HandleMatrixReaction(ctx context.Context, chatID, matrixEventID, matrixTargetEventID, reactionKey string) error {
	if s.matrixToBeeperDisabled() {
		return ErrMatrixToBeeperDisabled
	}
	mapping, ok, err := s.store.MessageByMatrixEventID(ctx, matrixTargetEventID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	txnID := DeterministicTxnID(chatID, matrixEventID, MutationReaction, reactionKey)
	if err := s.api.AddReaction(ctx, chatID, mapping.BeeperMessageID, reactionKey, txnID); err != nil {
		return err
	}
	return s.store.UpsertReactionMapping(ctx, ReactionMapping{
		BeeperMessageID: mapping.BeeperMessageID,
		ReactionKey:     reactionKey,
		MatrixEventID:   matrixEventID,
	})
}

func messageBody(msg Message) string {
	if msg.IsDeleted {
		return "Message deleted"
	}
	if msg.Text != "" {
		return msg.Text
	}
	if len(msg.Attachments) > 0 {
		return msg.Attachments[0].FileName
	}
	return "Unsupported Beeper message"
}

func matrixMsgType(msg Message) string {
	if msg.IsDeleted {
		return "m.notice"
	}
	if len(msg.Attachments) > 0 {
		return matrixMsgTypeForAttachment(msg.Attachments[0], msg.Type)
	}
	switch msg.Type {
	case MessageTypeImage:
		return "m.image"
	case MessageTypeVideo:
		return "m.video"
	case MessageTypeAudio, MessageTypeVoice:
		return "m.audio"
	case MessageTypeFile:
		return "m.file"
	case MessageTypeSticker:
		return "m.sticker"
	case MessageTypeNotice:
		return "m.notice"
	default:
		return "m.text"
	}
}

func matrixMsgTypeForAttachment(att Attachment, fallbackType string) string {
	if att.IsSticker || fallbackType == MessageTypeSticker {
		return "m.sticker"
	}
	mimeType := strings.ToLower(att.MimeType)
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "m.image"
	case strings.HasPrefix(mimeType, "video/"):
		return "m.video"
	case strings.HasPrefix(mimeType, "audio/"):
		return "m.audio"
	}
	switch fallbackType {
	case MessageTypeImage:
		return "m.image"
	case MessageTypeVideo:
		return "m.video"
	case MessageTypeAudio, MessageTypeVoice:
		return "m.audio"
	case MessageTypeFile:
		return "m.file"
	case MessageTypeNotice:
		return "m.notice"
	default:
		return "m.file"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
