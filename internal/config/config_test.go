package config

import "testing"

// A panel upgraded from the days of one token has its credential in the old
// field and a library full of plugins that name no token at all. Those plugins
// resolve to the head of the list, so that is where the old token has to land.
func TestTheSingleTokenBecomesTheDefaultOfTheList(t *testing.T) {
	panel := Panel{GitHubToken: "ghp_old"}
	panel.ApplyDefaults()

	if len(panel.GitHubTokens) != 1 {
		t.Fatalf("expected the token to be migrated: %+v", panel.GitHubTokens)
	}
	if panel.GitHubTokens[0].Token != "ghp_old" || panel.GitHubTokens[0].ID != LegacyTokenID {
		t.Fatalf("unexpected migrated token: %+v", panel.GitHubTokens[0])
	}
	// Emptied, so reading and writing the config again cannot produce a second
	// copy of the same credential.
	if panel.GitHubToken != "" {
		t.Errorf("the old field should have been cleared: %q", panel.GitHubToken)
	}

	// Idempotent: a config that still carries the old field beside a list it has
	// already been folded into gains nothing on the next load.
	again := Panel{GitHubToken: "ghp_old", GitHubTokens: panel.GitHubTokens}
	again.ApplyDefaults()
	if len(again.GitHubTokens) != 1 {
		t.Fatalf("the migration ran twice: %+v", again.GitHubTokens)
	}
}

func TestApplyDefaultsLeavesAnExistingTokenListAlone(t *testing.T) {
	panel := Panel{GitHubTokens: []GitHubToken{
		{ID: "a", Name: "我的私库", Token: "ghp_a"},
		{ID: "b", Name: "公司 org", Token: "ghp_b"},
	}}
	panel.ApplyDefaults()

	if len(panel.GitHubTokens) != 2 || panel.GitHubTokens[0].ID != "a" {
		t.Fatalf("the stored order is the default order: %+v", panel.GitHubTokens)
	}
}
