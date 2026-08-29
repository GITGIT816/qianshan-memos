package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"vps-billing/internal/model"
)

const subscriptionColumns = `
	id, customer_id, plan_id, email, uuid, inbound_tag, status, suspend_reason,
	traffic_limit_bytes, traffic_used_bytes, device_limit, last_seen_devices,
	starts_at, expires_at, created_at, updated_at`

func scanSubscription(row interface{ Scan(...any) error }) (model.Subscription, error) {
	var sub model.Subscription
	var status string
	var startsAt, expiresAt, createdAt, updatedAt int64
	err := row.Scan(
		&sub.ID, &sub.CustomerID, &sub.PlanID, &sub.Email, &sub.UUID, &sub.InboundTag, &status, &sub.SuspendReason,
		&sub.TrafficLimitBytes, &sub.TrafficUsedBytes, &sub.DeviceLimit, &sub.LastSeenDevices,
		&startsAt, &expiresAt, &createdAt, &updatedAt,
	)
	if err != nil {
		return model.Subscription{}, err
	}
	sub.Status = model.Status(status)
	sub.StartsAt = time.Unix(startsAt, 0)
	sub.ExpiresAt = time.Unix(expiresAt, 0)
	sub.CreatedAt = time.Unix(createdAt, 0)
	sub.UpdatedAt = time.Unix(updatedAt, 0)
	return sub, nil
}

// CreateSubscription inserts a new subscription and returns its assigned ID.
func (s *Store) CreateSubscription(sub model.Subscription) (int64, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO subscriptions (
			customer_id, plan_id, email, uuid, inbound_tag, status, suspend_reason,
			traffic_limit_bytes, traffic_used_bytes, device_limit, last_seen_devices,
			starts_at, expires_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, 0, ?, ?, ?, ?)`,
		sub.CustomerID, sub.PlanID, sub.Email, sub.UUID, sub.InboundTag, sub.Status, sub.SuspendReason,
		sub.TrafficLimitBytes, sub.DeviceLimit, sub.StartsAt.Unix(), sub.ExpiresAt.Unix(), now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("insert subscription: %w", err)
	}
	return res.LastInsertId()
}

// GetSubscription looks up a subscription by ID.
func (s *Store) GetSubscription(id int64) (model.Subscription, error) {
	row := s.db.QueryRow(`SELECT `+subscriptionColumns+` FROM subscriptions WHERE id = ?`, id)
	sub, err := scanSubscription(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Subscription{}, ErrNotFound
	}
	return sub, err
}

// GetSubscriptionByEmail looks up a subscription by its Xray user email.
func (s *Store) GetSubscriptionByEmail(email string) (model.Subscription, error) {
	row := s.db.QueryRow(`SELECT `+subscriptionColumns+` FROM subscriptions WHERE email = ?`, email)
	sub, err := scanSubscription(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Subscription{}, ErrNotFound
	}
	return sub, err
}

// ListSubscriptions returns every subscription, newest first.
func (s *Store) ListSubscriptions() ([]model.Subscription, error) {
	rows, err := s.db.Query(`SELECT ` + subscriptionColumns + ` FROM subscriptions ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("query subscriptions: %w", err)
	}
	defer rows.Close()

	var out []model.Subscription
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// ListActiveSubscriptions returns subscriptions currently marked active.
func (s *Store) ListActiveSubscriptions() ([]model.Subscription, error) {
	rows, err := s.db.Query(`SELECT `+subscriptionColumns+` FROM subscriptions WHERE status = ? ORDER BY id`, model.StatusActive)
	if err != nil {
		return nil, fmt.Errorf("query active subscriptions: %w", err)
	}
	defer rows.Close()

	var out []model.Subscription
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// UpdateSubscription persists the mutable fields of sub (status, usage, device count,
// expiry, suspend reason) and bumps updated_at.
func (s *Store) UpdateSubscription(sub model.Subscription) error {
	_, err := s.db.Exec(
		`UPDATE subscriptions SET
			status = ?, suspend_reason = ?, traffic_used_bytes = ?, last_seen_devices = ?,
			expires_at = ?, updated_at = ?
		 WHERE id = ?`,
		sub.Status, sub.SuspendReason, sub.TrafficUsedBytes, sub.LastSeenDevices,
		sub.ExpiresAt.Unix(), time.Now().Unix(), sub.ID,
	)
	if err != nil {
		return fmt.Errorf("update subscription %d: %w", sub.ID, err)
	}
	return nil
}
