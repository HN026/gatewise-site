#!/usr/bin/env python3
"""Native container smoke, overload and in-flight graceful-shutdown checks."""
import argparse,concurrent.futures,http.server,json,pathlib,socket,subprocess,tempfile,threading,time,urllib.request,urllib.error,uuid
p=argparse.ArgumentParser(description=__doc__);p.add_argument('--image',required=True);p.add_argument('--engine',default='docker');p.add_argument('--output',default='container-check.json');a=p.parse_args()
def command(*args):return subprocess.check_output([a.engine,*args],text=True).strip()
def call(base,path):
 try:
  with urllib.request.urlopen(base+path,timeout=5) as r:return r.status,r.read()
 except urllib.error.HTTPError as e:return e.code,e.read()
active=0;peak=0;lock=threading.Lock()
class Backend(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_GET(self):
  global active,peak
  with lock:active+=1;peak=max(active,peak)
  try:
   if self.path.startswith('/slow'):time.sleep(.4)
   self.send_response(200);self.end_headers();self.wfile.write(b'backend-ok')
  finally:
   with lock:active-=1
backend=http.server.ThreadingHTTPServer(('0.0.0.0',0),Backend);threading.Thread(target=backend.serve_forever,daemon=True).start()
with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
name='gatewise-container-'+uuid.uuid4().hex[:10];checks=[]
def check(name,ok):checks.append({'name':name,'pass':bool(ok)});assert ok,name
info=json.loads(command('image','inspect',a.image))[0]
report={'image':a.image,'image_id':info['Id'],'architecture':info['Architecture'],'checks':checks}
try:
 check('nonroot runtime',info['Config']['User']=='65532:65532')
 with tempfile.TemporaryDirectory(prefix='gatewise-container-') as tmp:
  file=pathlib.Path(tmp)/'config.json';host='host.containers.internal' if pathlib.Path(a.engine).name=='podman' else 'host.docker.internal'
  file.write_text(json.dumps({'listen_addr':'0.0.0.0:8080','shutdown_timeout':'2s','services':{'test':{'upstream_url':f'http://{host}:{backend.server_port}','max_concurrent':2,'reserved_critical':1}},'routes':[{'path_prefix':'/','service':'test','priority':'critical'}]}));file.chmod(0o644)
  offline=subprocess.run([a.engine,'run','--rm','--read-only','--cap-drop=ALL','-e','GATEWISE_SERVICE_ORDERS_URL=http://example.invalid',a.image,'-config','/etc/gatewise/config.json','-check-config'],capture_output=True);check('offline configuration validation',offline.returncode==0)
  args=['run','-d','--name',name,'--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','-p',f'127.0.0.1:{port}:8080','-v',str(file)+':/etc/gatewise/config.json:ro']
  if pathlib.Path(a.engine).name=='docker':args+=['--add-host','host.docker.internal:host-gateway']
  command(*args,a.image);base=f'http://127.0.0.1:{port}'
  for i in range(100):
   try:
    if call(base,'/healthz')[0]==200:break
   except Exception:pass
   time.sleep(.05)
  else:raise RuntimeError('container readiness failed')
  check('forwards real HTTP backend',call(base,'/hello')==(200,b'backend-ok'))
  with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
   barrier=threading.Barrier(6)
   def send(_):barrier.wait();return call(base,'/slow')[0]
   statuses=list(pool.map(send,range(6)))
  check('overload admits2 and refuses4',statuses.count(200)==2 and statuses.count(503)==4 and peak<=2)
  check('metrics available',b'gatewise_requests_in_flight' in call(base,'/metrics')[1])
  with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
   pending=pool.submit(call,base,'/slow')
   for i in range(100):
    with lock:running=active>0
    if running:break
    time.sleep(.01)
   check('shutdown begins with work in flight',running);command('kill','--signal','TERM',name);check('inflight response completes during shutdown',pending.result()[0]==200)
  code=command('wait',name);logs=command('logs',name);check('graceful shutdown exits0',code=='0' and 'shutdown complete' in logs)
 report['passed']=True
finally:
 subprocess.run([a.engine,'rm','-f',name],capture_output=True);backend.shutdown();pathlib.Path(a.output).write_text(json.dumps(report,indent=2)+'\n')
print('PASS',len(checks),'native container checks',report['architecture'])
