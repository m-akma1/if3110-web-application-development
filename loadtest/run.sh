#!/bin/sh
# Runs the k6 stress test against the running Compose stack, using only what is in this
# repository: the demo database plus extra databases created on target-postgres. Anyone with the
# repository can run it (Linux: k6 uses the host network and goes through Caddy on
# https://localhost, like a browser).
#
#   docker compose up -d --build        (or: make observability, to watch it in Grafana)
#   loadtest/run.sh                     (or: make loadtest)
#
# Environment (all optional):
#   EXTRA_DBS          databases to create on target-postgres and use (loadtest_a,loadtest_b)
#   INCLUDE_DEMO       also use the demo database (true)
#   MIGRATIONS_PER_DB  approved migrations prepared per database (700)
#   JOB_VU_RATIO       jobs VUs = api VUs x ratio (0.25)
#   RUN_NAME           result file name, results/<name>.json (next free stress-NN)
#   BASE_URL, K6_IMAGE
#   TARGETS            JSON list of other databases to register instead of EXTRA_DBS
#                      (set by local-run.sh)
set -eu
cd "$(dirname "$0")/.."

K6_IMAGE=${K6_IMAGE:-grafana/k6:0.55.0}
EXTRA_DBS=${EXTRA_DBS:-loadtest_a,loadtest_b}
TARGETS=${TARGETS:-}

# 1. The extra databases, owned by the platform's role (the role cannot create databases itself).
if [ -z "$TARGETS" ]; then
  for db in $(echo "$EXTRA_DBS" | tr ',' ' '); do
    docker compose exec -T target-postgres sh -c \
      'psql -q -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v db="$1" -v owner="$TARGET_APP_USER"' -- "$db" <<'SQL'
SELECT format('CREATE DATABASE %I OWNER %I', :'db', :'owner')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'db')
\gexec
SQL
  done
fi

# 2. Result name: the next free stress-NN unless RUN_NAME is set.
mkdir -p loadtest/results
if [ -z "${RUN_NAME:-}" ]; then
  n=1
  while [ -e "loadtest/results/$(printf 'stress-%02d' "$n").json" ]; do n=$((n + 1)); done
  RUN_NAME=$(printf 'stress-%02d' "$n")
fi

# 3. The accounts setup() logs in with, read from the app's own environment into a private file
#    (JSON values on one line, as docker --env-file needs), removed on exit.
envfile=$(mktemp)
chmod 600 "$envfile"
trap 'rm -f "$envfile"' EXIT
for name in BOOTSTRAP_ADMINS SEED_DEMO_USERS TARGET_APP_USER TARGET_APP_PASSWORD; do
  printf '%s=%s\n' "$name" "$(docker compose exec -T app printenv "$name" | tr -d '\n')" >> "$envfile"
done
if [ -n "$TARGETS" ]; then
  printf 'TARGETS=%s\n' "$(printf '%s' "$TARGETS" | tr -d '\n')" >> "$envfile"
else
  printf 'EXTRA_DBS=%s\n' "$EXTRA_DBS" >> "$envfile"
fi

# 4. k6. Results: loadtest/results/$RUN_NAME.json and the text summary on stdout.
echo "k6 run $RUN_NAME"
docker run --rm --network host --user "$(id -u):$(id -g)" \
  -v "$PWD/loadtest:/scripts" --env-file "$envfile" \
  -e BASE_URL="${BASE_URL:-https://localhost}" -e INCLUDE_DEMO="${INCLUDE_DEMO:-true}" \
  -e MIGRATIONS_PER_DB="${MIGRATIONS_PER_DB:-700}" -e JOB_VU_RATIO="${JOB_VU_RATIO:-0.25}" \
  -e SUMMARY_FILE="/scripts/results/$RUN_NAME.json" \
  "$K6_IMAGE" run /scripts/stress.js
