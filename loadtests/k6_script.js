// loadtests/k6_script.js
import http from 'k6/http';
import { check, sleep, group } from 'k6';
import { Rate, Trend, Counter, Gauge } from 'k6/metrics';
import { randomIntBetween, randomItem } from 'https://jslib.k6.io/k6-utils/1.4.0/index.js';

http.setResponseCallback(http.expectedStatuses(200, 201, 202, 204, 429));

// Custom metrics
const successRate = new Rate('successful_requests');
const requestLatency = new Trend('request_latency', true);
const rateLimited = new Counter('rate_limited_requests');
const dataTransferred = new Counter('data_transferred');
const connectionsOpened = new Counter('connections_opened');
const errorRate = new Rate('error_rate');
const throughput = new Gauge('throughput');
const activeVUs = new Gauge('active_vus');

// Configuration
const API_BASE_URL = __ENV.API_BASE_URL || 'http://localhost:8080';
const API_KEY = __ENV.API_KEY || 'test-api-key';
const VUS = __ENV.VUS ? parseInt(__ENV.VUS) : 200;
const DURATION = __ENV.DURATION || '30s';
const RAMP_UP = __ENV.RAMP_UP || '1m';

// Endpoints mapped to go-httpbin wildcard receiver
const endpoints = [
  { path: '/anything/data', method: 'GET', weight: 0.3 },
  { path: '/anything/users', method: 'GET', weight: 0.2 },
  { path: '/anything/orders', method: 'POST', weight: 0.2 },
  { path: '/anything/products', method: 'GET', weight: 0.15 },
  { path: '/anything/analytics', method: 'POST', weight: 0.15 },
];

// Test data generators
const generatePayload = () => {
  const types = ['read', 'write', 'update', 'delete'];
  return JSON.stringify({
    user_id: randomIntBetween(1000, 999999),
    action: randomItem(types),
    resource: randomItem(['users', 'orders', 'products', 'analytics']),
    data: {
      key: `metrics_${randomIntBetween(1, 100)}`,
      value: randomIntBetween(1, 10000),
      timestamp: new Date().toISOString(),
    }
  });
};

// Load test options with multiple scenarios
export const options = {
  discardResponseBodies: true,
  scenarios: {
    ramp_up: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '30s', target: Math.floor(VUS * 0.5) },
        { duration: '1m', target: VUS },
        { duration: '30s', target: 0 },
      ],
      gracefulRampDown: '10s',
      exec: 'regularTraffic',
    },
    sustained_load: {
      executor: 'constant-vus',
      vus: VUS,
      duration: DURATION,
      startTime: '2m10s',
      exec: 'regularTraffic',
    },
    spike: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '10s', target: VUS * 2 },
        { duration: '20s', target: VUS * 2 },
        { duration: '10s', target: 0 },
      ],
      startTime: '2m45s',
      exec: 'spikeTraffic',
    },
    stress: {
      executor: 'ramping-arrival-rate',
      startRate: 100,
      timeUnit: '1s',
      preAllocatedVUs: Math.floor(VUS * 0.5),
      maxVUs: VUS,
      stages: [
        { target: 1000, duration: '30s' },
        { target: 2000, duration: '30s' },
      ],
      startTime: '3m30s',
      exec: 'rateLimitTraffic',
    },
  },

// Pass/fail thresholds
  thresholds: {
    http_req_duration: ['p(95)<500', 'p(99)<1000'],
    http_req_failed: ['rate<0.01'],
    http_reqs: ['rate>1000'],
    successful_requests: ['rate>0.95'],
    error_rate: ['rate<0.05'],

    // Custom scenarios thresholds
    'http_req_duration{scenario:ramp_up}': ['p(95)<300'],
    'http_req_duration{scenario:spike}': ['p(95)<800', 'p(99)<1500'],
    'http_req_duration{scenario:sustained_load}': ['p(95)<400'],

    // Rate limit verification
    'http_req_duration{scenario:stress}': ['p(95)<600'],
  },
};

// Setup function - runs once before all scenarios
export function setup() {
  const healthCheck = http.get(`${API_BASE_URL}/healthz`);
  const readyCheck = http.get(`${API_BASE_URL}/readyz`);

  if (healthCheck.status !== 200) {
    throw new Error(`Health check failed: ${healthCheck.status}`);
  }

  if (readyCheck.status !== 200) {
    throw new Error(`Readiness check failed: ${readyCheck.status}`);
  }

  return {
    baseUrl: API_BASE_URL,
    apiKey: API_KEY,
    startTime: new Date().toISOString(),
  };
}

// Regular traffic pattern
export function regularTraffic(data) {
  const weights = endpoints.map(e => e.weight);
  const totalWeight = weights.reduce((a, b) => a + b, 0);
  let random = Math.random() * totalWeight;
  
  let selectedEndpoint = endpoints[0];
  for (let i = 0; i < endpoints.length; i++) {
    random -= endpoints[i].weight;
    if (random <= 0) {
      selectedEndpoint = endpoints[i];
      break;
    }
  }
  
  const url = `${data.baseUrl}${selectedEndpoint.path}`;
  const params = {
    headers: {
      'Content-Type': 'application/json',
      'X-API-Key': data.apiKey,
    },
  };
  
  let response;
  if (selectedEndpoint.method === 'GET') {
    response = http.get(url, params);
  } else {
    response = http.post(url, generatePayload(), params);
  }
  
  // Record metrics
  trackMetrics(response, selectedEndpoint.path);
  
  // Simulate realistic user think time
  sleep(randomIntBetween(0.1, 0.5));
  
  return response;
}

