package admin

import (
	"testing"

	madmin "github.com/buckit-io/madmin-go/v3"
)

func TestMapServerInfo_RecoversConsoleEnvVars(t *testing.T) {
	msg := &madmin.InfoMessage{
		Servers: []madmin.ServerProperties{
			{
				Endpoint: "node1:9000",
				Version:  "buckit",
				MinioEnvVars: map[string]string{
					"MINIO_CONSOLE_ADDRESS":      ":9007",
					"MINIO_BROWSER_REDIRECT_URL": "https://console.example.com",
				},
			},
			{Endpoint: "node2:9000"},
		},
	}

	out := mapServerInfo(msg)
	if out.ConsoleAddress != ":9007" {
		t.Errorf("ConsoleAddress = %q, want %q", out.ConsoleAddress, ":9007")
	}
	if out.BrowserRedirectURL != "https://console.example.com" {
		t.Errorf("BrowserRedirectURL = %q, want %q", out.BrowserRedirectURL, "https://console.example.com")
	}
}

func TestMapServerInfo_NoConsoleEnvVars(t *testing.T) {
	msg := &madmin.InfoMessage{
		Servers: []madmin.ServerProperties{{Endpoint: "node1:9000"}},
	}
	out := mapServerInfo(msg)
	if out.ConsoleAddress != "" || out.BrowserRedirectURL != "" {
		t.Errorf("expected empty console env, got addr=%q redirect=%q", out.ConsoleAddress, out.BrowserRedirectURL)
	}
}
