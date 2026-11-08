#!/usr/bin/env python3
"""Compare hard tenant caps with lending during solo load, returning tenants and recovery."""
import argparse,datetime,hashlib,json,pathlib,platform,subprocess,threading,time,uuid
from run import ROOT,Stack,api
from benchmark import metrics,metric
PROFILES=['middleware-fixed','proxy-fixed','middleware-adaptive','proxy-adaptive']
PHASES=[('solo',3,480,0,40),('returning',5,480,8,40),('all-busy',3,120,120,40),('solo-again',3,480,0,40),('recovery',3,8,8,0)]
STRATEGIES={'hard-cap':0,'sharing-4':4,'sharing-12':12}
def one(s,profile,strategy,repeat):
 ceiling=STRATEGIES[strategy];s.start_app(profile,tenants=True,sharing=ceiling);trace=[];errors=[];stop=threading.Event();phase=['startup'];started=time.monotonic()
 def sample():
  while not stop.is_set():
   try:
    values={}
    for url in s.metrics_urls:values.update(metrics(url))
    for role,url in s.admins.items():
     state=api(url,'/state');v={k:n for k,n in values.items() if 'service="'+role+'"' in k}
     trace.append({'seconds':time.monotonic()-started,'phase':phase[0],'service':role,'limit':metric(v,'gatewise_concurrency_limit'),'active':sum(n for k,n in v.items() if k.startswith('gatewise_requests_in_flight{')),'tracked':metric(v,'gatewise_tenants_tracked'),'tenant_leases':sum(n for k,n in v.items() if k.startswith('gatewise_tenant_requests_in_flight{')),'demanding':metric(v,'gatewise_tenant_sharing_demanding_tenants') if ceiling else None,'borrowed':sum(n for k,n in v.items() if k.startswith('gatewise_tenant_sharing_requests_in_flight{') and 'allocation="borrowed"' in k),'borrowing_paused':metric(v,'gatewise_tenant_sharing_borrowing_paused') if ceiling else None,'pool_wait_count':state['pool']['WaitCount'],'pool_wait_seconds':state['pool']['WaitDuration']/1e9,'heap_bytes':state['heap_bytes'],'goroutines':state['goroutines']})
   except Exception as e:errors.append(type(e).__name__)
   stop.wait(.25)
 worker=threading.Thread(target=sample,daemon=True);worker.start();phases=[]
 try:
  for name,duration,busy,ordinary,delay in PHASES:
   s.control(delay=delay);phase[0]=name;before={r:api(u,'/state')['pool'] for r,u in s.admins.items()}
   actors=[{'id':'busy','weight':busy}]+[{'id':'ordinary-'+str(i),'weight':ordinary} for i in range(1,4) if ordinary]
   rate=busy+3*ordinary;cfg={'urls':s.urls,'seconds':duration,'start_rps':rate,'end_rps':rate,'business_slots':5,'run_id':uuid.uuid4().hex[:10],'tenants':actors}
   out=subprocess.run([str(s.projects/'commerce-backend/bin/loadgen')],input=json.dumps(cfg),capture_output=True,text=True,check=True);result=json.loads(out.stdout)
   deadline=time.monotonic()+2;after={r:api(u,'/state')['pool'] for r,u in s.admins.items()}
   while any(p['InUse'] for p in after.values()) and time.monotonic()<deadline:time.sleep(.02);after={r:api(u,'/state')['pool'] for r,u in s.admins.items()}
   result.update(name=name,sql_delay_ms=delay,pools={r:{'wait_count':after[r]['WaitCount']-before[r]['WaitCount'],'wait_seconds':(after[r]['WaitDuration']-before[r]['WaitDuration'])/1e9,'in_use_after_drain':after[r]['InUse']} for r in after});phases.append(result)
   print(profile,strategy,repeat,name,'sent',result['sent'],'success',sum(v['successful'] for v in result['traffic'].values()),'ordinary first',[(k,v['first_success_seconds']) for k,v in result['tenant_traffic'].items() if k.startswith('ordinary')],flush=True)
  returning=next(p for p in phases if p['name']=='returning');recovery=phases[-1]
  checks=[{'name':'scheduled requests accounted for without missed arrivals','pass':all(p['sent']==p['scheduled'] and p['dropped_schedules']==0 for p in phases)},
   {'name':'no client transport errors','pass':all(v['transport_errors']==0 for p in phases for v in p['traffic'].values())},
   {'name':'no unexpected statuses','pass':all(v['other_statuses']==0 for p in phases for v in p['traffic'].values())},
   {'name':'generator dispatch p95 below 10ms','pass':all((v['dispatch_p95_ms'] or 0)<10 for p in phases for v in p['tenant_traffic'].values())},
   {'name':'database connections drained','pass':all(v['in_use_after_drain']==0 for p in phases for v in p['pools'].values())},
   {'name':'registry remains bounded','pass':all(0<=v['tracked']<=32 for v in trace)},
   {'name':'execution stays below configured service ceiling','pass':all(0<=v['active']<=12 for v in trace)},
   {'name':'every returning tenant completes work within two seconds','pass':all(returning['tenant_traffic']['ordinary-'+str(i)]['first_success_seconds'] is not None and returning['tenant_traffic']['ordinary-'+str(i)]['first_success_seconds']<2 for i in range(1,4))},
   {'name':'healthy recovery has no rejection or server errors','pass':all(v['rejected_503']==0 and v['backend_5xx']==0 for v in recovery['tenant_traffic'].values())},
   {'name':'monitoring available','pass':not errors}]
  if ceiling:checks += [{'name':'spare capacity actually borrowed during solo load','pass':phases[0]['tenant_traffic']['busy']['allocations'].get('borrowed',0)>0},{'name':'borrowing resumes when other tenants leave','pass':phases[3]['tenant_traffic']['busy']['allocations'].get('borrowed',0)>0},{'name':'aggregate borrowed accounting bounded','pass':all(0<=v['borrowed']<=v['tenant_leases'] for v in trace)},{'name':'all successful allocations have known classes','pass':all(set(v['allocations'])<={'share','borrowed','fallback'} for p in phases for v in p['traffic'].values())}]
  return {'profile':profile,'strategy':strategy,'repeat':repeat,'phases':phases,'trace':trace,'sampling_errors':errors,'checks':checks,'passed':all(c['pass'] for c in checks)}
 finally:stop.set();worker.join(timeout=5);s.stop_app()
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default='podman');p.add_argument('--profiles',nargs='+',choices=PROFILES,default=PROFILES);p.add_argument('--strategies',nargs='+',choices=STRATEGIES,default=list(STRATEGIES));p.add_argument('--repeats',type=int,default=3);p.add_argument('--output',type=pathlib.Path,default=ROOT/'results/sharing-results.json');a=p.parse_args()
 if not 1<=a.repeats<=10:p.error('repeats must be 1..10')
 variants=[(p,t) for p in a.profiles for t in a.strategies];report={'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'methodology':{'scope':'One local host, real native services and PostgreSQL in a container VM; no production capacity claim','phases':PHASES,'mix':'All requests critical; 60% orders, 20% catalog, 20% inventory; no HTTP priority cohorts in this experiment','policy':'Default tenant share/hard cap 2, borrowed ceiling 4 or 12; demand window 500ms, registry 32, idle expiry 2s; missing/overflow reject','capacity':'Service max 12, critical reservation 4, adaptive minimum 5/initial 12; each SQL pool 4','traffic':'Scheduled Go open-loop arrivals, no application retries, timeout 3s, max 4096 generator requests in flight','return_time':'First successful response completion after ordinary traffic restarts; includes waiting until the first offered request','sampling':'250ms; pool cleanup measured after each phase before the next; control calls excluded','limits':'Borrowing is not preemption or a latency guarantee; compare total throughput, ordinary p95 and rejection rates together'},'runs':[]}
 with Stack(a.projects,a.engine) as s:
  s.build();s.start_db();report.update(platform=platform.system()+'/'+platform.machine(),go_version=subprocess.check_output(['go','version'],text=True).strip(),postgres_version=s.db_version,postgres_image=s.image_digest,binary_sha256={n:hashlib.sha256((a.projects/n/'bin'/n).read_bytes()).hexdigest() for n in ['gatewise','commerce-backend']},generator_sha256=hashlib.sha256((a.projects/'commerce-backend/bin/loadgen').read_bytes()).hexdigest())
  for repeat in range(1,a.repeats+1):
   shift=(repeat-1)%len(variants)
   for profile,strategy in variants[shift:]+variants[:shift]:
    report['runs'].append(one(s,profile,strategy,repeat));a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(report,indent=2)+'\n')
 report.update(finished_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),passed=all(r['passed'] for r in report['runs']));a.output.write_text(json.dumps(report,indent=2)+'\n');print('Report',a.output,'passed',report['passed'],flush=True)
 if not report['passed']:raise SystemExit(1)
if __name__=='__main__':main()
