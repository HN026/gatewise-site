"""Seeded traffic and reservation sweeps; criteria are fixed before collection."""
import argparse,datetime,hashlib,json,pathlib,platform,statistics,subprocess,threading,time,uuid
from run import ROOT,Stack,api
from comparison_protocol import CASES as BASE_CASES,PHASES,host_field,container_resources
from comparison_recorded import refs,pools,drain
from queue_benchmark import values,state,QUEUE
SEEDS=[101,202,303]
CASES=[dict(c) for c in BASE_CASES]+[
 {'id':'fixed-reserved-2','profile':'middleware-fixed','reserved':2},
 {'id':'fixed-reserved-3','profile':'middleware-fixed','reserved':3},
 {'id':'fixed-service-reservations','profile':'middleware-fixed','reserved':1,'service_reservations':{'orders':2,'catalog':1,'inventory':1}}]
CRITERIA={'dispatch_p95_ms_max':25,'pool_ceiling':4,'execution_ceiling':4,'queue_ceiling':16,'drain_seconds':5,'criterion_scope':'Proxy bound applies to active proxy handlers; native semaphore/middleware bounds apply through actual handler return. Remote observer peaks are reported, not incorrectly constrained by proxy admission. No throughput-win gate.'}
def one(s,case,repeat,phases=PHASES):
 profile=case['profile'];s.start_app(profile,execution_limit=4,reserved=case['reserved'],reference_rate=case.get('rate',300),reference_burst=4,adaptive_settings={'profile':'stable','initial_concurrent':4},queue_settings=QUEUE if case.get('queue') else None,route_budgets=True,service_reservations=case.get('service_reservations'))
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
  for index,(name,seconds,rps,delay,failures,timeout) in enumerate(phases):
   before=drain(s);s.control(1,delay,failures);current[0]=name;at=time.monotonic()-started
   seed=SEEDS[repeat-1]*1000+index+1
   cfg={'urls':s.urls,'seconds':seconds,'start_rps':rps,'end_rps':rps,'business_slots':1 if name=='background-flood' else 4,'traffic_seed':seed,'client_timeout_ms':timeout,'run_id':uuid.uuid4().hex}
   p=subprocess.run([str(s.projects/'commerce-backend/bin/loadgen')],input=json.dumps(cfg),capture_output=True,text=True,check=True);result=json.loads(p.stdout);after=drain(s)
   result.update(name=name,seconds=seconds,rps=rps,sql_delay_ms=delay,failure_every=failures,client_timeout_ms=timeout,started=at,pool_wait_count=sum(after[r]['WaitCount']-before[r]['WaitCount'] for r in before),pool_wait_seconds=sum(after[r]['WaitDuration']-before[r]['WaitDuration'] for r in before)/1e9,pool_by_service={r:{'wait_count':after[r]['WaitCount']-before[r]['WaitCount'],'wait_seconds':(after[r]['WaitDuration']-before[r]['WaitDuration'])/1e9} for r in before});results.append(result)
   t=result['traffic']['critical'];print(case['id'],repeat,name,'timely',round(t['successful_within_100ms_from_schedule_per_offered_second'],1),'refused',t['rejected_503']+t['rejected_429'],'errors',t['backend_5xx'],flush=True)
  checks=[{'name':'all arrivals sent','pass':all(p['sent']==p['scheduled'] and not p['dropped_schedules'] for p in results)},{'name':'complete outcome accounting','pass':all(t['sent']==sum(t[k] for k in ['successful','rejected_503','rejected_429','backend_5xx','transport_errors','other_statuses']) and not t['other_statuses'] and not t['rejection_reasons'].get('other_503') for p in results for t in p['traffic'].values())},{'name':'only deliberate deadline transport errors','pass':all(t['transport_errors']==t['client_deadline_errors'] and (p['name']=='caller-deadline' or not t['transport_errors']) for p in results for t in p['traffic'].values())},{'name':'dispatch p95 below25ms','pass':all((t['dispatch_p95_ms'] or 0)<25 for p in results for t in p['traffic'].values())},{'name':'SQL pool bounded4','pass':all(0<=t['pool']['InUse']<=4 for t in trace)},{'name':'monitoring present','pass':bool(trace) and not errors},{'name':'all SQL and handlers drained','pass':all(not p['InUse'] for p in drain(s).values()) and all(not p['active'] for p in refs(s).values())},{'name':'seed and plan fingerprint retained','pass':all(p['traffic_seed']>0 and len(p['traffic_plan_sha256'])==64 for p in results)},{'name':'queue bounded or disabled','pass':all(0<=t['waiting']<=16 and t['high_water']<=16 for t in trace) if case.get('queue') else all(not t['waiting'] for t in trace)}]
  if profile not in ['direct','rate']:
   checks.append({'name':'appropriate execution lifetime bounded4','pass':all(0<=t['active']<=4 for t in trace) if profile.startswith('proxy-') else all(t['reference']['peak']<=4 for t in trace)})
  if profile.startswith(('middleware-','proxy-')):checks.append({'name':'configured controller bounds','pass':all((2 if profile.endswith('adaptive') else 4)<=t['limit']<=4 for t in trace)})
  return {'case':case,'repeat':repeat,'seed':SEEDS[repeat-1],'phases':results,'trace':trace,'checks':checks,'passed':all(c['pass'] for c in checks),'sampling_errors':errors}
 finally:stop.set();thread.join(5);s.stop_app()
