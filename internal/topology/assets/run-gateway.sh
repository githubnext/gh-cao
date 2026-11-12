#!/bin/sh
set -eu
if [ "$#" -ne 5 ]; then
  echo "Usage (on the selected host): sh run-gateway.sh receive|dispatch IMAGE@sha256:DIGEST POLICY_JSON ENV_FILE CREDENTIAL_DIRECTORY" >&2
  exit 2
fi
role=$1
image=$2
policy=$3
environment=$4
credentials=$5
case "$role" in receive|dispatch) ;; *) echo "Invalid gateway role" >&2; exit 2 ;; esac
if ! printf '%s\n' "$image" | grep -Eq '^[-a-zA-Z0-9._:/]+@sha256:[a-f0-9]{64}$'; then
  echo "A reviewed image digest is required" >&2
  exit 2
fi
for file in "$policy" "$environment"; do
  case "$file" in /*) ;; *) echo "Use absolute private file paths" >&2; exit 2 ;; esac
  if [ ! -f "$file" ] || [ -L "$file" ]; then echo "Missing private policy/environment file" >&2; exit 2; fi
done
case "$credentials" in /*) ;; *) echo "Use an absolute credential directory" >&2; exit 2 ;; esac
if [ ! -d "$credentials" ] || [ -L "$credentials" ]; then echo "Missing credential directory" >&2; exit 2; fi
if [ "$role" = receive ]; then set -- tls.crt tls.key; else set -- receive.pem dispatch.pem; fi
for name in "$@"; do
  if [ ! -f "$credentials/$name" ] || [ -L "$credentials/$name" ]; then
    echo "Missing role-specific credential file" >&2
    exit 2
  fi
done
python3 - "$policy" "$environment" "$credentials" "$@" <<'PY'
import os
import stat
import sys

policy, environment, directory, *names = sys.argv[1:]
if any("," in path for path in (policy, environment, directory)):
    sys.exit("Podman bind paths must not contain commas")
info = os.lstat(directory)
if not stat.S_ISDIR(info.st_mode) or info.st_mode & 0o077:
    sys.exit("Credential directory must be private")
for path in [policy, environment, *(os.path.join(directory, name) for name in names)]:
    info = os.lstat(path)
    if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077:
        sys.exit("Policy, environment and credential files must be private regular files")
PY
if [ "$role" = receive ]; then
  exec podman run --rm --name cao-gateway-receive --read-only --cap-drop ALL \
    --security-opt no-new-privileges --tmpfs /tmp:rw,noexec,nosuid \
    --env-file "$environment" --publish 443:8443 \
    --mount "type=bind,source=$policy,target=/run/policy.json,readonly" \
    --mount "type=bind,source=$credentials/tls.crt,target=/run/cao/tls.crt,readonly" \
    --mount "type=bind,source=$credentials/tls.key,target=/run/cao/tls.key,readonly" \
    "$image" --role receive --policy /run/policy.json --listen 0.0.0.0:8443 \
    --tls-cert /run/cao/tls.crt --tls-key /run/cao/tls.key
fi
exec podman run --rm --name cao-gateway-dispatch --read-only --cap-drop ALL \
  --security-opt no-new-privileges --tmpfs /tmp:rw,noexec,nosuid \
  --env-file "$environment" \
  --env CAO_GATEWAY_RECEIVE_KEY_FILE=/run/cao/receive.pem \
  --env CAO_GATEWAY_DISPATCH_KEY_FILE=/run/cao/dispatch.pem \
  --mount "type=bind,source=$policy,target=/run/policy.json,readonly" \
  --mount "type=bind,source=$credentials/receive.pem,target=/run/cao/receive.pem,readonly" \
  --mount "type=bind,source=$credentials/dispatch.pem,target=/run/cao/dispatch.pem,readonly" \
  "$image" --role dispatch --policy /run/policy.json
