#!/bin/sh
# The stress test with the custom local databases (airport, bioskop, sales) instead of the
# empty extra databases of run.sh. 
#
# FOR LOCAL DEVELOPMENT (TEAM ONLY): the databases are git-ignored.
# This script does not work from a fresh clone. Use run.sh to reproduce the test
# from the repository alone.
#
#   make observability                  (or: docker compose up -d --build)
#   loadtest/local-run.sh
#
# Each demo/<name>/docker-compose.yml starts PostgreSQL on the platform's network (service
# <name>-db, user praktikum), so the app registers it as a target by that host name. Every other
# setting is passed on to run.sh (RUN_NAME, INCLUDE_DEMO, MIGRATIONS_PER_DB, ...).
#
#   LOCAL_DBS   which of the demo databases to use (airport,bioskop,sales)
set -eu
cd "$(dirname "$0")/.."

LOCAL_DBS=${LOCAL_DBS:-airport,bioskop,sales}

targets=""
for db in $(echo "$LOCAL_DBS" | tr ',' ' '); do
  if [ ! -f "demo/$db/docker-compose.yml" ]; then
    echo "demo/$db/docker-compose.yml not found: local-run.sh needs the IF3140 datasets in demo/ (use run.sh instead)" >&2
    exit 1
  fi
  # The first start loads the dump, which takes a while; wait until it accepts TCP connections
  # (during the load the server only listens on its Unix socket).
  docker compose -f "demo/$db/docker-compose.yml" up -d
  until docker compose -f "demo/$db/docker-compose.yml" exec -T "$db-db" \
      pg_isready -q -h 127.0.0.1 -U praktikum -d "$db"; do
    sleep 2
  done
  echo "demo database $db ready"
  targets="$targets${targets:+,}{\"name\":\"IF3140 $db\",\"host\":\"$db-db\",\"port\":5432,\"dbname\":\"$db\",\"username\":\"praktikum\",\"password\":\"labdas\"}"
done

TARGETS="[$targets]" exec loadtest/run.sh
