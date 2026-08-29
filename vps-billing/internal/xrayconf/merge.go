// Package xrayconf non-destructively merges the API/stats/policy/routing
// blocks vps-billing needs into an existing Xray config.json, so the
// riskiest hand-edit in setup (typo a bracket, drop a comma) becomes a
// generated diff instead.
//
// It deliberately does NOT touch the inbound you actually serve traffic on:
// picking which inbound is "the real one" and clearing its clients is a
// judgment call left to the person deploying this, and guessing wrong would
// silently break their existing proxy. Merge reports back which inbounds it
// found so that step is at least a quick manual confirmation, not a hunt.
package xrayconf

import "fmt"

// Options controls the API inbound Merge adds.
type Options struct {
	APIListen string // default "127.0.0.1"
	APIPort   int    // default 10085
}

func (o Options) withDefaults() Options {
	if o.APIListen == "" {
		o.APIListen = "127.0.0.1"
	}
	if o.APIPort == 0 {
		o.APIPort = 10085
	}
	return o
}

// InboundSummary describes one inbound found in the config, for the
// "confirm this yourself" report.
type InboundSummary struct {
	Tag      string
	Protocol string
	Port     any
}

// Result reports what Merge changed and what still needs a human's judgment.
type Result struct {
	Notes         []string         // what was added or left alone, in order
	Inbounds      []InboundSummary // every inbound in the config after merging
	NeedsAttn     []string         // inbounds with no tag — Merge can't route billingctl at them
	HasOwnClients []string         // tagged inbounds that already list clients billingctl won't know about
}

func asObject(m map[string]any, key string) (map[string]any, bool) {
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	obj, ok := v.(map[string]any)
	return obj, ok
}

func ensureObject(m map[string]any, key string) map[string]any {
	if obj, ok := asObject(m, key); ok {
		return obj
	}
	obj := map[string]any{}
	m[key] = obj
	return obj
}

// Merge mutates config in place, adding whatever api/stats/policy/routing
// pieces vps-billing needs that aren't already there. Existing settings for
// these blocks are extended, never overwritten wholesale.
func Merge(config map[string]any, opts Options) (*Result, error) {
	opts = opts.withDefaults()
	res := &Result{}

	mergeAPI(config, res)
	mergeStats(config, res)
	mergePolicy(config, res)
	needsAPIInbound := mergeAPIInbound(config, opts, res)
	if needsAPIInbound {
		mergeRoutingRule(config, res)
	}
	summarizeInbounds(config, res)

	return res, nil
}

func mergeAPI(config map[string]any, res *Result) {
	api, existed := asObject(config, "api")
	if !existed {
		config["api"] = map[string]any{
			"tag":      "api",
			"services": []any{"HandlerService", "StatsService", "LoggerService"},
		}
		res.Notes = append(res.Notes, `added "api": {tag: "api", services: [HandlerService, StatsService, LoggerService]}`)
		return
	}

	if _, ok := api["tag"]; !ok {
		api["tag"] = "api"
		res.Notes = append(res.Notes, `api.tag was missing, set to "api"`)
	}

	want := []string{"HandlerService", "StatsService", "LoggerService"}
	have := map[string]bool{}
	existing, _ := api["services"].([]any)
	for _, s := range existing {
		if str, ok := s.(string); ok {
			have[str] = true
		}
	}
	added := false
	for _, w := range want {
		if !have[w] {
			existing = append(existing, w)
			added = true
		}
	}
	if added {
		api["services"] = existing
		res.Notes = append(res.Notes, "added missing services to existing api.services")
	} else {
		res.Notes = append(res.Notes, "api block already present, left as-is")
	}
}

func mergeStats(config map[string]any, res *Result) {
	if _, ok := config["stats"]; ok {
		res.Notes = append(res.Notes, "stats block already present, left as-is")
		return
	}
	config["stats"] = map[string]any{}
	res.Notes = append(res.Notes, `added empty "stats": {}`)
}