// Spike traffic pattern
export function spikeTraffic(data) {
  const url = `${data.baseUrl}/anything/spike-test`;
  const params = {
    headers: {
      'Content-Type': 'application/json',
      'X-API-Key': data.apiKey,
    },
  };

  const response = http.post(url, generatePayload(), params);
  trackMetrics(response, '/anything/spike-test');
  sleep(0.01);
  return response;
}

// Rate limit verification traffic
export function rateLimitTraffic(data) {
  const url = `${data.baseUrl}/anything/rate-limited`;
  const params = {
    headers: {
      'Content-Type': 'application/json',
      'X-API-Key': data.apiKey,
    },
  };

  const response = http.get(url, params);
  if (response.status === 429) {
    rateLimited.add(1);
  }

  trackMetrics(response, '/anything/rate-limited');
  return response;
}

// Track metrics helper function
function trackMetrics(response, path) {
  const isExpectedStatus = (response.status >= 200 && response.status < 300) || response.status === 429;
  const isFast = response.timings.duration < 1000;

  const success = check(response, {
    'status is 2xx or 429': () => isExpectedStatus,
    'response time < 1s': () => isFast,
  });

  successRate.add(success);
  errorRate.add(!success);

  if (!success && response.status >= 500) {
    console.error(`Server error on ${path}: ${response.status} ${response.body || ''}`);
  }

  requestLatency.add(response.timings.duration);
  if (response.body) {
    dataTransferred.add(response.body.length);
  }
}

// Teardown function - runs once after all scenarios
export function teardown(data) {
  const response = http.get(`${data.baseUrl}/healthz`);
  
  console.log(`\n=== Test Summary ===`);
  console.log(`Test completed at: ${new Date().toISOString()}`);
  console.log(`Final health check status: ${response.status}`);
  
  // Log key metrics
  const metrics = {
    total_requests: __VU,
    max_vus: VUS,
    duration: DURATION,
  };
  console.log(JSON.stringify(metrics, null, 2));
}

// Handle test abort conditions
export function handleSummary(data) {
  return {
    stdout: textSummary(data),
    'loadtests/results/summary.json': JSON.stringify(data, null, 2),
  };
}

function textSummary(data) {
  const lines = [];
  
  lines.push('=== High-Throughput API Gateway Load Test Results ===');
  lines.push('');
  
  // Overall metrics
  const metrics = data.metrics;
  lines.push('Overall Metrics:');
  lines.push(`  HTTP Requests: ${metrics.http_reqs.values.count}`);
  lines.push(`  Request Rate: ${formatRate(metrics.http_reqs.values.rate)} req/s`);
  lines.push(`  Success Rate: ${formatPercentage(metrics.http_req_failed ? 1 - metrics.http_req_failed.values.rate : 1)}`);
  lines.push('');
  
  // Latency percentiles
  lines.push('Latency Percentiles:');
  lines.push(`  P50: ${formatDuration(metrics.http_req_duration.values['med'])}`);
  lines.push(`  P90: ${formatDuration(metrics.http_req_duration.values['p(90)'])}`);
  lines.push(`  P95: ${formatDuration(metrics.http_req_duration.values['p(95)'])}`);
  lines.push(`  P99: ${formatDuration(metrics.http_req_duration.values['p(99)'])}`);
  lines.push('');
  
  // Rate limiting stats
  if (metrics.rate_limited_requests) {
    lines.push('Rate Limiting:');
    lines.push(`  Requests limited: ${metrics.rate_limited_requests.values.count}`);
    lines.push(`  Rate limited rate: ${formatRate(metrics.rate_limited_requests.values.rate)}/s`);
    lines.push('');
  }
  
  // Data transfer
  if (metrics.data_transferred) {
    lines.push('Data Transfer:');
    lines.push(`  Total: ${formatBytes(metrics.data_transferred.values.count)}`);
    lines.push('');
  }
  
  // Throughput
  if (metrics.throughput) {
    lines.push(`Throughput: ${formatRate(metrics.throughput.values.value)} req/s`);
    lines.push('');
  }
  
  // Scenario breakdown
  lines.push('Scenario Performance:');
  for (const [name, scenario] of Object.entries(data.scenarios || {})) {
    lines.push(`  ${name}:`);
    lines.push(`    Iterations: ${scenario.iterations}`);
    lines.push(`    Success rate: ${formatPercentage(scenario.successes / scenario.iterations)}`);
    lines.push('');
  }
  
  // Check results
  lines.push('Check Results:');
  for (const [name, check] of Object.entries(metrics)) {
    if (name.includes('checks')) {
      for (const [checkName, checkData] of Object.entries(check)) {
        if (typeof checkData === 'object' && checkData.fails !== undefined) {
          lines.push(`  ${checkName}: ${checkData.passes} passes, ${checkData.fails} fails`);
        }
      }
    }
  }
  
  return lines.join('\n');
}

// Helper formatting functions
function formatRate(value) {
  return (value || 0).toFixed(2);
}

function formatPercentage(value) {
  return `${(value * 100).toFixed(2)}%`;
}

function formatDuration(value) {
  if (!value) return 'N/A';
  if (value < 1000) return `${value.toFixed(2)}ms`;
  return `${(value / 1000).toFixed(2)}s`;
}

function formatBytes(value) {
  const units = ['B', 'KB', 'MB', 'GB'];
  let size = value || 0;
  let unitIndex = 0;
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024;
    unitIndex++;
  }
  return `${size.toFixed(2)} ${units[unitIndex]}`;
}