#!/usr/bin/env bash
# Staging rehearsal for POST /api/token/redeem. Run as root on the host that
# runs production new-api. Everything it creates is named nas-* or lives under
# $ROOT and is removed on exit. It reads the production database once
# (pg_dump into the staging copy) and never writes to it. In the staging copy
# every channel is disabled and its key scrubbed before any app starts, so the
# rehearsal can never call an upstream provider.
set -euo pipefail

IMAGE=${IMAGE:-mengyu/new-api:v1.0.0-rc.45-redeem.1}
OFFICIAL=${OFFICIAL:-calciumion/new-api@sha256:6c63c7ee9cd5bf7880df3984e99dd3ef07992d7a97538cfb2719adf9d87bc3f9}
PG_IMAGE=${PG_IMAGE:-postgres@sha256:c961aa287d8698297cb26cdfadfbe9fd2cbaf77e53cfffe9636e8d8a1e4d842c}
REDIS_IMAGE=${REDIS_IMAGE:-redis@sha256:4fa24486b8bcca8eec45ee0eb166edc674795e53a2b53d1a9ef263eecebaac85}
MYSQL_IMAGE=${MYSQL_IMAGE:-mysql:8.0}
PROD_PG=${PROD_PG:-new-api-postgres}
ROOT=${ROOT:-/data/new-api-staging/run}
NET=nas-net
PORT=3100
BASE=http://127.0.0.1:$PORT
PW=nas-staging-pw
PG_DSN="postgresql://newapi:$PW@nas-pg:5432/new-api"

step() { echo "== $*"; }
pass() { echo "   ok: $*"; }
fail() { echo "FAIL: $*" >&2; exit 1; }
cleanup() {
  docker rm -f nas-app nas-pg nas-mysql nas-redis >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  rm -rf "$ROOT"
}
trap cleanup EXIT
cleanup
mkdir -p "$ROOT"
chmod 700 "$ROOT"
docker network create "$NET" >/dev/null

pgq() { docker exec nas-pg psql -U newapi -d new-api -tAq -v ON_ERROR_STOP=1 -c "$1"; }
prodq() { docker exec "$PROD_PG" psql -U newapi -d new-api -tAq -v ON_ERROR_STOP=1 -c "$1"; }
myq() { docker exec nas-mysql mysql -uroot -p"$PW" -N -B new-api -e "$1" 2>/dev/null; }

wait_ready() {
  for _ in $(seq 1 90); do
    if curl -fsS --max-time 3 "$BASE/api/status" >/dev/null 2>&1; then return 0; fi
    sleep 2
  done
  docker logs --tail 40 nas-app >&2 || true
  fail "app did not become ready"
}

run_app() {
  local image=$1
  shift
  docker rm -f nas-app >/dev/null 2>&1 || true
  docker run -d --name nas-app --network "$NET" -p "127.0.0.1:$PORT:3000" \
    -e SESSION_SECRET=nas-staging-session -e TZ=Asia/Shanghai -e NODE_NAME=nas-staging \
    -e CRITICAL_RATE_LIMIT=1000 -e CRITICAL_RATE_LIMIT_DURATION=60 \
    "$@" "$image" >/dev/null
  wait_ready
}

restart_app() {
  docker restart nas-app >/dev/null
  wait_ready
}

start_pg() {
  docker rm -f nas-pg >/dev/null 2>&1 || true
  docker run -d --name nas-pg --network "$NET" -e POSTGRES_USER=newapi -e POSTGRES_PASSWORD="$PW" -e POSTGRES_DB=new-api "$PG_IMAGE" >/dev/null
  for _ in $(seq 1 90); do
    if docker logs nas-pg 2>&1 | grep -q "PostgreSQL init process complete" && docker exec nas-pg pg_isready -U newapi -d new-api >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  fail "postgres not ready"
}

# ---------- 1. fresh install on PostgreSQL, MySQL and SQLite, each started twice ----------

