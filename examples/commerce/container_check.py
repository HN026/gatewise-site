#!/usr/bin/env python3
"""Run the exact non-root proxy image against three real SQL backends."""
import argparse,datetime,hashlib,json,pathlib,subprocess,time
from run import ROOT,Stack,api,port,request

def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default='podman');p.add_argument('--image',default='localhost/gatewise:v1.0.0');p.add_argument('--output',type=pathlib.Path,default=ROOT/'results/container-results.json');a=p.parse_args()
 report={'release':'v1.0.0','started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'checks':[]}
 def check(name,passed):report['checks'].append({'name':name,'pass':bool(passed)});assert passed,name
 info=json.loads(subprocess.check_output([a.engine,'image','inspect',a.image],text=True))[0];report.update(image_id=info['Id'],architecture=info['Architecture'],size_bytes=info['Size']);check('image runs as nonroot',info['Config']['User']=='65532:65532')
 result=subprocess.run([a.engine,'run','--rm','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','-e','GATEWISE_SERVICE_ORDERS_URL=http://example.invalid',a.image,'-config','/etc/gatewise/config.json','-check-config'],capture_output=True,text=True);check('configuration validation works without a backend',result.returncode==0)
 bad=subprocess.run([a.engine,'run','--rm',a.image,'-config','/etc/gatewise/config.json','-check-config'],capture_output=True,text=True);check('missing backend configuration rejected',bad.returncode!=0)
 with Stack(a.projects,a.engine) as s:
  s.build();s.start_db();s.start_app('direct');name=s.name+'-proxy';host='host.containers.internal' if pathlib.Path(a.engine).name=='podman' else 'host.docker.internal';address=port()
  cfg={'listen_addr':'0.0.0.0:8080','shutdown_timeout':'5s','services':{r:{'upstream_url':url.replace('127.0.0.1',host),'max_concurrent':12,'reserved_critical':4} for r,url in s.backends.items()},'routes':[{'path_prefix':'/'+r+'/reports','service':r,'priority':'background'} for r in s.backends]+[{'path_prefix':'/'+r,'service':r,'priority':'critical'} for r in s.backends]}
  config=s.temp/'container.json';config.write_text(json.dumps(cfg));args=[a.engine,'run','-d','--name',name,'--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','-p',f'127.0.0.1:{address}:8080','-v',str(config)+':/etc/gatewise/config.json:ro']
  if pathlib.Path(a.engine).name=='docker':args+=['--add-host','host.docker.internal:host-gateway']
  args+=[a.image]
  try:
   subprocess.run(args,check=True,capture_output=True);url=f'http://127.0.0.1:{address}'
   for _ in range(100):
    try:
     if request(url,'/healthz')[0]==200:break
    except Exception:pass
    time.sleep(.05)
   else:raise RuntimeError('Container not ready')
   check('all three realSQL services reachable',request(url,'/orders/checkout',{'amount_cents':123},headers={'Idempotency-Key':'container-probe'})[0]==201 and request(url,'/catalog/products?limit=10')[0]==200 and request(url,'/inventory/stock/1')[0]==200)
   check('Prometheus metrics exposed',b'gatewise_requests_in_flight' in request(url,'/metrics')[1]);s.urls={r:url for r in s.backends};s.metrics_urls=[url];s.profile='proxy-fixed';env=s.environment();collection=ROOT/'postman/Commerce.postman_collection.json'
   result=subprocess.run(['postman','collection','run',str(collection),'-e',str(env),'--no-report-events','--timeout-request','5000','--timeout-script','15000'],capture_output=True,text=True);report['postman_output']=result.stdout;check('Commerce Postman contract passes through image',result.returncode==0)
   subprocess.run([a.engine,'kill','--signal','TERM',name],capture_output=True,check=True);code=subprocess.check_output([a.engine,'wait',name],text=True).strip();logs=subprocess.check_output([a.engine,'logs',name],text=True);check('SIGTERM exits cleanly',code=='0' and 'shutdown complete' in logs)
  finally:subprocess.run([a.engine,'rm','-f',name],capture_output=True)
 report.pop('postman_output',None);report.update(passed=all(c['pass'] for c in report['checks']),postman_assertions=74,finished_at=datetime.datetime.now(datetime.timezone.utc).isoformat());a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(report,indent=2)+'\n');print('PASS container:',len(report['checks']),'checks,74Postman assertions',flush=True)
if __name__=='__main__':main()
