// Command billingctl manages proxy plans, customers, and subscriptions
// backed by a local Xray-core instance, and can run as the background
// process that keeps Xray's live user list and traffic quotas in sync with
// the database. See vps-billing/docs/DEPLOY.md for setup.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"vps-billing/internal/billing"
	"vps-billing/internal/model"
	"vps-billing/internal/store"
	"vps-billing/internal/xrayctl"
)

// globals holds the settings shared by every subcommand. Each can come from
// an environment variable (convenient under systemd) or be overridden with a
// flag on the specific invocation.
type globals struct {
	dbPath             string
	xrayBin            string
	xrayAPI            string
	protocol           string
	flow               string
	enforceDeviceLimit bool
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func newGlobals(fs *flag.FlagSet) *globals {
	g := &globals{}
	fs.StringVar(&g.dbPath, "db", envOr("BILLING_DB", "./billing.db"), "path to the sqlite database file")
	fs.StringVar(&g.xrayBin, "xray-bin", envOr("XRAY_BIN", "xray"), "path to the xray executable")
	fs.StringVar(&g.xrayAPI, "xray-api", envOr("XRAY_API", "127.0.0.1:10085"), "xray gRPC API address")
	fs.StringVar(&g.protocol, "protocol", envOr("BILLING_PROTOCOL", "vless"), "xray protocol new subscriptions are provisioned on")
	fs.StringVar(&g.flow, "flow", envOr("BILLING_FLOW", ""), "vless flow, e.g. xtls-rprx-vision (leave empty unless required)")
	enforceDefault := envOr("BILLING_ENFORCE_DEVICE_LIMIT", "false") == "true"
	fs.BoolVar(&g.enforceDeviceLimit, "enforce-device-limit", enforceDefault, "suspend a subscription that exceeds its plan's device limit")
	return g
}

func (g *globals) open() (*store.Store, *billing.Service, error) {
	st, err := store.Open(g.dbPath)
	if err != nil {
		return nil, nil, err
	}
	xray := xrayctl.NewClient(g.xrayBin, g.xrayAPI)
	svc := billing.NewService(st, xray, billing.Config{
		Protocol:           g.protocol,
		Flow:               g.flow,
		EnforceDeviceLimit: g.enforceDeviceLimit,
	})
	return st, svc, nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "seed-plans":
		err = cmdSeedPlans(os.Args[2:])
	case "plan":
		err = cmdPlan(os.Args[2:])
	case "customer":
		err = cmdCustomer(os.Args[2:])
	case "sub":
		err = cmdSub(os.Args[2:])
	case "sync":
		err = cmdSync(os.Args[2:])
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `billingctl - proxy plan/subscription manager backed by Xray-core

Usage:
  billingctl seed-plans                          seed the three default plans (轻量/标准/重度)
  billingctl plan add -name NAME -price-cny X -traffic-gb G -days D -devices N
  billingctl plan list
  billingctl customer add -name NAME [-contact C]
  billingctl customer list
  billingctl sub create -customer ID -plan ID -email EMAIL -tag INBOUND_TAG
  billingctl sub renew -id ID
  billingctl sub suspend -id ID [-reason R]
  billingctl sub resume -id ID
  billingctl sub list
  billingctl sync [-once] [-interval 5m]          reconcile usage/expiry against xray; runs forever unless -once

Global flags (any subcommand): -db -xray-bin -xray-api -protocol -flow -enforce-device-limit
Same settings can be set via env: BILLING_DB, XRAY_BIN, XRAY_API, BILLING_PROTOCOL, BILLING_FLOW, BILLING_ENFORCE_DEVICE_LIMIT
`)
}

// --- plan ------------------------------------------------------------------

