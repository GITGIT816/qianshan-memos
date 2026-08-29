package xrayconf

import "testing"

func TestMergeEmptyConfig(t *testing.T) {
	config := map[string]any{}
	res, err := Merge(config, Options{})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	api, ok := config["api"].(map[string]any)
	if !ok || api["tag"] != "api" {
		t.Errorf("api block missing or wrong: %+v", config["api"])
	}
	if _, ok := config["stats"]; !ok {
		t.Error("stats block missing")
	}
	policy, ok := config["policy"].(map[string]any)
	if !ok {
		t.Fatalf("policy block missing")
	}
	levels := policy["levels"].(map[string]any)
	level0 := levels["0"].(map[string]any)
	if level0["statsUserUplink"] != true || level0["statsUserDownlink"] != true {
		t.Errorf("policy.levels.0 stats flags not set: %+v", level0)
	}
	system := policy["system"].(map[string]any)
	if system["statsInboundUplink"] != true || system["statsInboundDownlink"] != true {
		t.Errorf("policy.system stats flags not set: %+v", system)
	}

	inbounds := config["inbounds"].([]any)
	if len(inbounds) != 1 {
		t.Fatalf("expected exactly one (api-in) inbound, got %d", len(inbounds))
	}
	apiIn := inbounds[0].(map[string]any)
	if apiIn["tag"] != "api-in" || apiIn["protocol"] != "dokodemo-door" || apiIn["port"] != 10085 {
		t.Errorf("api-in inbound wrong: %+v", apiIn)
	}

	routing := config["routing"].(map[string]any)
	rules := routing["rules"].([]any)
	if len(rules) != 1 {
		t.Fatalf("expected exactly one routing rule, got %d", len(rules))
	}
	rule := rules[0].(map[string]any)
	if rule["outboundTag"] != "api" {
		t.Errorf("routing rule wrong: %+v", rule)
	}

	if len(res.NeedsAttn) != 0 {
		t.Errorf("expected no attention items for an empty config, got %v", res.NeedsAttn)
	}
}

func TestMergePreservesExistingRealInbound(t *testing.T) {
	config := map[string]any{
		"inbounds": []any{
			map[string]any{
				"tag":      "vless-in",
				"protocol": "vless",
				"port":     float64(443),
				"settings": map[string]any{"clients": []any{}},
			},
		},
	}
	res, err := Merge(config, Options{})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	inbounds := config["inbounds"].([]any)
	if len(inbounds) != 2 {
		t.Fatalf("expected the real inbound plus api-in, got %d", len(inbounds))
	}
	real := inbounds[0].(map[string]any)
	if real["tag"] != "vless-in" || real["protocol"] != "vless" {
		t.Errorf("existing real inbound was mutated: %+v", real)
	}

	found := false
	for _, s := range res.Inbounds {
		if s.Tag == "vless-in" && s.Protocol == "vless" {
			found = true
		}
	}
	if !found {
		t.Errorf("summary did not include the real inbound: %+v", res.Inbounds)
	}
	if len(res.NeedsAttn) != 0 {
		t.Errorf("tagged inbound should not need attention, got %v", res.NeedsAttn)
	}
}

func TestMergeFlagsExistingClients(t *testing.T) {
	config := map[string]any{
		"inbounds": []any{
			map[string]any{
				"tag": "vless-in", "protocol": "vless", "port": float64(443),
				"settings": map[string]any{
					"clients": []any{map[string]any{"id": "abc", "email": "hand-added@node"}},
				},
			},
		},
	}
	res, _ := Merge(config, Options{})
	if len(res.HasOwnClients) != 1 {
		t.Fatalf("expected one HasOwnClients warning, got %v", res.HasOwnClients)
	}
}

func TestMergeFlagsUntaggedInbound(t *testing.T) {
	config := map[string]any{
		"inbounds": []any{
			map[string]any{"protocol": "vless", "port": float64(443)},
		},
	}
	res, _ := Merge(config, Options{})
	if len(res.NeedsAttn) != 1 {
		t.Fatalf("expected exactly one attention item for the untagged inbound, got %v", res.NeedsAttn)
	}
}

func TestMergeIsIdempotent(t *testing.T) {
	config := map[string]any{}
	if _, err := Merge(config, Options{}); err != nil {
		t.Fatalf("first Merge: %v", err)
	}
	if _, err := Merge(config, Options{}); err != nil {
		t.Fatalf("second Merge: %v", err)
	}

	inbounds := config["inbounds"].([]any)
	apiInCount := 0
	for _, raw := range inbounds {
		ib := raw.(map[string]any)
		if ib["tag"] == "api-in" {
			apiInCount++
		}
	}
	if apiInCount != 1 {
		t.Errorf("expected exactly one api-in inbound after merging twice, got %d", apiInCount)
	}

	routing := config["routing"].(map[string]any)
	rules := routing["rules"].([]any)
	if len(rules) != 1 {
		t.Errorf("expected exactly one routing rule after merging twice, got %d", len(rules))
	}
}

func TestMergePreservesCustomPolicyFields(t *testing.T) {
	config := map[string]any{
		"policy": map[string]any{
			"levels": map[string]any{
				"0": map[string]any{"handshake": float64(4), "connIdle": float64(300)},
			},
			"system": map[string]any{"someOtherFlag": true},
		},
	}
	Merge(config, Options{})

	policy := config["policy"].(map[string]any)
	level0 := policy["levels"].(map[string]any)["0"].(map[string]any)
	if level0["handshake"] != float64(4) || level0["connIdle"] != float64(300) {
		t.Errorf("existing level0 fields were clobbered: %+v", level0)
	}
	if level0["statsUserUplink"] != true {
		t.Errorf("stats flag was not added alongside existing fields: %+v", level0)
	}
	system := policy["system"].(map[string]any)
	if system["someOtherFlag"] != true {
		t.Errorf("existing system field was clobbered: %+v", system)
	}
}
