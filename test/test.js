import http from "k6/http";
import { check, sleep } from "k6";
import { Counter, Trend } from "k6/metrics";

// ── Custom metrics ──────────────────────────────────────────────
const irisErrors   = new Counter("iris_errors");
const modelErrors  = new Counter("model_errors");
const irisDuration = new Trend("iris_duration_ms",  true);
const modelDuration= new Trend("model_duration_ms", true);

// ── Load stages ─────────────────────────────────────────────────
export const options = {
  stages: [
    { duration: "15s", target: 10  }, // ramp up
    { duration: "30s", target: 10  }, // steady
    { duration: "15s", target: 30  }, // spike
    { duration: "30s", target: 30  }, // hold spike
    { duration: "10s", target: 0   }, // ramp down
  ],
  thresholds: {
  "iris_duration_ms":  ["p(95)<500"],       // p95 of iris must be < 500ms
  "model_duration_ms": ["p(95)<30000"],      // p95 of model must be < 30s
  "http_req_failed":     ["rate<0.01"],
    // Overall HTTP failure rate < 1%
  },
};

const BASE = "http://localhost:8080";

const IRIS_CASES = [
  { sepal_length: 5.1, sepal_width: 3.5, petal_length: 1.4, petal_width: 0.2 }, // setosa
  { sepal_length: 6.0, sepal_width: 3.0, petal_length: 5.5, petal_width: 2.0 }, // virginica
  { sepal_length: 5.9, sepal_width: 3.0, petal_length: 4.2, petal_width: 1.5 }, // versicolor
];

const MODEL_PROMPTS = [
  "用一句话解释什么是机器学习。",
  "What is the capital of Australia?",
  "Write a haiku about rain.",
];

// ── Main VU loop ─────────────────────────────────────────────────
export default function () {
  testIris();
  sleep(0.5);
  testModel();
  sleep(0.5);
}

// ── Iris endpoint ────────────────────────────────────────────────
function testIris() {
  const body   = IRIS_CASES[Math.floor(Math.random() * IRIS_CASES.length)];
  const params = { headers: { "Content-Type": "application/json" } };

  const res = http.post(
    `${BASE}/predict/iris`,
    JSON.stringify(body),
    params
  );

  irisDuration.add(res.timings.duration);

  const ok = check(res, {
    "iris: status 200":          (r) => r.status === 200,
    "iris: has class_name":      (r) => JSON.parse(r.body).result !== undefined,
    "iris: valid class":         (r) => {
      const name = JSON.parse(r.body).result;
      return ["setosa", "versicolor", "virginica"].includes(name);
    },
  });

  if (!ok) irisErrors.add(1);
}

// ── Model endpoint ───────────────────────────────────────────────
function testModel() {
  const prompt = MODEL_PROMPTS[Math.floor(Math.random() * MODEL_PROMPTS.length)];
  const params = {
    headers:  { "Content-Type": "application/json" },
    timeout:  "90s",   // LLM can be slow — don't let k6 cut it short
  };

  const res = http.post(
    `${BASE}/predict/model`,
    JSON.stringify({ prompt }),
    params
  );

  modelDuration.add(res.timings.duration);

  const ok = check(res, {
    "model: status 200":         (r) => r.status === 200,
    "model: has reply":          (r) => JSON.parse(r.body).reply  !== undefined,
    "model: reply non-empty":    (r) => JSON.parse(r.body).reply.length > 0,
    "model: has output_tokens":  (r) => JSON.parse(r.body).metrics?.output_tokens > 0,
    "model: duration_sec > 0":   (r) => JSON.parse(r.body).metrics?.duration_sec  > 0,
  });

  if (!ok) modelErrors.add(1);
}

//docker run --rm -i --network host grafana/k6 run - < test.js