import { useState } from 'react';
import { middlewareExample, proxyExample } from '../data/content';
import { CodeBlock, Note, PageTitle, Section, Table, Tabs } from '../components/ui';

export default function Installation() {
  const [mode, setMode] = useState('middleware');
  return <div className="container">
    <PageTitle title="Installation and deployment">Integrate admission into a Go application or deploy Gatewise as a standalone HTTP proxy.</PageTitle>
    <p><strong>Requirements:</strong> Go 1.22 or newer. Install the Go module in your application, or build the standalone proxy from the tagged repository.</p>
    <Note label="Example configuration">The limits, service names, addresses and ports in this guide are examples. A limit of 20 means at most 20 requests running at the same time, not 20 requests per second. Reserving 5 slots for critical traffic limits background work to 15 concurrent requests. These values are configurable for each service in both middleware and proxy deployments; choose them through load testing against your latency targets and backend capacity. Limits apply per process.</Note>
    <Section id="source" title="1. Choose an integration"><p>Choose Go middleware to protect handlers inside your application. Choose the standalone proxy to protect an existing HTTP backend without changing its code.</p></Section>
    <Tabs label="Installation mode" value={mode} onChange={setMode} options={[{ value: 'middleware', label: 'Go middleware' }, { value: 'proxy', label: 'Standalone proxy' }]} />
    {mode === 'middleware' ? <>
      <Section id="configure" title="2. Create an application module"><p>In an existing Go module, run <code>go get github.com/HN026/gatewise@v0.1.0</code>. For a new application:</p><CodeBlock filename="Terminal" language="shell" code={'mkdir my-service\ncd my-service\ngo mod init example.com/my-service\ngo get github.com/HN026/gatewise@v0.1.0'} /></Section>
      <Section id="run" title="3. Add middleware and start the server"><p>Save this as <code>main.go</code>. In this example, both routes share a configurable limit of 20 concurrent requests; background requests can occupy at most 15 slots.</p><CodeBlock filename="main.go" code={middlewareExample} /><CodeBlock filename="Terminal" language="shell" code={'go mod tidy\ngo run .'} /></Section>
      <Section id="verify" title="4. Send a request"><CodeBlock filename="Another terminal" language="shell" code={'curl -i -X POST http://127.0.0.1:8080/checkout'} /><p>With this example configuration, expect <code>204 No Content</code>, <code>X-Gatewise-Mode: fixed</code> and <code>X-Gatewise-Limit: 20</code>. These minimal handlers return immediately. Replace their bodies with your application logic. The limit response header reflects the configured example ceiling.</p></Section>
    </> : <>
      <Section id="configure" title="2. Build and configure the proxy"><CodeBlock filename="Terminal" language="shell" code={'git clone --branch v0.1.0 https://github.com/HN026/gatewise.git\ncd gatewise\ngo build -o bin/gatewise ./cmd/gatewise'} /><p>Save this as <code>configs/orders.json</code>. Replace <code>upstream_url</code> with your backend and set <code>max_concurrent</code> and <code>reserved_critical</code> for its capacity. The more specific background prefix takes precedence.</p><CodeBlock filename="configs/orders.json" language="json" code={proxyExample} /></Section>
      <Section id="run" title="3. Start the proxy"><p>Keep your existing backend running at the configured <code>upstream_url</code>. Start Gatewise from the repository directory. An empty dotenv path uses the JSON configuration and process environment without loading a local dotenv file.</p><CodeBlock filename="Terminal" language="shell" code={'./bin/gatewise -config configs/orders.json -env-file ""'} /></Section>
      <Section id="verify" title="4. Verify health and forwarding"><CodeBlock filename="Another terminal" language="shell" code={'curl -i http://127.0.0.1:8080/healthz\ncurl -i http://127.0.0.1:8080/orders/hello'} /><p>Health returns 200 independently of backend availability. The forwarding example strips <code>/orders</code> and sends <code>/hello</code> to your backend; replace it with an endpoint your service supports. The backend status and body pass through. An unreachable upstream returns 502.</p></Section>
    </>}
    <p>Choose an available listen address for your application or proxy. The proxy listener must differ from its upstream address. Update the configuration and verification URLs together.</p>

    <Section id="deployment" title="Deployment">
      <p>Choose the integration that matches the service boundary. Both enforce limits per process; replica count changes the aggregate capacity. Deploy Gatewise behind the same authentication, TLS and network controls as the service it protects.</p>
      <Table headers={['Integration', 'Deployment unit', 'Configuration owner']} rows={[
        ['Go middleware', 'Your application binary; no separate Gatewise process.', 'The application constructs guards, assigns route priority, exposes metrics and owns server lifecycle.'],
        ['Standalone proxy', 'The Gatewise binary in front of one or more HTTP backends.', 'JSON routes and service budgets, with process environment or dotenv overrides.'],
      ]} />
    </Section>
    <Section id="library-deployment" title="Deploying a Go application">
      <p>Build and deploy the application using its existing release process. Reuse one guard for handlers sharing a resource budget; create independent guards only for independent budgets. Expose metrics outside guarded routes. Keep HTTP and dependency timeouts explicit, and call the application server’s Shutdown method on termination.</p>
      <p>For an existing service, create the guard once during startup, wrap the selected handlers and keep the application’s current server and shutdown lifecycle. Routes that share a capacity budget should reuse that guard.</p>
      <CodeBlock filename="Application build" language="shell" code={'# From your application module:\nGOOS=linux GOARCH=amd64 go build -trimpath -o dist/my-service .'} />
      <p>Use <code>GOARCH=arm64</code> for an ARM Linux target. Pin the Gatewise module version in your application’s go.mod and commit go.sum.</p>
    </Section>
    <Section id="proxy-deployment" title="Deploying the proxy as a service">
      <p>Build the proxy for the target OS and architecture. The following paths are examples for a Linux host managed by systemd.</p>
      <CodeBlock filename="Proxy build" language="shell" code={'# From the gatewise module:\nGOOS=linux GOARCH=amd64 go build -trimpath -o dist/gatewise ./cmd/gatewise'} />
      <ol className="technical-list"><li>Install the binary at <code>/opt/gatewise/bin/gatewise</code>.</li><li>Put the route configuration at <code>/etc/gatewise/config.json</code> and environment overrides at <code>/etc/gatewise/gatewise.env</code>.</li><li>Create a dedicated <code>gatewise</code> service account with read access to those configuration files.</li><li>Install and start a service unit through your deployment tooling.</li></ol>
      <CodeBlock filename="/etc/systemd/system/gatewise.service" language="ini" code={'[Unit]\nDescription=Gatewise HTTP admission proxy\nAfter=network.target\n\n[Service]\nUser=gatewise\nEnvironmentFile=/etc/gatewise/gatewise.env\nExecStart=/opt/gatewise/bin/gatewise -config /etc/gatewise/config.json -env-file ""\nRestart=on-failure\nTimeoutStopSec=10\nNoNewPrivileges=true\n\n[Install]\nWantedBy=multi-user.target'} />
      <p>The environment values below are examples. Set the concurrency ceiling and critical reservation for this service based on load testing.</p>
      <CodeBlock filename="/etc/gatewise/gatewise.env" language="shell" code={'GATEWISE_LISTEN_ADDR=127.0.0.1:8080\nGATEWISE_SERVICE_ORDERS_URL=http://127.0.0.1:9090\nGATEWISE_SERVICE_ORDERS_MAX_CONCURRENT=20\nGATEWISE_SERVICE_ORDERS_RESERVED_CRITICAL=5'} />
      <p>Binding to loopback suits an ingress on the same host. For a separate ingress, choose the appropriate private listen address and network policy. Forward traffic to this proxy address, check <code>GET /healthz</code> for readiness, and scrape <code>GET /metrics</code>. SIGTERM stops new connections and drains existing work within <code>shutdown_timeout</code>; set the supervisor stop timeout above that value.</p>
      <p>Example adaptive service settings, replica budget implications and route priority controls are covered in <a href="#/problem/operations">deployment constraints</a> and the <a href="#/reference">configuration reference</a>.</p>
    </Section>
    <Note label="Integration">Applications and deployment infrastructure own authentication, TLS, request timeouts and shutdown. See <a href="#/reference">configuration and API reference</a> for adaptive settings and environment overrides.</Note>
  </div>;
}
