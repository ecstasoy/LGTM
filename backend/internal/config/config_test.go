package config

import "testing"

func TestLoad_RedirectDefaultsFromPublicBaseURL(t *testing.T) {
	t.Setenv("PUBLIC_BASE_URL", "https://lgtm.example.com/")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicBaseURL != "https://lgtm.example.com" {
		t.Errorf("PublicBaseURL = %q, want trailing slash trimmed", cfg.PublicBaseURL)
	}
	if want := "https://lgtm.example.com/api/auth/github/callback"; cfg.GithubOAuthRedirectURI != want {
		t.Errorf("redirect = %q, want %q", cfg.GithubOAuthRedirectURI, want)
	}
}

func TestLoad_ExplicitRedirectWins(t *testing.T) {
	t.Setenv("PUBLIC_BASE_URL", "https://lgtm.example.com")
	t.Setenv("GITHUB_OAUTH_REDIRECT_URI", "http://localhost:3000/api/auth/github/callback")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GithubOAuthRedirectURI != "http://localhost:3000/api/auth/github/callback" {
		t.Errorf("explicit redirect overridden: %q", cfg.GithubOAuthRedirectURI)
	}
}

func TestLoad_DefaultPublicBaseURL(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicBaseURL != DefaultPublicBaseURL {
		t.Errorf("PublicBaseURL = %q, want %q", cfg.PublicBaseURL, DefaultPublicBaseURL)
	}
}

// A bad value would put broken links into every bot comment, so it must fail at startup.
func TestLoad_RejectsMalformedPublicBaseURL(t *testing.T) {
	for _, v := range []string{"lgtm.example.com", "ftp://lgtm.example.com", "https://", "https://lgtm.example.com/app"} {
		t.Setenv("PUBLIC_BASE_URL", v)
		if _, err := Load(); err == nil {
			t.Errorf("PUBLIC_BASE_URL=%q should be rejected", v)
		}
	}
}

// Webhook reviews must be bounded out of the box; a deployment that sets nothing still gets limits.
func TestLoad_WebhookLimitsHaveDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebhookMaxConcurrentReviews != 4 || cfg.WebhookReviewsPerInstallationHour != 30 || cfg.WebhookReviewsPerRepoHour != 15 {
		t.Errorf("defaults = %d/%d/%d, want 4/30/15",
			cfg.WebhookMaxConcurrentReviews, cfg.WebhookReviewsPerInstallationHour, cfg.WebhookReviewsPerRepoHour)
	}
}

func TestLoad_WebhookLimitsOverride(t *testing.T) {
	t.Setenv("WEBHOOK_MAX_CONCURRENT_REVIEWS", "2")
	t.Setenv("WEBHOOK_REVIEWS_PER_INSTALLATION_HOUR", "0")
	t.Setenv("WEBHOOK_REVIEWS_PER_REPO_HOUR", "5")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebhookMaxConcurrentReviews != 2 || cfg.WebhookReviewsPerInstallationHour != 0 || cfg.WebhookReviewsPerRepoHour != 5 {
		t.Errorf("overrides = %d/%d/%d, want 2/0/5",
			cfg.WebhookMaxConcurrentReviews, cfg.WebhookReviewsPerInstallationHour, cfg.WebhookReviewsPerRepoHour)
	}
}
