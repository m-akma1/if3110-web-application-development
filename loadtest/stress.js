// k6 stress test for the platform: API reads plus migration jobs (execute -> poll until done) on
// several target databases at once, ramping 10 -> 20 -> 40 -> 80 -> 10 -> 0 VUs.
// Started by loadtest/run.sh (reproducible from the repository) or loadtest/local-run.sh (our
// IF3140 datasets); see README "Stress Test (k6)".
//
// setup() prepares everything through the public API, as an admin and the group leaders would:
//   - a user k6runner who leads the group "k6 Load Test" (VIEW_EDIT on every database used);
//   - the databases in TARGETS registered as targets;
//   - per database a pool of MIGRATIONS_PER_DB approved migrations (submitted, then approved by
//     the leader of every group with access). Each is a one-row UPDATE whose SQL Down is its
//     exact inverse, on a real table of that database (see PROFILES).
import http from 'k6/http'
import exec from 'k6/execution'
import { check, sleep, fail } from 'k6'
import { Counter, Rate, Trend } from 'k6/metrics'

const BASE = __ENV.BASE_URL || 'https://localhost'
const DEMO_DB = __ENV.DEMO_DB_NAME || 'PostgreSQL Demo'
const INCLUDE_DEMO = (__ENV.INCLUDE_DEMO || 'true') === 'true'
// Targets to register: TARGETS as [{"name", "host", "port", "dbname", "username", "password"}]
// (local-run.sh), or else one per EXTRA_DBS database on target-postgres, reached with the
// platform's own role (run.sh).
const TARGETS = __ENV.TARGETS
  ? JSON.parse(__ENV.TARGETS)
  : (__ENV.EXTRA_DBS || '').split(',').filter(Boolean).map((db) => ({
    name: `k6 ${db}`, host: __ENV.TARGET_HOST || 'target-postgres', port: 5432, dbname: db,
    username: __ENV.TARGET_APP_USER, password: __ENV.TARGET_APP_PASSWORD,
  }))
const DB_NAMES = [...(INCLUDE_DEMO ? [DEMO_DB] : []), ...TARGETS.map((t) => t.name)]
const PER_DB = Number(__ENV.MIGRATIONS_PER_DB || 700)
const JOB_RATIO = Number(__ENV.JOB_VU_RATIO || 0.25) // jobs VUs = api VUs x ratio
const POLL_MS = Number(__ENV.POLL_MS || 200)
const RUNNER = { username: 'k6runner', password: __ENV.RUNNER_PASSWORD || 'k6-Runner-Passw0rd' }
const GROUP = 'k6 Load Test'

// Users from the app's own configuration, so setup can log in as the admin and the leaders.
const ADMINS = JSON.parse(__ENV.BOOTSTRAP_ADMINS || '[]')
const DEMO_USERS = JSON.parse(__ENV.SEED_DEMO_USERS || '[]')
const PASSWORDS = Object.fromEntries([...ADMINS, ...DEMO_USERS].map((u) => [u.username, u.password]))

// What the scenarios do on each database, by database name (dbname of the target; "demo" for the
// demo database). browse: the table the API scenario pages through. up/down: migration i.
// A database without a profile gets the table k6_counters, created by setup.
const PROFILES = {
  demo: {
    browse: { table: 'orders', pages: 4 },
    up: (i) => `UPDATE products SET stock = stock + 1 WHERE id = ${(i % 30) + 1};`,
    down: (i) => `UPDATE products SET stock = stock - 1 WHERE id = ${(i % 30) + 1};`,
  },
  airport: {
    browse: { table: 'booking', pages: 50 },
    up: (i) => `UPDATE booking SET price = price + 1 WHERE booking_id = ${(i * 97) % 72000 + 1};`,
    down: (i) => `UPDATE booking SET price = price - 1 WHERE booking_id = ${(i * 97) % 72000 + 1};`,
  },
  bioskop: {
    browse: { table: '_reservasi', pages: 50 },
    up: (i) => `UPDATE _film SET durasi = durasi + 1 WHERE idfilm = ${(i % 50) + 1};`,
    down: (i) => `UPDATE _film SET durasi = durasi - 1 WHERE idfilm = ${(i % 50) + 1};`,
  },
  sales: {
    browse: { table: 'orders', pages: 50 },
    up: (i) => `UPDATE customers SET updated_at = updated_at + interval '1 second' WHERE customer_id = ${(i * 37) % 10000 + 1};`,
    down: (i) => `UPDATE customers SET updated_at = updated_at - interval '1 second' WHERE customer_id = ${(i * 37) % 10000 + 1};`,
  },
}
const COUNTERS = {
  browse: { table: 'k6_counters', pages: 1 },
  up: (i) => `UPDATE k6_counters SET n = n + 1 WHERE id = ${(i % 100) + 1};`,
  down: (i) => `UPDATE k6_counters SET n = n - 1 WHERE id = ${(i % 100) + 1};`,
  create: {
    up: 'CREATE TABLE IF NOT EXISTS k6_counters (id int PRIMARY KEY, n bigint NOT NULL DEFAULT 0);\n' +
      'INSERT INTO k6_counters (id) SELECT g FROM generate_series(1, 100) g ON CONFLICT DO NOTHING;',
    down: 'DROP TABLE k6_counters;',
  },
}
const profileOf = (key) => PROFILES[key] || COUNTERS

