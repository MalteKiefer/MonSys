#!/usr/bin/env bash
# Install or refresh mon-agent on a single host. Idempotent — safe to
# re-run.
#
# Usage:
#   install.sh <label> <bootstrap-token> [<server_url>]
#
# Expects these files to already exist on the target (scp them first):
#   /tmp/mon-agent              (the linux binary)
#   /tmp/mon-agent.service      (the systemd unit)
#   /tmp/config.yaml            (the agent config; placeholders fixed up
#                                 below)
set -euo pipefail

LABEL="${1:?usage: install.sh <label> <bootstrap-token> [<server_url>]}"
TOKEN="${2:?usage: install.sh <label> <bootstrap-token> [<server_url>]}"
SERVER_URL="${3:-https://mon.example.com}"

# 1. user + filesystem layout
id -u monagent >/dev/null 2>&1 || \
  useradd --system --no-create-home --shell /usr/sbin/nologin monagent

install -d -o root     -g monagent -m 0750 /etc/mon-agent
install -d -o monagent -g monagent -m 0750 /var/lib/mon-agent /var/log/mon-agent

# 2. binary
install -m 0755 /tmp/mon-agent /usr/local/bin/mon-agent

# 3. config — substitute label + server_url; the rest of the template is
# kept verbatim from /tmp/config.yaml.
sed -e "s|@@LABEL@@|${LABEL}|g" -e "s|@@SERVER_URL@@|${SERVER_URL}|g" \
  /tmp/config.yaml > /etc/mon-agent/config.yaml
chown root:monagent /etc/mon-agent/config.yaml
chmod 0640 /etc/mon-agent/config.yaml

# 4. systemd unit — drop SupplementaryGroups entries that don't exist on
# this host (otherwise systemd refuses to start).
existing=""
for g in docker libvirt systemd-journal fail2ban podman; do
  getent group "$g" >/dev/null && existing="$existing $g"
done
existing="$(echo "$existing" | xargs)"
sed -E "s|^SupplementaryGroups=.*|SupplementaryGroups=${existing}|" \
  /tmp/mon-agent.service > /etc/systemd/system/mon-agent.service
[ -z "$existing" ] && \
  sed -i '/^SupplementaryGroups=$/d' /etc/systemd/system/mon-agent.service
chmod 0644 /etc/systemd/system/mon-agent.service
systemctl daemon-reload

# 5. bootstrap (only if no key file yet)
if [ ! -f /var/lib/mon-agent/agent.key ]; then
  timeout 6 sudo -u monagent /usr/local/bin/mon-agent \
    --config=/etc/mon-agent/config.yaml \
    --bootstrap-token="${TOKEN}" >/tmp/mon-agent-bootstrap.log 2>&1 || true
  head -3 /tmp/mon-agent-bootstrap.log
  rm -f /tmp/mon-agent-bootstrap.log
fi
[ -f /var/lib/mon-agent/agent.key ] || { echo "BOOTSTRAP FAILED" >&2; exit 1; }

# 6. enable + start
systemctl enable --now mon-agent.service
sleep 2
systemctl is-active mon-agent.service
