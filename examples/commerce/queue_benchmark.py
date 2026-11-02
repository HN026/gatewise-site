#!/usr/bin/env python3
"""Compare fail-fast admission with bounded waiting using real PostgreSQL."""
import argparse,datetime,hashlib,json,pathlib,platform,statistics,subprocess,threading,time,uuid
from run import ROOT,Stack,api
from benchmark import metrics
PROFILES=['middleware-fixed','proxy-fixed','middleware-adaptive','proxy-adaptive']
QUEUE={'max_waiting':16,'max_wait':'40ms','aging_after':'20ms','reserved':{'critical':4}}
# name, seconds, RPS, critical slots of five, SQL delay ms, client timeout ms
PHASES=[('healthy',1,120,4,0,3000),('warmup',1,120,4,10,3000)]
for i in range(3):PHASES += [(f'burst-{i}',.1,1800,4,10,3000),(f'relief-{i}',.5,60,4,10,3000)]
PHASES += [('sustained',2,1800,4,10,3000),('slowdown',2.5,600,4,120,3000),('caller-deadline',.8,1000,4,80,20),('recovery',2,1200,4,0,3000)]
CRITERIA={'burst_useful_ratio_min':1.1,'burst_successful_p95_ms_max':100,'sustained_useful_ratio_min':.95,'recovery_useful_ratio_min':.95,'nondeadline_extra_server_errors':0,'interpretation':'Median run-level outcomes for each deployment; useful work includes queue and scheduling delay. Short-burst improvement is tested separately from sustained overload. Caller-deadline failures are deliberately induced and shown, not silently removed.'}
def values(s):return {k:v for url in s.metrics_urls for k,v in metrics(url).items()}
def get(v,prefix,match=''):return sum(n for k,n in v.items() if k.startswith(prefix+'{') and match in k)
def state(v,role):
 match='service="'+role+'"';v={k:n for k,n in v.items() if match in k}
 return {'limit':get(v,'gatewise_concurrency_limit'),'active':get(v,'gatewise_requests_in_flight'),'waiting':get(v,'gatewise_queue_waiting'),'high_water':get(v,'gatewise_queue_high_watermark'),'capacity':get(v,'gatewise_queue_capacity'),'queue_events':{event:get(v,'gatewise_queue_events_total','event="'+event+'"') for event in ['enqueued','admitted','full','reserved','timeout','deadline','canceled','policy_rejected']}}
def drain(s):
 end=time.monotonic()+3
 while time.monotonic()<end:
  v=values(s);pools={r:api(u,'/state')['pool'] for r,u in s.admins.items()}
  if all(not p['InUse'] for p in pools.values()) and all(not state(v,r)['active'] and not state(v,r)['waiting'] for r in s.admins):return pools
  time.sleep(.01)
 raise RuntimeError('Connections, execution permits or waiters did not drain')