schema_pg() {
  pgq "select indexname || ' ' || indexdef from pg_indexes where tablename = 'token_redemptions' order by 1"
  pgq "select column_name || ' ' || data_type from information_schema.columns where table_name = 'token_redemptions' order by 1"
}

step "fresh postgres"
start_pg
run_app "$IMAGE" -e SQL_DSN="$PG_DSN"
first=$(schema_pg)
restart_app
second=$(schema_pg)
[ -n "$first" ] && [ "$first" = "$second" ] || fail "postgres: token_redemptions schema missing or changed on second start"
echo "$first" | grep -qi "unique index.*(redemption_id)" || fail "postgres: unique index on redemption_id missing"
pass "postgres: table + unique index, identical after second start"

step "fresh mysql"
docker run -d --name nas-mysql --network "$NET" -e MYSQL_ROOT_PASSWORD="$PW" -e MYSQL_DATABASE=new-api "$MYSQL_IMAGE" >/dev/null
for _ in $(seq 1 120); do
  if docker logs nas-mysql 2>&1 | grep -q "port: 3306  MySQL Community Server"; then break; fi
  sleep 2
done
docker logs nas-mysql 2>&1 | grep -q "port: 3306  MySQL Community Server" || fail "mysql not ready"
mysql_version=$(myq "select version()")
run_app "$IMAGE" -e SQL_DSN="root:$PW@tcp(nas-mysql:3306)/new-api"
schema_my() {
  myq "show index from token_redemptions" | awk '{print $2, $3, $5}'
  myq "show columns from token_redemptions"
}
first=$(schema_my)
restart_app
second=$(schema_my)
[ -n "$first" ] && [ "$first" = "$second" ] || fail "mysql: token_redemptions schema missing or changed on second start"
echo "$first" | grep -q "^0 idx_token_redemptions_redemption_id redemption_id" || fail "mysql: unique index on redemption_id missing"
pass "mysql $mysql_version: table + unique index, identical after second start"
docker rm -f nas-mysql >/dev/null

step "fresh sqlite"
mkdir -p "$ROOT/sqlite"
run_app "$IMAGE" -v "$ROOT/sqlite:/data"
schema_lite() {
  python3 - "$ROOT/sqlite/one-api.db" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
for (sql,) in db.execute("select sql from sqlite_master where tbl_name = 'token_redemptions' order by name"):
    print(sql)
PY
}
first=$(schema_lite)
restart_app
second=$(schema_lite)
[ -n "$first" ] && [ "$first" = "$second" ] || fail "sqlite: token_redemptions schema missing or changed on second start"
echo "$first" | grep -qi "create unique index.*redemption_id" || fail "sqlite: unique index on redemption_id missing"
sqlite_version=$(python3 -c "import sqlite3; print(sqlite3.sqlite_version)")
pass "sqlite (host lib $sqlite_version): table + unique index, identical after second start"
docker rm -f nas-app >/dev/null

# ---------- 2. upgrade a copy of the production database ----------

prod_counts() { prodq "select (select count(*) from users) || ',' || (select count(*) from tokens) || ',' || (select count(*) from redemptions) || ',' || (select count(*) from channels) || ',' || coalesce(to_regclass('token_redemptions')::text, 'absent')"; }
counts() { pgq "select (select count(*) from users) || ',' || (select count(*) from tokens) || ',' || (select count(*) from channels) || ',' || (select count(*) from options) || ',' || (select count(*) from logs) || ',' || (select count(*) from redemptions)"; }

