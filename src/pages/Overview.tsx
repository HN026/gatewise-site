import { useState } from 'react';
import { CodeBlock } from '../components/ui';

function AdmissionFlow() {
  const [paused, setPaused] = useState(false);
  return <figure className={`home-flow ${paused ? 'paused' : ''}`}>
    <div className="flow-caption"><span>Request admission</span><span>Illustration · not live traffic</span></div>
    <svg viewBox="0 0 480 310" role="img" aria-label="Critical and background requests pass through a shared concurrency guard. Excess background work receives 503.">
      <defs><marker id="home-arrow" markerWidth="6" markerHeight="6" refX="5" refY="3" orient="auto"><path d="M0 0L6 3L0 6" fill="#7da18c" /></marker></defs>
      <text x="24" y="76" className="flow-label">Critical</text><text x="24" y="161" className="flow-label">Background</text>
      <path d="M24 96H175M24 181H175M306 138H390M240 212V255" fill="none" stroke="#486d56" strokeWidth="1.5" markerEnd="url(#home-arrow)" />
      <circle cx="30" cy="96" r="4" className="flow-dot critical-dot" /><circle cx="30" cy="96" r="4" className="flow-dot critical-dot delayed" /><circle cx="30" cy="181" r="4" className="flow-dot background-dot" /><circle cx="30" cy="181" r="4" className="flow-dot background-dot delayed" />
      <rect x="177" y="59" width="130" height="154" rx="5" fill="#132b20" stroke="#6ab991" /><text x="242" y="91" textAnchor="middle" className="flow-title">Gatewise</text><text x="242" y="117" textAnchor="middle" className="flow-detail">limit = 20</text><line x1="194" x2="290" y1="131" y2="131" stroke="#315a40" /><text x="242" y="156" textAnchor="middle" className="flow-detail">background ≤ 15</text><text x="242" y="180" textAnchor="middle" className="flow-detail">reserved = 5</text>
      <circle cx="316" cy="138" r="4" className="flow-dot admitted-dot" /><circle cx="240" cy="222" r="4" className="flow-dot rejected-dot" />
      <rect x="391" y="108" width="73" height="61" rx="3" fill="#17261c" stroke="#4b7157" /><text x="427" y="135" textAnchor="middle" className="flow-title small">Handler</text><text x="427" y="153" textAnchor="middle" className="flow-detail small">execute</text>
      <rect x="178" y="258" width="130" height="38" rx="3" fill="#281d18" stroke="#9b5b4b" /><text x="243" y="282" textAnchor="middle" className="flow-rejection">503 · rejected</text>
    </svg>
    <figcaption>Background work cannot occupy the critical reservation. Both classes remain subject to the total limit.</figcaption><button className="flow-toggle" onClick={() => setPaused(p => !p)} aria-pressed={paused}>{paused ? 'Resume animation' : 'Pause animation'}</button>
  </figure>;
}

export default function Overview() {
  return <div className="home-page"><section className="home-intro"><div className="home-copy"><span className="doc-category">Gatewise · Go library and HTTP proxy</span><h1>Protect your service<br />from overload.</h1><p>Too many requests can overwhelm a service and slow it down. Gatewise limits concurrent requests, jobs and database operations, reserves room for important work, and rejects excess load before it starts.</p><div className="home-actions"><a href="#/problem" className="home-primary">Problem &amp; design <span>→</span></a><a href="#/installation" className="home-secondary">Installation &amp; deployment</a></div><ul className="home-facts"><li>Configurable priority tiers</li><li>Fixed or bounded adaptive limits</li><li>Prometheus metrics and Grafana</li></ul></div><AdmissionFlow /></section><section className="home-source"><div><h2>Source installation</h2><p>Install the Go module or build the proxy from the tagged source.</p></div><CodeBlock filename="Terminal" language="shell" code={'git clone --branch v0.8.1 https://github.com/HN026/gatewise.git\ncd gatewise'} /><a href="downloads/gatewise-demo.zip" download>Download demo example</a></section></div>;
}
