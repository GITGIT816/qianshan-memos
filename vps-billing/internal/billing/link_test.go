package billing

import (
	"net/url"
	"strings"
	"testing"

	"vps-billing/internal/model"
)

func TestBuildVLESSLinkReality(t *testing.T) {
	cfg := LinkConfig{
		Host: "node.example.com", Port: 443, Security: "reality",
		SNI: "www.example.com", PublicKey: "pubkey123", ShortID: "ab12", Flow: "xtls-rprx-vision",
	}
	sub := model.Subscription{UUID: "11111111-1111-4111-8111-111111111111", Email: "alice@node"}

	link, err := BuildVLESSLink(cfg, sub)
	if err != nil {
		t.Fatalf("BuildVLESSLink: %v", err)
	}

	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("generated link is not a valid URL: %v (%s)", err, link)
	}
	if u.Scheme != "vless" {
		t.Errorf("scheme = %q, want vless", u.Scheme)
	}
	if u.User.Username() != sub.UUID {
		t.Errorf("userinfo = %q, want %q", u.User.Username(), sub.UUID)
	}
	if u.Host != "node.example.com:443" {
		t.Errorf("host = %q", u.Host)
	}
	if u.Fragment != "alice@node" {
		t.Errorf("fragment = %q, want alice@node", u.Fragment)
	}

	q := u.Query()
	for k, want := range map[string]string{
		"encryption": "none",
		"security":   "reality",
		"type":       "tcp",
		"flow":       "xtls-rprx-vision",
		"pbk":        "pubkey123",
		"sni":        "www.example.com",
		"sid":        "ab12",
		"fp":         "chrome", // default when unset
	} {
		if got := q.Get(k); got != want {
			t.Errorf("query[%s] = %q, want %q", k, got, want)
		}
	}
}

func TestBuildVLESSLinkErrors(t *testing.T) {
	sub := model.Subscription{UUID: "u", Email: "e@node"}

	if _, err := BuildVLESSLink(LinkConfig{}, sub); err == nil {
		t.Error("expected error when Host is unset")
	}

	_, err := BuildVLESSLink(LinkConfig{Host: "h", Security: "reality"}, sub)
	if err == nil || !strings.Contains(err.Error(), "public key") {
		t.Errorf("expected public key error for reality without pbk, got %v", err)
	}

	if _, err := BuildVLESSLink(LinkConfig{Host: "h", Security: "bogus"}, sub); err == nil {
		t.Error("expected error for unsupported security")
	}
}

func TestBuildVLESSLinkNoUUID(t *testing.T) {
	cfg := LinkConfig{Host: "h", Security: "none"}
	if _, err := BuildVLESSLink(cfg, model.Subscription{Email: "e@node"}); err == nil {
		t.Error("expected error when subscription has no uuid")
	}
}
