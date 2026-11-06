import benchmarks from '../data/benchmarks.json';

export type PhaseName = 'baseline' | 'pressure' | 'recovery';
export type MetricName = 'timely' | 'p95' | 'rejected' | 'active';
export const metricOptions = {
  timely: { label: 'Work completed within 100ms', unit: 'successful requests / second', suffix: ' /s' },
  p95: { label: 'Critical-request p95', unit: 'milliseconds · successful responses only', suffix: ' ms' },
  rejected: { label: 'Admission rejection rate', unit: 'percent of sent requests', suffix: '%' },
  active: { label: 'Observed backend concurrency', unit: 'cumulative peak active requests through this phase', suffix: '' },
};
const names: Record<string, string[]> = { direct: ['Direct', 'unguarded'], 'proxy-fixed': ['Proxy', 'fixed'], 'proxy-adaptive': ['Proxy', 'adaptive'], 'middleware-fixed': ['Middleware', 'fixed'], 'middleware-adaptive': ['Middleware', 'adaptive'] };

export default function BenchmarkChart({ phase, metric }: { phase: PhaseName; metric: MetricName }) {
  const info = metricOptions[metric];
  const allValues = benchmarks.deployments.flatMap(d => d.phases[phase].samples.map(s => s[metric]));
  const max = Math.max(...allValues, 1) * 1.18;
  const plot = { x: 75, y: 40, width: 850, height: 270 };
  return <div className="benchmark-chart"><div className="chart-heading"><div><h3>{info.label}</h3><span>{info.unit}</span></div><span className="chart-legend"><i />Median <b>·</b> Individual repeats</span></div><svg viewBox="0 0 1000 400" role="img" aria-label={`${info.label} in the ${phase} phase, median and three measured repetitions`}>
    {Array.from({ length: 5 }, (_, i) => { const v = max * i / 4; const y = plot.y + plot.height - plot.height * i / 4; return <g key={i}><line x1={plot.x} x2={plot.x + plot.width} y1={y} y2={y} stroke="#2a3730" /><text x={plot.x - 18} y={y + 4} fill="#91a49a" textAnchor="end" fontSize="12">{v.toFixed(v < 10 ? 1 : 0)}</text></g>; })}
    {benchmarks.deployments.map((d, i) => {
      const p = d.phases[phase]; const x = plot.x + i * 175 + 29; const y = plot.y + plot.height - p[metric] / max * plot.height;
      const adaptive = d.name.includes('adaptive'); const color = adaptive ? '#72dfc3' : d.name === 'direct' ? '#ef604c' : '#657c70';
      return <g key={d.name}><title>{d.name}: {p[metric].toFixed(2)}{info.suffix}</title><rect x={x} y={y} width="94" height={Math.max(plot.y + plot.height - y, 2)} rx="3" fill={color} opacity={adaptive ? 1 : .8} /><text x={x + 47} y={y - 15} fill="#eaf0e3" textAnchor="middle" fontWeight="600" fontSize="14">{p[metric].toFixed(metric === 'active' ? 0 : 1)}</text>{p.samples.map((s, j) => <circle key={j} cx={x + 23 + j * 24} cy={plot.y + plot.height - s[metric] / max * plot.height} r="4" fill="#101715" stroke="#e4eee5" strokeWidth="1.5"><title>Repeat {s.repeat}: {s[metric].toFixed(2)}{info.suffix}</title></circle>)}<text x={x + 47} y="343" fill="#d4dfd5" textAnchor="middle" fontSize="14">{names[d.name][0]}</text><text x={x + 47} y="365" fill={adaptive ? '#72dfc3' : '#91a49a'} textAnchor="middle" fontSize="12">{names[d.name][1]}</text></g>;
    })}
  </svg><p className="chart-footnote mono">APPLE M4 PRO · {benchmarks.go.toUpperCase()} · 3 REPEATS · NO POOLED PERCENTILES</p></div>;
}
