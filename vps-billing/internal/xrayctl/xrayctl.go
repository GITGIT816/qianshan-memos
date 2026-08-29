// Package xrayctl drives the running Xray-core process through its gRPC API
// by shelling out to the `xray api` CLI that ships with every Xray build.
// This avoids vendoring xray-core's protobuf stubs just to add/remove users
// and read traffic counters.
//
// Prerequisites on the VPS: Xray's config.json must enable the API
// (HandlerService + StatsService), per-user stats in policy, and a routing
// rule sending the api inbound's traffic to the api outbound. See
// ../../configs/xray.config.example.json.
package xrayctl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Client shells out to the `xray` binary's `api` subcommands against one
// running Xray-core instance.
type Client struct {
	// BinPath is the path to the xray executable, e.g. "/usr/local/bin/xray".
	BinPath string
	// Server is the gRPC API listen address configured in Xray's "api" block,
	// e.g. "127.0.0.1:10085".
	Server string
	// Timeout bounds each `xray api` invocation.
	Timeout time.Duration
}

// NewClient returns a Client with a sane default timeout.
func NewClient(binPath, server string) *Client {
	return &Client{BinPath: binPath, Server: server, Timeout: 5 * time.Second}
}

func (c *Client) run(ctx context.Context, args ...string) (stdout string, err error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.BinPath, args...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("xray %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out.String(), nil
}

// --- adding / removing users -----------------------------------------------

// xrayClient mirrors one entry of an inbound's "settings.clients" array. Only
// the fields relevant to the protocol in use need to be non-empty.
type xrayClient struct {
	ID       string `json:"id,omitempty"`       // vless / vmess / shadowsocks2022
	Password string `json:"password,omitempty"` // trojan / shadowsocks
	Email    string `json:"email"`
	Flow     string `json:"flow,omitempty"` // vless, must match the inbound's expected flow
	Level    int    `json:"level"`
}

type xrayInboundFile struct {
	Inbounds []struct {
		Tag      string `json:"tag"`
		Protocol string `json:"protocol"`
		Settings struct {
			Clients []xrayClient `json:"clients"`
		} `json:"settings"`
	} `json:"inbounds"`
}

// NewUser describes a client to add to an inbound.
type NewUser struct {
	Protocol string // "vless", "trojan", "vmess", "shadowsocks", "shadowsocks2022"
	Email    string
	UUID     string // used as id (vless/vmess/ss2022) or password (trojan)
	Flow     string // vless only; leave empty unless the inbound requires it
}

// AddUser adds a client to the given inbound tag via `xray api adu`. It is
// idempotent from the caller's perspective: Reconcile in the billing package
// only calls this for emails not already present on the inbound.
func (c *Client) AddUser(ctx context.Context, inboundTag string, u NewUser) error {
	client := xrayClient{Email: u.Email, Level: 0}
	switch u.Protocol {
	case "trojan":
		client.Password = u.UUID
	default: // vless, vmess, shadowsocks2022 all key off "id"
		client.ID = u.UUID
		client.Flow = u.Flow
	}

	var file xrayInboundFile
	file.Inbounds = []struct {
		Tag      string `json:"tag"`
		Protocol string `json:"protocol"`
		Settings struct {
			Clients []xrayClient `json:"clients"`
		} `json:"settings"`
	}{{
		Tag:      inboundTag,
		Protocol: u.Protocol,
		Settings: struct {
			Clients []xrayClient `json:"clients"`
		}{Clients: []xrayClient{client}},
	}}

	tmp, err := os.CreateTemp("", "vps-billing-adu-*.json")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := json.NewEncoder(tmp).Encode(file); err != nil {
		tmp.Close()
		return fmt.Errorf("encode adu payload: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	_, err = c.run(ctx, "api", "adu", "--server="+c.Server, tmp.Name())
	return err
}

// RemoveUser removes a client (by email) from the given inbound via `xray api rmu`.
func (c *Client) RemoveUser(ctx context.Context, inboundTag, email string) error {
	_, err := c.run(ctx, "api", "rmu", "--server="+c.Server, "-tag="+inboundTag, email)
	return err
}

// inboundUsersResponse mirrors `xray api inbounduser`'s JSON output closely
// enough to pull out the list of emails currently configured on an inbound.
type inboundUsersResponse struct {
	Response struct {
		Users []struct {
			Email string `json:"Email"`
		} `json:"users"`
	} `json:"Response"`
	// Some Xray versions emit the users array at the top level instead of
	// nested under Response; handle both shapes defensively.
	Users []struct {
		Email string `json:"Email"`
	} `json:"users"`
}

// ListInboundUsers returns the emails currently present on inboundTag,
// according to the live Xray process (not this tool's database). Used to
// reconcile after a restart, since users added only via the API are not
// persisted to config.json and vanish on restart.
func (c *Client) ListInboundUsers(ctx context.Context, inboundTag string) ([]string, error) {
	out, err := c.run(ctx, "api", "inbounduser", "--server="+c.Server, "-tag="+inboundTag)
	if err != nil {
		return nil, err
	}
	var resp inboundUsersResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return nil, fmt.Errorf("parse inbounduser output: %w (raw: %s)", err, out)
	}
	emails := make([]string, 0)
	for _, u := range resp.Response.Users {
		if u.Email != "" {
			emails = append(emails, u.Email)
		}
	}
	for _, u := range resp.Users {
		if u.Email != "" {
			emails = append(emails, u.Email)
		}
	}
	return emails, nil
}

// --- traffic accounting ------------------------------------------------------

type statsQueryResponse struct {
	Stat []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"stat"`
}

// UserTraffic reports the uplink/downlink byte counters accumulated since the
// last reset for one user.
type UserTraffic struct {
	UplinkBytes   int64
	DownlinkBytes int64
}

// QueryUserTraffic reads a user's traffic counters via `xray api statsquery`.
// When reset is true, Xray zeroes the counters after reporting them, so the
// caller should treat the result as a delta to accumulate, not an absolute
// total. This requires per-user stats to be enabled in Xray's policy config
// (statsUserUplink / statsUserDownlink).
func (c *Client) QueryUserTraffic(ctx context.Context, email string, reset bool) (UserTraffic, error) {
	args := []string{"api", "statsquery", "--server=" + c.Server, "-pattern", "user>>>" + email + ">>>traffic"}
	if reset {
		args = append(args, "-reset")
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return UserTraffic{}, err
	}
	var resp statsQueryResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return UserTraffic{}, fmt.Errorf("parse statsquery output: %w (raw: %s)", err, out)
	}
	var t UserTraffic
	for _, s := range resp.Stat {
		v, perr := strconv.ParseInt(s.Value, 10, 64)
		if perr != nil {
			continue
		}
		switch {
		case strings.HasSuffix(s.Name, ">>>uplink"):
			t.UplinkBytes = v
		case strings.HasSuffix(s.Name, ">>>downlink"):
			t.DownlinkBytes = v
		}
	}
	return t, nil
}

// --- device count (best-effort) ---------------------------------------------

// OnlineIPCount returns the number of distinct source IPs `xray api
// statsonlineiplist` currently reports for a user, as a proxy for concurrent
// device count.
//
// This API is newer/less stable across Xray versions than statsquery or
// adu/rmu. If the response shape doesn't match what this parses, ok is false
// and callers should treat device enforcement as unavailable rather than
// failing the whole sync — verify locally with:
//
//	xray api statsonlineiplist --server=127.0.0.1:10085 -email=someone@node
func (c *Client) OnlineIPCount(ctx context.Context, email string) (count int, ok bool, err error) {
	out, err := c.run(ctx, "api", "statsonlineiplist", "--server="+c.Server, "-email="+email)
	if err != nil {
		return 0, false, err
	}
	// Try the plain shape: {"1.2.3.4": "<timestamp>", ...}
	var flat map[string]any
	if err := json.Unmarshal([]byte(out), &flat); err == nil && len(flat) >= 0 {
		return len(flat), true, nil
	}
	// Try a wrapped shape: {"Response"/"ips": {...}}. Fall through to
	// "unavailable" rather than guessing further.
	return 0, false, nil
}
