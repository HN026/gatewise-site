import demo from '../data/demo.json';
import { Arrow, Button, CodeBlock, Diagram, Note, PageTitle, Section, Table } from '../components/ui';

export default function Examples() {
  return <div className="container"><PageTitle title="Examples and demo">Three PostgreSQL-backed services. Run them directly, behind Gatewise, or with Gatewise middleware.</PageTitle>
    <div className="download-panel"><div className="download-mark"><img src="logo.svg" alt="Gatewise" /></div><div><span className="mono">Demo example · v0.2.0</span><h2>gatewise-demo.zip</h2><p>{demo.files} source files · {(demo.bytes / 1024).toFixed(0)} KB · Go 1.25+ · Python 3.10+ · Podman or Docker</p></div><Button href="downloads/gatewise-demo.zip" download>Download demo example</Button></div>
    <div className="download-links"><a href="downloads/gatewise-demo.sha256" download>SHA-256 <Arrow /></a><a href="downloads/postman.zip" download>Postman collection <Arrow /></a></div>
    <Section title="Application model"><Diagram name="commerce-services" caption="Each service owns a database, a four-connection pool and its admission budget. The proxy is the fourth HTTP application; middleware runs inside the three services." />
      <Table headers={['Service', 'Critical business operation', 'Background operation']} rows={[
        ['Orders', 'Idempotent checkout write and order lookup', 'Aggregate order amounts and update report'],
        ['Catalog', 'Read a bounded list of persisted products', 'Aggregate prices and description lengths'],
        ['Inventory', 'Idempotent reservation with atomic stock decrement', 'Aggregate available stock and update report'],
      ]} /><p>Each database starts with 20,000 rows. Business requests and reports share that service’s SQL connection pool. Services run as independent processes; checkout does not orchestrate a distributed transaction across all three. PostgreSQL and optional monitoring are infrastructure, separate from the four application processes.</p>
      <Note label="Example settings">Each guard has a ceiling of 12 requests and reserves 4 slots for critical work. Adaptive mode starts at 12 and stays between 5 and 12. The database pool allows 4 connections. HTTP concurrency and SQL connections are different limits; these experimental values require tuning for your application.</Note>
    </Section>
    <Section title="Run the demo"><CodeBlock filename="terminal" language="shell" code={`unzip gatewise-demo.zip
cd gatewise-demo
python3 run.py --profile proxy-adaptive
# Docker instead of Podman:
python3 run.py --engine docker --profile middleware-adaptive`} /><p>Start your container engine first. The launcher builds the included source, starts an isolated PostgreSQL 18.6 container and three services, then prints the addresses and generated Postman environment. Ports are selected dynamically. Ctrl+C removes only this example’s processes and disposable database volume.</p>
      <Table headers={['Profile', 'HTTP application processes', 'Admission placement']} rows={[
        ['direct', '3', 'Unprotected baseline'], ['proxy-fixed / proxy-adaptive', '4', 'Gatewise routes to three unmodified backends'], ['middleware-fixed / middleware-adaptive', '3', 'A guard inside each Go service'],
      ]} /><p>All listeners bind to localhost. Public disposable credentials are included for the local database. Add <code>--monitor</code> to start optional local Prometheus and Grafana with the correct scrape targets. No production credentials, compiled binaries or personal environment files are packaged. The included modules use local replacements so the example builds independently of a remote tag.</p>
    </Section>
    <Section title="Postman scenarios"><p>Import <code>postman/Commerce.postman_collection.json</code> and the environment file printed by the launcher. Run the whole collection in order against a fresh launch; use a 5s request timeout and 15s script timeout.</p>
      <Table headers={['Checks', 'Expected behavior']} rows={[
        ['Orders persistence', 'Create 201 → replay 200 with the same ID → conflicting amount 409'],
        ['Catalog', 'Persisted products; bounded query size; invalid limit 400'],
        ['Inventory consistency', 'Replay decrements stock once; key conflict and insufficient stock return 409'],
        ['SQL faults and recovery', 'Injected failures return 500; restored controls allow healthy requests'],
        ['Admission', 'Protected background overload produces 503; critical work still completes'],
        ['Observability', 'Bounded pools, mode headers, pool metrics and adaptive controller metrics'],
      ]} /><CodeBlock filename="optional Postman CLI" language="shell" code={`# Runs the same collection and cleans up on completion:
python3 run.py --profile proxy-adaptive --verify`} /><p>The separate loopback admin listeners expose benchmark controls. They are test instrumentation and are never routed through Gatewise. The collection restores controls after fault checks. Stop and relaunch for a clean repeat.</p><a className="text-link" href="data/commerce-contract-results.json" download>Recorded Postman contract results ↗</a>
    </Section>
    <Section title="Reproduce benchmarks"><CodeBlock filename="terminal · from gatewise-demo" language="shell" code={`# Three repeats × five deployment profiles; about 32 minutes:
python3 benchmark.py --output results/commerce-results.json

# Short wiring check, not equivalent benchmark evidence:
python3 benchmark.py --repeats 1 --phase-scale .1 \
  --output results/smoke.json`} /><p>The Go load generator schedules independent arrivals with bounded in-flight work and no application retries. It records missed arrivals, dispatch lag, successful latency, rejection rates, database waits and controller traces. Stop competing workloads before measuring.</p><a className="text-link" href="#/benchmarks">Read the measured protocol and limitations <Arrow /></a>
      <CodeBlock filename="bundle structure" language="text" code={`gatewise-demo/
├── run.py                 # isolated processes and PostgreSQL
├── benchmark.py           # phase protocol and sampled evidence
├── validate_controller.py # constant-load slowdown validation
├── monitor.py             # optional monitoring lifecycle
├── observability/         # commerce dashboard
├── gatewise/              # library and proxy source snapshot
├── commerce-backend/      # orders/catalog/inventory + Go loadgen
├── postman/               # portable collection and guide
├── README.md              # reproduction and safety boundary
└── manifest-sha256.json`} />
    </Section>
  </div>;
}
