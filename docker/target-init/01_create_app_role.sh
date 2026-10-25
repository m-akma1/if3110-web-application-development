#!/bin/sh
# Runs once, on the first start of the target-postgres volume.
# Creates the non-superuser role the platform uses to connect to this target
# and makes it the owner of the demo database (and therefore of schema public).
set -eu

: "${TARGET_APP_USER:?TARGET_APP_USER must be set}"
: "${TARGET_APP_PASSWORD:?TARGET_APP_PASSWORD must be set}"

psql -v ON_ERROR_STOP=1 \
  --username "$POSTGRES_USER" \
  --dbname "$POSTGRES_DB" \
  -v app_user="$TARGET_APP_USER" \
  -v app_password="$TARGET_APP_PASSWORD" \
  -v db_name="$POSTGRES_DB" <<'SQL'
CREATE ROLE :"app_user" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD :'app_password';
ALTER DATABASE :"db_name" OWNER TO :"app_user";
SQL
