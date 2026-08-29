package model

import (
	"testing"
	"time"
)

func TestSubscriptionIsExpired(t *testing.T) {
	now := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		expiry time.Time
		want   bool
	}{
		{"before expiry", now.Add(time.Hour), false},
		{"exactly at expiry", now, false},
		{"after expiry", now.Add(-time.Second), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sub := Subscription{ExpiresAt: c.expiry}
			if got := sub.IsExpired(now); got != c.want {
				t.Errorf("IsExpired() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestSubscriptionIsOverQuota(t *testing.T) {
	cases := []struct {
		name  string
		limit int64
		used  int64
		want  bool
	}{
		{"under limit", 100, 50, false},
		{"exactly at limit", 100, 100, true},
		{"over limit", 100, 101, true},
		{"zero limit means unlimited", 0, 1_000_000, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sub := Subscription{TrafficLimitBytes: c.limit, TrafficUsedBytes: c.used}
			if got := sub.IsOverQuota(); got != c.want {
				t.Errorf("IsOverQuota() = %v, want %v", got, c.want)
			}
		})
	}
}
