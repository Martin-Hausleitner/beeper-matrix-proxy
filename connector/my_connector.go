package connector

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/id"
	"maunium.net/go/mautrix/mediaproxy"
)

// Ensure MyConnector implements NetworkConnector.
var _ bridgev2.NetworkConnector = (*MyConnector)(nil)
var _ bridgev2.DirectMediableNetwork = (*MyConnector)(nil)

// MyConnector implements the NetworkConnector interface.
type MyConnector struct {
	log            zerolog.Logger
	bridge         *bridgev2.Bridge
	directMedia    bool
	directMediaMu  sync.RWMutex
	clientsMu      sync.RWMutex
	clientsByLogin map[networkid.UserLoginID]*MyNetworkClient
}

// NewMyConnector creates a new instance of MyConnector.
func NewMyConnector(log zerolog.Logger) *MyConnector {
	return &MyConnector{
		log: log.With().Str("component", "network-connector").Logger(),
	}
}

// Init initializes the connector with the bridge instance.
func (c *MyConnector) Init(br *bridgev2.Bridge) {
	c.bridge = br
	c.log = c.bridge.Log
	c.clientsByLogin = make(map[networkid.UserLoginID]*MyNetworkClient)
	c.log.Info().Msg("MyConnector Init called")
}

// GetName implements bridgev2.NetworkConnector.
func (c *MyConnector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:          "Beeper Matrix Proxy",
		NetworkURL:           localHomeserverURL(),
		NetworkIcon:          "",
		NetworkID:            "beeper-matrix-proxy",
		BeeperBridgeType:     "beeper-matrix-proxy",
		DefaultPort:          29320,
		DefaultCommandPrefix: "!matrixproxy",
	}
}

// GetNetworkID implements bridgev2.NetworkConnector.
func (c *MyConnector) GetNetworkID() string {
	return c.GetName().NetworkID
}

// GetCapabilities implements bridgev2.NetworkConnector.
func (c *MyConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{}
}

// GetDBMetaTypes implements bridgev2.NetworkConnector.
func (c *MyConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		Portal: func() any {
			return &PortalMetadata{}
		},
		Ghost: func() any {
			return &GhostMetadata{}
		},
		Reaction: func() any {
			return &ReactionMetadata{}
		},
		UserLogin: func() any {
			return &LoginMetadata{}
		},
	}
}

// GetLoginFlows implements bridgev2.NetworkConnector.
func (c *MyConnector) GetLoginFlows() []bridgev2.LoginFlow {
	return []bridgev2.LoginFlow{{
		ID:          LoginFlowIDUsernamePassword,
		Name:        "Username & Password",
		Description: "Log in to the remote Matrix homeserver.",
	}}
}

// CreateLogin implements bridgev2.NetworkConnector.
func (c *MyConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if flowID != LoginFlowIDUsernamePassword {
		return nil, fmt.Errorf("unsupported login flow ID: %s", flowID)
	}
	return &SimpleLogin{
		User: user,
		Main: c,
		Log:  user.Log.With().Str("action", "login").Str("flow", flowID).Logger(),
	}, nil
}

// GetConfig implements bridgev2.NetworkConnector.
func (c *MyConnector) GetConfig() (string, any, configupgrade.Upgrader) {
	return "beeper-matrix-proxy.yaml", nil, nil
}

// GetBridgeInfoVersion implements bridgev2.NetworkConnector.
func (c *MyConnector) GetBridgeInfoVersion() (int, int) {
	return 1, 5
}

// Start implements bridgev2.NetworkConnector.
func (c *MyConnector) Start(ctx context.Context) error {
	c.log.Info().Msg("MyConnector Start called")
	return nil
}

// Stop implements bridgev2.NetworkConnector.
func (c *MyConnector) Stop(ctx context.Context) error {
	c.log.Info().Msg("MyConnector Stop called")
	return nil
}