// VU load shape: ramp 30 s, hold 30 s at each level; 80 VUs are held 60 s.
const SHAPE = [
  { duration: '30s', target: 10 }, { duration: '30s', target: 10 },
  { duration: '30s', target: 20 }, { duration: '30s', target: 20 },
  { duration: '30s', target: 40 }, { duration: '30s', target: 40 },
  { duration: '30s', target: 80 }, { duration: '60s', target: 80 },
  { duration: '30s', target: 10 }, { duration: '30s', target: 10 },
  { duration: '30s', target: 0 },
]
// Named plateaus, by seconds since the start, for per-stage results (tag "stage").
const STAGES = [
  [0, 'a_ramp_to_10'], [30, 'b_hold_10'], [60, 'c_ramp_to_20'], [90, 'd_hold_20'],
  [120, 'e_ramp_to_40'], [150, 'f_hold_40'], [180, 'g_ramp_to_80'], [210, 'h_hold_80'],
  [270, 'i_down_to_10'], [300, 'j_hold_10'], [330, 'k_down_to_0'],
]
const stageAt = (s) => STAGES.filter(([t]) => s >= t).pop()[1]

const jobCompletion = new Trend('job_completion_ms', true)
const jobQueueWait = new Trend('job_queue_wait_ms', true)
const jobSuccess = new Rate('job_success')
const jobsExhausted = new Counter('jobs_pool_exhausted')

// Sub-metrics only appear in the summary when a threshold names them; the ones that only
// report (no real limit) use a limit that always passes.
const report = {}
for (const [, name] of STAGES) {
  report[`http_req_duration{scenario:api,stage:${name}}`] = ['p(95)<500']
  report[`http_reqs{stage:${name}}`] = ['count>=0']
  report[`job_completion_ms{stage:${name}}`] = ['p(95)<600000']
  report[`job_queue_wait_ms{stage:${name}}`] = ['p(95)<600000']
  report[`job_success{stage:${name}}`] = ['rate>=0']
}
for (const db of DB_NAMES) {
  report[`http_req_duration{scenario:api,db:${db}}`] = ['p(95)<600000']
  report[`job_completion_ms{db:${db}}`] = ['p(95)<600000']
  report[`job_queue_wait_ms{db:${db}}`] = ['p(95)<600000']
  report[`job_success{db:${db}}`] = ['rate>=0']
}

export const options = {
  insecureSkipTLSVerify: true, // Caddy's internal CA on https://localhost
  setupTimeout: '20m',
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
  scenarios: {
    api: { executor: 'ramping-vus', exec: 'api', startVUs: 0, stages: SHAPE, gracefulRampDown: '10s' },
    jobs: {
      executor: 'ramping-vus', exec: 'jobs', startVUs: 0, gracefulRampDown: '60s',
      stages: SHAPE.map((s) => ({ duration: s.duration, target: Math.round(s.target * JOB_RATIO) })),
    },
  },
  thresholds: {
    'http_req_duration{scenario:api}': ['p(95)<500'],
    http_req_failed: ['rate<0.01'],
    job_success: ['rate>0.99'],
    ...report,
  },
}

// ---- sessions -------------------------------------------------------------------------------

