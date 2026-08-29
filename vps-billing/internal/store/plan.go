package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"vps-billing/internal/model"
)

// ErrNotFound is returned when a lookup finds no matching row.
var ErrNotFound = errors.New("not found")

// CreatePlan inserts a new plan and returns its assigned ID.
func (s *Store) CreatePlan(p model.Plan) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO plans (name, price_cents, traffic_bytes, duration_days, device_limit, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		p.Name, p.PriceCents, p.TrafficBytes, p.DurationDays, p.DeviceLimit, time.Now().Unix(),
	)
	if err != nil {
		return 0, fmt.Errorf("insert plan: %w", err)
	}
	return res.LastInsertId()
}

func scanPlan(row interface{ Scan(...any) error }) (model.Plan, error) {
	var p model.Plan
	var createdAt int64
	if err := row.Scan(&p.ID, &p.Name, &p.PriceCents, &p.TrafficBytes, &p.DurationDays, &p.DeviceLimit, &createdAt); err != nil {
		return model.Plan{}, err
	}
	p.CreatedAt = time.Unix(createdAt, 0)
	return p, nil
}

const planColumns = `id, name, price_cents, traffic_bytes, duration_days, device_limit, created_at`

// GetPlanByName looks up a plan by its unique name.
func (s *Store) GetPlanByName(name string) (model.Plan, error) {
	row := s.db.QueryRow(`SELECT `+planColumns+` FROM plans WHERE name = ?`, name)
	p, err := scanPlan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Plan{}, ErrNotFound
	}
	return p, err
}

// GetPlan looks up a plan by ID.
func (s *Store) GetPlan(id int64) (model.Plan, error) {
	row := s.db.QueryRow(`SELECT `+planColumns+` FROM plans WHERE id = ?`, id)
	p, err := scanPlan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Plan{}, ErrNotFound
	}
	return p, err
}

// ListPlans returns all plans ordered by price.
func (s *Store) ListPlans() ([]model.Plan, error) {
	rows, err := s.db.Query(`SELECT ` + planColumns + ` FROM plans ORDER BY price_cents ASC`)
	if err != nil {
		return nil, fmt.Errorf("query plans: %w", err)
	}
	defer rows.Close()

	var plans []model.Plan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	return plans, rows.Err()
}