// LoadUserLogin implements bridgev2.NetworkConnector.
func (c *MyConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	c.log.Info().
		Str("user_id", string(login.ID)).
		Str("remote_name", login.RemoteName).
		Str("mxid", string(login.User.MXID)).
		Msg("LoadUserLogin called")

	client := &MyNetworkClient{
		log:        c.log.With().Str("user_id", string(login.ID)).Logger(),
		bridge:     c.bridge,
		login:      login,
		connector:  c,
		sentEvents: make(map[id.EventID]struct{}),
	}
	client.metadata = newLoginMetadataStore(login)
	if meta, ok := login.Metadata.(*LoginMetadata); ok {
		homeserverURL := meta.HomeserverURL
		if homeserverURL == "" {
			homeserverURL = localHomeserverURL()
		}
		cli, err := newLocalMatrixClientAt(homeserverURL, meta.UserID, meta.AccessToken)
		if err != nil {
			return err
		}
		cli.DeviceID = id.DeviceID(meta.DeviceID)
		cli.Store = newLoginSyncStore(client.metadata)
		client.mx = cli
		client.loggedIn = meta.AccessToken != ""
	}

	c.clientsMu.Lock()
	previousClient := c.clientsByLogin[login.ID]
	if previousClient != nil && previousClient != client {
		previousClient.retire()
	}
	login.Client = client
	c.clientsByLogin[login.ID] = client
	c.clientsMu.Unlock()

	c.log.Info().
		Str("user_id", string(login.ID)).
		Str("remote_name", login.RemoteName).
		Interface("client_type", client).
		Msg("Created and stored MyNetworkClient")

	return nil
}

type directMediaPayload struct {
	Version   int    `json:"v"`
	LoginID   string `json:"login_id"`
	MXC       string `json:"mxc"`
	ExpiresAt int64  `json:"exp"`
	Nonce     string `json:"nonce"`
	Signature string `json:"sig,omitempty"`
}

func (c *MyConnector) SetUseDirectMedia() {
	c.directMediaMu.Lock()
	defer c.directMediaMu.Unlock()
	c.directMedia = true
}

func (c *MyConnector) directMediaEnabled() bool {
	c.directMediaMu.RLock()
	defer c.directMediaMu.RUnlock()
	return c.directMedia
}

func encodeDirectMediaID(loginID networkid.UserLoginID, uri id.ContentURIString) (networkid.MediaID, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return networkid.MediaID(""), err
	}
	payload := directMediaPayload{
		Version:   2,
		LoginID:   string(loginID),
		MXC:       string(uri),
		ExpiresAt: time.Now().Add(directMediaIDTTL()).Unix(),
		Nonce:     base64.RawURLEncoding.EncodeToString(nonce),
	}
	signature, ok := signDirectMediaPayload(payload)
	if !ok {
		return networkid.MediaID(""), fmt.Errorf("direct media signing key is not configured")
	}
	payload.Signature = signature
	raw, err := json.Marshal(payload)
	if err != nil {
		return networkid.MediaID(""), err
	}
	return networkid.MediaID(base64.RawURLEncoding.EncodeToString(raw)), nil
}

func decodeDirectMediaID(mediaID networkid.MediaID) (directMediaPayload, error) {
	raw, err := base64.RawURLEncoding.DecodeString(string(mediaID))
	if err != nil {
		return directMediaPayload{}, err
	}
	var payload directMediaPayload
	if err = json.Unmarshal(raw, &payload); err != nil {
		return directMediaPayload{}, err
	}
	if payload.Version != 2 || payload.LoginID == "" || payload.MXC == "" || payload.ExpiresAt == 0 || payload.Nonce == "" || payload.Signature == "" {
		return directMediaPayload{}, fmt.Errorf("invalid direct media payload")
	}
	if time.Now().Unix() > payload.ExpiresAt {
		return directMediaPayload{}, fmt.Errorf("expired direct media payload")
	}
	if !validDirectMediaSignature(payload) {
		return directMediaPayload{}, fmt.Errorf("invalid direct media signature")
	}
	return payload, nil
}