// login returns a session: its own cookie jar and the CSRF token for state-changing requests.
function login(username, password, jar) {
  // 401 before login is expected: it hands out the CSRF token for the login request.
  const me = http.get(`${BASE}/api/auth/me`, {
    jar, tags: { name: 'GET /api/auth/me (login)' }, responseCallback: http.expectedStatuses(200, 401),
  })
  const r = http.post(`${BASE}/api/auth/login`, JSON.stringify({ identifier: username, password }), {
    jar, headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': me.json('csrf_token') },
    tags: { name: 'POST /api/auth/login' },
  })
  if (r.status !== 200) fail(`login ${username}: ${r.status} ${r.body}`)
  const after = http.get(`${BASE}/api/auth/me`, { jar, tags: { name: 'GET /api/auth/me' } })
  return { jar, csrf: after.json('csrf_token') }
}

function params(s, name, extra = {}) {
  return {
    jar: s.jar, tags: { name, ...extra },
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': s.csrf },
  }
}
const post = (s, path, body, name) => http.post(`${BASE}${path}`, JSON.stringify(body || {}), params(s, name || `POST ${path}`))
const get = (s, path, name, extra) => http.get(`${BASE}${path}`, params(s, name || `GET ${path}`, extra))

function must(r, what) {
  if (r.status >= 300) fail(`${what}: ${r.status} ${r.body}`)
  return r
}

// ---- setup ----------------------------------------------------------------------------------

function overview(admin) {
  return must(get(admin, '/api/admin/overview'), 'admin overview').json()
}

export function setup() {
  const adminName = ADMINS[0] && ADMINS[0].username
  if (!adminName) fail('BOOTSTRAP_ADMINS is empty: start the test with loadtest/run.sh, which reads it from the app')
  const admin = login(adminName, PASSWORDS[adminName], new http.CookieJar())

  // The runner and its group.
  let ov = overview(admin)
  if (!ov.users.find((u) => u.username === RUNNER.username)) {
    must(post(admin, '/api/admin/users', { username: RUNNER.username, password: RUNNER.password }), 'create k6runner')
    ov = overview(admin)
  }
  const runnerID = ov.users.find((u) => u.username === RUNNER.username).id
  let group = ov.groups.find((g) => g.name === GROUP)
  if (!group) {
    must(post(admin, '/api/admin/groups', { name: GROUP, description: 'Dipakai oleh stress test k6' }), 'create group')
    ov = overview(admin)
    group = ov.groups.find((g) => g.name === GROUP)
  }
  if (!(group.members || []).find((m) => m.id === runnerID)) {
    must(post(admin, `/api/admin/groups/${group.id}/members`, { user_id: runnerID }), 'add k6runner')
  }

  // The databases: registered once, then the group gets View & Edit on each.
  for (const t of TARGETS) {
    if (!ov.targets.find((x) => x.name === t.name)) {
      must(post(admin, '/api/admin/databases', {
        name: t.name, host: t.host, port: t.port || 5432, dbname: t.dbname,
        username: t.username, password: t.password, sslmode: 'disable',
      }), `register ${t.name}`)
    }
  }
  ov = overview(admin)
  const dbs = DB_NAMES.map((name) => {
    const t = ov.targets.find((x) => x.name === name)
    if (!t) fail(`target ${name} not found`)
    if (t.status !== 'CONNECTED') fail(`target ${name} is ${t.status}: ${t.last_error}`)
    must(post(admin, '/api/admin/grants', { group_id: group.id, database_id: t.id, level: 'VIEW_EDIT' }), `grant ${name}`)
    const key = name === DEMO_DB ? 'demo' : (TARGETS.find((x) => x.name === name) || {}).dbname
    return { id: t.id, name, key }
  })

  // Every leader of a group with access to a database must approve its migrations.
  ov = overview(admin)
  const runner = login(RUNNER.username, RUNNER.password, new http.CookieJar())
  const sessions = { [RUNNER.username]: runner }
  for (const db of dbs) {
    db.leaders = ov.groups.filter((g) => g.grants && g.grants[db.id]).map((g) => {
      if (!g.leader_name) fail(`group ${g.name} has access to ${db.name} but no leader`)
      return g.leader_name
    })
    for (const n of db.leaders) {
      if (sessions[n]) continue
      if (!PASSWORDS[n]) fail(`no password for leader ${n}; add it to SEED_DEMO_USERS or BOOTSTRAP_ADMINS`)
      sessions[n] = login(n, PASSWORDS[n], new http.CookieJar())
    }
  }

  // Approved drafts on db, created in parallel batches of 50 requests.
  const approved = (db, drafts) => {
    const leaders = db.leaders.map((n) => sessions[n])
    const ids = []
    for (let i = 0; i < drafts.length; i += 50) {
      const chunk = drafts.slice(i, i + 50)
      const created = http.batch(chunk.map((d) => ['POST', `${BASE}/api/migration/drafts`, JSON.stringify(d), params(runner, 'POST /api/migration/drafts')]))
      const chunkIDs = created.map((r, j) => must(r, `draft ${chunk[j].name}`).json('draft.id'))
      http.batch(chunkIDs.map((id) => ['POST', `${BASE}/api/migration/${id}/submit-review`, '{}', params(runner, 'POST /api/migration/{id}/submit-review')]))
      for (const l of leaders) {
        http.batch(chunkIDs.map((id) => ['POST', `${BASE}/api/migration/${id}/approve`, '{}', params(l, 'POST /api/migration/{id}/approve')]))
      }
      ids.push(...chunkIDs)
    }
    return ids
  }

  // Databases without a profile get the k6_counters table first.
  for (const db of dbs) {
    const create = profileOf(db.key).create
    if (!create) continue
    const [id] = approved(db, [{ db_id: db.id, name: 'k6: tabel k6_counters', sql_up: create.up, sql_down: create.down }])
    must(post(runner, `/api/migration/${id}/execute`, {}), `create k6_counters on ${db.name}`)
    for (let i = 0; i < 100; i++) {
      const st = get(runner, `/api/migration/${id}/status`).json('migration.status')
      if (st === 'SUCCESS') break
      if (st === 'FAILED') fail(`k6_counters on ${db.name} failed`)
      sleep(0.2)
    }
  }

  // The pool of approved migrations, interleaved across databases.
  const pools = dbs.map((db) => {
    const p = profileOf(db.key)
    return approved(db, Array.from({ length: PER_DB }, (_, i) => ({
      db_id: db.id, name: `k6 #${i + 1} (${db.name})`, sql_up: p.up(i), sql_down: p.down(i),
    }))).map((id) => ({ id, db: db.name }))
  })
  const migrations = []
  for (let i = 0; i < PER_DB; i++) for (const p of pools) migrations.push(p[i])
  return { migrations, dbs: dbs.map((d) => ({ id: d.id, name: d.name, browse: profileOf(d.key).browse })) }
}