func mergePolicy(config map[string]any, res *Result) {
	policy := ensureObject(config, "policy")

	levels := ensureObject(policy, "levels")
	level0 := ensureObject(levels, "0")
	changed := setIfDifferent(level0, "statsUserUplink", true)
	changed = setIfDifferent(level0, "statsUserDownlink", true) || changed
	if changed {
		res.Notes = append(res.Notes, "enabled statsUserUplink/statsUserDownlink on policy.levels.0 (per-user traffic accounting)")
	}

	system := ensureObject(policy, "system")
	changed = setIfDifferent(system, "statsInboundUplink", true)
	changed = setIfDifferent(system, "statsInboundDownlink", true) || changed
	if changed {
		res.Notes = append(res.Notes, "enabled statsInboundUplink/statsInboundDownlink on policy.system")
	}
}

// setIfDifferent sets m[key]=val unless it's already exactly that value, and
// reports whether it changed anything.
func setIfDifferent(m map[string]any, key string, val any) bool {
	if existing, ok := m[key]; ok && existing == val {
		return false
	}
	m[key] = val
	return true
}

// mergeAPIInbound adds the local-only dokodemo-door inbound billingctl talks
// to, unless something that looks like it is already there. Returns whether
// it added one (and therefore whether a matching routing rule is needed).
func mergeAPIInbound(config map[string]any, opts Options, res *Result) bool {
	inbounds, _ := config["inbounds"].([]any)
	for _, raw := range inbounds {
		ib, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if tag, _ := ib["tag"].(string); tag == "api-in" {
			res.Notes = append(res.Notes, `an inbound tagged "api-in" already exists, left it alone`)
			return false
		}
	}

	inbounds = append(inbounds, map[string]any{
		"listen":   opts.APIListen,
		"port":     opts.APIPort,
		"protocol": "dokodemo-door",
		"settings": map[string]any{"address": opts.APIListen},
		"tag":      "api-in",
	})
	config["inbounds"] = inbounds
	res.Notes = append(res.Notes, fmt.Sprintf("added a dokodemo-door inbound tagged \"api-in\" on %s:%d for billingctl to talk to", opts.APIListen, opts.APIPort))
	return true
}

func mergeRoutingRule(config map[string]any, res *Result) {
	routing := ensureObject(config, "routing")
	rules, _ := routing["rules"].([]any)

	for _, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		outboundTag, _ := rule["outboundTag"].(string)
		if outboundTag != "api" {
			continue
		}
		tags, _ := rule["inboundTag"].([]any)
		for _, t := range tags {
			if s, _ := t.(string); s == "api-in" {
				res.Notes = append(res.Notes, "a routing rule for api-in -> api already exists, left it alone")
				return
			}
		}
	}

	newRule := map[string]any{
		"type":        "field",
		"inboundTag":  []any{"api-in"},
		"outboundTag": "api",
	}
	// Prepend: routing rules match first-hit, and this must win over any
	// catch-all block rule further down the list.
	routing["rules"] = append([]any{newRule}, rules...)
	res.Notes = append(res.Notes, `added a routing rule sending api-in traffic to outboundTag "api" (inserted first, so it isn't shadowed by a catch-all rule)`)
}

func summarizeInbounds(config map[string]any, res *Result) {
	inbounds, _ := config["inbounds"].([]any)
	for _, raw := range inbounds {
		ib, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		tag, _ := ib["tag"].(string)
		protocol, _ := ib["protocol"].(string)
		port := ib["port"]

		res.Inbounds = append(res.Inbounds, InboundSummary{Tag: tag, Protocol: protocol, Port: port})

		if protocol == "dokodemo-door" {
			continue // this is the api-in inbound (ours or a lookalike), not a proxy inbound
		}
		if tag == "" {
			res.NeedsAttn = append(res.NeedsAttn,
				fmt.Sprintf("inbound protocol=%s port=%v has no tag — give it one (e.g. \"vless-in\") and pass it to `billingctl sub create -tag ...`", protocol, port))
			continue
		}
		if settings, ok := ib["settings"].(map[string]any); ok {
			if clients, ok := settings["clients"].([]any); ok && len(clients) > 0 {
				res.HasOwnClients = append(res.HasOwnClients,
					fmt.Sprintf("inbound %q already has %d client(s) defined in config.json — billingctl won't know about them (no quota/expiry tracking); consider clearing settings.clients and re-creating them with `billingctl sub create -tag %s`", tag, len(clients), tag))
			}
		}
	}
}