func directMediaIDTTL() time.Duration {
	raw := os.Getenv("BEEPER_MATRIX_PROXY_DIRECT_MEDIA_TTL")
	if raw == "" {
		return 24 * time.Hour
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		return 24 * time.Hour
	}
	return duration
}

func directMediaSigningKey() ([]byte, bool) {
	if key := os.Getenv("BEEPER_MATRIX_PROXY_DIRECT_MEDIA_KEY"); key != "" {
		return []byte(key), true
	}
	if key := os.Getenv("BEEPER_MATRIX_PROXY_MEDIA_KEY"); key != "" {
		return []byte(key), true
	}
	return nil, false
}

func signDirectMediaPayload(payload directMediaPayload) (string, bool) {
	key, ok := directMediaSigningKey()
	if !ok {
		return "", false
	}
	mac := hmac.New(sha256.New, key)
	fmt.Fprintf(mac, "%d\n%s\n%s\n%d\n%s", payload.Version, payload.LoginID, payload.MXC, payload.ExpiresAt, payload.Nonce)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), true
}

func validDirectMediaSignature(payload directMediaPayload) bool {
	expected, ok := signDirectMediaPayload(directMediaPayload{
		Version:   payload.Version,
		LoginID:   payload.LoginID,
		MXC:       payload.MXC,
		ExpiresAt: payload.ExpiresAt,
		Nonce:     payload.Nonce,
	})
	if !ok {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(payload.Signature)
	if err != nil {
		return false
	}
	want, err := base64.RawURLEncoding.DecodeString(expected)
	if err != nil {
		return false
	}
	return hmac.Equal(got, want)
}

func (c *MyConnector) Download(ctx context.Context, mediaID networkid.MediaID, params map[string]string) (mediaproxy.GetMediaResponse, error) {
	payload, err := decodeDirectMediaID(mediaID)
	if err != nil {
		return nil, mautrix.MNotFound.WithMessage("Invalid direct media ID")
	}
	c.clientsMu.RLock()
	client := c.clientsByLogin[networkid.UserLoginID(payload.LoginID)]
	c.clientsMu.RUnlock()
	if client == nil {
		return nil, mautrix.MNotFound.WithMessage("Direct media login is not connected")
	}
	data, err := client.downloadFromLocalMatrix(ctx, id.ContentURIString(payload.MXC), nil)
	if err != nil {
		return nil, err
	}
	return mediaproxy.GetMediaResponseRawData(data), nil
}

func localHomeserverURL() string {
	if value := os.Getenv("LOCAL_MATRIX_HS"); value != "" {
		return value
	}
	return "https://matrix.example.com"
}

func newLocalMatrixClient(userID, accessToken string) (*mautrix.Client, error) {
	return newLocalMatrixClientAt(localHomeserverURL(), userID, accessToken)
}

func newLocalMatrixClientAt(homeserverURL, userID, accessToken string) (*mautrix.Client, error) {
	cli, err := mautrix.NewClient(homeserverURL, id.UserID(userID), accessToken)
	if err != nil {
		return nil, err
	}
	if insecureLocalTLS() {
		cli.Client = &http.Client{
			Timeout: 180 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
	}
	return cli, nil
}

func insecureLocalTLS() bool {
	switch strings.ToLower(os.Getenv("LOCAL_MATRIX_INSECURE_TLS")) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func (c *MyConnector) resyncRoom(ctx context.Context, login *bridgev2.UserLogin, roomID id.RoomID, info *bridgev2.ChatInfo) {
	c.bridge.QueueRemoteEvent(login, &simplevent.ChatResync{
		EventMeta: simplevent.EventMeta{
			Type:         bridgev2.RemoteEventChatResync,
			PortalKey:    networkid.PortalKey{ID: networkid.PortalID(roomID), Receiver: login.ID},
			CreatePortal: true,
			Timestamp:    time.Now(),
		},
		ChatInfo: info,
	})
}
