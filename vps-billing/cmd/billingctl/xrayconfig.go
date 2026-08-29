package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"vps-billing/internal/xrayconf"
)

func cmdXrayMergeConfig(args []string) error {
	fs := flag.NewFlagSet("xray-merge-config", flag.ExitOnError)
	in := fs.String("in", "", "path to your existing Xray config.json")
	out := fs.String("out", "", "output path (default: <in>.merged.json)")
	write := fs.Bool("write", false, "write the result back to -in instead of a separate file")
	apiListen := fs.String("api-listen", "127.0.0.1", "address the new api-in inbound listens on")
	apiPort := fs.Int("api-port", 10085, "port the new api-in inbound listens on")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return fmt.Errorf("-in is required (path to your existing Xray config.json)")
	}

	raw, err := os.ReadFile(*in)
	if err != nil {
		return fmt.Errorf("read %s: %w", *in, err)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var config map[string]any
	if err := dec.Decode(&config); err != nil {
		return fmt.Errorf("parse %s as JSON: %w", *in, err)
	}

	res, err := xrayconf.Merge(config, xrayconf.Options{APIListen: *apiListen, APIPort: *apiPort})
	if err != nil {
		return err
	}

	merged, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("re-encode merged config: %w", err)
	}

	outPath := *out
	switch {
	case *write:
		outPath = *in
	case outPath == "":
		outPath = *in + ".merged.json"
	}
	if err := os.WriteFile(outPath, append(merged, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}

	fmt.Printf("wrote merged config to %s\n\n", outPath)
	fmt.Println("changes:")
	for _, n := range res.Notes {
		fmt.Println("  - " + n)
	}

	fmt.Println("\ninbounds found (confirm the -tag you pass to `sub create` matches one of these):")
	for _, ib := range res.Inbounds {
		tag := ib.Tag
		if tag == "" {
			tag = "(no tag)"
		}
		fmt.Printf("  - tag=%-14s protocol=%-12s port=%v\n", tag, ib.Protocol, ib.Port)
	}

	if len(res.NeedsAttn) > 0 {
		fmt.Println("\n⚠ needs your attention:")
		for _, w := range res.NeedsAttn {
			fmt.Println("  - " + w)
		}
	}
	if len(res.HasOwnClients) > 0 {
		fmt.Println("\n⚠ heads up:")
		for _, w := range res.HasOwnClients {
			fmt.Println("  - " + w)
		}
	}

	fmt.Printf("\nnext steps:\n  xray -test -config %s\n  # if that passes, replace your real config.json with it and reload xray\n", outPath)
	if !*write {
		fmt.Printf("  (this did not touch %s — %s is a separate file until you copy it over)\n", *in, outPath)
	}
	return nil
}
