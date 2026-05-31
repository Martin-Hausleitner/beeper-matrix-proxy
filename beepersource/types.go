package beepersource

import (
	"io"
	"time"
)

const (
	SyncModeBidirectional = "bidirectional"
	SyncModeReadOnly      = "read_only"
)

const (
	MutationMessage  = "message"
	MutationEdit     = "edit"
	MutationDelete   = "delete"
	MutationReaction = "reaction"
)

const (
	MessageTypeText    = "TEXT"
	MessageTypeNotice  = "NOTICE"
	MessageTypeImage   = "IMAGE"
	MessageTypeVideo   = "VIDEO"
	MessageTypeAudio   = "AUDIO"
	MessageTypeVoice   = "VOICE"
	MessageTypeFile    = "FILE"
	MessageTypeSticker = "STICKER"
	MessageTypeUnknown = "UNKNOWN"
)

type Chat struct {
	ID           string
	AccountID    string
	Network      string
	Name         string
	AvatarURL    string
	Participants []Sender
	IsGroup      bool
	IsArchived   bool
}

type Sender struct {
	ID           string
	DisplayName  string
	AvatarID     string
	MessageCount int
}

type Attachment struct {
	ID          string
	URL         string
	FileName    string
	MimeType    string
	SizeBytes   int64
	Width       int
	Height      int
	DurationMS  int
	IsVoiceNote bool
	IsGIF       bool
	IsSticker   bool
}

type Message struct {
	ID              string
	AccountID       string
	ChatID          string
	SenderID        string
	SenderName      string
	SortKey         string
	Type            string
	Text            string
	HTML            string
	Timestamp       time.Time
	EditedTimestamp *time.Time
	IsDeleted       bool
	IsHidden        bool
	IsSender        bool
	IsUnread        bool
	LinkedMessageID string
	Mentions        []string
	Attachments     []Attachment
	RawJSON         string
}

type MessagePage struct {
	Messages     []Message
	OldestCursor string
	NewestCursor string
	HasMore      bool
}

type MatrixOutbound struct {
	RoomID        string
	MessageID     string
	AccountID     string
	ChatID        string
	SenderID      string
	SenderName    string
	SenderMXID    string
	SenderAvatar  *MatrixMedia
	SortKey       string
	Body          string
	HTML          string
	MsgType       string
	Timestamp     time.Time
	ReplyToEvent  string
	TransactionID string
	IsHidden      bool
	IsSender      bool
	IsUnread      bool
	Mentions      []string
	AttachmentID  string
	AttachmentIdx int
	Media         *MatrixMedia
	VoiceAI       *VoiceAIMetadata
}

type VoiceAIMetadata struct {
	SourceMessageID string `json:"source_message_id"`
	SourceEventID   string `json:"source_event_id"`
	ChatID          string `json:"chat_id"`
	AccountID       string `json:"account_id,omitempty"`
	Network         string `json:"network,omitempty"`
	Language        string `json:"language,omitempty"`
	Model           string `json:"model,omitempty"`
	Kind            string `json:"kind"`
}

type AssetStream struct {
	Content    io.ReadCloser
	FileName   string
	MimeType   string
	SizeBytes  int64
	StatusCode int
}

type MatrixMedia struct {
	AssetID     string
	ContentHash string
	CachedMXC   string
	Content     io.Reader
	Close       func() error
	FileName    string
	MimeType    string
	SizeBytes   int64
	Width       int
	Height      int
	DurationMS  int
	IsGIF       bool
	IsVoiceNote bool
}

type MatrixInbound struct {
	ChatID        string
	RoomID        string
	MatrixEventID string
	SenderID      string
	SenderName    string
	Body          string
	HTML          string
	ReplyToEvent  string
	Timestamp     time.Time
	Attachment    *OutboundAttachment
}

type BeeperOutbound struct {
	ChatID      string
	Text        string
	HTML        string
	ReplyToID   string
	ClientTxnID string
	Attachment  *OutboundAttachment
}

type OutboundAttachment struct {
	Content    io.ReadCloser
	FileName   string
	MimeType   string
	SizeBytes  int64
	Width      int
	Height     int
	DurationMS int
	Type       string
}

type MessageMapping struct {
	BeeperMessageID string
	MatrixEventID   string
	ChatID          string
	Version         string
	DeletedAt       *time.Time
}

type ReactionMapping struct {
	BeeperMessageID string
	ReactionKey     string
	MatrixEventID   string
}

type PendingMutation struct {
	ID              int64
	BeeperMessageID string
	MutationType    string
	PayloadJSON     []byte
	CreatedAt       time.Time
}