def one(s,profile,policy,repeat,phases=PHASES):
 s.start_app(profile,adaptive_settings={'profile':'stable','initial_concurrent':4},queue_settings=QUEUE if policy=='queue' else None,execution_limit=4,reserved=1,route_budgets=True)
 trace=[];errors=[];stop=threading.Event();current=['startup'];started=time.monotonic();results=[]
 def sample():
  while not stop.is_set():
   try:
    v=values(s)
    for role,url in s.admins.items():trace.append({'seconds':time.monotonic()-started,'phase':current[0],'service':role,**state(v,role),'pool':api(url,'/state')['pool']})
   except Exception as e:errors.append(type(e).__name__)
   stop.wait(.025)
 thread=threading.Thread(target=sample,daemon=True);thread.start()
 try:
  for name,seconds,rps,slots,delay,timeout in phases:
   s.control(1,delay,0);before=values(s);pools=drain(s);current[0]=name;at=time.monotonic()-started
   cfg={'urls':s.urls,'seconds':seconds,'start_rps':rps,'end_rps':rps,'business_slots':slots,'client_timeout_ms':timeout,'run_id':uuid.uuid4().hex}
   p=subprocess.run([str(s.projects/'commerce-backend/bin/loadgen')],input=json.dumps(cfg),capture_output=True,text=True,check=True);r=json.loads(p.stdout);after_pools=drain(s);after=values(s)
   r.update(name=name,seconds=seconds,rps=rps,sql_delay_ms=delay,client_timeout_ms=timeout,started=at,pool_wait_count=sum(after_pools[k]['WaitCount']-pools[k]['WaitCount'] for k in pools),pool_wait_seconds=sum(after_pools[k]['WaitDuration']-pools[k]['WaitDuration'] for k in pools)/1e9,queue_events={event:get(after,'gatewise_queue_events_total','event="'+event+'"')-get(before,'gatewise_queue_events_total','event="'+event+'"') for event in ['enqueued','admitted','full','reserved','timeout','deadline','canceled','policy_rejected']});results.append(r)
   t=r['traffic']['critical'];print(profile,policy,repeat,name,'timely',round(t['successful_within_100ms_from_schedule_per_offered_second'],2),'p95',t['successful_p95_ms'],'reject',t['rejected_503'],'deadline',t['client_deadline_errors'],flush=True)
  checks=[{'name':'all scheduled requests delivered','pass':all(r['sent']==r['scheduled'] and not r['dropped_schedules'] for r in results)},{'name':'only intentional deadline transport failures','pass':all(t['transport_errors']==t['client_deadline_errors'] and (r['name']=='caller-deadline' or not t['transport_errors']) and not t['other_statuses'] for r in results for t in r['traffic'].values())},{'name':'generator dispatch p95 under20ms','pass':all((t['dispatch_p95_ms'] or 0)<20 for r in results for t in r['traffic'].values())},{'name':'execution and adaptive bounds retained','pass':all((2 if profile.endswith('adaptive') else 4)<=p['limit']<=4 and p['active']<=4 for p in trace)},{'name':'queue count bounded','pass':all(0<=p['waiting']<=16 and p['high_water']<=16 for p in trace)},{'name':'monitoring available','pass':not errors},{'name':'pool wait and background outcomes retained','pass':all('background' in r['traffic'] and r['pool_wait_seconds']>=0 for r in results)}]
  if policy=='queue':checks += [{'name':'queue exercised and deadline cancellation observed','pass':sum(r['queue_events']['admitted'] for r in results)>0 and sum(r['queue_events']['canceled'] for r in results)>0},{'name':'queued lifecycle counts balance after drain','pass':all(r['queue_events']['enqueued']==sum(r['queue_events'][event] for event in ['admitted','timeout','deadline','canceled','policy_rejected']) for r in results)}]
  else:checks.append({'name':'queue disabled by default','pass':all(not p['waiting'] and not p['capacity'] for p in trace)})
  return {'profile':profile,'policy':policy,'repeat':repeat,'phases':results,'trace':trace,'checks':checks,'passed':all(c['pass'] for c in checks),'sampling_errors':errors}
 finally:stop.set();thread.join(5);s.stop_app()
def median(v):
 v=[x for x in v if x is not None];return statistics.median(v) if v else None
