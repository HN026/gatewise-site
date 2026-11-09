#!/usr/bin/env python3
"""Sustained local PostgreSQL reliability validation, not a production capacity claim."""
import argparse,datetime,hashlib,json,pathlib,platform,subprocess,threading,time,uuid
from run import ROOT,Stack,api
from queue_benchmark import values,state
from comparison_recorded import drain,refs
PROFILES=['middleware-fixed','middleware-adaptive','proxy-fixed','proxy-adaptive']
PHASES=[('healthy',10,120,0,0,3000),('warmup',10,120,10,0,3000),('sustained',60,1800,10,0,3000),('slowdown',30,600,120,0,3000),('errors',20,600,5,10,3000),('caller-deadline',10,1000,80,0,20),('recovery',30,120,0,0,3000),('low-traffic',10,10,0,0,3000)]
CRITERIA={'execution_ceiling':4,'queue_ceiling':16,'pool_ceiling':4,'dispatch_p95_ms_max':25,'idle_backend_goroutines_max':64,'backend_heap_bytes_max':128*1024*1024,'proxy_rss_bytes_max':256*1024*1024,'cleanup_seconds':5,'recovery_success_fraction_min':.99,'scope':'Four three-minute local runs, one per deployment; no replication of capacity claims. Heap/goroutine bounds are coarse retained-growth screens, not proof that every allocation is leak-free. Proxy execution bound concerns proxy handlers; remote cancellation cleanup is separate.'}
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
def one(s,profile,phases):
 s.start_app(profile,execution_limit=4,reserved=1,adaptive_settings={'profile':'stable','initial_concurrent':4},queue_settings={'max_waiting':16,'max_wait':'40ms','aging_after':'20ms','reserved':{'critical':4}},route_budgets=True)
 report={'profile':profile,'started_at':now(),'phases':[],'trace':[],'checks':[]};errors=[];stop=threading.Event();current=['startup'];started=time.monotonic()
 def sample():
  while not stop.is_set():
   try:
    v=values(s)
    proxy_rss=None
    if profile.startswith('proxy-'):proxy_rss=int(subprocess.check_output(['ps','-o','rss=','-p',str(s.children[-1].pid)],text=True).strip())*1024
    for role,u in s.admins.items():
     a=api(u,'/state');report['trace'].append({'seconds':time.monotonic()-started,'phase':current[0],'service':role,**state(v,role),'pool':a['pool'],'heap_bytes':a['heap_bytes'],'goroutines':a['goroutines'],'proxy_rss_bytes':proxy_rss})
   except Exception as e:errors.append(type(e).__name__)
   stop.wait(.25)
 thread=threading.Thread(target=sample,daemon=True);thread.start()
 try:
  for index,(name,seconds,rps,delay,failures,timeout) in enumerate(phases):
   before=drain(s);s.control(1,delay,failures);current[0]=name
   cfg={'urls':s.urls,'seconds':seconds,'start_rps':rps,'end_rps':rps,'business_slots':4,'traffic_seed':10001+index,'client_timeout_ms':timeout,'run_id':uuid.uuid4().hex}
   result=json.loads(subprocess.run([str(s.projects/'commerce-backend/bin/loadgen')],input=json.dumps(cfg),capture_output=True,text=True,check=True).stdout);after=drain(s)
   result.update(name=name,seconds=seconds,offered_rps=rps,sql_delay_ms=delay,failure_every=failures,client_timeout_ms=timeout,pool_wait_seconds=sum(after[r]['WaitDuration']-before[r]['WaitDuration'] for r in before)/1e9,drained=all(not x['InUse'] for x in after.values()) and all(not x['active'] for x in refs(s).values()))
   report['phases'].append(result);print(profile,name,'sent',result['sent'],'critical timely/sec',round(result['traffic']['critical']['successful_within_100ms_from_schedule_per_offered_second'],1),flush=True)
  current[0]='idle';s.control(1,0,0);time.sleep(2);drain(s);final={r:api(u,'/state') for r,u in s.admins.items()};v=values(s)
  def check(name,ok):report['checks'].append({'name':name,'pass':bool(ok)})
  ps=report['phases'];trace=report['trace'];ts=[t for p in ps for t in p['traffic'].values()]
  check('all scheduled traffic delivered',all(p['scheduled']==p['sent'] and not p['dropped_schedules'] for p in ps))
  check('complete response accounting',all(t['sent']==sum(t[k] for k in ['successful','rejected_503','rejected_429','backend_5xx','transport_errors','other_statuses']) and not t['other_statuses'] for t in ts))
  check('only deliberate deadline transport failures',all(t['transport_errors']==t['client_deadline_errors'] and (p['name']=='caller-deadline' or not t['transport_errors']) for p in ps for t in p['traffic'].values()))
  check('dispatch p95 below25ms',all((t['dispatch_p95_ms'] or 0)<25 for t in ts))
  check('execution and controller bounds',all(0<=x['active']<=4 and (2 if profile.endswith('adaptive') else 4)<=x['limit']<=4 for x in trace))
  check('queue stays bounded',all(0<=x['waiting']<=16 and x['high_water']<=16 for x in trace))
  check('pool stays bounded',all(0<=x['pool']['InUse']<=4 for x in trace))
  check('all phases drain actual backend work',all(p['drained'] for p in ps))
  check('queue lifecycle balances after final drain',all((q:=state(v,r))['queue_events']['enqueued']==sum(q['queue_events'][e] for e in ['admitted','timeout','deadline','canceled','policy_rejected']) and not q['active'] and not q['waiting'] for r in s.admins))
  check('queue admission and cancellation exercised',sum(state(v,r)['queue_events']['admitted'] for r in s.admins)>0 and sum(state(v,r)['queue_events']['canceled'] for r in s.admins)>0)
  check('recovery accepts at least99percent',all(t['successful']>=.99*t['sent'] for p in ps if p['name'] in ['recovery','low-traffic'] for t in p['traffic'].values()))
  check('backend heap stays below128MiB',all(x['heap_bytes']<128*1024*1024 for x in trace))
  check('proxy RSS stays below256MiB',all(x['proxy_rss_bytes'] is None or x['proxy_rss_bytes']<256*1024*1024 for x in trace))
  check('idle backend goroutines below64',all(x['goroutines']<64 for x in final.values()))
  check('monitoring complete',bool(trace) and not errors)
  report.update(finished_at=now(),idle=final,sampling_errors=errors,passed=all(c['pass'] for c in report['checks']))
  return report
 finally:stop.set();thread.join(5);s.stop_app()
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default='podman');p.add_argument('--profiles',nargs='+',choices=PROFILES,default=PROFILES);p.add_argument('--source-ref',default='',help='Immutable core commit under qualification');p.add_argument('--quick',action='store_true',help='Exploratory short run; cannot qualify the release');p.add_argument('--output',type=pathlib.Path,default=ROOT/'results/reliability-results.json');a=p.parse_args()
 phases=[(n,min(t,2),r,d,f,c) for n,t,r,d,f,c in PHASES] if a.quick else PHASES
 report={'release':'v1.0.0','started_at':now(),'exploratory':a.quick,'criteria':CRITERIA,'methodology':{'platform':platform.platform(),'core_ref':a.source_ref,'harness_sha256':hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),'phases':phases,'traffic':'Seeded open-loop arrivals, exact counts, no retries, independently shuffled80percent critical/20percent background and60/20/20 services. Work drains at phase boundaries; controller state survives.100ms timely success measured from scheduled arrival.','settings':'Four execution slots, reservation1, SQLpool4; waiting16,max40ms,aging20ms,waitingreservation4. Stable adaptive starts4,floor2. Other policies disabled. Three services plus optional proxy.','go':subprocess.check_output([__import__('os').environ.get('GO_BIN','go'),'version'],text=True).strip()},'runs':[]}
 def save():a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(report,indent=2)+'\n')
 with Stack(a.projects,a.engine) as s:
  s.build();s.start_db();report['methodology'].update(postgres=s.db_version,image_digest=s.image_digest,binaries_sha256={name:hashlib.sha256((s.projects/file).read_bytes()).hexdigest() for name,file in [('proxy','gatewise/bin/gatewise'),('commerce','commerce-backend/bin/commerce-backend'),('loadgen','commerce-backend/bin/loadgen')]})
  for module in ['gatewise','commerce-backend']:
   files={str(f.relative_to(s.projects/module)):hashlib.sha256(f.read_bytes()).hexdigest() for f in (s.projects/module).rglob('*') if f.is_file() and (f.suffix=='.go' or f.name in ['go.mod','go.sum']) and 'tests' not in f.parts};report['methodology'][module+'_source_sha256']=hashlib.sha256(json.dumps(files,sort_keys=True).encode()).hexdigest()
  for profile in a.profiles:report['runs'].append(one(s,profile,phases));save()
 report.update(finished_at=now(),passed=all(r['passed'] for r in report['runs']));save();print('PASS' if report['passed'] else 'FAIL',flush=True)
 if not report['passed']:raise SystemExit(1)
if __name__=='__main__':main()
