#!/usr/bin/env python3
"""Fixed-policy comparison. Integrity gates never require a Gatewise win."""
import argparse,datetime,hashlib,json,pathlib,platform,statistics,subprocess,threading,time,uuid
from run import ROOT,Stack,api
from queue_benchmark import values,state,QUEUE
CASES=[{'id':'direct','profile':'direct','reserved':0},{'id':'semaphore','profile':'semaphore','reserved':0}]
CASES += [{'id':f'rate-{r}','profile':'rate','rate':r,'reserved':0} for r in [100,300,900]]
CASES += [{'id':'fixed-unreserved','profile':'middleware-fixed','reserved':0}]
CASES += [{'id':p+('-queue' if q else ''),'profile':p,'queue':q,'reserved':1} for p in ['middleware-fixed','middleware-adaptive','proxy-fixed','proxy-adaptive'] for q in [False,True]]
# name, offered seconds, aggregate RPS, SQL delay ms, failure every N, client timeout ms
PHASES=[('healthy',1.5,120,0,0,3000),('warmup',1,120,10,0,3000)]
for i in range(3):PHASES += [(f'burst-{i}',.1,1800,10,0,3000),(f'relief-{i}',.5,60,10,0,3000)]
PHASES += [('sustained',3,1800,10,0,3000),('background-flood',3,1800,10,0,3000),('slowdown',3,600,120,0,3000),('errors',2,600,5,10,3000),('caller-deadline',.8,1000,80,0,20),('recovery',3,240,0,0,3000),('low-traffic',2,10,0,0,3000)]
CRITERIA={'all_schedules_delivered':True,'dispatch_p95_ms_max':25,'unexpected_transport_errors':0,'pool_maximum':4,'execution_maximum_when_bounded':4,'queue_maximum':16,'drain_timeout_seconds':5,'comparison':'Report every policy and outcome; neither usefulness nor latency gates require Gatewise to beat a reference. SQL/error and caller-deadline failures are deliberate workload outcomes, not erased or defined as measurement failures.'}
def refs(s):return {r:api(u,'/reference/state') for r,u in s.admins.items()}
def pools(s):return {r:api(u,'/state')['pool'] for r,u in s.admins.items()}
def drain(s):
 end=time.monotonic()+5
 while time.monotonic()<end:
  p=pools(s);v=values(s);rs=refs(s)
  if all(not x['InUse'] for x in p.values()) and all(not x['active'] for x in rs.values()) and all(not state(v,r)['active'] and not state(v,r)['waiting'] for r in rs):return p
  time.sleep(.01)
 raise RuntimeError('drain failed')