def analyze(runs):
 rows=[];groups=[];checks=[]
 for profile in sorted({r['profile'] for r in runs}):
  for policy in ['fail-fast','queue']:
   selected=[r for r in runs if r['profile']==profile and r['policy']==policy]
   if not selected:continue
   burst=[sum(p['traffic']['critical']['successful_within_100ms_from_schedule_per_offered_second']*p['seconds'] for p in r['phases'] if p['name'].startswith('burst-')) for r in selected]
   groups.append({'profile':profile,'policy':policy,'burst_useful':median(burst),'burst_useful_repeats':burst,'burst_p95_ms':median(max(p['traffic']['critical']['successful_p95_ms'] or 0 for p in r['phases'] if p['name'].startswith('burst-')) for r in selected)})
   for name in [p['name'] for p in selected[0]['phases']]:
    phases=[next(p for p in r['phases'] if p['name']==name) for r in selected]
    for priority in ['critical','background']:
     traffic=[p['traffic'][priority] for p in phases];sent=sum(t['sent'] for t in traffic);waits=sum(p['pool_wait_count'] for p in phases)
     rows.append({'profile':profile,'policy':policy,'phase':name,'priority':priority,'sent':sent,'success_rps':median(t['successful']/p['seconds'] for t,p in zip(traffic,phases)),'timely_rps':median(t['successful_within_100ms_from_schedule_per_offered_second'] for t in traffic),'p95_ms':median(t['successful_p95_ms'] for t in traffic),'queue_p95_ms':median(t['admitted_queue_wait_p95_ms'] for t in traffic),'rejected_percent':sum(t['rejected_503'] for t in traffic)/sent*100 if sent else 0,'deadline_percent':sum(t['client_deadline_errors'] for t in traffic)/sent*100 if sent else 0,'errors':sum(t['backend_5xx'] for t in traffic),'pool_mean_wait_ms':sum(p['pool_wait_seconds'] for p in phases)/waits*1000 if waits else 0})
  if not all(any(g['profile']==profile and g['policy']==p for g in groups) for p in ['fail-fast','queue']):continue
  a=next(g for g in groups if g['profile']==profile and g['policy']=='fail-fast');b=next(g for g in groups if g['profile']==profile and g['policy']=='queue')
  def check(name,ok):checks.append({'profile':profile,'name':name,'pass':bool(ok)})
  check('short bursts improve useful critical completions at least10%',b['burst_useful']>=1.1*a['burst_useful'])
  check('short burst successful critical p95 within100ms',b['burst_p95_ms']<=100)
  for name in ['sustained','recovery']:
   old=next(r for r in rows if r['profile']==profile and r['policy']=='fail-fast' and r['phase']==name and r['priority']=='critical');new=next(r for r in rows if r['profile']==profile and r['policy']=='queue' and r['phase']==name and r['priority']=='critical')
   check(name+' useful critical work retained95%',new['timely_rps']>=.95*old['timely_rps']);check(name+' no extra critical server errors',new['errors']<=old['errors'])
 return {'rows':rows,'groups':groups,'comparison_checks':checks,'criteria':CRITERIA}
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default='podman');p.add_argument('--profiles',nargs='+',choices=PROFILES,default=PROFILES);p.add_argument('--repeats',type=int,default=3);p.add_argument('--output',type=pathlib.Path,default=ROOT/'results/queue-results.json');a=p.parse_args()
 report={'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'methodology':{'platform':platform.platform(),'scope':'Local real-PostgreSQL comparison, not production capacity or a starvation guarantee.','queue':QUEUE,'execution':{'maximum':4,'reserved_critical':1,'adaptive_minimum':2,'adaptive_initial':4,'adaptive_profile':'stable','pool_connections':4,'processing_budget_ms':100,'report_budget_ms':500},'phases':PHASES,'traffic':'Open-loop, no retries, 60/20/20 services, 80% critical. Responses and all execution/waiters/SQL drain between phases; controller state survives. Client20ms deadline phase intentionally produces deadline errors. Useful critical responses must finish within100ms of schedule including queue and dispatch.','selection':'Exploratory runs may inform implementation; this report records a subsequent fixed-code repeated matrix.','repeats':a.repeats},'criteria':CRITERIA,'runs':[]}
 def save():a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(report,indent=2)+'\n')
 with Stack(a.projects,a.engine) as s:
  s.build();s.start_db();files={str(p.relative_to(s.projects/'gatewise')):hashlib.sha256(p.read_bytes()).hexdigest() for p in (s.projects/'gatewise').rglob('*') if p.is_file() and (p.suffix=='.go' or p.name in ('go.mod','go.sum')) and 'tests' not in p.parts};report['methodology'].update(runtime_source_sha256=hashlib.sha256(json.dumps(files,sort_keys=True).encode()).hexdigest(),postgres=s.db_version,image_digest=s.image_digest,binaries_sha256={k:hashlib.sha256((s.projects/v).read_bytes()).hexdigest() for k,v in [('commerce','commerce-backend/bin/commerce-backend'),('proxy','gatewise/bin/gatewise'),('loadgen','commerce-backend/bin/loadgen')]})
  cases=[(profile,policy) for profile in a.profiles for policy in ['fail-fast','queue']]
  for repeat in range(1,a.repeats+1):
   shift=(repeat-1)%len(cases)
   for profile,policy in cases[shift:]+cases[:shift]:report['runs'].append(one(s,profile,policy,repeat));save()
 report.update(analyze(report['runs']));report.update(finished_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),passed=all(r['passed'] for r in report['runs']) and all(c['pass'] for c in report['comparison_checks']));save();print('PASS' if report['passed'] else 'FAIL',flush=True)
 if not report['passed']:raise SystemExit(1)
if __name__=='__main__':main()
