package beepersource

import (
	"errors"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type contactAvatarOverridesFile struct {
	Contacts []contactAvatarOverride `yaml:"contacts"`
}

type contactAvatarOverride struct {
	DisplayName    string   `yaml:"display_name"`
	Aliases        []string `yaml:"aliases"`
	BeeperChatIDs  []string `yaml:"beeper_chat_ids"`
	MatrixRoomIDs  []string `yaml:"matrix_room_ids"`
	SenderIDs      []string `yaml:"sender_ids"`
	AvatarFile     string   `yaml:"avatar_file"`
	AppleContactID string   `yaml:"apple_contact_id"`
	Confidence     string   `yaml:"confidence"`
}

func (s *Service) contactOverrideAvatarForChat(chat Chat) (*MatrixMedia, bool, error) {
	override, ok, err := s.contactOverrideForChat(chat)
	if !ok || err != nil {
		return nil, ok, err
	}
	return contactOverrideMedia("contact-override:"+chat.ID, override.AvatarFile)
}

func (s *Service) contactOverrideAvatarForSender(sender Sender) (*MatrixMedia, bool, error) {
	override, ok, err := s.contactOverrideForSender(sender)
	if !ok || err != nil {
		return nil, ok, err
	}
	return contactOverrideMedia("contact-override:"+sender.ID, override.AvatarFile)
}

func contactOverrideMedia(assetID, path string) (*MatrixMedia, bool, error) {
	avatar, ok, err := localAvatarMedia(path)
	if avatar != nil {
		avatar.AssetID = assetID
	}
	return avatar, ok, err
}

func (s *Service) contactOverrideForChat(chat Chat) (contactAvatarOverride, bool, error) {
	overrides, err := s.loadContactAvatarOverrides()
	if err != nil {
		return contactAvatarOverride{}, false, err
	}
	for _, override := range overrides {
		if override.AvatarFile == "" {
			continue
		}
		if containsString(override.BeeperChatIDs, chat.ID) || containsString(override.Aliases, chat.Name) {
			return override, true, nil
		}
	}
	return contactAvatarOverride{}, false, nil
}

func (s *Service) contactOverrideForSender(sender Sender) (contactAvatarOverride, bool, error) {
	overrides, err := s.loadContactAvatarOverrides()
	if err != nil {
		return contactAvatarOverride{}, false, err
	}
	for _, override := range overrides {
		if override.AvatarFile == "" {
			continue
		}
		if containsString(override.SenderIDs, sender.ID) || containsString(override.Aliases, sender.DisplayName) {
			return override, true, nil
		}
	}
	return contactAvatarOverride{}, false, nil
}

func (s *Service) loadContactAvatarOverrides() ([]contactAvatarOverride, error) {
	path := strings.TrimSpace(s.cfg.Matrix.ContactAvatarOverridesPath)
	if path == "" {
		return nil, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var file contactAvatarOverridesFile
	if err := yaml.Unmarshal(body, &file); err != nil {
		return nil, err
	}
	return file.Contacts, nil
}

func containsString(values []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
}
