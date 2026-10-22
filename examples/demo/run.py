#!/usr/bin/env python3
"""Build and run the self-contained Gatewise demo. Ctrl+C stops only its children."""
import argparse
import json
import os
import pathlib
import shutil
import signal
import socket
import subprocess
import sys
import time
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parent


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base-port', type=int, default=8080, help='proxy port; other demo ports are offsets from this')
    parser.add_argument('--skip-build', action='store_true')
    args = parser.parse_args()
    base = args.base_port
    if not 1024 <= base <= 64000:
        parser.error('base port must be between 1024 and 64000')
    ports = {'proxy': base, 'adaptive_proxy': base+8, 'users': base+1010, 'catalog': base+1011, 'middleware': base+1012, 'adaptive_middleware': base+1013, 'benchmark': base+1110}
    held = []
    try:
        for port in ports.values():
            s = socket.socket()
            s.bind(('127.0.0.1', port))
            held.append(s)
    except OSError as e:
        raise RuntimeError('A demo port is occupied. Stop the conflicting service or choose --base-port 18080.') from e
    finally:
        for s in held:
            s.close()
    go = os.environ.get('GO_BIN', 'go')
    if not args.skip_build:
        if not shutil.which(go):
            raise RuntimeError('Go is required. Install Go, put it on PATH, or set GO_BIN.')
        for module in ['gatewise', 'demo-backend', 'catalog-backend', 'middleware-backend', 'benchmark-backend']:
            print('Building', module, flush=True)
            subprocess.run([go, 'build', '-o', f'bin/{module}', f'./cmd/{module}'], cwd=ROOT/module, env={**os.environ, 'GOWORK': 'off'}, check=True)
    env = {k: v for k, v in os.environ.items() if not k.startswith('GATEWISE_')}
    env.update(GATEWISE_SERVICE_USERS_URL=f'http://127.0.0.1:{ports["users"]}', GATEWISE_SERVICE_CATALOG_URL=f'http://127.0.0.1:{ports["catalog"]}')
    children, logs = [], []
    log_dir = ROOT/'logs'
    log_dir.mkdir(exist_ok=True)
    interrupted = False

    def stop(_sig, _frame):
        nonlocal interrupted
        interrupted = True

    signal.signal(signal.SIGINT, stop)
    signal.signal(signal.SIGTERM, stop)
    def start(name, module, extra):
        log = (log_dir/f'{name}.log').open('w')
        logs.append(log)
        child_env = env.copy()
        if module == 'gatewise':
            child_env['GATEWISE_LISTEN_ADDR'] = f'127.0.0.1:{ports[name]}'
        p = subprocess.Popen([str(ROOT/module/f'bin/{module}'), *extra], cwd=ROOT/module, env=child_env, stdout=log, stderr=log)
        children.append(p)
        url = f'http://127.0.0.1:{ports[name]}'
        ready_path = '/hello' if name == 'users' else '/products' if name == 'catalog' else '/healthz'
        deadline = time.monotonic()+5
        while time.monotonic() < deadline:
            if interrupted or p.poll() is not None:
                raise RuntimeError(f'{name} stopped; inspect {log_dir/name}.log')
            try:
                with urllib.request.urlopen(url+ready_path, timeout=.3) as response:
                    if response.status == 200:
                        return
            except Exception:
                time.sleep(.05)
        raise RuntimeError(f'{name} did not become ready; inspect {log_dir/name}.log')

    try:
        start('users', 'demo-backend', ['-listen', f'127.0.0.1:{ports["users"]}'])
        start('catalog', 'catalog-backend', ['-listen', f'127.0.0.1:{ports["catalog"]}'])
        start('proxy', 'gatewise', ['-config', 'configs/local.json', '-env-file', ''])
        start('adaptive_proxy', 'gatewise', ['-config', 'configs/adaptive.json', '-env-file', ''])
        start('middleware', 'middleware-backend', ['-listen', f'127.0.0.1:{ports["middleware"]}'])
        start('adaptive_middleware', 'middleware-backend', ['-listen', f'127.0.0.1:{ports["adaptive_middleware"]}', '-adaptive', '-max-concurrent', '6', '-reserved-critical', '1', '-min-concurrent', '2', '-initial-concurrent', '4', '-adaptive-interval', '500ms', '-min-samples', '4', '-target-latency', '100ms'])
        start('benchmark', 'benchmark-backend', ['-listen', f'127.0.0.1:{ports["benchmark"]}', '-mode', 'fixed'])
        postman = json.loads((ROOT/'postman/Gatewise-Local.postman_environment.json').read_text())
        overrides = {'gatewise_url': ports['proxy'], 'backend_url': ports['users'], 'catalog_url': ports['catalog'], 'middleware_url': ports['middleware'], 'adaptive_proxy_url': ports['adaptive_proxy'], 'adaptive_middleware_url': ports['adaptive_middleware'], 'benchmarkBaseUrl': ports['benchmark']}
        for v in postman['values']:
            if v['key'] in overrides:
                v['value'] = f'http://127.0.0.1:{overrides[v["key"]]}'
        path = ROOT/'postman/Gatewise-Demo.postman_environment.json'
        path.write_text(json.dumps(postman, indent=2)+'\n')
        print('Demo ready. Import postman/Gatewise.postman_collection.json and', path.name, flush=True)
        for name, port in ports.items():
            print(f'  {name:22} http://127.0.0.1:{port}', flush=True)
        print('Run collection folders individually. Manual stopped-backend stages require stopping a selected child separately. Ctrl+C stops this demo.', flush=True)
        while not interrupted:
            for p in children:
                if p.poll() is not None:
                    raise RuntimeError('A demo process exited; inspect logs/')
            time.sleep(.2)
    finally:
        for p in reversed(children):
            if p.poll() is None:
                p.terminate()
        for p in children:
            try:
                p.wait(timeout=5)
            except subprocess.TimeoutExpired:
                p.kill()
                p.wait()
        for log in logs:
            log.close()


if __name__ == '__main__':
    try:
        main()
    except Exception as e:
        print(e, file=sys.stderr)
        sys.exit(1)
