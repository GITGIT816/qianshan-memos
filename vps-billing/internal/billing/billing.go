// Package billing implements the use cases that turn a "plan + customer"
// decision into an actual Xray user, and keeps live Xray state in sync with
// what the database says each subscription is entitled to.
package billing

import (
	"context"
	"fmt"
	"log"
	"time"

	"vps-billing/internal/model"
	"vps-billing/internal/store"
	"vps-billing/internal/xrayctl"
)

// Config holds the knobs that affect billing decisions but aren't part of
// any single plan or subscription.
type Config struct {
	// Protocol is the Xray protocol new subscriptions are provisioned on
	// ("vless", "trojan", "vmess", "shadowsocks2022").
	Protocol string
	// Flow is passed through to vless clients; leave empty unless the
	// inbound requires a specific flow (e.g. "xtls-rprx-vision").
	Flow string
	// EnforceDeviceLimit, when true, suspends a subscription as soon as
	// OnlineIPCount reports more distinct IPs than its plan's device limit.
	// Off by default because that API's response shape is less stable
	// across Xray versions than the traffic/user-management ones — see
	// xrayctl.Client.OnlineIPCount.
	EnforceDeviceLimit bool
}

// Service wires the database to the live Xray process.
type Service struct {
	store  *store.Store
	xray   *xrayctl.Client
	config Config
}

// NewService builds a Service.
func NewService(st *store.Store, xray *xrayctl.Client, cfg Config) *Service {
	if cfg.Protocol == "" {
		cfg.Protocol = "vless"
	}
	return &Service{store: st, xray: xray, config: cfg}
}

// CreateSubscription provisions a new Xray user for customerID on planID and
// records it as active. email must be unique (e.g. "alice@yourdomain") and
// is what shows up in Xray logs/stats and in the client's share link.
func (s *Service) CreateSubscription(ctx context.Context, customerID, planID int64, email, inboundTag string) (model.Subscription, error) {
	plan, err := s.store.GetPlan(planID)
	if err != nil {
		return model.Subscription{}, fmt.Errorf("look up plan: %w", err)
	}
	if _, err := s.store.GetSubscriptionByEmail(email); err == nil {
		return model.Subscription{}, fmt.Errorf("email %q is already in use by a subscription", email)
	}

	uuid, err := newUUIDv4()
	if err != nil {
		return model.Subscription{}, err
	}

	now := time.Now()
	sub := model.Subscription{
		CustomerID:        customerID,
		PlanID:            planID,
		Email:             email,
		UUID:              uuid,
		InboundTag:        inboundTag,
		Status:            model.StatusActive,
		TrafficLimitBytes: plan.TrafficBytes,
		DeviceLimit:       plan.DeviceLimit,
		StartsAt:          now,
		ExpiresAt:         now.AddDate(0, 0, plan.DurationDays),
	}

	id, err := s.store.CreateSubscription(sub)
	if err != nil {
		return model.Subscription{}, err
	}
	sub.ID = id

	if err := s.xray.AddUser(ctx, inboundTag, xrayctl.NewUser{
		Protocol: s.config.Protocol,
		Email:    email,
		UUID:     uuid,
		Flow:     s.config.Flow,
	}); err != nil {
		return model.Subscription{}, fmt.Errorf("add user to xray (subscription %d was still recorded, run reconcile to retry): %w", id, err)
	}

	return sub, nil
}

// Renew extends a subscription by its plan's duration from whichever is
// later, now or its current expiry, resets usage to zero, and re-adds the
// user to Xray if it had been suspended.
func (s *Service) Renew(ctx context.Context, subID int64) (model.Subscription, error) {
	sub, err := s.store.GetSubscription(subID)
	if err != nil {
		return model.Subscription{}, err
	}
	plan, err := s.store.GetPlan(sub.PlanID)
	if err != nil {
		return model.Subscription{}, fmt.Errorf("look up plan: %w", err)
	}

	base := time.Now()
	if sub.ExpiresAt.After(base) {
		base = sub.ExpiresAt
	}
	sub.ExpiresAt = base.AddDate(0, 0, plan.DurationDays)
	sub.TrafficUsedBytes = 0
	sub.TrafficLimitBytes = plan.TrafficBytes
	sub.DeviceLimit = plan.DeviceLimit

	wasSuspended := sub.Status == model.StatusSuspended
	sub.Status = model.StatusActive
	sub.SuspendReason = ""

	if wasSuspended {
		if err := s.xray.AddUser(ctx, sub.InboundTag, xrayctl.NewUser{
			Protocol: s.config.Protocol,
			Email:    sub.Email,
			UUID:     sub.UUID,
			Flow:     s.config.Flow,
		}); err != nil {
			return model.Subscription{}, fmt.Errorf("re-add user to xray: %w", err)
		}
	}

	if err := s.store.UpdateSubscription(sub); err != nil {
		return model.Subscription{}, err
	}
	return sub, nil
}

// Suspend cuts a subscription off from Xray immediately and records why.
func (s *Service) Suspend(ctx context.Context, subID int64, reason string) error {
	sub, err := s.store.GetSubscription(subID)
	if err != nil {
		return err
	}
	if sub.Status == model.StatusSuspended {
		return nil
	}
	if err := s.xray.RemoveUser(ctx, sub.InboundTag, sub.Email); err != nil {
		return fmt.Errorf("remove user from xray: %w", err)
	}
	sub.Status = model.StatusSuspended
	sub.SuspendReason = reason
	return s.store.UpdateSubscription(sub)
}

