import { CodeBlock, Diagram, Section, Table } from '../components/ui';
import { sources } from '../data/content';

export default function Problem() {
  return <div className="container problem-doc">
    <header className="page-title"><span className="doc-category">Problem &amp; design</span><h1>From request concurrency to adaptive admission</h1><p>How overload develops, why request rate is insufficient, and how Gatewise bounds work before adding priorities, feedback and observability.</p></header>
    <Section id="basics" title="Requests, latency and concurrency">
      <p>An HTTP client sends a request and the server runs a handler. That handler may read a database, call another service, or wait for a response to be written. Waiting does not necessarily use much CPU, but the request still retains a goroutine, buffers, connections or application state.</p>
      <Table headers={['Term', 'Meaning']} rows={[
        ['Arrival rate', 'Requests reaching the service per second (RPS).'], ['Latency', 'Time between starting a request and completing its synchronous work.'], ['Concurrency', 'Requests active at the same time. This is different from RPS and open TCP connections.'], ['Throughput', 'Requests completed per second. Accepted and rejected completions must be counted separately.'], ['Admission control', 'A decision to accept or reject work before executing the application handler.'],
      ]} />
      <p>Gatewise controls the lifetime of admitted synchronous handlers. It does not count database operations, CPU utilization or bytes processed, and it does not manage an application’s asynchronous jobs.</p>
    </Section>
    <Section id="overload" title="How dependency slowdown causes overload">
      <p>Arrival rate alone does not determine service load. The number of active requests also depends on how long each request occupies resources. In a stable system, Little’s law relates average work in flight to arrival rate and average duration:</p>
      <pre className="formula">average in-flight work ≈ requests/second × average duration</pre>
      <p>At 120 requests/second and 50ms duration, approximately 6 requests are active. If a dependency slows to 500ms at the same rate, that becomes approximately 60. Active handlers retain goroutines, buffers, connections or application state. This describes a stable average; a growing backlog is a transient overload condition.</p>
      <Diagram name="overload" caption="Dependency slowdown increases work in flight. Admission rejects excess arrivals before they reach the handler." />
      <h3>Rate limiting and concurrency limiting</h3><p>A rate limit bounds arrivals per unit time. It does not directly bound active work when request duration changes. Gatewise bounds admitted synchronous requests instead. It adds no waiting queue: when capacity is unavailable, the caller receives 503 and decides whether to retry.</p>
      <h3>Priority-aware admission</h3><p>A shared ceiling can be filled by background work before a critical request arrives. Gatewise therefore limits background concurrency within the total budget. Priority is assigned by server route configuration or a middleware wrapper; an incoming header cannot promote a request.</p>
    </Section>
    <Section id="evolution" title="From fixed thresholds to feedback">
      <p>Static request-rate quotas are useful for API policy and caller fairness. They become a weak overload signal when requests have different costs or a dependency changes speed. A fixed concurrency limit directly bounds active work, but still needs tuning as workload and service conditions change.</p>
      <p>Gatewise was built in the following order. Each layer solves a separate limitation; the proxy and Go middleware now share the same admission implementation.</p>
      <Table headers={['Layer', 'Implemented behavior', 'Remaining limitation']} rows={[
        ['HTTP proxy', 'Named upstreams, segment-prefix routing, configurable timeouts, error mapping and graceful shutdown.', 'Forwarding alone does not bound active work.'],
        ['Fixed concurrency', 'Independent service budgets; immediate 503 when full.', 'Background work can occupy the whole budget.'],
        ['Critical reservation', 'A background quota within the shared total limit.', 'A fixed ceiling may be too high or low after service conditions change.'],
        ['Adaptive control', 'Latency-budget and server-error feedback within configured bounds.', 'Signals are local; no tenant fairness or resource-specific detectors.'],
        ['Observability', 'Bounded native statistics, Prometheus metrics and a Grafana dashboard.', 'Monitoring does not change configuration or prove production capacity.'],
        ['Validation', 'Independent backend consumers, API scenarios and repeatable baseline/pressure/recovery experiments.', 'Results apply to the documented local workload.'],
      ]} />
      <h3>Relationship to Uber’s load manager</h3>
      <p><a href="https://www.uber.com/us/en/blog/from-static-rate-limiting-to-intelligent-load-management/" target="_blank" rel="noreferrer">Uber’s article</a> describes a database load manager with priority-aware shedding, tenant controls and resource-specific overload signals. Gatewise applies a narrower HTTP admission model. The following distinctions are part of its current scope.</p>
      <Table headers={['Concern', 'Gatewise implementation']} rows={[
        ['Priority', 'Two server-assigned classes with critical reservation; no propagated tier/cohort hierarchy.'],
        ['Queuing', 'No waiting queue, queue timeout policy or FIFO/LIFO scheduling. Unavailable capacity produces 503 immediately.'],
        ['Fairness', 'No per-tenant or per-caller quotas. A critical route still needs authorization and caller controls.'],
        ['Signals', 'Synchronous duration relative to a route budget, server failures and observed saturation.'],
        ['Resource and remote signals', 'No write-byte, memory, goroutine, hot-key or follower-lag regulator.'],
        ['Controller', 'Configured bounds and bucketed latency feedback; no PID or TCP-Vegas autotuner.'],
      ]} />
    </Section>
    <Section id="architecture" title="Proxy and middleware architecture">
      <h3>Standalone proxy</h3><p>The proxy matches the longest segment-prefix route, resolves a named service, checks its guard, and forwards admitted requests. Routes to one service share a guard and upstream connection pool. Different named services have independent budgets.</p>
      <Diagram name="proxy-architecture" caption="Each named service has its own admission guard and transport pool. Backends require no Gatewise integration." />
      <p>One service has one upstream URL; this layer does not perform load balancing. Prefix stripping, upstream timeouts and environment substitution are configured independently of admission.</p>
      <h3>Go middleware</h3><p>The application creates a <code>middleware.Service</code> and reuses it across handlers sharing capacity. Critical and background wrappers use the same counters. Separate Service instances have independent budgets, even if their names match.</p>
      <Diagram name="middleware-architecture" caption="The application router assigns priority. Multiple wrappers share one Service instance." />
      <p>The public library does not start a server, read dotenv files or modify global registries. The application owns authentication, timeouts and shutdown. Health and metrics routes should remain outside the guard so saturation does not block them.</p>
    </Section>
    <Section id="admission" title="Admission rules and critical reservation">
      <p>The limiter checks and updates its counters atomically under a short mutex. It never waits for a capacity slot; it is not a lock-free algorithm.</p>
      <Table headers={['Symbol', 'Meaning']} rows={[
        ['L', 'Current total concurrency limit.'], ['R', 'Capacity unavailable to background work.'], ['A', 'All admitted requests whose synchronous handlers have not returned.'], ['B', 'The background subset of A.'],
      ]} />
      <CodeBlock filename="Admission decision (pseudocode)" language="text" code={'if A >= L:\n    reject 503, reason = concurrency_limit\nelse if priority == background and B >= L - R:\n    reject 503, reason = critical_capacity_reserved\nelse:\n    A += 1\n    if priority == background: B += 1\n    execute handler'} />
      <Diagram name="admission-decision" caption="Total capacity is checked first; the background quota is checked only when total capacity remains." />
      <h3>Example: L = 20, R = 5</h3><Table headers={['State', 'Arrival', 'Decision']} rows={[
        ['A = 15, B = 15', 'Background', 'Reject: background quota is full.'], ['A = 15, B = 15', 'Critical', 'Admit: five total slots remain.'], ['A = 19, B = 10', 'Background', 'Admit: both checks pass.'], ['A = 20, B = 0', 'Either priority', 'Reject: total limit is full.'],
      ]} />
      <p>Critical work can use the entire limit. Reservation does not preempt running work or guarantee success. If only background traffic is present, reserved capacity remains unused. In fixed mode, R = L refuses all background requests.</p>
    </Section>
    <Section id="lifecycle" title="Request lifetime and failure handling">
      <ol className="technical-list"><li>The guard checks cancellation, attempts admission and records the decision.</li><li>A rejected request receives structured 503 JSON; the application handler is not called.</li><li>An admitted request retains its slot for the synchronous handler’s lifetime, including response-body delivery.</li><li>A deferred completion path releases capacity on return or panic. A panic still propagates to the application’s recovery boundary.</li></ol>
      <p>Cancellation does not forcibly terminate work. A handler that ignores its context retains the slot until it returns. Work in goroutines that outlive the handler is outside this budget and needs separate resource controls.</p>
      <p><code>X-Gatewise-Limit</code> and <code>X-Gatewise-Mode</code> are snapshots near admission, not reservation tokens or latency guarantees.</p>
    </Section>
    <Section id="adaptive" title="Adaptive concurrency controller">
      <p>Fixed mode retains its configured limit. Adaptive mode evaluates request completions and adjusts a local limit between explicit bounds. There is no controller goroutine; without relevant completions, no adjustment occurs.</p>
      <Diagram name="feedback-loop" caption="Completions release capacity and supply feedback. Limit changes affect future admissions." />
      <h3>Normalized latency samples</h3><pre className="formula">ratio = synchronous handler duration / route latency target</pre>
      <p>A 200ms handler with a 1s target contributes 0.2. The same duration against a 100ms target contributes 2.0. Route-specific targets allow handlers with different expected costs to share a guard.</p>
      <p>Ratios enter eight bounded histogram buckets: upper bounds 0.25, 0.5, 0.8, 1, 1.5, 2 and 4, plus overflow. The controller uses a bucket-based p90 estimate, not an exact percentile over retained samples.</p>
      <h3>Evaluation rules</h3><p>Both the feedback interval and minimum completed sample count must be reached. Let q be the estimated p90 ratio and f the failed-sample fraction.</p>
      <Table headers={['Condition', 'Next limit']} rows={[
        ['q > 1 or f ≥ 0.20', 'max(minimum, L − max(1, floor(L / 3)))'], ['q ≤ 0.8, zero failures, saturation observed', 'min(maximum, L + 1)'], ['Otherwise', 'L is unchanged.'],
      ]} />
      <p>Application 5xx responses and panics supply failure evidence. Canceled completions release capacity but do not contribute controller samples. Saturation includes reaching the total limit or background quota, or rejecting an arrival because admission capacity is unavailable.</p>
      <h3>Generation boundaries and draining</h3><p>Each admission records a controller generation. When the limit changes, older-generation completions still release capacity but are excluded from the new feedback window. This avoids attributing old-limit work to the new limit. The measurement window resets when a new admission is the only active request and the last feedback sample is older than twice the configured interval.</p>
      <p>The adaptive minimum must exceed the critical reservation. Existing requests are not interrupted when the limit decreases, so active work may temporarily exceed the new limit while they drain. Latency targets guide feedback; they are not enforced deadlines.</p>
    </Section>
    <Section id="operations" title="Deployment constraints">
      <Table headers={['Concern', 'Behavior or required integration']} rows={[
        ['Replicas', 'Limits are per process. Three replicas limited to 20 can collectively admit 60 requests. No shared quota exists.'], ['Priority assignment', 'Protect critical routes with application authorization or trusted edge routing. Gatewise does not authenticate callers.'], ['Timeouts and retries', 'Configure HTTP/upstream timeouts. Callers own bounded retries and backoff. Gatewise does not retry rejected work.'], ['Downstream resources', 'One handler may issue multiple database or fan-out operations. Those resources need separate controls.'], ['Monitoring', 'Scrape each replica; inspect admissions, rejection reasons, admitted latency and limit changes.'],
      ]} />
      <p>Start with a measured fixed limit and explicit priorities. Enable adaptation after selecting realistic latency targets and bounds. Available evidence is the <a href="#/benchmarks">local benchmark workload</a>; production adoption and multi-host capacity have not been established.</p>
    </Section>
    <Section id="references" title="References"><ul className="reference-list">{sources.map(s => <li key={s.url}><a href={s.url} target="_blank" rel="noreferrer">{s.title}</a> — {s.organization}, {s.year}.</li>)}</ul><p>QALM and Cinnamon provide background on criticality-aware shedding and adaptive capacity. Gatewise uses the rules above, not QALM’s queue or Cinnamon’s PID/TCP-Vegas controller and propagated cohorts.</p></Section>
  </div>;
}
