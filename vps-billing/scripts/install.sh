#!/usr/bin/env bash
# One-shot installer for billingctl + its systemd sync service on a VPS.
# Run from inside the vps-billing/ directory, as root (or via sudo):
#
#   sudo ./scripts/install.sh
#
# It builds the binary, installs it to /usr/local/bin, creates a dedicated
# system user/group, sets up /var/lib/vps-billing and /etc/vps-billing, and
# installs (but does not enable) the systemd sync unit. It does NOT touch
# your Xray config — see docs/DEPLOY.md step 1, or `billingctl
# xray-merge-config`, for that.
#
# Safe to re-run: existing config/env files are never overwritten.
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
  echo "this script needs root (installs a system user, files under /etc and /usr/local/bin) — try: sudo $0" >&2
  exit 1
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(dirname "$script_dir")"
cd "$repo_dir"

if ! command -v go >/dev/null 2>&1; then
  echo "go is not installed or not on PATH — install Go first (https://go.dev/dl/), then re-run this script" >&2
  exit 1
fi
if ! command -v xray >/dev/null 2>&1; then
  echo "warning: 'xray' not found on PATH — billingctl will build fine, but sub create/sync will fail until Xray is installed and reachable" >&2
fi

echo "==> building billingctl"
go build -o billingctl ./cmd/billingctl

echo "==> installing binary to /usr/local/bin/billingctl"
install -o root -g root -m 755 billingctl /usr/local/bin/billingctl

if ! id vps-billing >/dev/null 2>&1; then
  echo "==> creating system user 'vps-billing'"
  useradd --system --no-create-home --shell /usr/sbin/nologin vps-billing
else
  echo "==> system user 'vps-billing' already exists, skipping"
fi

echo "==> creating /var/lib/vps-billing and /etc/vps-billing"
install -d -o vps-billing -g vps-billing -m 750 /var/lib/vps-billing
install -d -o root -g root -m 750 /etc/vps-billing

if [[ ! -f /etc/vps-billing/billing.env ]]; then
  echo "==> installing default /etc/vps-billing/billing.env (edit this before starting the service)"
  install -o root -g root -m 600 configs/billing.env.example /etc/vps-billing/billing.env
else
  echo "==> /etc/vps-billing/billing.env already exists, leaving it alone"
fi

echo "==> installing systemd unit (not enabling it yet)"
install -o root -g root -m 644 configs/systemd/vps-billing-sync.service /etc/systemd/system/vps-billing-sync.service
systemctl daemon-reload

cat <<EOF

done. Before starting anything:

  1. Point Xray's API at 127.0.0.1:10085 (see docs/DEPLOY.md step 1, or run
     'billingctl xray-merge-config -in /path/to/your/xray/config.json' to
     generate the merged config for you).
  2. Edit /etc/vps-billing/billing.env — at minimum set BILLING_HOST, and
     BILLING_PUBLIC_KEY/BILLING_SNI/BILLING_SHORT_ID if you use Reality.
  3. Seed the default plans and try opening one subscription by hand ('sudo
     -u' does not read /etc/vps-billing/billing.env, so pass -db explicitly
     after each subcommand for these manual runs — the systemd service picks
     it up fine via EnvironmentFile):
       sudo -u vps-billing billingctl seed-plans -db /var/lib/vps-billing/billing.db
       sudo -u vps-billing billingctl customer add -db /var/lib/vps-billing/billing.db -name "test"
       sudo -u vps-billing billingctl sub create -db /var/lib/vps-billing/billing.db \\
         -customer 1 -plan 1 -email test@yournode -tag <your inbound tag>
  4. Once that works: systemctl enable --now vps-billing-sync.service
EOF