def one(s,case,repeat):
 profile=case['profile'];s.start_app(profile,execution_limit=4,reserved=case['reserved'],reference_rate=case.get('rate',300),reference_burst=4,adaptive_settings={'profile':'stable','initial_concurrent':4},queue_settings=QUEUE if case.get('queue') else None,route_budgets=True)
 trace=[];errors=[];stop=threading.Event();current=['startup'];started=time.monotonic();results=[]
 def sample():
  while not stop.is_set():
   try:
    v=values(s);ps=pools(s);rs=refs(s)
    for r in rs:trace.append({'seconds':time.monotonic()-started,'phase':current[0],'service':r,'reference':rs[r],'pool':ps[r],**state(v,r)})
   except Exception as e:errors.append(str(e))
   stop.wait(.05)
 thread=threading.Thread(target=sample,daemon=True);thread.start()
 try:
  for name,seconds,rps,delay,failures,timeout in PHASES:
   before=drain(s);s.control(1,delay,failures);current[0]=name;at=time.monotonic()-started
   config={'urls':s.urls,'seconds':seconds,'start_rps':rps,'end_rps':rps,'business_slots':1 if name=='background-flood' else 4,'client_timeout_ms':timeout,'run_id':uuid.uuid4().hex}
   p=subprocess.run([str(s.projects/'commerce-backend/bin/loadgen')],input=json.dumps(config),capture_output=True,text=True,check=True);result=json.loads(p.stdout);after=drain(s)
   result.update(name=name,seconds=seconds,rps=rps,sql_delay_ms=delay,failure_every=failures,client_timeout_ms=timeout,started=at,pool_wait_count=sum(after[r]['WaitCount']-before[r]['WaitCount'] for r in before),pool_wait_seconds=sum(after[r]['WaitDuration']-before[r]['WaitDuration'] for r in before)/1e9)
   results.append(result);t=result['traffic']['critical'];print(case['id'],repeat,name,'timely',round(t['successful_within_100ms_from_schedule_per_offered_second'],1),'reject',t['rejected_503']+t['rejected_429'],'errors',t['backend_5xx'],'deadline',t['client_deadline_errors'],flush=True)
  checks=[{'name':'all scheduled arrivals delivered','pass':all(p['sent']==p['scheduled'] and not p['dropped_schedules'] for p in results)},{'name':'complete outcome accounting','pass':all(t['sent']==sum(t[k] for k in ['successful','rejected_503','rejected_429','backend_5xx','transport_errors','other_statuses']) and not t['other_statuses'] for p in results for t in p['traffic'].values())},{'name':'only intentional client deadline transport errors','pass':all(t['transport_errors']==t['client_deadline_errors'] and (p['name']=='caller-deadline' or not t['transport_errors']) for p in results for t in p['traffic'].values())},{'name':'generator dispatch p95 within25ms','pass':all((t['dispatch_p95_ms'] or 0)<25 for p in results for t in p['traffic'].values())},{'name':'SQL pool remains bounded','pass':all(0<=v['pool']['InUse']<=4 for v in trace)},{'name':'all work drains','pass':all(not x['InUse'] for x in drain(s).values()) and all(not x['active'] for x in refs(s).values())},{'name':'monitoring complete','pass':bool(trace) and not errors}]
  if profile not in ['direct','rate']:
   checks.append({'name':'execution bounded at4','pass':all(v['reference']['peak']<=4 for v in trace)})
  if profile.startswith(('proxy-','middleware-')):
   minimum=2 if profile.endswith('adaptive') else 4
   checks.append({'name':'controller stays within configured bounds','pass':all(minimum<=v['limit']<=4 for v in trace)})
  checks.append({'name':'waiting bounded or disabled','pass':all(0<=v['waiting']<=16 and v['high_water']<=16 for v in trace) if case.get('queue') else all(v['waiting']==0 for v in trace)})
  return {'case':case,'repeat':repeat,'phases':results,'trace':trace,'checks':checks,'passed':all(c['pass'] for c in checks),'sampling_errors':errors}
 finally:stop.set();thread.join(5);s.stop_app()
def median(v):
 x=[z for z in v if z is not None];return statistics.median(x) if x else None