func cmdSeedPlans(args []string) error {
	fs := flag.NewFlagSet("seed-plans", flag.ExitOnError)
	g := newGlobals(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, _, err := g.open()
	if err != nil {
		return err
	}
	defer st.Close()

	defaults := []model.Plan{
		{Name: "轻量", PriceCents: 1500, TrafficBytes: 100 * gb, DurationDays: 30, DeviceLimit: 3},
		{Name: "标准", PriceCents: 2500, TrafficBytes: 300 * gb, DurationDays: 30, DeviceLimit: 3},
		{Name: "重度", PriceCents: 5000, TrafficBytes: 1024 * gb, DurationDays: 30, DeviceLimit: 5},
	}
	for _, p := range defaults {
		if _, err := st.GetPlanByName(p.Name); err == nil {
			fmt.Printf("plan %q already exists, skipping\n", p.Name)
			continue
		}
		id, err := st.CreatePlan(p)
		if err != nil {
			return fmt.Errorf("seed plan %q: %w", p.Name, err)
		}
		fmt.Printf("created plan %q (id=%d)\n", p.Name, id)
	}
	return nil
}

const gb = int64(1024 * 1024 * 1024)

func cmdPlan(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: billingctl plan <add|list> ...")
	}
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("plan add", flag.ExitOnError)
		g := newGlobals(fs)
		name := fs.String("name", "", "plan name")
		priceCNY := fs.Float64("price-cny", 0, "price in CNY per cycle")
		trafficGB := fs.Float64("traffic-gb", 0, "traffic quota in GB per cycle")
		days := fs.Int("days", 30, "validity length in days")
		devices := fs.Int("devices", 3, "device (concurrent IP) limit")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *name == "" {
			return fmt.Errorf("-name is required")
		}
		st, _, err := g.open()
		if err != nil {
			return err
		}
		defer st.Close()

		id, err := st.CreatePlan(model.Plan{
			Name:         *name,
			PriceCents:   int64(*priceCNY*100 + 0.5),
			TrafficBytes: int64(*trafficGB * float64(gb)),
			DurationDays: *days,
			DeviceLimit:  *devices,
		})
		if err != nil {
			return err
		}
		fmt.Printf("created plan %q (id=%d)\n", *name, id)
		return nil

	case "list":
		fs := flag.NewFlagSet("plan list", flag.ExitOnError)
		g := newGlobals(fs)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		st, _, err := g.open()
		if err != nil {
			return err
		}
		defer st.Close()

		plans, err := st.ListPlans()
		if err != nil {
			return err
		}
		fmt.Printf("%-4s %-10s %8s %10s %6s %8s\n", "ID", "NAME", "PRICE", "TRAFFIC", "DAYS", "DEVICES")
		for _, p := range plans {
			fmt.Printf("%-4d %-10s %7.2f%s %8.0fGB %6d %8d\n",
				p.ID, p.Name, float64(p.PriceCents)/100, "元", float64(p.TrafficBytes)/float64(gb), p.DurationDays, p.DeviceLimit)
		}
		return nil

	default:
		return fmt.Errorf("usage: billingctl plan <add|list> ...")
	}
}

// --- customer ----------------------------------------------------------------

func cmdCustomer(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: billingctl customer <add|list> ...")
	}
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("customer add", flag.ExitOnError)
		g := newGlobals(fs)
		name := fs.String("name", "", "customer name")
		contact := fs.String("contact", "", "contact info (optional)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *name == "" {
			return fmt.Errorf("-name is required")
		}
		st, _, err := g.open()
		if err != nil {
			return err
		}
		defer st.Close()

		id, err := st.CreateCustomer(model.Customer{Name: *name, Contact: *contact})
		if err != nil {
			return err
		}
		fmt.Printf("created customer %q (id=%d)\n", *name, id)
		return nil

	case "list":
		fs := flag.NewFlagSet("customer list", flag.ExitOnError)
		g := newGlobals(fs)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		st, _, err := g.open()
		if err != nil {
			return err
		}
		defer st.Close()

		customers, err := st.ListCustomers()
		if err != nil {
			return err
		}
		fmt.Printf("%-4s %-16s %s\n", "ID", "NAME", "CONTACT")
		for _, c := range customers {
			fmt.Printf("%-4d %-16s %s\n", c.ID, c.Name, c.Contact)
		}
		return nil

	default:
		return fmt.Errorf("usage: billingctl customer <add|list> ...")
	}
}

// --- sub -----------------------------------------------------------------

