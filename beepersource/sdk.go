package beepersource

import (
	"context"
	"fmt"
	"io"
	"time"

	beeperdesktopapi "github.com/beeper/desktop-api-go/v5"
	"github.com/beeper/desktop-api-go/v5/option"
	"github.com/beeper/desktop-api-go/v5/packages/param"
	"github.com/beeper/desktop-api-go/v5/shared"
)

type SDKClient struct {
	Client beeperdesktopapi.Client
}

func NewSDKClient(cfg Config, token string) *SDKClient {
	client := beeperdesktopapi.NewClient(
		option.WithBaseURL(cfg.Beeper.BaseURL),
		option.WithAccessToken(token),
		option.WithRequestTimeout(20*time.Second),
	)
	return &SDKClient{Client: client}
}

type DesktopAPIAdapter struct {
	cfg Config
	sdk *SDKClient
}

func NewDesktopAPIAdapter(cfg Config, token string) *DesktopAPIAdapter {
	return &DesktopAPIAdapter{cfg: cfg, sdk: NewSDKClient(cfg, token)}
}

func (a *DesktopAPIAdapter) Health(ctx context.Context) error {
	_, err := a.sdk.Client.Info.Get(ctx)
	return err
}

func (a *DesktopAPIAdapter) ListChats(ctx context.Context) ([]Chat, error) {
	if len(a.cfg.Beeper.ChatIDs) > 0 {
		chats := make([]Chat, 0, len(a.cfg.Beeper.ChatIDs))
		for _, chatID := range a.cfg.Beeper.ChatIDs {
			chat, err := a.sdk.Client.Chats.Get(ctx, chatID, beeperdesktopapi.ChatGetParams{})
			if err != nil {
				return nil, err
			}
			chats = append(chats, convertSDKChat(*chat))
		}
		return chats, nil
	}
	page, err := a.sdk.Client.Chats.List(ctx, beeperdesktopapi.ChatListParams{})
	if err != nil {
		return nil, err
	}
	chats := make([]Chat, 0, len(page.Items))
	for page != nil {
		for _, item := range page.Items {
			chats = append(chats, convertSDKChat(item.Chat))
		}
		page, err = page.GetNextPage()
		if err != nil {
			return nil, err
		}
	}
	return chats, nil
}

func (a *DesktopAPIAdapter) ListMessages(ctx context.Context, chatID string, afterCursor string, limit int) ([]Message, string, error) {
	page, err := a.ListMessagePage(ctx, chatID, afterCursor, "after")
	if err != nil {
		return nil, "", err
	}
	return page.Messages, page.NewestCursor, nil
}

func (a *DesktopAPIAdapter) ListMessagePage(ctx context.Context, chatID string, cursor string, direction string) (MessagePage, error) {
	params := beeperdesktopapi.MessageListParams{}
	if cursor != "" {
		params.Cursor = beeperdesktopapi.String(cursor)
	}
	switch direction {
	case "after":
		if cursor != "" {
			params.Direction = beeperdesktopapi.MessageListParamsDirectionAfter
		}
	case "before", "":
		if cursor != "" {
			params.Direction = beeperdesktopapi.MessageListParamsDirectionBefore
		}
	default:
		return MessagePage{}, fmt.Errorf("unsupported Beeper message page direction %q", direction)
	}
	page, err := a.sdk.Client.Messages.List(ctx, chatID, params)
	if err != nil {
		return MessagePage{}, err
	}
	messages := make([]Message, 0, len(page.Items))
	for _, item := range page.Items {
		messages = append(messages, convertSDKMessage(item))
	}
	return MessagePage{
		Messages:     messages,
		OldestCursor: page.OldestCursor,
		NewestCursor: page.NewestCursor,
		HasMore:      page.HasMore,
	}, nil
}

func (a *DesktopAPIAdapter) DownloadAsset(ctx context.Context, assetURL string) (*AssetStream, error) {
	resp, err := a.sdk.Client.Assets.Serve(ctx, beeperdesktopapi.AssetServeParams{URL: assetURL})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("Beeper asset serve returned HTTP %d", resp.StatusCode)
	}
	return &AssetStream{
		Content:    resp.Body,
		MimeType:   resp.Header.Get("Content-Type"),
		SizeBytes:  resp.ContentLength,
		StatusCode: resp.StatusCode,
	}, nil
}

func (a *DesktopAPIAdapter) SendMessage(ctx context.Context, outbound BeeperOutbound) (string, error) {
	params := beeperdesktopapi.MessageSendParams{
		Text: beeperdesktopapi.String(outbound.Text),
	}
	if outbound.ReplyToID != "" {
		params.ReplyToMessageID = beeperdesktopapi.String(outbound.ReplyToID)
	}
	if outbound.Attachment != nil {
		upload, err := a.uploadAttachment(ctx, outbound.Attachment)
		if err != nil {
			return "", err
		}
		params.Attachment = beeperdesktopapi.MessageSendParamsAttachment{
			UploadID: upload.UploadID,
			FileName: param.NewOpt(upload.FileName),
			MimeType: param.NewOpt(upload.MimeType),
			Type:     outbound.Attachment.Type,
		}
		if upload.Width > 0 && upload.Height > 0 {
			params.Attachment.Size = beeperdesktopapi.MessageSendParamsAttachmentSize{
				Width:  upload.Width,
				Height: upload.Height,
			}
		}
		if upload.Duration > 0 {
			params.Attachment.Duration = param.NewOpt(upload.Duration)
		}
	}
	resp, err := a.sdk.Client.Messages.Send(ctx, outbound.ChatID, params)
	if err != nil {
		return "", err
	}
	return resp.PendingMessageID, nil
}

