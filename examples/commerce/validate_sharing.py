#!/usr/bin/env python3
"""Check live handoff while borrowed requests are still running (not a throughput benchmark)."""
import argparse,concurrent.futures,datetime,json,pathlib,threading,time,uuid
from run import ROOT,Stack,request,api
from benchmark import metrics
PROFILES=['middleware-fixed','proxy-fixed','middleware-adaptive','proxy-adaptive']
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default='podman');p.add_argument('--output',type=pathlib.Path,default=ROOT/'results/sharing-validation.json');a=p.parse_args();runs=[]
 with Stack(a.projects,a.engine) as s:
  s.build();s.start_db()
  for profile in PROFILES:
   s.start_app(profile,tenants=True,sharing=12);s.control(delay=150);stop=threading.Event();busy=[];began=time.monotonic()
   def call(identity):return request(s.urls['orders'],'/orders/checkout',{'amount_cents':1000},'POST',{'X-Demo-Tenant':identity,'Idempotency-Key':uuid.uuid4().hex})
   def load():
    while not stop.is_set():
     code,_,_=call('handoff-busy');busy.append(code)
     if code==503:time.sleep(.005)
   def observed():
    values={}
    for base in s.metrics_urls:values.update(metrics(base))
    values={k:v for k,v in values.items() if 'service="orders"' in k}
    return {'borrowed':sum(v for k,v in values.items() if k.startswith('gatewise_tenant_sharing_requests_in_flight{') and 'allocation="borrowed"' in k),'leases':sum(v for k,v in values.items() if k.startswith('gatewise_tenant_requests_in_flight{')),'active':sum(v for k,v in values.items() if k.startswith('gatewise_requests_in_flight{'))}
   with concurrent.futures.ThreadPoolExecutor(max_workers=20) as pool:
    workers=[pool.submit(load) for _ in range(16)];deadline=time.monotonic()+3;before=observed()
    while before['borrowed']<6 and time.monotonic()<deadline:time.sleep(.01);before=observed()
    return_start=time.monotonic()
    def returning(identity):
     attempts=[];until=time.monotonic()+3
     while time.monotonic()<until:
      code,body,headers=call(identity);attempts.append({'status':code,'reason':json.loads(body).get('reason')})
      if code==201:return {'tenant':identity,'completion_seconds':time.monotonic()-return_start,'allocation':headers.get('X-Gatewise-Tenant-Admission'),'share':headers.get('X-Gatewise-Tenant-Share'),'attempts':attempts}
      if code!=503:return {'tenant':identity,'unexpected_status':code,'attempts':attempts}
      time.sleep(.01)
     return {'tenant':identity,'timed_out':True,'attempts':attempts}
    try:
     futures=[pool.submit(returning,'returning-'+str(i//2)) for i in range(4)];returned=[f.result() for f in futures]
    finally:stop.set()
    for f in workers:f.result()
   s.control(delay=0);deadline=time.monotonic()+2;after=observed()
   while (after['leases'] or any(api(u,'/state')['pool']['InUse'] for u in s.admins.values())) and time.monotonic()<deadline:time.sleep(.02);after=observed()
   checks=[{'name':'borrowed requests active before tenants return','pass':before['borrowed']>=6 and before['active']>0},{'name':'all four returning requests eventually succeed','pass':all('completion_seconds' in v for v in returned)},{'name':'returning admissions use their share','pass':all(v.get('allocation')=='share' and int(v.get('share','0'))>=1 for v in returned)},{'name':'each returning request completes within three seconds','pass':all(v.get('completion_seconds',99)<3 for v in returned)},{'name':'no unexpected errors in returning attempts','pass':all(c['status'] in [201,503] for v in returned for c in v['attempts'])},{'name':'tenant leases released after clients stop','pass':after['leases']==0 and after['borrowed']==0},{'name':'SQL connections released','pass':all(api(u,'/state')['pool']['InUse']==0 for u in s.admins.values())}]
   runs.append({'profile':profile,'before_return':before,'returning':returned,'busy_statuses':{str(c):busy.count(c) for c in set(busy)},'elapsed_seconds':time.monotonic()-began,'checks':checks,'passed':all(c['pass'] for c in checks)});print(profile,'passed',runs[-1]['passed'],'return times',[v.get('completion_seconds') for v in returned],flush=True)
 report={'recorded_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'scope':'Live orders API with PostgreSQL; share 2, ceiling 12; 16 busy clients, 150ms SQL delay; start two requests for each of two returning tenants while at least six borrowed leases are active. Test clients retry 503 every 10ms for at most 3s; this targeted handoff test is not the open-loop performance experiment.','runs':runs,'checks':sum(len(r['checks']) for r in runs),'passed':all(r['passed'] for r in runs)};a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(report,indent=2)+'\n');print('Passed',report['passed'],'checks',report['checks'])
 if not report['passed']:raise SystemExit(1)
if __name__=='__main__':main()
