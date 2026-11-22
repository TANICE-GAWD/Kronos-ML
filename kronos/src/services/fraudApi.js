// REST helpers for the Fraud Shield endpoints (analysts only).
import { API_BASE_URL } from '../config';

function authHeaders() {
  const token = localStorage.getItem('authToken');
  return {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  };
}

async function request(path, options = {}) {
  const res = await fetch(`${API_BASE_URL}${path}`, { headers: authHeaders(), ...options });
  let data = null;
  try {
    data = await res.json();
  } catch {
    data = null;
  }
  if (!res.ok) {
    throw new Error((data && data.error) || `Request failed (${res.status})`);
  }
  return data;
}

export const fraudApi = {
  me: () => request('/api/me'),
  queue: () => request('/api/fraud/queue'),
  stats: () => request('/api/fraud/stats'),
  model: () => request('/api/fraud/model'),
  caseDetail: (txId) => request(`/api/fraud/case/${txId}`),
  release: (txId, note = '') =>
    request(`/api/fraud/${txId}/release`, { method: 'POST', body: JSON.stringify({ note }) }),
  block: (txId, note = '') =>
    request(`/api/fraud/${txId}/block`, { method: 'POST', body: JSON.stringify({ note }) }),
  simSeed: (n = 300) => request('/api/sim/seed', { method: 'POST', body: JSON.stringify({ n }) }),
  simStart: (body) => request('/api/sim/start', { method: 'POST', body: JSON.stringify(body) }),
  simStop: () => request('/api/sim/stop', { method: 'POST' }),
  simStatus: () => request('/api/sim/status'),
};

// Opening a case from anywhere (3D scene, alert feed, queue) goes through one event.
export const OPEN_CASE_EVENT = 'kronos:open-case';
export function openCase(txId) {
  window.dispatchEvent(new CustomEvent(OPEN_CASE_EVENT, { detail: txId }));
}

export default fraudApi;