func cmdSub(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: billingctl sub <create|renew|suspend|resume|list> ...")
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("sub create", flag.ExitOnError)
		g := newGlobals(fs)
		customerID := fs.Int64("customer", 0, "customer id")
		planID := fs.Int64("plan", 0, "plan id")
		email := fs.String("email", "", "unique xray user identifier, e.g. alice@yournode")
		tag := fs.String("tag", "", "xray inbound tag to add the user to")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *customerID == 0 || *planID == 0 || *email == "" || *tag == "" {
			return fmt.Errorf("-customer, -plan, -email, and -tag are all required")
		}
		st, svc, err := g.open()
		if err != nil {
			return err
		}
		defer st.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		sub, err := svc.CreateSubscription(ctx, *customerID, *planID, *email, *tag)
		if err != nil {
			return err
		}
		fmt.Printf("created subscription id=%d\n  email:  %s\n  uuid:   %s\n  expires: %s\n",
			sub.ID, sub.Email, sub.UUID, sub.ExpiresAt.Format(time.RFC3339))
		fmt.Println("  (build the client share link from this uuid/email plus your inbound's host/port/TLS settings)")
		return nil

	case "renew":
		id, g, rest, err := parseIDFlag("renew", args[1:])
		if err != nil {
			return err
		}
		_ = rest
		st, svc, err := g.open()
		if err != nil {
			return err
		}
		defer st.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		sub, err := svc.Renew(ctx, id)
		if err != nil {
			return err
		}
		fmt.Printf("renewed subscription %d, new expiry: %s\n", sub.ID, sub.ExpiresAt.Format(time.RFC3339))
		return nil

	case "suspend":
		fs := flag.NewFlagSet("sub suspend", flag.ExitOnError)
		g := newGlobals(fs)
		id := fs.Int64("id", 0, "subscription id")
		reason := fs.String("reason", "手动停用", "suspend reason")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id == 0 {
			return fmt.Errorf("-id is required")
		}
		st, svc, err := g.open()
		if err != nil {
			return err
		}
		defer st.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := svc.Suspend(ctx, *id, *reason); err != nil {
			return err
		}
		fmt.Printf("suspended subscription %d: %s\n", *id, *reason)
		return nil

	case "resume":
		id, g, _, err := parseIDFlag("resume", args[1:])
		if err != nil {
			return err
		}
		st, svc, err := g.open()
		if err != nil {
			return err
		}
		defer st.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := svc.Resume(ctx, id); err != nil {
			return err
		}
		fmt.Printf("resumed subscription %d\n", id)
		return nil

	case "list":
		fs := flag.NewFlagSet("sub list", flag.ExitOnError)
		g := newGlobals(fs)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		st, _, err := g.open()
		if err != nil {
			return err
		}
		defer st.Close()

		subs, err := st.ListSubscriptions()
		if err != nil {
			return err
		}
		fmt.Printf("%-4s %-20s %-9s %8s %8s %8s %-20s %s\n", "ID", "EMAIL", "STATUS", "USED", "LIMIT", "DEVICES", "EXPIRES", "REASON")
		for _, s := range subs {
			pct := 0.0
			if s.TrafficLimitBytes > 0 {
				pct = float64(s.TrafficUsedBytes) / float64(s.TrafficLimitBytes) * 100
			}
			fmt.Printf("%-4d %-20s %-9s %6.1f%% %6.0fGB %3d/%-4d %-20s %s\n",
				s.ID, s.Email, s.Status, pct, float64(s.TrafficLimitBytes)/float64(gb),
				s.LastSeenDevices, s.DeviceLimit, s.ExpiresAt.Format("2006-01-02 15:04"), s.SuspendReason)
		}
		return nil

	default:
		return fmt.Errorf("usage: billingctl sub <create|renew|suspend|resume|list> ...")
	}
}

// parseIDFlag handles the common "-id N" subcommand shape.
func parseIDFlag(name string, args []string) (int64, *globals, []string, error) {
	fs := flag.NewFlagSet("sub "+name, flag.ExitOnError)
	g := newGlobals(fs)
	id := fs.Int64("id", 0, "subscription id")
	if err := fs.Parse(args); err != nil {
		return 0, nil, nil, err
	}
	if *id == 0 {
		return 0, nil, nil, fmt.Errorf("-id is required")
	}
	return *id, g, fs.Args(), nil
}

// --- sync ------------------------------------------------------------------

func cmdSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	g := newGlobals(fs)
	once := fs.Bool("once", false, "run a single sync pass and exit")
	interval := fs.Duration("interval", 5*time.Minute, "how often to sync when not -once")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, svc, err := g.open()
	if err != nil {
		return err
	}
	defer st.Close()

	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		report, err := svc.SyncOnce(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "sync error:", err)
			return
		}
		fmt.Printf("sync: checked=%d suspended=%d reconciled=%d device-warnings=%d query-failures=%d\n",
			report.Checked, len(report.Suspended), report.Reconciled, len(report.DeviceWarn), len(report.QueryFailures))
		for _, s := range report.Suspended {
			fmt.Printf("  suspended: %s (%s)\n", s.Email, s.SuspendReason)
		}
		for _, s := range report.DeviceWarn {
			fmt.Printf("  device warning: %s has %d online, limit %d\n", s.Email, s.LastSeenDevices, s.DeviceLimit)
		}
		for _, e := range report.QueryFailures {
			fmt.Println("  query failure:", e)
		}
	}

	run()
	if *once {
		return nil
	}

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	for {
		select {
		case <-ticker.C:
			run()
		case <-stop:
			return nil
		}
	}
}
