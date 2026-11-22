// Shared formatting for Fraud Shield components.
export const BAND_COLORS = {
  low: '#6fae8b',
  medium: '#d29b3a',
  high: '#d8505c',
  unscored: '#7d8290',
};

export const STATUS_LABELS = {
  held: 'Held',
  watch: 'Watching',
  launched: 'Sent',
  released: 'Released',
  blocked: 'Blocked',
};

export const PATTERN_LABELS = {
  micro_drain: 'Micro-draining',
  whale: 'Whale transfer',
  odd_hour_spike: 'Odd-hour spike',
  layering: 'Layering ring',
  mule_fan_in: 'Mule fan-in',
  new_account_burst: 'New-account burst',
};

export const SCENARIOS = [
  { id: 'mixed_attack', label: 'Mixed attack', hint: 'All six fraud patterns mixed into normal traffic.' },
  { id: 'account_takeover', label: 'Account takeover', hint: 'One bot drains its account, plus two oversized transfers.' },
  { id: 'laundering_ring', label: 'Laundering ring', hint: 'Money moved in loops, plus one mule account collecting payments.' },
  { id: 'normal_day', label: 'Normal day', hint: 'No fraud at all. Shows how often normal transfers get held.' },
];

export function fmtAmount(v) {
  if (v == null) return '-';
  return Number(v).toLocaleString('en-US', { maximumFractionDigits: 2 });
}

export function fmtScore(v) {
  return v == null ? '-' : Number(v).toFixed(2);
}

export function timeAgo(iso) {
  if (!iso) return '';
  const s = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  return `${Math.round(s / 3600)}h ago`;
}
