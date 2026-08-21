package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// store writes an identity the way the coordinator keeps them and points
// XDG_STATE_HOME at it.
func store(t *testing.T, files map[string]string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "flywheel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XDG_STATE_HOME", root)
}

func TestIdentityPairsTheRecordedNameWithItsOwnToken(t *testing.T) {
	// blackbird ties a name to its FIRST token permanently, so the live store
	// holds a "-v2" identity after one was burned (fw-t7d). The recorded name
	// and the default token file belong to different identities, and pairing
	// them authenticates as nobody — which looks exactly like the
	// UNAUTHENTICATED that started all this.
	store(t, map[string]string{
		"r-builder.name":     "r/builder-v2\n",
		"r-builder.token":    "burned-v1-token\n",
		"r-builder-v2.token": "live-v2-token\n",
	})
	name, token, ok := blackbirdIdentity("r")
	if !ok {
		t.Fatal("found no identity where one is stored")
	}
	if name != "r/builder-v2" {
		t.Errorf("name = %q, want the recorded one", name)
	}
	if token != "live-v2-token" {
		t.Errorf("token = %q, want the one belonging to that name", token)
	}
}

func TestIdentityFallsBackToTheDefaultNameWhenNoneRecorded(t *testing.T) {
	store(t, map[string]string{"r-builder.token": "tok\n"})
	name, token, ok := blackbirdIdentity("r")
	if !ok || name != "r/builder" || token != "tok" {
		t.Errorf("got (%q, %q, %v), want (r/builder, tok, true)", name, token, ok)
	}
}

func TestRecordedNameWithoutItsTokenIsNoIdentity(t *testing.T) {
	// Returning the v1 token here would authenticate as nobody; returning "no
	// identity" makes the skill register a fresh name, which burns one more.
	// Both are bad, but only one of them is silent.
	store(t, map[string]string{
		"r-builder.name":  "r/builder-v2\n",
		"r-builder.token": "burned-v1-token\n",
	})
	if _, _, ok := blackbirdIdentity("r"); ok {
		t.Error("claimed an identity whose recorded name has no token of its own")
	}
}

func TestNoStoredIdentityIsNotAnError(t *testing.T) {
	// A repo the fleet has never run in has no identity yet, and the skill's
	// register-and-persist path is the right answer there.
	store(t, map[string]string{})
	if _, _, ok := blackbirdIdentity("never-run"); ok {
		t.Error("invented an identity for a repo that has none")
	}
	env := withIdentity([]string{"PATH=/bin"}, "never-run")
	if len(env) != 1 {
		t.Errorf("added credentials that do not exist: %v", env)
	}
}

func TestWithIdentityHandsTheBuilderBothHalves(t *testing.T) {
	store(t, map[string]string{"r-builder.token": "tok\n"})
	env := withIdentity(nil, "r")
	if !slices.Contains(env, "FLYWHEEL_BLACKBIRD_AGENT=r/builder") {
		t.Errorf("no agent name in env: %v", env)
	}
	if !slices.Contains(env, "FLYWHEEL_BLACKBIRD_TOKEN=tok") {
		t.Error("no token in env; the builder would go looking for the file the sandbox blocks")
	}
}

func TestIdentityIsTrimmed(t *testing.T) {
	// These files are written by shell redirection and by hand. A trailing
	// newline inside an auth header fails in a way nobody enjoys debugging.
	store(t, map[string]string{"r-builder.token": "  tok\n\n"})
	_, token, _ := blackbirdIdentity("r")
	if strings.TrimSpace(token) != token || token != "tok" {
		t.Errorf("token = %q, want it trimmed", token)
	}
}
