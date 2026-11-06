"""Capture actual Grafana PNGs from an isolated local Gatewise workload.

Requires built sibling Go binaries, running Podman and Grafana's official image
renderer image. This workload is for monitoring screenshots, not M6 benchmarking.
"""
import argparse
import base64
import concurrent.futures
import datetime
import http.client
import json
import os
import pathlib
import shutil
import signal
import secrets
import socket
import subprocess
import tempfile
import threading
import time
import urllib.parse
import urllib.request

SITE = pathlib.Path(__file__).resolve().parents[1]


def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def api(base, path, payload=None, raw=False, timeout=8):
    headers = {}
    if payload is not None:
        payload = json.dumps(payload).encode()
        headers['Content-Type'] = 'application/json'
    request = urllib.request.Request(base+path, data=payload, headers=headers)
    with urllib.request.urlopen(request, timeout=timeout) as response:
        data = response.read()
        return data if raw else json.loads(data) if data else None


def ready(base, path='/healthz'):
    deadline = time.monotonic()+90
    while time.monotonic() < deadline:
        try:
            api(base, path, raw=True)
            return
        except Exception:
            time.sleep(.3)
    raise RuntimeError('Failed to start '+base+path)


def traffic(base, rps, seconds):
    # Scheduled arrivals with a bounded thread pool and reused connections.
    local = threading.local()
    lock = threading.Lock()
    counts = {'200': 0, '503': 0, 'errors': 0}
    parsed = urllib.parse.urlsplit(base)
    def work(index):
        if not hasattr(local, 'connection'):
            local.connection = http.client.HTTPConnection(parsed.hostname, parsed.port, timeout=2)
        priority = 'critical' if index % 8 == 0 else 'background'
        try:
            local.connection.request('GET', '/'+priority+'/work')
            response = local.connection.getresponse()
            response.read()
            key = str(response.status) if response.status in (200, 503) else 'errors'
        except Exception:
            local.connection.close()
            key = 'errors'
        with lock:
            counts[key] += 1
    with concurrent.futures.ThreadPoolExecutor(max_workers=96) as pool:
        start = time.monotonic()
        for i in range(int(rps*seconds)):
            delay = start+i/rps-time.monotonic()
            if delay > 0:
                time.sleep(delay)
            pool.submit(work, i)
    return counts


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--projects', type=pathlib.Path, default=SITE.parent)
    parser.add_argument('--renderer-image', default='docker.io/grafana/grafana-image-renderer:latest')
    args = parser.parse_args()
    root, library = args.projects, args.projects/'gatewise'
    output = SITE/'public/images'
    output.mkdir(parents=True, exist_ok=True)
    stack = f'gatewise-site-capture-{os.getpid()}'
    renderer_token = secrets.token_hex(24)
    children, log_files = [], []
    env = {k: v for k, v in os.environ.items() if not k.startswith('GATEWISE_')}
    with tempfile.TemporaryDirectory(prefix='gatewise-metrics-capture-') as temp:
        temp = pathlib.Path(temp)
        stack_dir = temp/'observability'
        shutil.copytree(library/'deploy/observability', stack_dir)
        stack_env = {**env, 'STACK_NAME': stack, 'PROMETHEUS_PORT': str(port()), 'GRAFANA_PORT': str(port())}
        pathlib.Path('/private/tmp/gatewise-metrics-live.json').write_text(json.dumps({'stack':stack,'temp':str(temp),'prometheus_port':stack_env['PROMETHEUS_PORT'],'grafana_port':stack_env['GRAFANA_PORT']}))
        prom = 'http://127.0.0.1:'+stack_env['PROMETHEUS_PORT']
        grafana = 'http://127.0.0.1:'+stack_env['GRAFANA_PORT']
        targets, backends = {}, {}
        metadata = {'source': 'Fresh local screenshot workload; separate from the original M6 experiment', 'phases': [], 'screenshots': []}
        def spawn(name, binary, argv):
            log = (temp/(name+'.log')).open('w')
            log_files.append(log)
            child = subprocess.Popen([str(binary), *argv], stdout=log, stderr=log, env=env)
            children.append(child)
        try:
            for deployment in ['proxy', 'middleware']:
                for mode in ['fixed', 'adaptive']:
                    name = deployment+'-'+mode
                    backend_port = port()
                    backend = f'http://127.0.0.1:{backend_port}'
                    spawn(name+'-backend', root/'benchmark-backend/bin/benchmark-backend', ['-listen',f'127.0.0.1:{backend_port}','-mode',mode if deployment == 'middleware' else 'direct'])
                    ready(backend)
                    backends[name] = backend
                    targets[name] = backend
                    if deployment == 'proxy':
                        proxy_port = port()
                        cfg = {'listen_addr':f'127.0.0.1:{proxy_port}', 'services':{'benchmark':{'upstream_url':backend,'max_concurrent':6,'reserved_critical':1}},'routes':[{'path_prefix':'/critical','service':'benchmark','priority':'critical'},{'path_prefix':'/background','service':'benchmark','priority':'background'}]}
                        if mode == 'adaptive':
                            cfg['services']['benchmark'].update(mode='adaptive',adaptive={'min_concurrent':2,'initial_concurrent':6,'interval':'500ms','min_samples':10,'target_latency':'100ms'})
                        path = temp/(name+'.json')
                        path.write_text(json.dumps(cfg))
                        spawn(name, library/'bin/gatewise', ['-config',str(path),'-env-file',''])
                        targets[name] = f'http://127.0.0.1:{proxy_port}'
                        ready(targets[name])
            scrape = '\n'.join(f'      - targets: [host.docker.internal:{urllib.parse.urlsplit(url).port}]\n        labels: {{deployment: {name.split("-")[0]}, profile: {name.split("-")[1]}}}' for name,url in targets.items())
            (stack_dir/'prometheus.yml').write_text('global:\n  scrape_interval: 1s\n  evaluation_interval: 1s\nscrape_configs:\n  - job_name: gatewise\n    static_configs:\n'+scrape+'\n')
            script = (stack_dir/'run.sh').read_text().replace('-e GF_ANALYTICS_REPORTING_ENABLED=false', f'-e GF_RENDERING_SERVER_URL=http://renderer:8081/render -e GF_RENDERING_CALLBACK_URL=http://{stack}-grafana:3000/ -e GF_RENDERING_RENDERER_TOKEN={renderer_token} -e GF_ANALYTICS_REPORTING_ENABLED=false')
            (stack_dir/'run.sh').write_text(script)
            subprocess.run(['podman','network','create',stack],check=True,capture_output=True)
            subprocess.run(['podman','run','-d','--name',stack+'-renderer','--network',stack,'--network-alias','renderer','--memory','2g','--cpus','2','-e','AUTH_TOKEN='+renderer_token,args.renderer_image],check=True,capture_output=True)
            subprocess.run([str(stack_dir/'run.sh'),'start'],env=stack_env,check=True,capture_output=True)
            ready(prom,'/api/v1/status/buildinfo')
            ready(grafana,'/api/health')
            time.sleep(4)
            active = api(prom,'/api/v1/targets')['data']['activeTargets']
            assert len(active)==4 and all(t['health']=='up' for t in active), active
            print('PASS: all four real deployment targets are being scraped',flush=True)
            start_ms = int(time.time()*1000)
            for phase, capacity, rate, seconds in [('baseline',12,100,10),('pressure',2,300,20),('recovery',12,300,20)]:
                for backend in backends.values():
                    api(backend,'/control',{'capacity':capacity})
                with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
                    futures = {name:pool.submit(traffic,url,rate,seconds) for name,url in targets.items()}
                    counts = {name:f.result() for name,f in futures.items()}
                metadata['phases'].append({'name':phase,'capacity':capacity,'scheduled_rps_per_target':rate,'seconds':seconds,'outcomes':counts})
                assert all(c['errors']==0 for c in counts.values()), counts
                print(phase,counts,flush=True)
            time.sleep(3)
            end_ms = int(time.time()*1000)
            for query in ['gatewise_concurrency_limit','gatewise_limit_adjustments_total','gatewise_requests_rejected_total','gatewise_request_duration_seconds_count']:
                response=api(prom,'/api/v1/query_range?'+urllib.parse.urlencode({'query':query,'start':start_ms/1000,'end':end_ms/1000,'step':1}))
                assert response['status']=='success' and response['data']['result'],query
                metadata[query]=response['data']['result']
            for name, panel_id, filename in [('proxy-adaptive',None,'grafana-proxy-adaptive.png'),('middleware-adaptive',None,'grafana-middleware-adaptive.png'),('proxy-adaptive',2,'grafana-admission-limit.png'),('proxy-adaptive',5,'grafana-rejections.png')]:
                instance = 'host.docker.internal:'+str(urllib.parse.urlsplit(targets[name]).port)
                params = {'from':start_ms,'to':end_ms,'width':1600,'height':2400,'theme':'dark','tz':'UTC','var-instance':instance,'var-service':'benchmark','kiosk':'tv'}
                if panel_id is not None:
                    params.update(panelId=panel_id,width=1200,height=430)
                path=('/render/d-solo/' if panel_id is not None else '/render/d/')+'gatewise-overview/gatewise-overview?'+urllib.parse.urlencode(params)
                request=urllib.request.Request(grafana+path,headers={'Authorization':'Basic '+base64.b64encode(b'admin:gatewise-local').decode()})
                with urllib.request.urlopen(request,timeout=120) as response:
                    image=response.read()
                assert image.startswith(b'\x89PNG\r\n\x1a\n'),image[:200]
                (output/filename).write_bytes(image)
                metadata['screenshots'].append({'file':filename,'deployment':name,'from_ms':start_ms,'to_ms':end_ms,'renderer':'Grafana native render endpoint','bytes':len(image)})
                print('CAPTURED',filename,len(image),'bytes',flush=True)
            metadata['captured_at_utc']=datetime.datetime.now(datetime.timezone.utc).isoformat()
            metadata['renderer_image']=subprocess.check_output(['podman','image','inspect',args.renderer_image,'--format','{{index .RepoDigests 0}}'],text=True).strip()
            (SITE/'public/data/metrics-capture.json').write_text(json.dumps(metadata,indent=2)+'\n')
            print('PASS: actual Grafana PNGs and source time series saved',flush=True)
        except BaseException:
            for suffix in ['grafana','prometheus','renderer']:
                logs=subprocess.run(['podman','logs','--tail','80',stack+'-'+suffix],capture_output=True,text=True,timeout=15)
                pathlib.Path('/private/tmp/gatewise-capture-'+suffix+'.log').write_text(logs.stdout+logs.stderr)
                print(suffix, logs.stdout[-3000:]+logs.stderr[-3000:], flush=True)
            raise
        finally:
            for child in reversed(children):
                if child.poll() is None:
                    child.send_signal(signal.SIGTERM)
            for child in children:
                try:child.wait(timeout=5)
                except subprocess.TimeoutExpired:child.kill();child.wait()
            for log in log_files:log.close()
            subprocess.run([str(stack_dir/'run.sh'),'stop'],env=stack_env,capture_output=True,timeout=45)
            subprocess.run(['podman','rm','-f',stack+'-renderer'],capture_output=True,timeout=15)
            subprocess.run(['podman','network','rm',stack],capture_output=True,timeout=15)
            for suffix in ['prometheus-data','grafana-data']:
                subprocess.run(['podman','volume','rm',stack+'-'+suffix],capture_output=True,timeout=15)


if __name__ == '__main__':
    main()