// ---- scenarios ------------------------------------------------------------------------------

let session // one login per VU

function vuSession() {
  if (!session) session = login(RUNNER.username, RUNNER.password, http.cookieJar())
  const elapsed = (Date.now() - exec.scenario.startTime) / 1000
  exec.vu.metrics.tags.stage = stageAt(elapsed)
  return session
}

// api: what a user does on the pages: session, viewer (a page of a real table), migration list
// and one migration's detail.
export function api(data) {
  const s = vuSession()
  const db = data.dbs[exec.vu.idInTest % data.dbs.length]
  const m = data.migrations[Math.floor(Math.random() * data.migrations.length)]
  const page = 1 + Math.floor(Math.random() * db.browse.pages)
  const tags = { db: db.name }
  const res = [
    get(s, '/api/auth/me', undefined, tags),
    get(s, `/api/viewer?db_id=${db.id}`, 'GET /api/viewer', tags),
    get(s, `/api/viewer/table?db_id=${db.id}&schema=public&name=${db.browse.table}&page=${page}`, 'GET /api/viewer/table', tags),
    get(s, `/api/migration?db_id=${db.id}`, 'GET /api/migration', tags),
    get(s, `/api/migration/${m.id}`, 'GET /api/migration/{id}', { db: m.db }),
  ]
  check(res, { 'api: all 200': (rs) => rs.every((r) => r.status === 200) })
  sleep(0.5 + Math.random())
}