step "upgrade production copy"
prod_before=$(prod_counts)
start_pg
docker exec "$PROD_PG" pg_dump -U newapi -d new-api --no-owner --no-privileges > "$ROOT/prod.sql"
chmod 600 "$ROOT/prod.sql"
docker exec -i nas-pg psql -U newapi -d new-api -q -v ON_ERROR_STOP=1 < "$ROOT/prod.sql" >/dev/null
rm -f "$ROOT/prod.sql"
pgq "update channels set status = 2, \"key\" = 'staging-disabled'"
imported=$(counts)
run_app "$OFFICIAL" -e SQL_DSN="$PG_DSN"
docker rm -f nas-app >/dev/null
[ "$(pgq "select to_regclass('token_redemptions') is null")" = "t" ] || fail "official image unexpectedly has token_redemptions"
baseline=$(counts)
docker run -d --name nas-redis --network "$NET" "$REDIS_IMAGE" >/dev/null
run_app "$IMAGE" -e SQL_DSN="$PG_DSN" -e REDIS_CONN_STRING=redis://nas-redis:6379 -e BATCH_UPDATE_ENABLED=true
restart_app
upgraded=$(counts)
[ "$baseline" = "$upgraded" ] || fail "row counts changed by upgrade: $baseline -> $upgraded"
[ "$(pgq "select to_regclass('token_redemptions') is not null")" = "t" ] || fail "token_redemptions missing after upgrade"
pass "imported $imported; after official start $baseline; after new image x2 $upgraded"

# ---------- 3. behaviour over real HTTP, PostgreSQL and Redis ----------

step "scenarios"
NOW=$(date +%s)
uid=$(pgq "insert into users (username, password, display_name, role, status, quota, \"group\", aff_code) values ('nas-$NOW', 'x', 'nas', 1, 1, 1000000, 'default', 'nas$NOW') returning id")
mk_token() { pgq "insert into tokens (user_id, \"key\", status, name, created_time, accessed_time, expired_time, remain_quota, unlimited_quota, \"group\") values ($uid, '$1', $4, 'nas', $NOW, $NOW, -1, $2, $3, 'default') returning id"; }
mk_code() { pgq "insert into redemptions (user_id, \"key\", status, name, quota, created_time, redeemed_time, used_user_id, expired_time) values (1, '$1', 1, 'nas', 500000, $NOW, 0, 0, 0) returning id" >/dev/null; }
randkey() { openssl rand -hex 24; }
randcode() { openssl rand -hex 16; }
redeem() { curl -sS --max-time 30 -o "$3" -w '%{http_code}' -X POST "$BASE/api/token/redeem" -H "Authorization: Bearer sk-$1" -H 'Content-Type: application/json' -d "{\"code\":\"$2\"}"; }
user_quota() { pgq "select quota from users where id = $uid"; }
token_field() { pgq "select $2 from tokens where \"key\" = '$1'"; }
rows_for() { pgq "select count(*) from token_redemptions tr join redemptions r on r.id = tr.redemption_id where r.\"key\" = '$1'"; }

K1=$(randkey); K2=$(randkey); K3=$(randkey); K4=$(randkey); K5=$(randkey); K6=$(randkey)
mk_token "$K1" 100000 false 1 >/dev/null
mk_token "$K2" 100000 false 1 >/dev/null
mk_token "$K3" 0 true 1 >/dev/null
mk_token "$K4" 0 false 4 >/dev/null
mk_token "$K5" 0 false 1 >/dev/null
mk_token "$K6" 0 false 1 >/dev/null
C1=$(randcode); C2=$(randcode); C3=$(randcode); C4=$(randcode); C5=$(randcode)
for c in "$C1" "$C2" "$C3" "$C4" "$C5"; do mk_code "$c"; done

code=$(redeem "$K1" "$C1" "$ROOT/b1")
[ "$code" = 200 ] && grep -q '"success":true' "$ROOT/b1" && grep -q '"token_remain_quota":600000' "$ROOT/b1" || fail "limited token: $code $(cat "$ROOT/b1")"
[ "$(token_field "$K1" remain_quota)" = 600000 ] && [ "$(user_quota)" = 1500000 ] && [ "$(rows_for "$C1")" = 1 ] || fail "limited token: database not credited once"
pass "limited token: key 100000 -> 600000, account 1000000 -> 1500000"

