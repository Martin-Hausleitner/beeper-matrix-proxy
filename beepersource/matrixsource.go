package beepersource

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const matrixSyncSinceKey = "matrix_sync_since"

type MatrixClientSource struct {
	cfg    Config
	store  *Store
	token  string
	client *http.Client
}

func NewMatrixClientSource(cfg Config, store *Store, accessToken string) *MatrixClientSource {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.Matrix.InsecureSkipTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // Local self-signed cert opt-in.
	}
	return &MatrixClientSource{
		cfg:    cfg,
		store:  store,
		token:  accessToken,
		client: &http.Client{Timeout: 35 * time.Second, Transport: transport},
	}
}

func (m *MatrixClientSource) SyncOnce(ctx context.Context, service *Service) (int, error) {
	if m.cfg.Safety.DisableMatrixToBeeper || m.cfg.Sync.Mode == SyncModeReadOnly {
		return 0, nil
	}
	since, err := m.store.GetValue(ctx, matrixSyncSinceKey)
	if err != nil {
		return 0, err
	}
	resp, err := m.sync(ctx, since)
	if err != nil {
		return 0, err
	}
	handled := 0
	for roomID, room := range resp.Rooms.Join {
		chatID, ok, err := m.store.PortalChatIDByRoomID(ctx, roomID)
		if err != nil {
			return handled, err
		}
		if !ok {
			continue
		}
		for _, ev := range room.Timeline.Events {
			if ev.Sender == m.cfg.Matrix.UserID || ev.EventID == "" {
				continue
			}
			if ev.Content.VoiceAI != nil {
				continue
			}
			if err := m.handleEvent(ctx, service, chatID, roomID, ev); err != nil {
				return handled, err
			}
			handled++
		}
	}
	if resp.NextBatch != "" {
		return handled, m.store.SetValue(ctx, matrixSyncSinceKey, resp.NextBatch)
	}
	return handled, nil
}

func (m *MatrixClientSource) handleEvent(ctx context.Context, service *Service, chatID string, roomID string, ev matrixSyncEvent) error {
	switch ev.Type {
	case "m.room.message":
		if target := ev.Content.RelatesTo.EventID; ev.Content.RelatesTo.RelType == "m.replace" && target != "" {
			body := strings.TrimSpace(firstNonEmpty(ev.Content.NewContent.Body, strings.TrimPrefix(ev.Content.Body, "* ")))
			if body == "" {
				return nil
			}
			return service.HandleMatrixEdit(ctx, chatID, target, body)
		}
		if ev.Content.MsgType != "m.text" && ev.Content.MsgType != "m.notice" && ev.Content.URL == "" {
			return nil
		}
		body := strings.TrimSpace(ev.Content.Body)
		if body == "" {
			return nil
		}
		inbound := MatrixInbound{
			ChatID:        chatID,
			RoomID:        roomID,
			MatrixEventID: ev.EventID,
			SenderID:      ev.Sender,
			Body:          body,
			HTML:          ev.Content.FormattedBody,
			ReplyToEvent:  ev.Content.RelatesTo.InReplyTo.EventID,
		}
		if ev.Content.URL != "" {
			attachment, err := m.downloadAttachment(ctx, ev)
			if err != nil {
				return err
			}
			inbound.Attachment = attachment
		}
		return service.HandleMatrixMessage(ctx, inbound)
	case "m.reaction":
		rel := ev.Content.RelatesTo
		if rel.RelType != "m.annotation" || rel.EventID == "" || rel.Key == "" {
			return nil
		}
		return service.HandleMatrixReaction(ctx, chatID, ev.EventID, rel.EventID, rel.Key)
	case "m.room.redaction":
		target := ev.Redacts
		if target == "" {
			target = ev.Content.Redacts
		}
		if target == "" {
			return nil
		}
		return service.HandleMatrixRedaction(ctx, chatID, target)
	default:
		return nil
	}
}