// jobs: run one approved migration and wait for its job, like the migration page does.
export function jobs(data) {
  const s = vuSession()
  const i = exec.scenario.iterationInTest
  if (i >= data.migrations.length) {
    jobsExhausted.add(1)
    sleep(1)
    return
  }
  const m = data.migrations[i]
  const tags = { db: m.db }
  const start = Date.now()
  const r = http.post(`${BASE}/api/migration/${m.id}/execute`, '{}', params(s, 'POST /api/migration/{id}/execute', tags))
  if (!check(r, { 'execute: 202': (x) => x.status === 202 })) {
    jobSuccess.add(false, tags)
    return
  }
  const jobID = r.json('job_id')
  let job
  for (let n = 0; n < 600; n++) { // at most 2 minutes at 200 ms
    sleep(POLL_MS / 1000)
    const jr = http.get(`${BASE}/api/jobs/${jobID}`, params(s, 'GET /api/jobs/{id}', tags))
    if (jr.status !== 200) continue
    job = jr.json('job')
    if (job && job.done) break
  }
  const ok = !!job && job.done && job.status === 'SUCCESS'
  jobSuccess.add(ok, tags)
  if (job && job.done) {
    jobCompletion.add(Date.now() - start, tags)
    if (job.started_at) jobQueueWait.add(Date.parse(job.started_at) - Date.parse(job.created_at), tags)
  }
  sleep(1)
}

export function handleSummary(data) {
  const out = __ENV.SUMMARY_FILE || '/scripts/results/summary.json'
  return { [out]: JSON.stringify(data, null, 2), stdout: textSummary(data) }
}

// A compact text summary: the overall numbers, then one line per stage and per database.
function textSummary(d) {
  const m = d.metrics
  const v = (name, stat) => (m[name] && m[name].values[stat] !== undefined ? m[name].values[stat] : NaN)
  const f = (x) => (Number.isFinite(x) ? x.toFixed(1) : '-')
  const n = (x) => (Number.isFinite(x) ? String(x) : '-')
  const lines = [
    '',
    `requests ${v('http_reqs', 'count')} (${f(v('http_reqs', 'rate'))}/s), failed ${(v('http_req_failed', 'rate') * 100).toFixed(2)}%`,
    `api p50/p95/p99 ms: ${f(v('http_req_duration{scenario:api}', 'med'))} / ${f(v('http_req_duration{scenario:api}', 'p(95)'))} / ${f(v('http_req_duration{scenario:api}', 'p(99)'))}`,
    `jobs: ${v('job_success', 'passes')} ok, ${v('job_success', 'fails')} failed, pool exhausted ${v('jobs_pool_exhausted', 'count') || 0}`,
    `job completion p50/p95 ms: ${f(v('job_completion_ms', 'med'))} / ${f(v('job_completion_ms', 'p(95)'))}`,
    `job queue wait p50/p95 ms: ${f(v('job_queue_wait_ms', 'med'))} / ${f(v('job_queue_wait_ms', 'p(95)'))}`,
    '',
    'stage            reqs  jobs   api p50   api p95   job p50   job p95  queue p95',
  ]
  for (const [, s] of STAGES) {
    lines.push([
      s.padEnd(14),
      n(v(`http_reqs{stage:${s}}`, 'count')).padStart(6),
      n(v(`job_success{stage:${s}}`, 'passes')).padStart(5),
      f(v(`http_req_duration{scenario:api,stage:${s}}`, 'med')).padStart(9),
      f(v(`http_req_duration{scenario:api,stage:${s}}`, 'p(95)')).padStart(9),
      f(v(`job_completion_ms{stage:${s}}`, 'med')).padStart(9),
      f(v(`job_completion_ms{stage:${s}}`, 'p(95)')).padStart(9),
      f(v(`job_queue_wait_ms{stage:${s}}`, 'p(95)')).padStart(10),
    ].join(' '))
  }
  lines.push('', 'database                 jobs   api p95   job p50   job p95  queue p50  queue p95')
  for (const db of DB_NAMES) {
    lines.push([
      db.padEnd(22),
      n(v(`job_success{db:${db}}`, 'passes')).padStart(6),
      f(v(`http_req_duration{scenario:api,db:${db}}`, 'p(95)')).padStart(9),
      f(v(`job_completion_ms{db:${db}}`, 'med')).padStart(9),
      f(v(`job_completion_ms{db:${db}}`, 'p(95)')).padStart(9),
      f(v(`job_queue_wait_ms{db:${db}}`, 'med')).padStart(10),
      f(v(`job_queue_wait_ms{db:${db}}`, 'p(95)')).padStart(10),
    ].join(' '))
  }
  return lines.join('\n') + '\n'
}
