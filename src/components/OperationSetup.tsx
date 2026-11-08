import { CodeBlock, Diagram, Note, Section, Table } from './ui';
export function OperationSetup(){return <Section id="operation-setup" title="Protect jobs and database operations">
<p>HTTP admission covers work until a handler returns. A worker job may continue without an HTTP request, and several handlers can share one database pool. Give those operations their own budget. The HTTP middleware, proxy and operation API now use the same admission engine.</p>
<Diagram name="operation-lifetimes" caption="Each resource has an independent budget. Transaction and stream permits outlive individual SQL calls."/><Table headers={['Layer','Example budget','Lifetime']} rows={[
 ['HTTP service','12 concurrent requests','Until the handler returns.'],['Worker executor','6 running jobs; fixture queue holds 16','Until the job callback finishes, including cleanup.'],['Primary database','4 operations, with 1 slot reserved for critical work','Until a statement finishes, rows close or a transaction finishes.'],['Follower reads','2 concurrent queries','Until rows close; reject new reads when the named standby is behind or unavailable.']
]}/>
<p>These numbers describe the example, not recommended production defaults. Use separate engines for independent resources. Reusing one engine inside its own active context returns <code>operation.ErrNested</code>; it would count the same budget twice and could reject legitimate nested work. Always pass the context supplied to the callback or permit.</p>
<CodeBlock filename="Worker integration · inside your application startup" code={`// Imports: context, github.com/HN026/gatewise/operation.
// ctx is your application lifecycle context; processJob is your job function.
workers, err := operation.New(operation.Config{
    Name: "workers", MaxConcurrent: 6,
})
if err != nil { return err }
err = workers.Do(ctx, operation.Request{
    Priority: operation.Background, Resource: "reindex",
}, func(work context.Context) error {
    return processJob(work)
})
// Handle *operation.Rejection separately from job failures.
// Do does not enqueue or retry rejected work.`}/>
<p><code>Do</code> releases once after callback completion or panic unwinding. For an asynchronous lifetime, call <code>Begin</code>, carry <code>permit.Context()</code>, and call <code>Finish(operation.Outcome{'{Err: err}'})</code> only after all owned work ends. Cancellation requests cleanup; it does not free running capacity. With adaptive mode configured, Request.LatencyTarget is a feedback budget, not an execution timeout; use a context deadline for execution.</p>
<p>For rejected jobs, inspect the typed error before deciding whether your application should drop, reschedule or report it. Gatewise does not make that delivery decision.</p><CodeBlock filename="Handle an operation result · import errors" code={`var rejection *operation.Rejection
if errors.As(err, &rejection) {
    // rejection.Reason identifies the failed rule.
    // Your scheduler decides whether/when to retry, with a bounded policy.
    return err
}
if err != nil { return err } // the admitted callback failed
`}/>
<h3>Wrap an existing SQL pool</h3>
<CodeBlock filename="SQL integration · startup and transaction fragments" code={`// Imports: context, database/sql, operation and sqlguard.
// raw is an existing *sql.DB with a driver and lifecycle owned by your app.
raw.SetMaxOpenConns(4) // example: align the budget with measured pool capacity
storage, err := operation.New(operation.Config{
    Name: "storage", MaxConcurrent: 4, ReservedCritical: 1,
})
if err != nil { return err }
guarded, err := sqlguard.New(raw, storage, func(ctx context.Context) operation.Request {
    // Replace with metadata from your authenticated, validated operation plan.
    return operation.Request{Priority: operation.Critical, Resource: "checkout"}
})
if err != nil { return err }
err = guarded.InTx(ctx, nil, func(tx *sqlguard.Tx) error {
    _, err := tx.ExecContext(tx.Context(),
        "UPDATE inventory SET stock = stock - $1 WHERE id = $2", quantity, productID)
    return err
})`}/>
<p>Call transaction methods inside <code>InTx</code>; they share its permit. Do not call the outer guarded DB from the transaction using the original context. Pass the transaction context through nested functions. <code>ExecContext</code> holds a permit through driver completion. <code>QueryContext</code> holds one through explicit Close or terminal iteration; always defer <code>rows.Close()</code>. A QueryRow permit is released by Scan, so do not abandon a Row without calling Scan. Close streaming rows before Commit/Rollback, particularly when using <code>sql.RawBytes</code>.</p>
<Note label="Transaction cancellation">The wrapper acquires a connection with your context, then owns transaction cleanup. Begin/Commit/Rollback use a noncanceling control context so an automatic database/sql rollback cannot release admission before the driver finishes. Query execution remains cancelable. Configure driver/network timeouts: transaction control can wait for them while retaining capacity. An ambiguous network failure does not prove that the database server stopped executing.</Note>
<h3 id="pool-contention">Choose occupancy or contention</h3><p>A pool can be busy without callers waiting. The original SQLPool provider measures occupancy; it does not infer contention. v0.8.1 adds an opt-in SQLPoolContention provider that watches new WaitCount increments. It emits 1 after new waits, and 0 after the configured quiet interval with a nonfull pool. Use threshold 1 in its decision rule.</p>
<CodeBlock filename="Optional contention provider · same background collector lifecycle" code={`source, err := regulator.NewSQLPoolContention(regulator.SQLPoolContentionConfig{
    DB: raw, RecoveryQuiet: 200 * time.Millisecond, // example
})
if err != nil { return err }
// Add source to decision.Collector; use Threshold: 1.
// A background-resource rule can retain pressure cap 1 / unknown cap 1.
// With a critical reservation of 1, that cap blocks background work.
// Collect in the background; this provider makes no SQL calls.`}/>
<p>WaitCount is cumulative. The provider records a baseline at construction and detects subsequent increments; it cannot infer the number or age of already-waiting callers from historical totals. A full pool after an observed wait remains pressured until it becomes nonfull and quiet. Snapshot reports the last sample, utilization, WaitCount delta, detection time and bounded reason. LastWaitAt is the sample’s detection time. It is not a database health check or queue-depth gauge.</p>
<p>The balanced demo also reuses existing sharing with server-owned interactive/batch classes, shares 3:1 and borrowing ceiling 4. Borrowed jobs drain when interactive demand returns; nothing preempts a running transaction. These classes occupy the example’s tenant-policy dimension and do not add a second independent customer-isolation policy. Values are examples, not new library defaults. Applications needing both dimensions should choose separate resource budgets and authenticated identity policies.</p>
<p>Occupancy-only and fixed policies remain available. The balanced policy still sheds background work during actual pool contention, and cannot guarantee progress when outside consumers or critical work exhaust physical capacity. See the <a href="#/benchmarks/pool-balance">v0.8.1 comparison</a> for background completions alongside critical latency, rejection and recovery.</p>
<h3>Sample the pool and a PostgreSQL standby</h3>
<p><code>regulator.SQLPool{'{DB: raw}'}</code> is a decision provider measuring InUse / MaxOpenConnections. A finite pool limit is required. It does not measure queue length, free server capacity or database availability. Collect outside the request path with <code>decision.Engine.Run</code>. In the demo, a 75% occupied pool reduces admission only for the server-owned background resource; interactive work retains its reservation.</p>
<CodeBlock filename="Physical replication provider · collector fragment" code={`// observer is a separate primary-side *sql.DB. The app owns and closes it.
// Import github.com/HN026/gatewise/postgres.
source, err := postgres.NewReplication(postgres.Config{
    DB: observer, ApplicationName: "orders_standby",
    StaleAfter: 3 * time.Second, // example: standby heartbeat configured at 1s
})
if err != nil { return err }
// Add source to decision.Collector, with a configured "replica_read" rule.
// Example: threshold 65536 WAL bytes; pressure and unknown both reject.
// Run collection in the background; cancel and wait before closing observer.`}/>
<p>The provider queries the primary’s pg_stat_replication for exactly one named physical standby. WAL replay byte distance distinguishes idle catch-up from an actual backlog. Fresh reply timestamps and valid positions are required. Missing, duplicate, stale, NULL or failed observations become unknown, subject to the rule’s fallback. Give the observer a monitoring role with access to the required statistics. Choose heartbeat age and sampling intervals for your network, and keep clocks synchronized when interpreting reply age.</p>
<p>A standalone proxy can consume a backend’s private observation endpoint using the existing signal-source contract. It cannot observe worker or SQL lifetimes inside another process: Go applications add these adapters in the backend. Database adapters do not install PostgreSQL-engine enforcement, preempt admitted queries, or coordinate limits across replicas.</p>
<p>See <a href="#/examples/operation-example">the runnable operation example</a>, <a href="#/benchmarks/operation-benchmarks">measured results</a> and the primary references for <a href="https://pkg.go.dev/database/sql#Tx">database/sql transactions</a> and <a href="https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-REPLICATION-VIEW">PostgreSQL replication statistics</a>.</p>
</Section>}
