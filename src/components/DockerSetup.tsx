import image from '../data/container-image.json';
import { CodeBlock, Note, Section, Table } from './ui';
export function DockerSetup(){return <Section id="docker" title="Run the proxy with Docker">
<p>The Dockerfile builds the standalone HTTP proxy. Middleware users continue to include Gatewise in their own Go application image. The runtime image contains the binary, CA certificates and an example configuration; it runs as UID/GID 65532 and contains no shell or Go toolchain.</p>
<h3>Build from the release source</h3>
<CodeBlock filename="From the Gatewise repository" language="shell" code={'git clone --branch v1.0.0 https://github.com/HN026/gatewise.git\ncd gatewise\ndocker build --build-arg VERSION=v1.0.0 -t gatewise:v1.0.0 .'} />
<p>The build uses the official Go image in a separate stage. The final image starts from scratch. The .dockerignore allows only build inputs; local environment files, Git history, demo data and binaries are excluded.</p>
<h3>Connect an existing backend</h3>
<CodeBlock filename="Example: an orders API on host port 9090" language="shell" code={'docker run --rm --name gatewise \\\n  --read-only --cap-drop=ALL --security-opt=no-new-privileges \\\n  --add-host=host.docker.internal:host-gateway \\\n  -p 127.0.0.1:8080:8080 \\\n  -e GATEWISE_SERVICE_ORDERS_URL=http://host.docker.internal:9090 \\\n  gatewise:v1.0.0'} />
<p>The backend URL is required at runtime. This is an example address; use a backend reachable from the container. On a shared Docker network, use the backend’s service name, such as http://orders:9090. On Linux, a host backend bound only to 127.0.0.1 is not reachable through the bridge gateway. The proxy listens on 0.0.0.0 inside the container; the published example port is restricted to host loopback.</p>
<p>The supplied configuration routes /orders to that backend, with /orders/reports classified as background. It keeps the path prefix. The example ceiling is 20 with 5 slots reserved for critical requests; choose values for your service. Containers have their own loopback interface, so an upstream URL using 127.0.0.1 points back into the proxy container.</p>
<p>With Podman, replace docker with podman, use host.containers.internal for a host backend, and omit the Docker-specific --add-host option. Mounted configuration must be readable by container UID 65532.</p>
<h3>Mount your own configuration</h3>
<CodeBlock filename="Validate first, then run" language="shell" code={'docker run --rm \\\n  -v "$PWD/configs/production.json:/etc/gatewise/config.json:ro" \\\n  gatewise:v1.0.0 -config /etc/gatewise/config.json -check-config\n\ndocker run --rm --name gatewise \\\n  --read-only --cap-drop=ALL --security-opt=no-new-privileges \\\n  -p 127.0.0.1:8080:8080 \\\n  -v "$PWD/configs/production.json:/etc/gatewise/config.json:ro" \\\n  gatewise:v1.0.0'} />
<p>Create configs/production.json with your services and routes, or copy configs/docker.json and replace its upstream placeholder with your backend URL. Bind 0.0.0.0:8080. Pass any referenced environment variables with -e or --env-file to Docker. The image disables automatic application .env loading; it does not bake credentials into the image. The validation command checks syntax and policy without opening a listener or contacting upstreams.</p>
<h3>Health, metrics and shutdown</h3>
<CodeBlock filename="From the host" language="shell" code={'curl http://127.0.0.1:8080/healthz\ncurl http://127.0.0.1:8080/metrics\ndocker stop --time 10 gatewise'} />
<p>Health reports proxy liveness, not backend health. SIGTERM starts graceful HTTP shutdown; give Docker more time than shutdown_timeout before it sends SIGKILL. Logs go to standard output. Runtime configuration is read at startup; restart the container to apply changes.</p>
<h3>Published image</h3>
<CodeBlock filename="Versioned image" language="shell" code={'docker pull ghcr.io/hn026/gatewise:v1.0.0'} />
<p>Anonymous manifest access and image pull were verified. For an immutable deployment, pin <code>ghcr.io/hn026/gatewise@{image.digest}</code>. <a href="data/container-image.json">Registry verification record →</a></p>
<Table headers={['Build','Architecture']} rows={[
['Local docker build','The build host’s Linux architecture; native ARM64 validated with Podman.'],
['Published v1.0.0 image','linux/amd64 and linux/arm64; Docker selects the native architecture. Both images pass native runtime and overload/shutdown checks before the version tag is assembled.']
]}/>
<p>The release index includes an SBOM and BuildKit provenance for each architecture. The workflow scans each runtime image for HIGH/CRITICAL vulnerabilities before publishing the version tag. These are time-stamped checks, not a guarantee that future advisories will never affect the image. Build inputs use a pinned Go builder digest, and the OCI revision label records the source commit. Inspect or download the release evidence before deployment.</p><Note label="What the image bounds">Containerizing Gatewise does not change its admission lifetime. A proxy bounds active proxy requests; canceled backend work may still be cleaning up. Protect handler or SQL lifetimes inside the service when those are the resources that must be bounded.</Note>
<p>The release container was exercised against the three PostgreSQL-backed demo services with read-only root filesystem, dropped capabilities, metrics, configuration validation and graceful shutdown. See the benchmark report for the exact image and checks.</p>
</Section>}
