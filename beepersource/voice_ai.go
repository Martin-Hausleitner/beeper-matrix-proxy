package beepersource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const voiceAIKindTranscriptSummary = "transcript_summary"

type VoiceAIProcessor interface {
	ProcessVoice(ctx context.Context, input VoiceAIInput) (VoiceAIResult, error)
}

type VoiceAIInput struct {
	Chat       Chat
	Message    Message
	Sender     Sender
	Attachment Attachment
	AudioPath  string
}

type VoiceAIResult struct {
	Transcript string
	Summary    string
	Language   string
	Model      string
}

type pendingMatrixVoiceAI struct {
	key       string
	chat      Chat
	sender    Sender
	message   Message
	att       Attachment
	roomID    string
	audioPath string
	cleanup   func()
}

type LocalVoiceAIProcessor struct {
	cfg    VoiceAIConfig
	client *http.Client
}

func NewLocalVoiceAIProcessor(cfg VoiceAIConfig) *LocalVoiceAIProcessor {
	timeout := time.Duration(cfg.SummaryTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &LocalVoiceAIProcessor{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
	}
}

func (p *LocalVoiceAIProcessor) ProcessVoice(ctx context.Context, input VoiceAIInput) (VoiceAIResult, error) {
	if strings.TrimSpace(p.cfg.StartCommand) != "" {
		commandCtx, cancel := p.commandContext(ctx)
		_, err := runVoiceAICommand(commandCtx, p.cfg.StartCommand, input.AudioPath, p.cfg.Language)
		cancel()
		if err != nil {
			return VoiceAIResult{}, fmt.Errorf("start local voice AI runtime: %w", err)
		}
	}
	if strings.TrimSpace(p.cfg.StopCommand) != "" {
		defer func() {
			stopCtx, cancel := context.WithTimeout(context.Background(), minDuration(p.commandTimeout(), 20*time.Second))
			defer cancel()
			_, _ = runVoiceAICommand(stopCtx, p.cfg.StopCommand, input.AudioPath, p.cfg.Language)
		}()
	}
	transcript, err := p.transcribe(ctx, input)
	if err != nil {
		return VoiceAIResult{}, err
	}
	result := VoiceAIResult{
		Transcript: transcript,
		Language:   firstNonEmpty(p.cfg.Language, "auto"),
		Model:      p.cfg.SummaryModel,
	}
	if strings.TrimSpace(p.cfg.SummaryModel) == "" {
		return result, nil
	}
	summary, err := p.summarize(ctx, input, transcript)
	if err != nil {
		return VoiceAIResult{}, err
	}
	result.Summary = summary
	return result, nil
}

func (p *LocalVoiceAIProcessor) transcribe(ctx context.Context, input VoiceAIInput) (string, error) {
	command := strings.TrimSpace(p.cfg.TranscribeCommand)
	if command == "" {
		return "", fmt.Errorf("voice AI transcription command is not configured")
	}
	commandCtx, cancel := p.commandContext(ctx)
	defer cancel()
	output, err := runVoiceAICommand(commandCtx, command, input.AudioPath, p.cfg.Language)
	if err != nil {
		return "", fmt.Errorf("transcribe voice message: %w", err)
	}
	transcript := strings.TrimSpace(output)
	if transcript == "" {
		return "", fmt.Errorf("transcribe voice message: empty transcript")
	}
	return transcript, nil
}

func (p *LocalVoiceAIProcessor) summarize(ctx context.Context, input VoiceAIInput, transcript string) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(p.cfg.SummaryBaseURL), "/")
	if baseURL == "" {
		return "", fmt.Errorf("voice AI summary base URL is not configured")
	}
	body := map[string]any{
		"model": p.cfg.SummaryModel,
		"messages": []map[string]string{
			{
				"role": "system",
				"content": "Du verbesserst Transkripte von Matrix/Beeper-Sprachnachrichten. Antworte immer auf Deutsch. " +
					"Erfinde keine Fakten. Gib ausschließlich die fertige Antwort aus, keine Gedanken, kein Reasoning, keine Analyse. " +
					"Format: 1-2 Sätze Zusammenfassung und, falls vorhanden, konkrete To-dos.",
			},
			{
				"role": "user",
				"content": fmt.Sprintf(
					"/no_think\n\nPlattform: %s\nChat: %s\nSender: %s\nSprache: %s\n\nTranskript:\n%s",
					firstNonEmpty(input.Chat.Network, input.Chat.AccountID, "unbekannt"),
					input.Chat.Name,
					input.Sender.DisplayName,
					firstNonEmpty(p.cfg.Language, "auto"),
					transcript,
				),
			},
		},
		"temperature": 0.2,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(os.Getenv(p.cfg.SummaryAPIKeyEnv)); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("summary model returned HTTP %d with %d response bytes", resp.StatusCode, len(strings.TrimSpace(string(data))))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("summary model returned no content")
	}
	summary := cleanupVoiceAISummary(out.Choices[0].Message.Content)
	if summary == "" {
		return "", fmt.Errorf("summary model returned no usable content")
	}
	return summary, nil
}

