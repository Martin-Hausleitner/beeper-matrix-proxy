package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/bridgeconfig"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func TestSubmitUserInputLoadsAndConnectsReturnedClient(t *testing.T) {
	const (
		remoteUser  = id.UserID("@remote:example.org")
		accessToken = "test-access-token"
		deviceID    = id.DeviceID("TESTDEVICE")
	)

	joinedRoomsCalled := make(chan struct{}, 1)
	syncStarted := make(chan struct{}, 1)
	syncStopped := make(chan struct{}, 1)
	requestCounts := make(chan string, 16)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCounts <- r.URL.Path

		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/login"):
			writeJSON(t, w, map[string]any{
				"user_id":      remoteUser,
				"access_token": accessToken,
				"device_id":    deviceID,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/_matrix/client/versions":
			writeJSON(t, w, map[string]any{"versions": []string{"v1.11"}})
		case r.Method == http.MethodGet && r.URL.Path == "/_matrix/client/v1/media/config":
			writeJSON(t, w, map[string]any{"m.upload.size": 1024 * 1024})
		case r.Method == http.MethodGet && r.URL.Path == "/_matrix/client/v3/joined_rooms":
			signalTestEvent(joinedRoomsCalled)
			writeJSON(t, w, map[string]any{"joined_rooms": []string{}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/filter"):
			writeJSON(t, w, map[string]any{"filter_id": "test-filter"})
		case r.Method == http.MethodGet && r.URL.Path == "/_matrix/client/v3/sync":
			signalTestEvent(syncStarted)
			<-r.Context().Done()
			signalTestEvent(syncStopped)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("LOCAL_MATRIX_HS", server.URL)
	t.Setenv("LOCAL_MATRIX_INSECURE_TLS", "false")

	db, err := dbutil.NewWithDialect("file:submit-login-test?mode=memory&cache=shared", "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	db.RawDB.SetMaxOpenConns(1)
	defer db.Close()

	connector := NewMyConnector(zerolog.Nop())
	matrixConfig := &bridgeconfig.Config{
		Homeserver: bridgeconfig.HomeserverConfig{
			Address: server.URL,
			Domain:  "example.org",
		},
		AppService: bridgeconfig.AppserviceConfig{
			Bot:      bridgeconfig.BotUserConfig{Username: "bridgebot"},
			NoServer: true,
		},
	}
	bridge := bridgev2.NewBridge(
		networkid.BridgeID("login-test"),
		db,
		zerolog.Nop(),
		&matrixConfig.Bridge,
		matrix.NewConnector(matrixConfig),
		connector,
		commands.NewProcessor,
	)
	bridge.BackgroundCtx = context.Background()
	if err := bridge.DB.Upgrade(context.Background()); err != nil {
		t.Fatalf("failed to create bridge test schema: %v", err)
	}

	const localUser = id.UserID("@local:example.org")
	if err := bridge.DB.User.Insert(context.Background(), &database.User{
		BridgeID: bridge.ID,
		MXID:     localUser,
	}); err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}
	user, err := bridge.GetUserByMXID(context.Background(), localUser)
	if err != nil {
		t.Fatalf("failed to load test user: %v", err)
	}

	login := &SimpleLogin{User: user, Main: connector, Log: zerolog.Nop()}
	step, err := login.SubmitUserInput(context.Background(), map[string]string{
		"username": "remote",
		"password": "password",
	})
	if err != nil {
		t.Fatalf("SubmitUserInput returned an error: %v", err)
	}
	if step == nil || step.Type != bridgev2.LoginStepTypeComplete || step.CompleteParams == nil || step.CompleteParams.UserLogin == nil {
		t.Fatalf("expected complete login step, got %#v", step)
	}
	userLogin := step.CompleteParams.UserLogin
	loadedClient, ok := userLogin.Client.(*MyNetworkClient)
	if !ok {
		t.Fatalf("expected MyNetworkClient, got %T", userLogin.Client)
	}
	disconnected := false
	defer func() {
		if !disconnected {
			loadedClient.Disconnect()
		}
	}()

	select {
	case <-joinedRoomsCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("connected client did not fetch joined rooms")
	}
	select {
	case <-syncStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("connected client did not start Matrix sync")
	}

	if !loadedClient.IsLoggedIn() {
		t.Fatal("expected returned user login client to be connected")
	}

	loadedClient.Disconnect()
	disconnected = true
	select {
	case <-syncStopped:
	case <-time.After(2 * time.Second):
		t.Fatal("connected client did not stop Matrix sync after disconnect")
	}

	loginRequests := 0
	for {
		select {
		case path := <-requestCounts:
			if path == "/_matrix/client/v3/login" {
				loginRequests++
			}
		default:
			goto counted
		}
	}

counted:
	if loginRequests != 1 {
		t.Fatalf("expected one remote login request, got %d", loginRequests)
	}
}

func TestSubmitUserInputDisconnectsReplacedClientBeforeReconnect(t *testing.T) {
	const (
		remoteUser = id.UserID("@remote:example.org")
		deviceID   = id.DeviceID("TESTDEVICE")
	)

	var loginCalls atomic.Int32
	var versionsCalls atomic.Int32
	var syncCalls atomic.Int32
	syncStarted := make(chan struct{}, 2)
	oldSyncStopped := make(chan struct{}, 1)
	newSyncStopped := make(chan struct{}, 1)
	replacementPreflightAfterStop := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/login"):
			loginCalls.Add(1)
			writeJSON(t, w, map[string]any{
				"user_id":      remoteUser,
				"access_token": "test-access-token",
				"device_id":    deviceID,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/_matrix/client/versions":
			if versionsCalls.Add(1) == 2 {
				select {
				case <-oldSyncStopped:
					signalTestEvent(replacementPreflightAfterStop)
				case <-time.After(2 * time.Second):
					http.Error(w, "old sync was not stopped before replacement", http.StatusGatewayTimeout)
					return
				}
			}
			writeJSON(t, w, map[string]any{"versions": []string{"v1.11"}})
		case r.Method == http.MethodGet && r.URL.Path == "/_matrix/client/v1/media/config":
			writeJSON(t, w, map[string]any{"m.upload.size": 1024 * 1024})
		case r.Method == http.MethodGet && r.URL.Path == "/_matrix/client/v3/joined_rooms":
			writeJSON(t, w, map[string]any{"joined_rooms": []string{}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/filter"):
			writeJSON(t, w, map[string]any{"filter_id": "test-filter"})
		case r.Method == http.MethodGet && r.URL.Path == "/_matrix/client/v3/sync":
			call := syncCalls.Add(1)
			signalTestEvent(syncStarted)
			<-r.Context().Done()
			if call == 1 {
				signalTestEvent(oldSyncStopped)
			} else {
				signalTestEvent(newSyncStopped)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("LOCAL_MATRIX_HS", server.URL)
	t.Setenv("LOCAL_MATRIX_INSECURE_TLS", "false")

	_, user, connector := newLoginTestBridge(t)
	login := &SimpleLogin{User: user, Main: connector, Log: zerolog.Nop()}

	firstStep, err := login.SubmitUserInput(context.Background(), map[string]string{
		"username": "remote",
		"password": "password",
	})
	if err != nil {
		t.Fatalf("first SubmitUserInput returned an error: %v", err)
	}
	firstClient, ok := firstStep.CompleteParams.UserLogin.Client.(*MyNetworkClient)
	if !ok {
		t.Fatalf("expected first MyNetworkClient, got %T", firstStep.CompleteParams.UserLogin.Client)
	}
	defer firstClient.Disconnect()
	select {
	case <-syncStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first client did not start Matrix sync")
	}

	secondStep, err := login.SubmitUserInput(context.Background(), map[string]string{
		"username": "remote",
		"password": "password",
	})
	if err != nil {
		t.Fatalf("replacement SubmitUserInput returned an error: %v", err)
	}
	secondClient, ok := secondStep.CompleteParams.UserLogin.Client.(*MyNetworkClient)
	if !ok {
		t.Fatalf("expected replacement MyNetworkClient, got %T", secondStep.CompleteParams.UserLogin.Client)
	}
	if secondClient == firstClient {
		t.Fatal("replacement login reused the connected client")
	}
	defer secondClient.Disconnect()

	select {
	case <-replacementPreflightAfterStop:
	case <-time.After(2 * time.Second):
		t.Fatal("replacement preflight did not wait for the previous sync to stop")
	}
	if firstClient.IsLoggedIn() {
		t.Fatal("replaced client still reports an active login")
	}
	firstClient.Connect(context.Background())
	firstClient.connectMu.Lock()
	retired := firstClient.retired
	firstClientActive := firstClient.cancel != nil
	firstClient.connectMu.Unlock()
	if !retired || firstClientActive {
		t.Fatal("a stale client pointer can reconnect after replacement")
	}
	select {
	case <-syncStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("replacement client did not start Matrix sync")
	}
	if got := loginCalls.Load(); got != 2 {
		t.Fatalf("expected two remote login requests, got %d", got)
	}

	secondClient.Disconnect()
	select {
	case <-newSyncStopped:
	case <-time.After(2 * time.Second):
		t.Fatal("replacement client did not stop Matrix sync after disconnect")
	}
}

func TestStaleGenerationZeroRetryCannotConnect(t *testing.T) {
	client := &MyNetworkClient{
		reconnectGeneration: 1,
		loggedIn:            true,
	}
	staleGeneration := uint64(0)

	client.connect(context.Background(), &staleGeneration)

	if !client.loggedIn {
		t.Fatal("stale generation-zero retry changed the disconnected client's state")
	}
}

func TestStaleRetryDoesNotClearNewGenerationSchedule(t *testing.T) {
	client := &MyNetworkClient{reconnectScheduled: true}
	client.login = &bridgev2.UserLogin{UserLogin: &database.UserLogin{}}
	delayEntered := make(chan struct{})
	releaseDelay := make(chan struct{})
	workerDone := make(chan struct{})

	go func() {
		client.reconnectAfterRemoteSyncFailureWithDelay(func(int) time.Duration {
			close(delayEntered)
			<-releaseDelay
			return 0
		})
		close(workerDone)
	}()

	select {
	case <-delayEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("retry worker did not enter its delay")
	}

	client.Disconnect()
	client.connectMu.Lock()
	client.reconnectScheduled = true
	client.connectMu.Unlock()
	close(releaseDelay)

	select {
	case <-workerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("stale retry worker did not finish")
	}
	client.connectMu.Lock()
	scheduled := client.reconnectScheduled
	client.connectMu.Unlock()
	if !scheduled {
		t.Fatal("stale retry worker cleared the newer generation's retry schedule")
	}
}

func newLoginTestBridge(t *testing.T) (*bridgev2.Bridge, *bridgev2.User, *MyConnector) {
	t.Helper()
	db, err := dbutil.NewWithDialect("file:submit-login-replacement-test?mode=memory&cache=shared", "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	db.RawDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	connector := NewMyConnector(zerolog.Nop())
	matrixConfig := &bridgeconfig.Config{
		Homeserver: bridgeconfig.HomeserverConfig{
			Address: localHomeserverURL(),
			Domain:  "example.org",
		},
		AppService: bridgeconfig.AppserviceConfig{
			Bot:      bridgeconfig.BotUserConfig{Username: "bridgebot"},
			NoServer: true,
		},
	}
	bridge := bridgev2.NewBridge(
		networkid.BridgeID("login-replacement-test"),
		db,
		zerolog.Nop(),
		&matrixConfig.Bridge,
		matrix.NewConnector(matrixConfig),
		connector,
		commands.NewProcessor,
	)
	bridge.BackgroundCtx = context.Background()
	if err := bridge.DB.Upgrade(context.Background()); err != nil {
		t.Fatalf("failed to create bridge test schema: %v", err)
	}

	const localUser = id.UserID("@local:example.org")
	if err := bridge.DB.User.Insert(context.Background(), &database.User{
		BridgeID: bridge.ID,
		MXID:     localUser,
	}); err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}
	user, err := bridge.GetUserByMXID(context.Background(), localUser)
	if err != nil {
		t.Fatalf("failed to load test user: %v", err)
	}
	return bridge, user, connector
}

func signalTestEvent(ch chan<- struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("failed to write JSON response: %v", err)
	}
}
