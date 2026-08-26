#!/usr/bin/env bash
set -euo pipefail

# Source, fixtures, and public documentation must stay invented and safe to
# publish. Contract documents may name current parser compatibility concepts,
# but never retired deployment identifiers or sensitive values.
#
# This uses grep deliberately. An earlier version called ripgrep and sent its
# errors to /dev/null, so on a machine without rg every check reported "no
# match" and the gate passed without inspecting anything.
if (($# == 0)); then
  targets=(.)
else
  targets=("$@")
fi
status=0

scan() {
  grep -rnE --binary-files=without-match \
    --exclude-dir=.git --exclude-dir=.repowise --exclude-dir=bin \
    --exclude=go.sum --exclude=check-prohibited-content.sh "$@" "${targets[@]}" 2>/dev/null || true
}

report() {
  printf 'prohibited content (%s):\n%s\n' "$1" "$2" >&2
  status=1
}

if matches=$(scan -i 'cloverleaf|pdl-tcpip|fac_[0-9]+|qbirt|tpp6x|clapi|(prod|production)[-_](server|host|site|[0-9]+)'); then
  [[ -n $matches ]] && report 'retired deployment term' "$matches"
fi

if matches=$(scan -i 'authorization:[[:space:]]*(bearer|basic)|(password|secret|api[_-]?key|token)[[:space:]]*[:=][[:space:]]*[^[:space:]]{8,}'); then
  [[ -n $matches ]] && report 'credential-like value' "$matches"
fi

# Documentation and fixtures may reference only the RFC 2606 example domains.
if matches=$(scan -o 'https?://[^[:space:])"'"'"'<>]+' |
  grep -vE '://([A-Za-z0-9-]+\.)*(example\.(com|net|org|test)|rfc-editor\.org|repowise\.dev|github\.com|go\.dev)([:/]|$)' || true); then
  [[ -n $matches ]] && report 'external URL' "$matches"
fi

# Permit loopback, RFC 1918, and the three RFC 5737 documentation ranges.
if matches=$(scan -o '([0-9]{1,3}\.){3}[0-9]{1,3}' |
  grep -vE ':(127|10|192\.168|172\.(1[6-9]|2[0-9]|3[01])|192\.0\.2|198\.51\.100|203\.0\.113)\.[0-9.]+$' || true); then
  [[ -n $matches ]] && report 'non-reserved IPv4 address' "$matches"
fi

exit "$status"