func (p *LocalVoiceAIProcessor) commandContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, p.commandTimeout())
}

func (p *LocalVoiceAIProcessor) commandTimeout() time.Duration {
	timeout := time.Duration(p.cfg.CommandTimeoutSeconds) * time.Second
	if timeout <= 0 {
		return 300 * time.Second
	}
	return timeout
}

func runVoiceAICommand(ctx context.Context, command string, audioPath string, language string) (string, error) {
	cmd := exec.CommandContext(ctx, "sh", "-lc", command)
	cmd.Env = append(os.Environ(),
		"AUDIO_PATH="+audioPath,
		"VOICE_AI_LANGUAGE="+language,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v with %d output bytes", err, len(strings.TrimSpace(string(output))))
	}
	return string(output), nil
}

func (s *Service) maybeProcessVoiceAI(ctx context.Context, roomID string, chat Chat, msg Message, sender Sender, att Attachment, beeperMessageID string, matrixEventID string, version string) error {
	if s.voiceAI == nil || !s.allowsVoiceAI(chat, msg, sender, att) {
		return nil
	}
	key := voiceAIKVKey(beeperMessageID, version)
	if existing, err := s.store.GetValue(ctx, key); err != nil {
		return err
	} else if existing != "" {
		return nil
	}
	asset, err := s.api.DownloadAsset(ctx, att.URL)
	if err != nil {
		return s.rememberVoiceAIError(ctx, key, err)
	}
	defer asset.Content.Close()
	if s.cfg.VoiceAI.MaxAudioBytes > 0 && asset.SizeBytes > s.cfg.VoiceAI.MaxAudioBytes {
		return s.rememberVoiceAIError(ctx, key, fmt.Errorf("%s is %d bytes, over configured voice AI limit %d", firstNonEmpty(att.FileName, asset.FileName, "voice message"), asset.SizeBytes, s.cfg.VoiceAI.MaxAudioBytes))
	}
	tmp, err := os.CreateTemp("", "beeper-voice-ai-*"+filepath.Ext(firstNonEmpty(att.FileName, asset.FileName, ".audio")))
	if err != nil {
		return s.rememberVoiceAIError(ctx, key, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	written, err := copyVoiceAIAsset(tmp, asset.Content, s.cfg.VoiceAI.MaxAudioBytes)
	if err != nil {
		_ = tmp.Close()
		return s.rememberVoiceAIError(ctx, key, err)
	}
	if s.cfg.VoiceAI.MaxAudioBytes > 0 && written > s.cfg.VoiceAI.MaxAudioBytes {
		_ = tmp.Close()
		return s.rememberVoiceAIError(ctx, key, fmt.Errorf("%s is over configured voice AI limit %d", firstNonEmpty(att.FileName, asset.FileName, "voice message"), s.cfg.VoiceAI.MaxAudioBytes))
	}
	if err := tmp.Close(); err != nil {
		return s.rememberVoiceAIError(ctx, key, err)
	}
	result, err := s.voiceAI.ProcessVoice(ctx, VoiceAIInput{
		Chat:       chat,
		Message:    msg,
		Sender:     sender,
		Attachment: att,
		AudioPath:  tmpPath,
	})
	if err != nil {
		return s.rememberVoiceAIError(ctx, key, err)
	}
	body, formatted := voiceAIMessageBody(chat, sender, result)
	eventID, err := s.matrix.SendMessage(ctx, MatrixOutbound{
		RoomID:        roomID,
		MessageID:     beeperMessageID + "/voice-ai",
		AccountID:     msg.AccountID,
		ChatID:        msg.ChatID,
		Body:          body,
		HTML:          formatted,
		MsgType:       "m.notice",
		Timestamp:     time.Now().UTC(),
		ReplyToEvent:  matrixEventID,
		TransactionID: DeterministicTxnID(msg.ChatID, beeperMessageID, "voice_ai", version),
		VoiceAI: &VoiceAIMetadata{
			SourceMessageID: beeperMessageID,
			SourceEventID:   matrixEventID,
			ChatID:          msg.ChatID,
			AccountID:       msg.AccountID,
			Network:         chat.Network,
			Language:        result.Language,
			Model:           result.Model,
			Kind:            voiceAIKindTranscriptSummary,
		},
	})
	if err != nil {
		return s.rememberVoiceAIError(ctx, key, err)
	}
	return s.store.SetValue(ctx, key, eventID)
}

func (s *Service) prepareMatrixInboundVoiceAI(ctx context.Context, inbound *MatrixInbound) (*pendingMatrixVoiceAI, error) {
	if s.voiceAI == nil || inbound.Attachment == nil || inbound.Attachment.Content == nil || inbound.MatrixEventID == "" {
		return nil, nil
	}
	chat, ok, err := s.store.PortalChat(ctx, inbound.ChatID)
	if err != nil {
		return nil, err
	}
	if !ok {
		chat = Chat{ID: inbound.ChatID}
	}
	roomID := firstNonEmpty(inbound.RoomID)
	if roomID == "" {
		if storedRoomID, ok, err := s.store.PortalRoomID(ctx, inbound.ChatID); err != nil {
			return nil, err
		} else if ok {
			roomID = storedRoomID
		}
	}
	sender := Sender{ID: inbound.SenderID, DisplayName: firstNonEmpty(inbound.SenderName, inbound.SenderID, "Martin")}
	msg := Message{
		ID:         "matrix:" + inbound.MatrixEventID,
		AccountID:  chat.AccountID,
		ChatID:     inbound.ChatID,
		SenderID:   sender.ID,
		SenderName: sender.DisplayName,
		Type:       MessageTypeAudio,
		Text:       inbound.Body,
		Timestamp:  inbound.Timestamp,
	}
	att := Attachment{
		ID:         inbound.MatrixEventID,
		URL:        "matrix://" + inbound.MatrixEventID,
		FileName:   inbound.Attachment.FileName,
		MimeType:   inbound.Attachment.MimeType,
		SizeBytes:  inbound.Attachment.SizeBytes,
		Width:      inbound.Attachment.Width,
		Height:     inbound.Attachment.Height,
		DurationMS: inbound.Attachment.DurationMS,
	}
	if !s.allowsVoiceAI(chat, msg, sender, att) {
		return nil, nil
	}
	version := firstNonEmpty(inbound.MatrixEventID, inbound.Body)
	key := voiceAIKVKey("matrix:"+inbound.MatrixEventID, version)
	if existing, err := s.store.GetValue(ctx, key); err != nil {
		return nil, err
	} else if existing != "" {
		return nil, nil
	}
	if s.cfg.VoiceAI.MaxAudioBytes > 0 && inbound.Attachment.SizeBytes <= 0 {
		return nil, s.rememberVoiceAIError(ctx, key, fmt.Errorf("%s has no Matrix media size; skipping local voice AI buffering", firstNonEmpty(inbound.Attachment.FileName, "Matrix audio")))
	}
	if s.cfg.VoiceAI.MaxAudioBytes > 0 && inbound.Attachment.SizeBytes > s.cfg.VoiceAI.MaxAudioBytes {
		return nil, s.rememberVoiceAIError(ctx, key, fmt.Errorf("%s is %d bytes, over configured voice AI limit %d", firstNonEmpty(inbound.Attachment.FileName, "Matrix audio"), inbound.Attachment.SizeBytes, s.cfg.VoiceAI.MaxAudioBytes))
	}
	tmp, err := os.CreateTemp("", "matrix-voice-ai-*"+filepath.Ext(firstNonEmpty(inbound.Attachment.FileName, ".audio")))
	if err != nil {
		return nil, s.rememberVoiceAIError(ctx, key, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := io.Copy(tmp, inbound.Attachment.Content); err != nil {
		_ = tmp.Close()
		cleanup()
		return nil, s.rememberVoiceAIError(ctx, key, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return nil, s.rememberVoiceAIError(ctx, key, err)
	}
	_ = inbound.Attachment.Content.Close()
	uploadFile, err := os.Open(tmpPath)
	if err != nil {
		cleanup()
		return nil, s.rememberVoiceAIError(ctx, key, err)
	}
	inbound.Attachment.Content = uploadFile
	return &pendingMatrixVoiceAI{
		key:       key,
		chat:      chat,
		sender:    sender,
		message:   msg,
		att:       att,
		roomID:    roomID,
		audioPath: tmpPath,
		cleanup:   cleanup,
	}, nil
}

func (s *Service) completeMatrixInboundVoiceAI(ctx context.Context, pending *pendingMatrixVoiceAI, beeperMessageID string, matrixEventID string) error {
	if pending == nil {
		return nil
	}
	if pending.cleanup != nil {
		defer pending.cleanup()
	}
	if pending.roomID == "" {
		return s.rememberVoiceAIError(ctx, pending.key, fmt.Errorf("missing Matrix room for voice AI reply"))
	}
	result, err := s.voiceAI.ProcessVoice(ctx, VoiceAIInput{
		Chat:       pending.chat,
		Message:    pending.message,
		Sender:     pending.sender,
		Attachment: pending.att,
		AudioPath:  pending.audioPath,
	})
	if err != nil {
		return s.rememberVoiceAIError(ctx, pending.key, err)
	}
	body, formatted := voiceAIMessageBody(pending.chat, pending.sender, result)
	eventID, err := s.matrix.SendMessage(ctx, MatrixOutbound{
		RoomID:        pending.roomID,
		MessageID:     "matrix:" + matrixEventID + "/voice-ai",
		AccountID:     pending.chat.AccountID,
		ChatID:        pending.chat.ID,
		Body:          body,
		HTML:          formatted,
		MsgType:       "m.notice",
		Timestamp:     time.Now().UTC(),
		ReplyToEvent:  matrixEventID,
		TransactionID: DeterministicTxnID(pending.chat.ID, "matrix:"+matrixEventID, "voice_ai", matrixEventID),
		VoiceAI: &VoiceAIMetadata{
			SourceMessageID: beeperMessageID,
			SourceEventID:   matrixEventID,
			ChatID:          pending.chat.ID,
			AccountID:       pending.chat.AccountID,
			Network:         pending.chat.Network,
			Language:        result.Language,
			Model:           result.Model,
			Kind:            voiceAIKindTranscriptSummary,
		},
	})
	if err != nil {
		return s.rememberVoiceAIError(ctx, pending.key, err)
	}
	return s.store.SetValue(ctx, pending.key, eventID)
}

func (s *Service) rememberVoiceAIError(ctx context.Context, key string, err error) error {
	if err == nil {
		return nil
	}
	detail := sanitizedVoiceAIError(err)
	if storeErr := s.store.SetValue(ctx, key, "error:"+detail); storeErr != nil {
		return storeErr
	}
	return nil
}

func sanitizedVoiceAIError(err error) string {
	detail := strings.TrimSpace(err.Error())
	if detail == "" {
		return "voice AI failed"
	}
	replacements := []string{
		"Bearer ",
		"Authorization",
		"Transkript:",
		"Transcript:",
		"Zusammenfassung:",
		"Summary:",
	}
	for _, marker := range replacements {
		if strings.Contains(strings.ToLower(detail), strings.ToLower(marker)) {
			return "voice AI failed; see process logs for local diagnostic details"
		}
	}
	if strings.Contains(detail, "/var/") || strings.Contains(detail, "/tmp/") || strings.Contains(detail, os.TempDir()) {
		return "voice AI failed while processing local temporary audio"
	}
	if len(detail) > 180 {
		detail = detail[:180]
	}
	return detail
}

func copyVoiceAIAsset(dst io.Writer, src io.Reader, maxBytes int64) (int64, error) {
	if maxBytes <= 0 {
		return io.Copy(dst, src)
	}
	return io.Copy(dst, io.LimitReader(src, maxBytes+1))
}

func (s *Service) allowsVoiceAI(chat Chat, msg Message, sender Sender, att Attachment) bool {
	cfg := s.cfg.VoiceAI
	if !cfg.Enabled || att.URL == "" || !isVoiceAIAudio(msg, att) {
		return false
	}
	if cfg.MaxAudioBytes > 0 && att.SizeBytes > cfg.MaxAudioBytes {
		return false
	}
	if len(cfg.AllowNetworks) > 0 && !matchesAny(cfg.AllowNetworks, chat.Network) {
		return false
	}
	if len(cfg.AllowAccountIDs) > 0 && !matchesAny(cfg.AllowAccountIDs, firstNonEmpty(chat.AccountID, msg.AccountID)) {
		return false
	}
	hasIdentityAllowlist := len(cfg.AllowChatIDs)+len(cfg.AllowChatNames)+len(cfg.AllowSenderIDs)+len(cfg.AllowSenderNames) > 0
	if !hasIdentityAllowlist {
		return false
	}
	return matchesAny(cfg.AllowChatIDs, chat.ID) ||
		matchesAny(cfg.AllowChatNames, chat.Name) ||
		matchesAny(cfg.AllowSenderIDs, sender.ID, msg.SenderID) ||
		matchesAny(cfg.AllowSenderNames, sender.DisplayName, msg.SenderName)
}

func isVoiceAIAudio(msg Message, att Attachment) bool {
	if att.IsVoiceNote || msg.Type == MessageTypeVoice || msg.Type == MessageTypeAudio {
		return true
	}
	return strings.HasPrefix(strings.ToLower(att.MimeType), "audio/")
}

func voiceAIKVKey(beeperMessageID string, version string) string {
	return "voice_ai:" + beeperMessageID + ":" + version
}

func voiceAIMessageBody(chat Chat, sender Sender, result VoiceAIResult) (string, string) {
	var parts []string
	header := fmt.Sprintf("Sprachnachricht transkribiert (%s / %s)", firstNonEmpty(chat.Network, chat.AccountID, "Matrix"), firstNonEmpty(sender.DisplayName, "unbekannt"))
	parts = append(parts, header)
	if result.Summary != "" {
		parts = append(parts, "Zusammenfassung:\n"+result.Summary)
	}
	parts = append(parts, "Transkript:\n"+result.Transcript)
	body := strings.Join(parts, "\n\n")

	var htmlParts []string
	htmlParts = append(htmlParts, "<p><strong>"+html.EscapeString(header)+"</strong></p>")
	if result.Summary != "" {
		htmlParts = append(htmlParts, "<p><strong>Zusammenfassung</strong><br>"+html.EscapeString(result.Summary)+"</p>")
	}
	htmlParts = append(htmlParts, "<p><strong>Transkript</strong><br>"+html.EscapeString(result.Transcript)+"</p>")
	return body, strings.Join(htmlParts, "\n")
}

func cleanupVoiceAISummary(summary string) string {
	summary = strings.TrimSpace(summary)
	for {
		lower := strings.ToLower(summary)
		start := strings.Index(lower, "<think>")
		if start < 0 {
			break
		}
		end := strings.Index(lower[start:], "</think>")
		if end < 0 {
			summary = strings.TrimSpace(summary[:start])
			break
		}
		end += start + len("</think>")
		summary = strings.TrimSpace(summary[:start] + summary[end:])
	}
	lines := strings.Split(summary, "\n")
	kept := lines[:0]
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if lower == "" || lower == "/think" || lower == "/no_think" || strings.HasPrefix(lower, "reasoning:") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func matchesAny(allowed []string, values ...string) bool {
	for _, allow := range allowed {
		allow = strings.ToLower(strings.TrimSpace(allow))
		if allow == "" {
			continue
		}
		for _, value := range values {
			value = strings.ToLower(strings.TrimSpace(value))
			if value == "" {
				continue
			}
			if value == allow {
				return true
			}
		}
	}
	return false
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