func (a *DesktopAPIAdapter) uploadAttachment(ctx context.Context, attachment *OutboundAttachment) (*beeperdesktopapi.AssetUploadResponse, error) {
	defer attachment.Content.Close()
	resp, err := a.sdk.Client.Assets.Upload(ctx, beeperdesktopapi.AssetUploadParams{
		File:     namedAttachmentReader{ReadCloser: attachment.Content, fileName: attachment.FileName, contentType: attachment.MimeType},
		FileName: param.NewOpt(attachment.FileName),
		MimeType: param.NewOpt(attachment.MimeType),
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("Beeper asset upload failed: %s", resp.Error)
	}
	if resp.FileName == "" {
		resp.FileName = attachment.FileName
	}
	if resp.MimeType == "" {
		resp.MimeType = attachment.MimeType
	}
	if resp.Width == 0 && attachment.Width > 0 {
		resp.Width = float64(attachment.Width)
	}
	if resp.Height == 0 && attachment.Height > 0 {
		resp.Height = float64(attachment.Height)
	}
	if resp.Duration == 0 && attachment.DurationMS > 0 {
		resp.Duration = float64(attachment.DurationMS) / 1000
	}
	return resp, nil
}

type namedAttachmentReader struct {
	io.ReadCloser
	fileName    string
	contentType string
}

func (r namedAttachmentReader) Name() string        { return r.fileName }
func (r namedAttachmentReader) ContentType() string { return r.contentType }

func (a *DesktopAPIAdapter) UpdateMessage(ctx context.Context, chatID, messageID, text string) error {
	_, err := a.sdk.Client.Messages.Update(ctx, messageID, beeperdesktopapi.MessageUpdateParams{
		ChatID: chatID,
		Text:   text,
	})
	return err
}

func (a *DesktopAPIAdapter) DeleteMessage(ctx context.Context, chatID, messageID string, forEveryone bool) error {
	return a.sdk.Client.Messages.Delete(ctx, messageID, beeperdesktopapi.MessageDeleteParams{
		ChatID:      chatID,
		ForEveryone: param.NewOpt(forEveryone),
	})
}

func (a *DesktopAPIAdapter) AddReaction(ctx context.Context, chatID, messageID, reactionKey, txnID string) error {
	_, err := a.sdk.Client.Chats.Messages.Reactions.Add(ctx, messageID, beeperdesktopapi.ChatMessageReactionAddParams{
		ChatID:        chatID,
		ReactionKey:   reactionKey,
		TransactionID: param.NewOpt(txnID),
	})
	return err
}

func (a *DesktopAPIAdapter) RemoveReaction(ctx context.Context, chatID, messageID, reactionKey string) error {
	_, err := a.sdk.Client.Chats.Messages.Reactions.Delete(ctx, reactionKey, beeperdesktopapi.ChatMessageReactionDeleteParams{
		ChatID:    chatID,
		MessageID: messageID,
	})
	return err
}

func convertSDKChat(in beeperdesktopapi.Chat) Chat {
	return Chat{
		ID:         in.ID,
		AccountID:  in.AccountID,
		Network:    in.Network,
		Name:       in.Title,
		AvatarURL:  in.ImgURL,
		IsGroup:    string(in.Type) == "group",
		IsArchived: in.IsArchived,
	}
}

func convertSDKMessage(in shared.Message) Message {
	msg := Message{
		ID:              in.ID,
		AccountID:       in.AccountID,
		ChatID:          in.ChatID,
		SenderID:        in.SenderID,
		SenderName:      in.SenderName,
		SortKey:         in.SortKey,
		Type:            string(in.Type),
		Text:            in.Text,
		Timestamp:       in.Timestamp,
		IsDeleted:       in.IsDeleted,
		IsHidden:        in.IsHidden,
		IsSender:        in.IsSender,
		IsUnread:        in.IsUnread,
		LinkedMessageID: in.LinkedMessageID,
		Mentions:        append([]string(nil), in.Mentions...),
		Attachments:     make([]Attachment, 0, len(in.Attachments)),
		RawJSON:         in.RawJSON(),
	}
	if !in.EditedTimestamp.IsZero() {
		edited := in.EditedTimestamp
		msg.EditedTimestamp = &edited
	}
	for _, att := range in.Attachments {
		msg.Attachments = append(msg.Attachments, Attachment{
			ID:          att.ID,
			URL:         att.SrcURL,
			FileName:    att.FileName,
			MimeType:    att.MimeType,
			SizeBytes:   int64(att.FileSize),
			Width:       int(att.Size.Width),
			Height:      int(att.Size.Height),
			DurationMS:  int(att.Duration * 1000),
			IsVoiceNote: att.IsVoiceNote,
			IsGIF:       att.IsGif,
			IsSticker:   att.IsSticker,
		})
	}
	return msg
}
