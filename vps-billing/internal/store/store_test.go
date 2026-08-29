package store

import (
	"path/filepath"
	"testing"
	"time"

	"vps-billing/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	// modernc.org/sqlite's ":memory:" support is per-connection, and Store
	// pins MaxOpenConns(1), so a temp file is simpler than reasoning about
	// in-memory DB lifetime across the pool.
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestPlanCRUD(t *testing.T) {
	st := newTestStore(t)

	id, err := st.CreatePlan(model.Plan{
		Name: "轻量", PriceCents: 1500, TrafficBytes: 100 << 30, DurationDays: 30, DeviceLimit: 3,
	})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	got, err := st.GetPlan(id)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if got.Name != "轻量" || got.PriceCents != 1500 || got.TrafficBytes != 100<<30 || got.DurationDays != 30 || got.DeviceLimit != 3 {
		t.Fatalf("GetPlan returned unexpected plan: %+v", got)
	}

	byName, err := st.GetPlanByName("轻量")
	if err != nil || byName.ID != id {
		t.Fatalf("GetPlanByName mismatch: %+v, err=%v", byName, err)
	}

	if _, err := st.GetPlan(9999); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for missing plan, got %v", err)
	}

	plans, err := st.ListPlans()
	if err != nil || len(plans) != 1 {
		t.Fatalf("ListPlans: %+v, err=%v", plans, err)
	}
}

func TestSubscriptionLifecycle(t *testing.T) {
	st := newTestStore(t)

	planID, err := st.CreatePlan(model.Plan{Name: "标准", PriceCents: 2500, TrafficBytes: 300 << 30, DurationDays: 30, DeviceLimit: 3})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	custID, err := st.CreateCustomer(model.Customer{Name: "Alice", Contact: "alice@example.com"})
	if err != nil {
		t.Fatalf("CreateCustomer: %v", err)
	}

	now := time.Now()
	subID, err := st.CreateSubscription(model.Subscription{
		CustomerID: custID, PlanID: planID, Email: "alice@node", UUID: "u-1", InboundTag: "vless-in",
		Status: model.StatusActive, TrafficLimitBytes: 300 << 30, DeviceLimit: 3,
		StartsAt: now, ExpiresAt: now.AddDate(0, 0, 30),
	})
	if err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}

	sub, err := st.GetSubscription(subID)
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	if sub.Status != model.StatusActive || sub.TrafficUsedBytes != 0 || sub.Email != "alice@node" {
		t.Fatalf("unexpected subscription: %+v", sub)
	}

	byEmail, err := st.GetSubscriptionByEmail("alice@node")
	if err != nil || byEmail.ID != subID {
		t.Fatalf("GetSubscriptionByEmail mismatch: %+v, err=%v", byEmail, err)
	}

	// Simulate a sync tick: accumulate usage and suspend.
	sub.TrafficUsedBytes = 310 << 30
	sub.Status = model.StatusSuspended
	sub.SuspendReason = "流量超限"
	if err := st.UpdateSubscription(sub); err != nil {
		t.Fatalf("UpdateSubscription: %v", err)
	}

	reloaded, err := st.GetSubscription(subID)
	if err != nil {
		t.Fatalf("GetSubscription after update: %v", err)
	}
	if reloaded.Status != model.StatusSuspended || reloaded.SuspendReason != "流量超限" || reloaded.TrafficUsedBytes != 310<<30 {
		t.Fatalf("update did not persist: %+v", reloaded)
	}

	active, err := st.ListActiveSubscriptions()
	if err != nil {
		t.Fatalf("ListActiveSubscriptions: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("expected no active subscriptions after suspend, got %d", len(active))
	}

	all, err := st.ListSubscriptions()
	if err != nil || len(all) != 1 {
		t.Fatalf("ListSubscriptions: %+v, err=%v", all, err)
	}
}

func TestSubscriptionDuplicateEmailRejected(t *testing.T) {
	st := newTestStore(t)
	planID, _ := st.CreatePlan(model.Plan{Name: "P", PriceCents: 100, TrafficBytes: 1, DurationDays: 1, DeviceLimit: 1})
	custID, _ := st.CreateCustomer(model.Customer{Name: "C"})

	now := time.Now()
	mk := func() model.Subscription {
		return model.Subscription{
			CustomerID: custID, PlanID: planID, Email: "dup@node", UUID: "u", InboundTag: "in",
			Status: model.StatusActive, StartsAt: now, ExpiresAt: now.AddDate(0, 0, 1),
		}
	}
	if _, err := st.CreateSubscription(mk()); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := st.CreateSubscription(mk()); err == nil {
		t.Fatalf("expected unique constraint violation on duplicate email")
	}
}
