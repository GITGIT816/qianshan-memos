package billing

import (
	"fmt"
	"net/url"

	"vps-billing/internal/model"
)

// LinkConfig holds the connection details that don't belong to any one
// subscription but are needed to turn a subscription into a client-importable
// share link: where the node actually is, and how its TLS/Reality layer is
// set up. These come from the same config.json you already run Xray with.
type LinkConfig struct {
	Host        string // domain or IP clients connect to
	Port        int
	Security    string // "reality", "tls", or "none"
	SNI         string // reality/tls server name
	PublicKey   string // reality public key (pbk)
	ShortID     string // reality short id (sid)
	Fingerprint string // client TLS fingerprint, e.g. "chrome"
	Network     string // "tcp", "ws", "grpc", ...
	Flow        string // vless flow, e.g. "xtls-rprx-vision"
}

// Empty reports whether Host is unset, meaning link generation should be skipped.
func (c LinkConfig) Empty() bool { return c.Host == "" }

// BuildVLESSLink renders a vless:// share link for sub using cfg. Only VLESS
// is supported today, matching the protocol this tool provisions by default;
// other protocols would need their own URI shape (trojan://, ss://, ...).
func BuildVLESSLink(cfg LinkConfig, sub model.Subscription) (string, error) {
	if cfg.Empty() {
		return "", fmt.Errorf("no host configured (set -host or BILLING_HOST)")
	}
	if sub.UUID == "" {
		return "", fmt.Errorf("subscription has no uuid")
	}

	q := url.Values{}
	q.Set("encryption", "none")
	security := cfg.Security
	if security == "" {
		security = "reality"
	}
	q.Set("security", security)

	network := cfg.Network
	if network == "" {
		network = "tcp"
	}
	q.Set("type", network)

	if cfg.Flow != "" {
		q.Set("flow", cfg.Flow)
	}

	switch security {
	case "reality":
		if cfg.PublicKey == "" {
			return "", fmt.Errorf("security=reality requires a public key (set -pbk or BILLING_PUBLIC_KEY)")
		}
		q.Set("pbk", cfg.PublicKey)
		if cfg.SNI != "" {
			q.Set("sni", cfg.SNI)
		}
		if cfg.ShortID != "" {
			q.Set("sid", cfg.ShortID)
		}
		fp := cfg.Fingerprint
		if fp == "" {
			fp = "chrome"
		}
		q.Set("fp", fp)
	case "tls":
		if cfg.SNI != "" {
			q.Set("sni", cfg.SNI)
		}
		if cfg.Fingerprint != "" {
			q.Set("fp", cfg.Fingerprint)
		}
	case "none":
		// nothing extra
	default:
		return "", fmt.Errorf("unsupported security %q (want reality, tls, or none)", security)
	}

	port := cfg.Port
	if port == 0 {
		port = 443
	}

	u := url.URL{
		Scheme:   "vless",
		User:     url.User(sub.UUID),
		Host:     fmt.Sprintf("%s:%d", cfg.Host, port),
		RawQuery: q.Encode(),
		Fragment: sub.Email,
	}
	return u.String(), nil
}
