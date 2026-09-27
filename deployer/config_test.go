package main

import (
	"strings"
	"testing"
)

func TestLoadConfigAllowsFreshInstallationWithoutGiteaOrSecrets(t *testing.T) {
	for _, name := range []string{"GITEA_API_URL", "GITEA_PUBLIC_URL", "OAUTH_CLIENT_ID", "OAUTH_CLIENT_SECRET", "SESSION_SECRET_FILE", "TOKEN_ENCRYPTION_KEY_FILE", "OAUTH_CLIENT_SECRET_FILE"} {
		t.Setenv(name, "")
	}
	c, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.GiteaAPIURL != "" || c.OAuthClientID != "" || len(c.SessionSecret) != 0 || len(c.MetadataSigningKey) != 0 {
		t.Fatalf("unexpected bootstrap settings: %#v", c)
	}
}

func TestLoadConfigUsesOptionalGiteaDefaults(t *testing.T) {
	t.Setenv("GITEA_API_URL", "https://gitea.example.com")
	t.Setenv("GITEA_PUBLIC_URL", "https://git.example.com")
	t.Setenv("OAUTH_CLIENT_ID", "client")
	t.Setenv("OAUTH_CLIENT_SECRET", "secret")
	c, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.GiteaAPIURL != "https://gitea.example.com" || c.OAuthClientSecret != "secret" {
		t.Fatalf("defaults = %#v", c)
	}
}

func TestLoadConfigRejectsNonHTTPSGiteaOutsideDevelopment(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("GITEA_API_URL", "http://gitea.example.com")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("production URL must use HTTPS")
	}
}

func TestLoadConfigAllowsLocalHTTPGiteaOnlyInDevelopment(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("GITEA_API_URL", "http://gitea")
	if _, err := LoadConfig(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITEA_API_URL", "http://127.0.0.2:3000")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("nonlocal HTTP URL accepted")
	}
}

func TestLoadConfigRejectsHTTPPublicURLsInProduction(t *testing.T) {
	for _, name := range []string{"OAUTH_REDIRECT_URL", "WEBHOOK_PUBLIC_URL"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("APP_ENV", "production")
			t.Setenv(name, "http://localhost:8080/path")
			if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestLoadConfigEnablesOrganizationHooksByDefault(t *testing.T) {
	t.Setenv("ENABLE_ORGANIZATION_HOOKS", "")
	c, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !c.EnableOrganizationHooks {
		t.Fatal("organization hooks disabled by default")
	}
}
