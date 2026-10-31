import demo from '../data/demo.json';
import { Arrow, Button, CodeBlock, Diagram, Note, PageTitle, Section, Table } from '../components/ui';

export default function Examples() {
  return <div className="container"><PageTitle title="Examples and demo">Three PostgreSQL-backed services. Run them directly, behind Gatewise, or with Gatewise middleware.</PageTitle>
    <div className="download-panel"><div className="download-mark"><img src="logo.svg" alt="Gatewise" /></div><div><span className="mono">Demo example · v0.9.0</span><h2>gatewise-demo.zip</h2><p>{demo.files} source files · {(demo.bytes / 1024).toFixed(0)} KB · Go 1.25+ · Python 3.10+ · Podman or Docker</p></div><Button href="downloads/gatewise-demo.zip" download>Download demo example</Button></div>
    <div className="download-links"><a href="downloads/gatewise-demo.sha256" download>SHA-256 <Arrow /></a><a href="downloads/postman.zip" download>Postman collection <Arrow /></a></div>
<Section id="adaptive-example" title="Controller evaluation">
<p>The v0.9.0 example retains the existing tuned legacy settings and adds an explicit controller-profile option. With the option, unspecified adaptive fields use library defaults: floor/initial 5 for this reservation, nominal interval 1s and minimum samples 10. Without it, previous demo commands keep their tuned legacy behavior.</p>
<CodeBlock filename="From the extracted demo" language="shell" code={'python3 run.py --profile middleware-adaptive --controller-profile stable --verify\n# Include the controlled signal interaction contract:\npython3 run.py --profile proxy-adaptive --controller-profile stable --decisions enforce --verify\n# Repeat the full controller comparison:\npython3 adaptation_benchmark.py --output /tmp/adaptation-results.json'}/>
<p>The Adaptive Postman collection verifies profile selection and metrics, real injected checkout failures, sparse-feedback holds after idle, and the optional signal ceiling below the adaptive floor. GET order lookup does not inject SQL failures; the fault check uses checkout deliberately. Set a 5s request timeout and 15s script timeout. The sparse check includes a 5.2s idle pause and three spaced requests.</p>
<p>The benchmark compares the retained tuned legacy controller, legacy defaults and stable defaults across middleware and proxy deployments. Its scenarios cover abrupt/gradual slowdown, periodic overload, errors, sparse traffic, idle restart, mixed costs and a controlled signal ceiling. The controller is the only changed policy; v0.8.1’s SQL/worker balanced policy is not enabled in this experiment. Three service processes plus the optional proxy retain the four-application-process limit.</p>
<p>Reports include both priority classes, individual services, generator dispatch, scheduled-time client latency, exact adjustment-counter deltas and sampled limit traces. Acceptance criteria and aggregation live in controller_evaluation/. Early exploratory runs informed the candidate; published charts use the fixed-code repeated matrix.</p>
</Section><Section id="operation-example" title="SQL, worker jobs and a real standby">
<p>The orders process contains a reference storage service: interactive transactions and six background workers compete for the same four PostgreSQL connections. A physical streaming standby serves read queries. Catalog and inventory stay available; proxy mode adds one HTTP proxy, keeping the application-process maximum at four.</p>
<CodeBlock filename="From the extracted demo · requires running Podman or Docker" language="shell" code={'python3 run.py --profile middleware-fixed --operations pool --verify\n# Or keep services running and import the printed Postman environment:\npython3 run.py --profile proxy-adaptive --operations pool'} />
<p>For the opt-in v0.8.1 policy, run <code>python3 run.py --operations balanced --profile middleware-fixed --verify</code>. Balanced uses real pool-wait feedback and existing 3:1 workload sharing; the prior modes remain unchanged. Repeat its comparison with <code>python3 pool_balance_benchmark.py --output /tmp/pool-balance-results.json</code>.</p><p>Use <code>--operations disabled</code> to bypass public SQL admission, <code>fixed</code> for a four-operation SQL budget with a critical reservation, or <code>pool</code> to add real pool pressure feedback for background jobs. Worker admission stays enabled in all variants. The lifetime probe uses sqlguard in every variant. The launcher creates isolated primary/standby containers and volumes; stopping it removes only its own fixtures.</p>
<p>The Operations Postman collection commits a transaction, reads the standby, verifies that a SQL stream keeps a permit after its HTTP handler returns, exhausts the pool, checks deadline cleanup and recovery, submits background jobs and inspects operation metrics. Admin controls bind numeric loopback and are fixture tools, not production APIs.</p>
<CodeBlock filename="Repeat contention and replication validation" language="shell" code={'python3 operation_benchmark.py --output /tmp/operation-results.json'} />
<p>The runner pauses actual WAL replay, generates database writes, resumes replay, stops the standby and verifies reconnect recovery. No fake lag values are injected. Jobs use a bounded, non-durable example queue with visible drops and rejections; durable delivery, retries and idempotency belong to the application.</p>
</Section>
    <Section title="Application model"><Diagram name="commerce-services" caption="Each service owns a database, a four-connection pool and its admission budget. The proxy is the fourth HTTP application; middleware runs inside the three services." />
      <Table headers={['Service', 'Critical business operation', 'Background operation']} rows={[
        ['Orders', 'Idempotent checkout write and order lookup', 'Aggregate order amounts and update report'],
        ['Catalog', 'Read a bounded list of persisted products', 'Aggregate prices and description lengths'],
        ['Inventory', 'Idempotent reservation with atomic stock decrement', 'Aggregate available stock and update report'],
      ]} /><p>Each database starts with 20,000 rows. Business requests and reports share that service’s SQL connection pool. Services run as independent processes; checkout does not orchestrate a distributed transaction across all three. PostgreSQL and optional monitoring are infrastructure, separate from the four application processes.</p>
      <Note label="Example settings">Each guard has a ceiling of 12 requests and reserves 4 slots for critical work. Adaptive mode starts at 12 and stays between 5 and 12. The database pool allows 4 connections. HTTP concurrency and SQL connections are different limits; these experimental values require tuning for your application.</Note>
    </Section>
    <Section id="priority-chain" title="Priority through a real service chain">
      <p>Enable the optional v0.4.0 scenario. Orders calls the catalog API, which performs a real PostgreSQL read. The flow reads catalog data; checkout remains a separate operation. Middleware uses three business processes; proxy mode adds one shared proxy for both hops.</p>
      <CodeBlock filename="terminal · extracted demo" language="shell" code={`python3 run.py --profile middleware-fixed --priorities
# Or exercise both hops through one Gatewise proxy:
python3 run.py --profile proxy-adaptive --priorities
# Run the focused Postman contract and clean up:
python3 run.py --profile proxy-fixed --priorities --verify`} />
      <p>The launcher prints the environment and dynamic addresses. Import <code>postman/Priorities.postman_collection.json</code> for this scenario. Routes <code>/orders/flows/urgent</code>, <code>/orders/flows/normal</code> and <code>/orders/flows/batch</code> select server defaults. Catalog defaults to batch, so the returned upstream/downstream diagnostics show whether importance survived. Loopback is intentionally trusted in this isolated demo; this is not an authentication mechanism.</p>
      <p>Orders allows 12 concurrent requests, catalog 6, and each reserves 2 for urgent plus 1 for normal. SQL pools remain 4. Cohorts use 128 buckets and 2s rotations with a public demo-only seed. Run tenant and priority contract modes separately. Production deployments need their own authenticated identities, ingress trust policy, seed and measured limits.</p>
      <CodeBlock filename="Reproduce per-tier measurements" language="shell" code={`python3 priority_benchmark.py --output results/priority-results.json
python3 validate_priority.py --output results/priority-validation.json`} />
      <p>The repeated experiment measures both hops at baseline, two catalog-pressure levels and recovery. It retains outcomes for every tier, successful-flow propagation checks, same-epoch cohort comparisons and stable-key rotation probes.</p>
    </Section>
    <Section id="resource-regulators" title="Exercise real resource regulators"><p>The same three services now expose bounded allocation work, fanout, persisted writes and hot-key updates. No extra application process is added. Memory and goroutine observations come from the backend process in both integrations.</p><CodeBlock filename="terminal · extracted demo" language="shell" code={`python3 run.py --profile middleware-fixed --resources enforce --verify
python3 run.py --profile proxy-adaptive --resources observe --verify
python3 run.py --profile proxy-fixed --resources disabled --verify
python3 resource_benchmark.py --output results/resource-results.json`}/><p>Import <code>Resources.postman_collection.json</code> with the printed environment. Loopback clients act as the trusted operation planner in this isolated demo. Write cost is the actual persisted value size computed from the validated plan; the JSON body itself is much smaller. The backend rejects metadata that understates or changes that plan.</p><p>Memory requests retain 8 MiB and fanout creates 32 waiting child goroutines. Writes persist 128 KiB values under a 256 KiB concurrent byte budget. Hot-key work contends on one row while other requests update 16 cold keys, with a per-key limit of 1 and registry bound 32. These are experimental values. Runtime thresholds sample every 50 ms and can overshoot. The benchmark explicitly collects unused runtime pages between phases; that is fixture cleanup, not a production recovery guarantee.</p></Section><Section id="controlled-signals" title="Test signal decisions">
 <p>This scenario adds two controlled observation endpoints to each existing service’s loopback admin port. It keeps real PostgreSQL work and the maximum of four application processes. The values are test inputs, not measurements of actual pool pressure.</p>
 <CodeBlock filename="terminal · extracted demo" language="shell" code={`python3 run.py --profile middleware-fixed --decisions enforce --verify
python3 run.py --profile proxy-adaptive --decisions observe --verify
python3 decision_benchmark.py --output results/decision-results.json`} />
 <p>Import <code>postman/Signals.postman_collection.json</code> with the printed environment. The collection checks healthy readings, conflicting sources, stale readings, unknown state, provider errors/timeouts and recovery. It also verifies that caller headers cannot select observe mode and that observation controls are unavailable on the business port.</p>
 <p>The primary source caps matching work at 4 under pressure and uses 2 when unavailable. The secondary source requests rejection under pressure and permits work when unavailable. Both are sampled every 100 ms with a 50 ms deadline and 600 ms freshness bound. In observe mode, these remain proposals; hard capacity controls continue to run. Run this focused contract separately from the tenant/priority scenarios.</p>
 </Section>
 <Section id="tenant-sharing" title="Try tenant sharing">
      <p>The existing three PostgreSQL services can now use hard caps or lend spare capacity. The example share is 2 concurrent requests per tenant. A ceiling of 4 means up to 4 total requests, not 4 extra borrowed requests.</p>
      <CodeBlock filename="terminal · extracted demo" language="shell" code={`python3 run.py --profile middleware-fixed --sharing 4
# Or put the sharing policy in the proxy:
python3 run.py --profile proxy-adaptive --sharing 4
# Run the focused Postman contract and remove the disposable environment:
python3 run.py --profile proxy-fixed --sharing 4 --verify`} />
      <p>Import <code>postman/Sharing.postman_collection.json</code> with the environment printed by the launcher. It checks actual borrowing, progress for other tenants, bounded tracking and aggregate-only metrics. Use <code>--tenants</code> without sharing for the earlier hard-cap scenario. The CLI runs priority, tenant-hard-cap and sharing contracts separately; the library can combine tenant sharing with priority tiers.</p>
      <CodeBlock filename="Compare capacity use and returning customers" language="shell" code={`python3 sharing_benchmark.py --output results/sharing-results.json`} />
      <p>The benchmark starts with one busy customer, brings three others back, makes all four busy, lets the other customers leave, then restores healthy traffic. It compares a hard cap of 2 with a share of 2 and borrowing ceilings of 4 and 12. Every service still has 4 SQL connections, so more borrowed HTTP requests can also mean more database waiting. Loopback identities and these limits are only local demo settings.</p>
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
    <Section id="tenant-isolation" title="Tenant isolation example">
      <CodeBlock filename="terminal · extracted gatewise-demo" language="shell" code={`python3 run.py --profile proxy-adaptive --tenants
# Middleware mode:
python3 run.py --profile middleware-adaptive --tenants
# With an installed Postman CLI:
python3 run.py --profile proxy-adaptive --tenants --verify`} />
      <p>The tenant profile uses an example cap of 2 concurrent requests per tenant, 32 identified entries and a 2s idle TTL. Import <code>postman/Tenants.postman_collection.json</code> and the generated environment. It exercises ordinary tenants alongside a noisy tenant, registry pressure, expiry, identity errors and aggregate metrics. The existing Commerce collection targets the default tenant-disabled profile.</p>
      <Note label="Local identity">The demo trusts <code>X-Demo-Tenant</code> only from loopback peers so you can create synthetic tenants in Postman. This is not authentication. Production ingress must authenticate and overwrite identity metadata, or middleware must resolve an authenticated principal.</Note>
      <CodeBlock filename="terminal · noisy-neighbor comparison" language="shell" code={`# Three repeats across ten policy variants; roughly 12 minutes:
python3 tenant_benchmark.py --output results/tenant-results.json
# Short wiring check:
python3 tenant_benchmark.py --repeats 1 --phase-scale .2 \
  --adapters proxy-fixed-tenants middleware-adaptive-tenants`} />
      <p>Comparison adapters share the same real application and Go arrival generator. Variants include direct traffic, a plain fixed semaphore, and fixed/adaptive proxy and middleware with tenant limits on and off. All benchmark requests are critical so priority reservation does not confound tenant isolation. The semaphore reference is a demo benchmark policy.</p>
    </Section>
    <Section title="Reproduce benchmarks"><CodeBlock filename="terminal · from gatewise-demo" language="shell" code={`# Three repeats × five deployment profiles; about 32 minutes:
python3 benchmark.py --output results/commerce-results.json

# Short wiring check, not equivalent benchmark evidence:
python3 benchmark.py --repeats 1 --phase-scale .1 \
  --output results/smoke.json`} /><p>The Go load generator schedules independent arrivals with bounded in-flight work and no application retries. It records missed arrivals, dispatch lag, successful latency, rejection rates, database waits and controller traces. Stop competing workloads before measuring.</p><a className="text-link" href="#/benchmarks">Read the measured protocol and limitations <Arrow /></a>
      <CodeBlock filename="bundle structure" language="text" code={`gatewise-demo/
├── run.py                 # isolated processes and PostgreSQL
├── benchmark.py           # service-overload protocol
├── tenant_benchmark.py    # noisy-neighbor/churn comparison
├── comparison.py          # policy adapters and metadata
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