def median(v):
 xs=[x for x in v if x is not None];return statistics.median(xs) if xs else None
def analyze(runs):
 rows=[]
 for case in CASES:
  selected=[r for r in runs if r['case']['id']==case['id']]
  if not selected:continue
  for name,*_ in PHASES:
   ps=[next(p for p in r['phases'] if p['name']==name) for r in selected]
   for service in ['all','orders','catalog','inventory']:
    for priority in ['critical','background']:
     ts=[p['traffic'][priority] if service=='all' else p['services'][service][priority] for p in ps];sent=sum(t['sent'] for t in ts);vs=[t['successful_within_100ms_from_schedule_per_offered_second'] for t in ts]
     wait=[{'wait_count':p['pool_wait_count'],'wait_seconds':p['pool_wait_seconds']} if service=='all' else p['pool_by_service'][service] for p in ps]
     rows.append({'case':case['id'],'phase':name,'service':service,'priority':priority,'sent':sent,'timely_rps':median(vs),'timely_min':min(vs),'timely_max':max(vs),'success_rps':median(t['successful']/p['seconds'] for t,p in zip(ts,ps)),'p95_ms':median(t['successful_p95_ms'] for t in ts),'queue_p95_ms':median(t['admitted_queue_wait_p95_ms'] for t in ts),'rejected_percent':100*sum(t['rejected_503']+t['rejected_429'] for t in ts)/sent if sent else 0,'server_error_percent':100*sum(t['backend_5xx'] for t in ts)/sent if sent else 0,'deadline_percent':100*sum(t['client_deadline_errors'] for t in ts)/sent if sent else 0,'pool_wait_count':sum(w['wait_count'] for w in wait),'pool_wait_seconds':sum(w['wait_seconds'] for w in wait)})
 return rows
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default='podman');p.add_argument('--cases',nargs='+',choices=[c['id'] for c in CASES]);p.add_argument('--repeats',type=int,choices=[1,2,3],default=3);p.add_argument('--output',type=pathlib.Path,default=ROOT/'results/hardening-results.json');a=p.parse_args()
 report={'release':'v0.11.1','started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'methodology':{'platform':platform.platform(),'cpu':host_field('machdep.cpu.brand_string'),'logical_cpus':host_field('hw.logicalcpu'),'memory_bytes':host_field('hw.memsize'),'go':subprocess.check_output([__import__('os').environ.get('GO_BIN','go'),'version'],text=True).strip(),'cases':CASES,'phases':PHASES,'seeds':SEEDS,'queue':QUEUE,'repeats':a.repeats,'traffic':'Sorted independent uniform arrival times, conditional on exact request count; service and priority shuffled independently. Exactly matched per-phase seed and plan digest across policies. Fresh UUIDs for isolated writes excluded from plan identity. Service mix60/20/20,80%critical except background-flood20%; no retries.100ms useful budget includes scheduling, HTTP, queue and execution. SQLtimeout1s; client3s except deliberate20ms deadline.','configuration':'Ceiling4 and SQL pool4 per service. Stable adaptive starts4/floor2; reservation1. Fixed reservations0,1,2,3 and per-service orders2/catalog1/inventory1 are predeclared. Other Gatewise policies disabled; proxy adds fourth application process/hop.','selection':'Criteria and all17cases frozen before confirmatory matrix. Exploratory pilot is excluded; no win gate. Seeds101/202/303 are matched across all cases; rotated case order.'},'criteria':CRITERIA,'runs':[]}
 def save():a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(report,indent=2)+'\n')
 with Stack(a.projects,a.engine) as s:
  s.build();s.start_db();report['methodology'].update(postgres=s.db_version,image_digest=s.image_digest,container_resources=container_resources(a.engine))
  for module in ['gatewise','commerce-backend']:
   files={str(f.relative_to(s.projects/module)):hashlib.sha256(f.read_bytes()).hexdigest() for f in (s.projects/module).rglob('*') if f.is_file() and (f.suffix=='.go' or f.name in ['go.mod','go.sum']) and 'tests' not in f.parts};report['methodology'][module+'_source_sha256']=hashlib.sha256(json.dumps(files,sort_keys=True).encode()).hexdigest()
  report['methodology']['harness_sha256']=hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest()
  cases=[c for c in CASES if not a.cases or c['id'] in a.cases]
  for repeat in range(1,a.repeats+1):
   shift=(repeat-1)*5%len(cases)
   for case in cases[shift:]+cases[:shift]:report['runs'].append(one(s,case,repeat));save()
 pairs=[]
 for repeat in range(1,a.repeats+1):
  selected=[r for r in report['runs'] if r['repeat']==repeat]
  for name,*_ in PHASES:
   fingerprints={next(p for p in r['phases'] if p['name']==name)['traffic_plan_sha256'] for r in selected};pairs.append({'repeat':repeat,'phase':name,'pass':len(fingerprints)==1})
 report.update(pairing_checks=pairs,rows=analyze(report['runs']),finished_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),passed=all(r['passed'] for r in report['runs']) and all(c['pass'] for c in pairs));save();print('PASS' if report['passed'] else 'FAIL',flush=True)
 if not report['passed']:raise SystemExit(1)
