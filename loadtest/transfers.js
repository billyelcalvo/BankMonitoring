// Load test for POST /transfers.
//
// Worst case on purpose: every request moves money between the same two
// accounts (Alice <-> Bob), so all of them compete for the same two row
// locks. About 10% of iterations immediately retry with the same
// Idempotency-Key, like a client that timed out, and must get a replay.
//
//   docker compose --profile loadtest run --rm k6
//   BASE_URL=https://your-app k6 run loadtest/transfers.js

import http from 'k6/http';
import { check, fail } from 'k6';
import { Counter, Trend } from 'k6/metrics';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const PASSWORD = __ENV.DEMO_PASSWORD || 'demo-password';
const VUS = Number(__ENV.LOAD_VUS || 50);
const DURATION = __ENV.LOAD_DURATION || '60s';
// Both demo PEN accounts start with 1,000,000.00; transfers only move money between them.
const EXPECTED_TOTAL = Number(__ENV.EXPECTED_TOTAL || 200000000);

const replays = new Counter('idempotent_replays');
const duplicatedMoves = new Counter('duplicated_moves');
const transferLatency = new Trend('transfer_latency', true);
// Time spent inside the API, read from its Server-Timing header. The gap
// between this and http_req_duration is network (and proxy) time.
const serverTime = new Trend('server_time', true);

function recordServerTime(res) {
  const match = /dur=([\d.]+)/.exec(res.headers['Server-Timing'] || '');
  if (match) serverTime.add(Number(match[1]));
}

export const options = {
  scenarios: {
    transfers: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '10s', target: VUS },
        { duration: DURATION, target: VUS },
        { duration: '5s', target: 0 },
      ],
      gracefulRampDown: '5s',
    },
  },
  thresholds: {
    'http_req_duration{name:create_transfer}': ['p(95)<100'],
    'http_req_failed{name:create_transfer}': ['rate<0.01'],
    'http_reqs{name:create_transfer}': ['count>0'],
    checks: ['rate>0.99'],
    duplicated_moves: ['count==0'],
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

function login(email) {
  const res = http.post(`${BASE_URL}/auth/login`, JSON.stringify({ email, password: PASSWORD }), {
    headers: { 'Content-Type': 'application/json' },
    tags: { name: 'login' },
  });
  if (res.status !== 200) fail(`login failed for ${email}: ${res.status} ${res.body}`);
  return res.json('access_token');
}

export function setup() {
  return {
    alice: { token: login('alice@example.com'), from: 'acc-alice-pen', to: 'acc-bob-pen' },
    bob: { token: login('bob@example.com'), from: 'acc-bob-pen', to: 'acc-alice-pen' },
  };
}

export default function (users) {
  const user = Math.random() < 0.5 ? users.alice : users.bob;
  const key = crypto.randomUUID();
  const body = JSON.stringify({
    from_account_id: user.from,
    to_account_id: user.to,
    amount: 1 + Math.floor(Math.random() * 1000),
    currency: 'PEN',
  });
  const params = {
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${user.token}`,
      'Idempotency-Key': key,
    },
    tags: { name: 'create_transfer' },
  };

  const res = http.post(`${BASE_URL}/transfers`, body, params);
  transferLatency.add(res.timings.duration);
  recordServerTime(res);
  check(res, { 'transfer created (201)': (r) => r.status === 201 });

  if (Math.random() < 0.1) {
    const retry = http.post(`${BASE_URL}/transfers`, body, params);
    transferLatency.add(retry.timings.duration);
    recordServerTime(retry);
    const ok = check(retry, {
      'retry replayed (200)': (r) => r.status === 200 && r.headers['Idempotent-Replayed'] === 'true',
      'retry returns the same transfer': (r) => r.json('id') === res.json('id'),
    });
    if (ok) replays.add(1);
    if (retry.status === 201) duplicatedMoves.add(1);
  }
}

// After the run, the money held by both accounts must be exactly what it was.
export function teardown(users) {
  const balance = (user) => {
    const res = http.get(`${BASE_URL}/accounts/${user.from}`, {
      headers: { Authorization: `Bearer ${user.token}` },
      tags: { name: 'get_account' },
    });
    return res.json('balance');
  };
  const total = balance(users.alice) + balance(users.bob);
  check(total, { 'total money conserved': (t) => t === EXPECTED_TOTAL });
  console.log(`total PEN across both accounts: ${total} (expected ${EXPECTED_TOTAL})`);
}

export function handleSummary(data) {
  const m = data.metrics;
  const d = m['http_req_duration{name:create_transfer}'] || m.transfer_latency;
  const reqs = m['http_reqs{name:create_transfer}'];
  const fmt = (v) => `${v.toFixed(2)} ms`;
  const lines = [
    '| Metric | Value |',
    '| --- | --- |',
    `| Virtual users | ${VUS} |`,
    `| Requests to POST /transfers | ${reqs.values.count} total, ${reqs.values.rate.toFixed(0)} req/s |`,
    `| Latency avg / p50 | ${fmt(d.values.avg)} / ${fmt(d.values.med)} |`,
    `| Latency p90 / p95 / p99 | ${fmt(d.values['p(90)'])} / ${fmt(d.values['p(95)'])} / ${fmt(d.values['p(99)'])} |`,
    ...(m.server_time
      ? [`| Server time p50 / p95 / p99 (Server-Timing) | ${fmt(m.server_time.values.med)} / ${fmt(m.server_time.values['p(95)'])} / ${fmt(m.server_time.values['p(99)'])} |`]
      : []),
    `| Failed requests | ${(m['http_req_failed{name:create_transfer}'].values.rate * 100).toFixed(2)}% |`,
    `| Idempotent replays verified | ${m.idempotent_replays ? m.idempotent_replays.values.count : 0} |`,
    `| Duplicated money movements | ${m.duplicated_moves ? m.duplicated_moves.values.count : 0} |`,
  ];
  const table = lines.join('\n') + '\n';
  return {
    stdout: `\n${table}\n`,
    '/scripts/results/summary.md': table,
    '/scripts/results/summary.json': JSON.stringify(data, null, 2),
  };
}
