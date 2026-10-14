package connector

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
)

const (
	LoginFlowIDUsernamePassword = "user-pass"
	LoginStepIDUsernamePassword = "user-pass-input"
	LoginStepIDComplete         = "complete"
)

// SimpleLogin represents an ongoing username/password login attempt.
type SimpleLogin struct {
	User *bridgev2.User
	Main *MyConnector
	Log  zerolog.Logger
}

// Ensure SimpleLogin implements the required interface.
var _ bridgev2.LoginProcessUserInput = (*SimpleLogin)(nil)

// Start implements bridgev2.LoginProcessUserInput.
func (sl *SimpleLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	sl.Log.Debug().Msg("Starting username/password login flow")
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       LoginStepIDUsernamePassword,
		Instructions: "Enter your username and password for the remote Matrix homeserver.",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{
				{
					Type: bridgev2.LoginInputFieldTypeUsername,
					ID:   "username",
					Name: "Username",
				},
				{
					Type: bridgev2.LoginInputFieldTypePassword,
					ID:   "password",
					Name: "Password",
				},
			},
		},
	}, nil
}

// SubmitUserInput implements bridgev2.LoginProcessUserInput.
func (sl *SimpleLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	username := input["username"]
	password := input["password"]

	if username == "" {
		return nil, fmt.Errorf("username cannot be empty")
	}
	if password == "" {
		return nil, fmt.Errorf("password cannot be empty")
	}

	cli, err := newLocalMatrixClient("", "")
	if err != nil {
		return nil, err
	}
	loginResp, err := cli.Login(ctx, &mautrix.ReqLogin{
		Type: mautrix.AuthTypePassword,
		Identifier: mautrix.UserIdentifier{
			Type: "m.id.user",
			User: username,
		},
		Password:                 password,
		InitialDeviceDisplayName: "Beeper Matrix Proxy",
		StoreCredentials:         true,
		StoreHomeserverURL:       true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to log in to remote Matrix: %w", err)
	}

	namespace := uuid.MustParse("f7a4f3e3-5d5a-4a9e-8d8a-3b0b9e8a1b2c")
	loginIDStr := uuid.NewSHA1(namespace, []byte(strings.ToLower(loginResp.UserID.String()))).String()
	var loginID networkid.UserLoginID = networkid.UserLoginID(loginIDStr)

	ul, err := sl.User.NewLogin(ctx, &database.UserLogin{
		ID:         loginID,
		RemoteName: loginResp.UserID.String(),
		RemoteProfile: status.RemoteProfile{
			Name: loginResp.UserID.String(),
		},
		Metadata: &LoginMetadata{
			RemoteUserID:  loginResp.UserID.String(),
			UserID:        loginResp.UserID.String(),
			HomeserverURL: localHomeserverURL(),
			AccessToken:   loginResp.AccessToken,
			DeviceID:      string(loginResp.DeviceID),
		},
	}, &bridgev2.NewLoginParams{
		DeleteOnConflict: false,
	})
	if err != nil {
		sl.Log.Err(err).Msg("Failed to create user login entry")
		return nil, fmt.Errorf("failed to create user login: %w", err)
	}

	sl.Log.Info().Str("login_id", string(ul.ID)).Msg("Successfully 'logged in' and created user login")
	ul.Client.Connect(ctx)

	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeComplete,
		StepID:       LoginStepIDComplete,
		Instructions: fmt.Sprintf("Successfully logged in as '%s'", loginResp.UserID.String()),
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLoginID: ul.ID,
			UserLogin:   ul,
		},
	}, nil
}

// Cancel implements bridgev2.LoginProcessUserInput.
func (sl *SimpleLogin) Cancel() {
	sl.Log.Debug().Msg("Login process cancelled")
}