// Resume manually re-activates a suspended subscription without changing its
// expiry or usage — use Renew instead for the normal "customer paid again" flow.
func (s *Service) Resume(ctx context.Context, subID int64) error {
	sub, err := s.store.GetSubscription(subID)
	if err != nil {
		return err
	}
	if sub.Status == model.StatusActive {
		return nil
	}
	if err := s.xray.AddUser(ctx, sub.InboundTag, xrayctl.NewUser{
		Protocol: s.config.Protocol,
		Email:    sub.Email,
		UUID:     sub.UUID,
		Flow:     s.config.Flow,
	}); err != nil {
		return fmt.Errorf("add user to xray: %w", err)
	}
	sub.Status = model.StatusActive
	sub.SuspendReason = ""
	return s.store.UpdateSubscription(sub)
}

// SyncReport summarizes one SyncOnce run.
type SyncReport struct {
	Checked       int
	Suspended     []model.Subscription
	Reconciled    int // users added or removed on Xray to match the DB
	DeviceWarn    []model.Subscription
	QueryFailures []error
}

// SyncOnce pulls traffic deltas for every active subscription, accumulates
// them, suspends anything that is now over quota or past its expiry, checks
// device counts, and reconciles Xray's live user list against the database
// (self-healing after an Xray restart, since API-added users don't persist
// to config.json). Intended to be called on a timer by `billingctl sync`.
func (s *Service) SyncOnce(ctx context.Context) (SyncReport, error) {
	var report SyncReport

	subs, err := s.store.ListActiveSubscriptions()
	if err != nil {
		return report, fmt.Errorf("list active subscriptions: %w", err)
	}
	now := time.Now()

	for _, sub := range subs {
		report.Checked++

		traffic, err := s.xray.QueryUserTraffic(ctx, sub.Email, true)
		if err != nil {
			report.QueryFailures = append(report.QueryFailures, fmt.Errorf("%s: %w", sub.Email, err))
			// Traffic query failing (e.g. user not yet live on Xray) shouldn't
			// block expiry enforcement below, so fall through with a zero delta.
		} else {
			sub.TrafficUsedBytes += traffic.UplinkBytes + traffic.DownlinkBytes
		}

		// Device count is always recorded for visibility; only
		// config.EnforceDeviceLimit turns it into a suspension below.
		if count, ok, err := s.xray.OnlineIPCount(ctx, sub.Email); err == nil && ok {
			sub.LastSeenDevices = count
			if sub.DeviceLimit > 0 && count > sub.DeviceLimit {
				report.DeviceWarn = append(report.DeviceWarn, sub)
			}
		}

		suspendReason := ""
		if sub.IsExpired(now) {
			suspendReason = "已过期"
		} else if sub.IsOverQuota() {
			suspendReason = "流量超限"
		} else if s.config.EnforceDeviceLimit && sub.DeviceLimit > 0 && sub.LastSeenDevices > sub.DeviceLimit {
			suspendReason = "设备数超限"
		}

		if suspendReason != "" {
			if err := s.xray.RemoveUser(ctx, sub.InboundTag, sub.Email); err != nil {
				log.Printf("sync: suspend %s: remove from xray failed: %v", sub.Email, err)
			}
			sub.Status = model.StatusSuspended
			sub.SuspendReason = suspendReason
			report.Suspended = append(report.Suspended, sub)
		}

		if err := s.store.UpdateSubscription(sub); err != nil {
			return report, fmt.Errorf("persist subscription %d: %w", sub.ID, err)
		}
	}

	reconciled, err := s.reconcileInbounds(ctx)
	report.Reconciled = reconciled
	if err != nil {
		return report, fmt.Errorf("reconcile: %w", err)
	}
	return report, nil
}

// reconcileInbounds makes each inbound's live Xray user list match the set of
// currently-active subscriptions for that tag.
func (s *Service) reconcileInbounds(ctx context.Context) (int, error) {
	subs, err := s.store.ListActiveSubscriptions()
	if err != nil {
		return 0, err
	}

	byTag := make(map[string][]model.Subscription)
	for _, sub := range subs {
		byTag[sub.InboundTag] = append(byTag[sub.InboundTag], sub)
	}

	changed := 0
	for tag, desired := range byTag {
		desiredByEmail := make(map[string]model.Subscription, len(desired))
		for _, sub := range desired {
			desiredByEmail[sub.Email] = sub
		}

		current, err := s.xray.ListInboundUsers(ctx, tag)
		if err != nil {
			return changed, fmt.Errorf("list current users on %q: %w", tag, err)
		}
		currentSet := make(map[string]bool, len(current))
		for _, e := range current {
			currentSet[e] = true
		}

		for email, sub := range desiredByEmail {
			if currentSet[email] {
				continue
			}
			if err := s.xray.AddUser(ctx, tag, xrayctl.NewUser{
				Protocol: s.config.Protocol,
				Email:    sub.Email,
				UUID:     sub.UUID,
				Flow:     s.config.Flow,
			}); err != nil {
				log.Printf("reconcile: add %s to %s failed: %v", email, tag, err)
				continue
			}
			changed++
		}

		for _, email := range current {
			if desiredByEmail[email].Email != "" {
				continue
			}
			// Present on Xray but not an active subscription in the DB
			// (suspended, expired, or unknown) — remove it.
			if err := s.xray.RemoveUser(ctx, tag, email); err != nil {
				log.Printf("reconcile: remove %s from %s failed: %v", email, tag, err)
				continue
			}
			changed++
		}
	}
	return changed, nil
}