def analyze(runs):
 rows=[]
 for case in CASES:
  selected=[r for r in runs if r['case']['id']==case['id']]
  if not selected:continue
  for name,*_ in PHASES:
   ps=[next(p for p in r['phases'] if p['name']==name) for r in selected]
   for service in ['all','orders','catalog','inventory']:
    for priority in ['critical','background']:
     ts=[p['traffic'][priority] if service=='all' else p['services'][service][priority] for p in ps];sent=sum(t['sent'] for t in ts);useful=[t['successful_within_100ms_from_schedule_per_offered_second'] for t in ts]
     rows.append({'case':case['id'],'phase':name,'service':service,'priority':priority,'sent':sent,'timely_rps':median(useful),'timely_min':min(useful),'timely_max':max(useful),'success_rps':median(t['successful']/p['seconds'] for t,p in zip(ts,ps)),'p95_ms':median(t['successful_p95_ms'] for t in ts),'queue_p95_ms':median(t['admitted_queue_wait_p95_ms'] for t in ts),'rejected_percent':100*sum(t['rejected_503']+t['rejected_429'] for t in ts)/sent if sent else 0,'server_error_percent':100*sum(t['backend_5xx'] for t in ts)/sent if sent else 0,'deadline_percent':100*sum(t['client_deadline_errors'] for t in ts)/sent if sent else 0,'pool_wait_count':sum(p['pool_wait_count'] for p in ps),'pool_wait_seconds':sum(p['pool_wait_seconds'] for p in ps)})
 return rows
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default='podman');p.add_argument('--cases',nargs='+',choices=[c['id'] for c in CASES]);p.add_argument('--repeats',type=int,default=3);p.add_argument('--output',type=pathlib.Path,default=ROOT/'results/comparison-results.json');a=p.parse_args()
 report={'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'methodology':{'platform':platform.platform(),'cpu':subprocess.check_output(['sysctl','-n','machdep.cpu.brand_string'],text=True).strip(),'logical_cpus':subprocess.check_output(['sysctl','-n','hw.logicalcpu'],text=True).strip(),'memory_bytes':subprocess.check_output(['sysctl','-n','hw.memsize'],text=True).strip(),'go':subprocess.check_output([__import__('os').environ.get('GO_BIN','go'),'version'],text=True).strip(),'cases':CASES,'phases':PHASES,'queue':QUEUE,'repeats':a.repeats,'pool_connections_per_service':4,'execution_ceiling':4,'rate_burst':4,'sql_timeout_ms':1000,'critical_budget_ms':100,'report_controller_budget_ms':500,'traffic':'Open-loop, no retries, 80% critical except background-flood20%, independent60/20/20 orders/catalog/inventory mix.100ms useful-work budget for both priorities includes scheduling+network+queue+handler. SQL delay/failure injection per admitted transaction; everyNth errors are not identical requests across policies. Each phase fully drains before next; controller/token state survives; next phase has no artificial cooldown.','fairness':'All policies share three service processes, PostgreSQL image, pool sizes, deadlines, route handlers and offered schedule. Semaphore and fixed-unreserved isolate a classless concurrency ceiling. Other Gatewise cases reserve1of4 for critical. Rate limits are per service, classless and do not cap active work;100/300/900 rates predeclared, burst4. Proxy cases add a fourth process and hop and are reported as deployment outcomes, not isolated policy-speed comparisons. The direct/rate cases deliberately have no HTTP concurrency cap. Other Gatewise features off.','selection':'Exploratory runs revealed the need for a background-heavy phase; they are excluded. Rates, cases, revised workload and integrity criteria fixed before this subsequent confirmatory matrix; no win gate. Fresh databases each run; rotating case order each repeat. Single local host, no production capacity claim.'},'criteria':CRITERIA,'runs':[]}
 def save():a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(report,indent=2)+'\n')
 with Stack(a.projects,a.engine) as s:
  s.build();s.start_db();report['methodology'].update(postgres=s.db_version,image_digest=s.image_digest,podman_resources=[x['Resources'] for x in json.loads(subprocess.check_output([a.engine,'machine','inspect'],text=True))])
  for module in ['gatewise','commerce-backend']:
   files={str(f.relative_to(s.projects/module)):hashlib.sha256(f.read_bytes()).hexdigest() for f in (s.projects/module).rglob('*') if f.is_file() and (f.suffix=='.go' or f.name in ['go.mod','go.sum']) and 'tests' not in f.parts};report['methodology'][module+'_source_sha256']=hashlib.sha256(json.dumps(files,sort_keys=True).encode()).hexdigest()
  report['methodology']['harness_sha256']=hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest();report['methodology']['binaries_sha256']={v:hashlib.sha256((s.projects/m/'bin'/v).read_bytes()).hexdigest() for m,v in [('gatewise','gatewise'),('commerce-backend','commerce-backend'),('commerce-backend','loadgen')]}
  cases=[c for c in CASES if not a.cases or c['id'] in a.cases]
  for repeat in range(1,a.repeats+1):
   shift=(repeat-1)*5%len(cases)
   for case in cases[shift:]+cases[:shift]:report['runs'].append(one(s,case,repeat));save()
 report.update(rows=analyze(report['runs']),finished_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),passed=all(r['passed'] for r in report['runs']));save();print('PASS' if report['passed'] else 'FAIL',flush=True)
 if not report['passed']:raise SystemExit(1)
if __name__=='__main__':main()
