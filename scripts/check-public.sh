#!/bin/sh
set -eu

tracked=$(git ls-files --cached --others --exclude-standard | grep -v '^scripts/check-public.sh$' || true)
[ -n "$tracked" ] || exit 0

if printf '%s\n' "$tracked" | xargs grep -nE '(BEGIN (RSA|OPENSSH|EC) PRIVATE KEY|api[_-]?key[[:space:]]*[:=][[:space:]]*[A-Za-z0-9_-]{20,}|secret[_-]?key[[:space:]]*[:=][[:space:]]*[A-Za-z0-9_/-]{20,})'; then
  echo "possible secret found in public tree" >&2
  exit 1
fi

private_octets='(10\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}|172\.(1[6-9]|2[0-9]|3[01])\.[0-9]{1,3}\.[0-9]{1,3}|192\.168\.[0-9]{1,3}\.[0-9]{1,3})'
if printf '%s\n' "$tracked" | xargs grep -nE "$private_octets"; then
  echo "private infrastructure address found in public tree" >&2
  exit 1
fi
