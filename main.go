package main

import (
	"fmt"
	"os"

	"maunium.net/go/mautrix/bridgev2/matrix/mxmain"

	"github.com/Martin-Hausleitner/beeper-matrix-proxy/connector"
)

var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

const legacyAppserviceEnableEnv = "BEEPER_LEGACY_APPSERVICE_ENABLE"

func main() {
	if !legacyAppserviceEnabled() {
		fmt.Fprintf(os.Stderr, "Legacy BIPA/bridgev2 startup is disabled. Set %s=1 only for an intentional legacy bridge run.\n", legacyAppserviceEnableEnv)
		os.Exit(64)
	}

	connector := &connector.MyConnector{}

	m := mxmain.BridgeMain{
		// Keep the internal mxmain name stable for existing installations:
		// mautrix uses it as the database owner key ("megabridge/<name>").
		// The public bridge/network identity is exposed by the connector.
		Name:        "minibridge",
		Description: "A Matrix-to-Beeper bridgev2 proxy for private Matrix homeservers.",
		Version:     "0.1.0",
		URL:         "https://github.com/Martin-Hausleitner/beeper-matrix-proxy",
		Connector:   connector,
	}

	m.InitVersion(Tag, Commit, BuildTime)
	m.Run()
}

func legacyAppserviceEnabled() bool {
	return os.Getenv(legacyAppserviceEnableEnv) == "1"
}