code=$(redeem "$K1" "$C1" "$ROOT/b2")
[ "$code" = 200 ] && grep -q '"success":true' "$ROOT/b2" || fail "replay: $code $(cat "$ROOT/b2")"
[ "$(token_field "$K1" remain_quota)" = 600000 ] && [ "$(user_quota)" = 1500000 ] && [ "$(rows_for "$C1")" = 1 ] || fail "replay credited again"
pass "replay with the same key: success, nothing credited again"

code=$(redeem "$K2" "$C1" "$ROOT/b3")
[ "$code" = 200 ] && grep -q '"success":false' "$ROOT/b3" || fail "other key of same account: $code $(cat "$ROOT/b3")"
[ "$(token_field "$K2" remain_quota)" = 100000 ] || fail "other key was credited"
pass "same code from another key of the same account: refused"

code=$(redeem "$K3" "$C2" "$ROOT/b4")
[ "$code" = 200 ] && grep -q '"token_unlimited":true' "$ROOT/b4" && ! grep -q token_remain_quota "$ROOT/b4" || fail "unlimited token: $code $(cat "$ROOT/b4")"
[ "$(user_quota)" = 2000000 ] || fail "unlimited token: account not credited"
pass "unlimited key: account credited, key balance not returned"

before_models=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/models" -H "Authorization: Bearer sk-$K4")
code=$(redeem "$K4" "$C3" "$ROOT/b5")
[ "$code" = 200 ] && grep -q '"success":true' "$ROOT/b5" || fail "exhausted token: $code $(cat "$ROOT/b5")"
[ "$(token_field "$K4" status)" = 1 ] || fail "exhausted token not re-enabled"
after_models=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/models" -H "Authorization: Bearer sk-$K4")
[ "$before_models" = 401 ] && [ "$after_models" = 200 ] || fail "exhausted token: /v1/models $before_models -> $after_models"
pass "exhausted key revived; /v1/models $before_models -> $after_models (cache sees the new balance)"

quota_before=$(user_quota)
mkdir -p "$ROOT/concurrent"
for i in $(seq 1 20); do
  { redeem "$K5" "$C4" "$ROOT/concurrent/body$i"; echo; } >"$ROOT/concurrent/status$i" &
done
wait
successes=$(cat "$ROOT"/concurrent/body* | grep -c '"success":true' || true)
[ "$successes" = 1 ] || fail "concurrency: $successes successes"
[ "$(token_field "$K5" remain_quota)" = 500000 ] && [ "$(rows_for "$C4")" = 1 ] && [ "$(user_quota)" = $((quota_before + 500000)) ] || fail "concurrency: credited more than once"
pass "20 concurrent redemptions of one code: exactly 1 success, credited once (HTTP statuses: $(cat "$ROOT"/concurrent/status* | sort | uniq -c | tr -s ' \n' ' '))"

code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/token/")
[ "$code" = 401 ] || fail "GET /api/token/ without login returned $code"
pass "GET /api/token/ without login: 401"

for key in "$K6" "$(randkey)"; do
  for i in $(seq 1 5); do
    code=$(redeem "$key" "$(randcode)" "$ROOT/l$i")
    [ "$code" = 200 ] || fail "lockout: attempt $i returned $code"
  done
  code=$(redeem "$key" "$C5" "$ROOT/l6")
  [ "$code" = 429 ] || fail "lockout: 6th attempt returned $code"
done
[ "$(pgq "select status from redemptions where \"key\" = '$C5'")" = 1 ] || fail "lockout: locked request consumed a code"
pass "existing and made-up keys both locked after 5 failures; locked request consumed nothing"

prod_after=$(prod_counts)
[ "$prod_before" = "$prod_after" ] || fail "production changed: $prod_before -> $prod_after"
pass "production database unchanged ($prod_after)"

echo "ALL STAGING CHECKS PASSED"
