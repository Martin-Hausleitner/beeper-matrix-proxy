package main

import "testing"

func TestLegacyAppserviceEnabledDefaultsOff(t *testing.T) {
	t.Setenv(legacyAppserviceEnableEnv, "")

	if legacyAppserviceEnabled() {
		t.Fatal("legacy BIPA/bridgev2 appservice should default to disabled")
	}
}

func TestLegacyAppserviceEnabledRequiresExplicitOne(t *testing.T) {
	t.Setenv(legacyAppserviceEnableEnv, "true")

	if legacyAppserviceEnabled() {
		t.Fatal("legacy BIPA/bridgev2 appservice should only enable with exact value 1")
	}

	t.Setenv(legacyAppserviceEnableEnv, "1")
	if !legacyAppserviceEnabled() {
		t.Fatal("legacy BIPA/bridgev2 appservice should enable when explicitly set to 1")
	}
}
