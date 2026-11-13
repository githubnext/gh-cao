#!/bin/sh
set -eu

: "${CAO_DATABASE_ADMIN_URL:?provisioning requires the administrator URL}"
: "${CAO_POSTGRES_OWNER_PASSWORD:?provisioning requires the owner password}"
: "${CAO_POSTGRES_RUNTIME_PASSWORD:?provisioning requires the runtime password}"
: "${PGHOST:?provisioning requires PGHOST}"
: "${PGDATABASE:?provisioning requires PGDATABASE}"

if [ "$CAO_POSTGRES_OWNER_PASSWORD" = "$CAO_POSTGRES_RUNTIME_PASSWORD" ]; then
  echo 'Owner and runtime passwords must be distinct' >&2
  exit 1
fi
case "$CAO_POSTGRES_OWNER_PASSWORD$CAO_POSTGRES_RUNTIME_PASSWORD" in
  *'
'*|*"$(printf '\r')"*)
    echo 'Role passwords must not contain line breaks' >&2
    exit 1
    ;;
esac

case "$CAO_DATABASE_ADMIN_URL" in
  postgresql://*:*@*/*) ;;
  *) echo 'Expected the template-generated PostgreSQL administrator URI' >&2; exit 1 ;;
esac
# The template URI-encodes both credential components. libpq does not expand
# connection URIs supplied through PGDATABASE; decode into its PG* environment.
credentials=${CAO_DATABASE_ADMIN_URL#postgresql://}
credentials=${credentials%%@*}
decode_component() {
  printf '%b' "$(printf '%s' "$1" | sed 's/%/\\x/g')"
}
PGUSER=$(decode_component "${credentials%%:*}")
PGPASSWORD=$(decode_component "${credentials#*:}")
export PGUSER PGPASSWORD
unset credentials
if [ "$PGPASSWORD" = "$CAO_POSTGRES_OWNER_PASSWORD" ] || [ "$PGPASSWORD" = "$CAO_POSTGRES_RUNTIME_PASSWORD" ]; then
  echo 'Administrator, owner and runtime passwords must be distinct' >&2
  exit 1
fi
export PGSSLMODE="${PGSSLMODE:-verify-full}"
# Without sslrootcert, libpq verify-full looks only for ~/.postgresql/root.crt.
# "system" uses the image CA bundle for both psql (libpq) and pgx.
export PGSSLROOTCERT="${PGSSLROOTCERT:-system}"
unset PGOPTIONS
psql -X -q -v ON_ERROR_STOP=1 -f /app/azure/provision.sql
# psql hashes \password input client-side; plaintext never becomes SQL or argv.
printf '%s\n%s\n' "$CAO_POSTGRES_OWNER_PASSWORD" "$CAO_POSTGRES_OWNER_PASSWORD" |
  psql -X -q -v ON_ERROR_STOP=1 -c '\password cao_bootstrap_login'
printf '%s\n%s\n' "$CAO_POSTGRES_RUNTIME_PASSWORD" "$CAO_POSTGRES_RUNTIME_PASSWORD" |
  psql -X -q -v ON_ERROR_STOP=1 -c '\password cao_runtime_login'

export PGUSER=cao_bootstrap_login
export PGPASSWORD="$CAO_POSTGRES_OWNER_PASSWORD"
export PGOPTIONS='-c role=cao_owner -c search_path=cao'
# Both libpq and pgx use PG* defaults; this URL contains no credentials.
export CAO_DATABASE_ADMIN_URL='postgresql://'
unset CAO_POSTGRES_OWNER_PASSWORD CAO_POSTGRES_RUNTIME_PASSWORD
/app/cao-dashboard database bootstrap --store canonical --namespace "${CAO_DATABASE_NAMESPACE:-azure-dashboard}"
/app/cao-dashboard database bootstrap --store operational --namespace "${CAO_OPERATIONAL_NAMESPACE:-azure-dashboard-operational}"
psql -X -q -v ON_ERROR_STOP=1 -v schema=cao -v runtime=cao_runtime \
  -v retention=cao_retention -v reader=cao_evidence -f /app/postgres/operational-grants.sql
psql -X -q -v ON_ERROR_STOP=1 -v schema=cao -v runtime=cao_runtime \
  -f /app/postgres/canonical-grants.sql
echo 'CAO database provisioning and explicit grants complete'
