// Package model defines the domain types shared across the billing tool.
package model

import "time"

// Status is the lifecycle state of a Subscription.
type Status string

const (
	StatusActive    Status = "active"
	StatusSuspended Status = "suspended"
)

// Plan is a purchasable tier, e.g. the 轻量/标准/重度 tiers from the pricing page.
type Plan struct {
	ID           int64
	Name         string
	PriceCents   int64 // price in 分 (1/100 CNY), avoids float rounding
	TrafficBytes int64 // quota per billing cycle
	DurationDays int   // validity length of one purchase, in days
	DeviceLimit  int   // soft cap on concurrent distinct source IPs
	CreatedAt    time.Time
}

// Customer is a person the node owner is granting access to (family/friends).
type Customer struct {
	ID        int64
	Name      string
	Contact   string
	CreatedAt time.Time
}

// Subscription binds a Customer to a Plan and to one Xray inbound user
// (identified by Email/UUID). This is the row the sync loop reconciles
// against the live Xray process.
type Subscription struct {
	ID                int64
	CustomerID        int64
	PlanID            int64
	Email             string // Xray user identifier, e.g. "alice@node"
	UUID              string // VLESS client id
	InboundTag        string // which Xray inbound this user belongs to
	Status            Status
	SuspendReason     string
	TrafficLimitBytes int64
	TrafficUsedBytes  int64
	DeviceLimit       int
	LastSeenDevices   int
	StartsAt          time.Time
	ExpiresAt         time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// IsExpired reports whether the subscription's validity window has passed at t.
func (s Subscription) IsExpired(t time.Time) bool {
	return t.After(s.ExpiresAt)
}

// IsOverQuota reports whether accumulated usage has reached the plan's traffic limit.
func (s Subscription) IsOverQuota() bool {
	return s.TrafficLimitBytes > 0 && s.TrafficUsedBytes >= s.TrafficLimitBytes
}