func (m *MatrixClientSource) downloadAttachment(ctx context.Context, ev matrixSyncEvent) (*OutboundAttachment, error) {
	downloadURL, err := m.matrixMediaDownloadURL(ev.Content.URL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+m.token)
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("Matrix media download returned HTTP %d", resp.StatusCode)
	}
	fileName := firstNonEmpty(ev.Content.FileName, ev.Content.Body, "matrix-attachment")
	mimeType := firstNonEmpty(ev.Content.Info.MimeType, resp.Header.Get("Content-Type"), "application/octet-stream")
	return &OutboundAttachment{
		Content:    resp.Body,
		FileName:   fileName,
		MimeType:   mimeType,
		SizeBytes:  firstNonZeroInt64(int64(ev.Content.Info.Size), resp.ContentLength),
		Width:      ev.Content.Info.Width,
		Height:     ev.Content.Info.Height,
		DurationMS: ev.Content.Info.Duration,
		Type:       beeperAttachmentType(ev.Content.MsgType, mimeType),
	}, nil
}

func (m *MatrixClientSource) matrixMediaDownloadURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "mxc" || parsed.Host == "" || strings.Trim(parsed.Path, "/") == "" {
		return "", fmt.Errorf("unsupported Matrix media URL %q", raw)
	}
	base := strings.TrimRight(m.cfg.Matrix.HomeserverURL, "/")
	return base + "/_matrix/client/v1/media/download/" + url.PathEscape(parsed.Host) + "/" + url.PathEscape(strings.Trim(parsed.Path, "/")), nil
}

func beeperAttachmentType(msgType string, mimeType string) string {
	switch msgType {
	case "m.image":
		if mimeType == "image/gif" {
			return "gif"
		}
		return "image"
	case "m.video":
		return "video"
	case "m.audio":
		return "audio"
	case "m.sticker":
		return "sticker"
	default:
		return "file"
	}
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func (m *MatrixClientSource) sync(ctx context.Context, since string) (*matrixSyncResponse, error) {
	base := strings.TrimRight(m.cfg.Matrix.HomeserverURL, "/")
	endpoint, err := url.Parse(base + "/_matrix/client/v3/sync")
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("timeout", "0")
	if since != "" {
		query.Set("since", since)
	}
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+m.token)
	httpResp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, fmt.Errorf("Matrix sync returned HTTP %d", httpResp.StatusCode)
	}
	var resp matrixSyncResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

type matrixSyncResponse struct {
	NextBatch string `json:"next_batch"`
	Rooms     struct {
		Join map[string]struct {
			Timeline struct {
				Events []matrixSyncEvent `json:"events"`
			} `json:"timeline"`
		} `json:"join"`
	} `json:"rooms"`
}

type matrixSyncEvent struct {
	Type    string `json:"type"`
	EventID string `json:"event_id"`
	Sender  string `json:"sender"`
	Redacts string `json:"redacts"`
	Content struct {
		MsgType       string `json:"msgtype"`
		Body          string `json:"body"`
		Format        string `json:"format"`
		FormattedBody string `json:"formatted_body"`
		FileName      string `json:"filename"`
		URL           string `json:"url"`
		Redacts       string `json:"redacts"`
		Info          struct {
			MimeType string `json:"mimetype"`
			Size     int    `json:"size"`
			Width    int    `json:"w"`
			Height   int    `json:"h"`
			Duration int    `json:"duration"`
		} `json:"info"`
		NewContent struct {
			Body string `json:"body"`
		} `json:"m.new_content"`
		RelatesTo struct {
			RelType   string `json:"rel_type"`
			EventID   string `json:"event_id"`
			Key       string `json:"key"`
			InReplyTo struct {
				EventID string `json:"event_id"`
			} `json:"m.in_reply_to"`
		} `json:"m.relates_to"`
		VoiceAI *VoiceAIMetadata `json:"com.openclaw.voice_ai"`
	} `json:"content"`
}
