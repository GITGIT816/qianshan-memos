package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"vps-billing/internal/model"
)

// CreateCustomer inserts a new customer and returns its assigned ID.
func (s *Store) CreateCustomer(c model.Customer) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO customers (name, contact, created_at) VALUES (?, ?, ?)`,
		c.Name, c.Contact, time.Now().Unix(),
	)
	if err != nil {
		return 0, fmt.Errorf("insert customer: %w", err)
	}
	return res.LastInsertId()
}

// GetCustomer looks up a customer by ID.
func (s *Store) GetCustomer(id int64) (model.Customer, error) {
	var c model.Customer
	var createdAt int64
	err := s.db.QueryRow(`SELECT id, name, contact, created_at FROM customers WHERE id = ?`, id).
		Scan(&c.ID, &c.Name, &c.Contact, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Customer{}, ErrNotFound
	}
	if err != nil {
		return model.Customer{}, err
	}
	c.CreatedAt = time.Unix(createdAt, 0)
	return c, nil
}

// ListCustomers returns all customers ordered by name.
func (s *Store) ListCustomers() ([]model.Customer, error) {
	rows, err := s.db.Query(`SELECT id, name, contact, created_at FROM customers ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("query customers: %w", err)
	}
	defer rows.Close()

	var out []model.Customer
	for rows.Next() {
		var c model.Customer
		var createdAt int64
		if err := rows.Scan(&c.ID, &c.Name, &c.Contact, &createdAt); err != nil {
			return nil, err
		}
		c.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, c)
	}
	return out, rows.Err()
}
