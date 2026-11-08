#!/usr/bin/env python3
"""Controller evaluation against real SQL: defaults and the retained tuned baseline."""
import argparse,datetime,hashlib,json,pathlib,platform,statistics,subprocess,threading,time,uuid
from run import ROOT,Stack,api,request
from benchmark import metrics,metric
PROFILES=['middleware-adaptive','proxy-adaptive']
SETTINGS={'legacy-tuned':{'min_concurrent':5,'initial_concurrent':12,'interval':'500ms','min_samples':10,'target_latency':'100ms'},'legacy-defaults':{},'stable-defaults':{'profile':'stable'}}
# name, duration, RPS, critical slots of five, report multiplier, SQL delay ms, everyNth failure, signal
PHASES=[('healthy',2,600,4,1,0,0,0),('capacity-loss',3,600,4,1,60,0,0),('loss-recovery',5,1500,4,1,0,0,0),('gradual-10',2,600,4,1,10,0,0),('gradual-25',2,600,4,1,25,0,0),('gradual-45',2,600,4,1,45,0,0),('gradual-recovery',3,1500,4,1,0,0,0)]
for i in range(3):PHASES += [(f'periodic-pressure-{i}',1.2,600,4,1,45,0,0),(f'periodic-relief-{i}',1.3,1500,4,1,0,0,0)]
PHASES += [('intermittent-errors',2,600,4,1,0,4,0),('error-recovery',5,1500,4,1,0,0,0),('low-traffic',5,4,4,1,0,0,0),('idle',5,0,4,1,0,0,0),('idle-restart',3,600,4,1,0,0,0),('mixed-cost',3,900,1,20,10,0,0),('mixed-recovery',4,1500,4,1,0,0,0),('signal-cap',2,600,4,1,0,0,1),('signal-recovery',3,1500,4,1,0,0,0)]
def one(s,profile,variant,repeat,phases=PHASES):
 s.start_app(profile,decisions='enforce',adaptive_settings=SETTINGS[variant],route_budgets=True)
 trace=[];errors=[];stop=threading.Event();phase=['startup'];started=time.monotonic();results=[]
 def sample():
  while not stop.is_set():
   try:
    values={}
    for url in s.metrics_urls:values.update(metrics(url))
    for role,url in s.admins.items():
     state=api(url,'/state');v={k:n for k,n in values.items() if 'service="'+role+'"' in k}
     trace.append({'seconds':time.monotonic()-started,'phase':phase[0],'service':role,'pool':state['pool'],'limit':metric(v,'gatewise_concurrency_limit'),'active':sum(n for k,n in v.items() if k.startswith('gatewise_requests_in_flight{')),'evaluation_at':metric(v,'gatewise_controller_last_evaluation_timestamp_seconds'),'samples':metric(v,'gatewise_controller_last_window_samples'),'p90_ratio':metric(v,'gatewise_controller_last_p90_ratio'),'failure_fraction':metric(v,'gatewise_controller_last_failure_fraction'),'decisions':{k:n for k,n in v.items() if k.startswith('gatewise_controller_last_decision{') and n},'adjustments':{k:n for k,n in v.items() if k.startswith('gatewise_limit_adjustments_total{')},'extended':{k:n for k,n in v.items() if k.startswith(('gatewise_controller_smoothed','gatewise_controller_pending','gatewise_controller_sparse','gatewise_controller_external','gatewise_controller_window','gatewise_controller_info','gatewise_controller_control'))},'backend_controller':state.get('controller')})
   except Exception as e:errors.append(type(e).__name__)
   stop.wait(.1)
 thread=threading.Thread(target=sample,daemon=True);thread.start()
 try:
  for name,seconds,rps,slots,mult,delay,failures,signal in phases:
   s.control(mult,delay,failures)
   for url in s.admins.values():
    api(url,'/signals/primary',{'state':'measured','value':signal},'PUT');api(url,'/signals/secondary',{'state':'measured','value':0},'PUT')
   before_changes={k:n for url in s.metrics_urls for k,n in metrics(url).items() if k.startswith('gatewise_limit_adjustments_total{')}
   phase[0]=name;at=time.monotonic()-started;before={r:api(u,'/state')['pool'] for r,u in s.admins.items()}
   if not rps:stop.wait(seconds);results.append({'name':name,'seconds':seconds,'rps':0,'started':at});continue
   cfg={'urls':s.urls,'seconds':seconds,'start_rps':rps,'end_rps':rps,'business_slots':slots,'run_id':uuid.uuid4().hex};result=json.loads(subprocess.run([str(s.projects/'commerce-backend/bin/loadgen')],input=json.dumps(cfg),capture_output=True,text=True,check=True).stdout)
   end=time.monotonic()+2
   while any(api(u,'/state')['pool']['InUse'] for u in s.admins.values()) and time.monotonic()<end:time.sleep(.02)
   after_changes={k:n for url in s.metrics_urls for k,n in metrics(url).items() if k.startswith('gatewise_limit_adjustments_total{')}
   after={r:api(u,'/state')['pool'] for r,u in s.admins.items()};result.update(adjustments={k:v-before_changes.get(k,0) for k,v in after_changes.items()},name=name,seconds=seconds,rps=rps,started=at,delay_ms=delay,failure_every=failures,signal=signal,pool_wait_seconds=sum(after[r]['WaitDuration']-before[r]['WaitDuration'] for r in after)/1e9,pool_wait_count=sum(after[r]['WaitCount']-before[r]['WaitCount'] for r in after),pool_in_use_after=sum(p['InUse'] for p in after.values()));results.append(result)
   t=result['traffic']['critical'];print(profile,variant,repeat,name,'timely',round(t['successful_within_100ms_per_offered_second'],2),'p95',t['successful_p95_ms'],'reject',t['rejected_503'],flush=True)
  checks=[{'name':'scheduled arrivals all delivered','pass':all(r['sent']==r['scheduled'] and not r['dropped_schedules'] for r in results if r['rps'])},{'name':'no transport or unknown statuses','pass':all(not t['transport_errors'] and not t['other_statuses'] for r in results if r['rps'] for t in r['traffic'].values())},{'name':'generator dispatch p95 below20ms','pass':all((t['dispatch_p95_ms'] or 0)<20 for r in results if r['rps'] for t in r['traffic'].values())},{'name':'SQL pools drain after every phase','pass':all(not r['pool_in_use_after'] for r in results if r['rps'])},{'name':'adaptive floor and ceiling respected','pass':all(5<=t['limit']<=12 and t['active']<=12 for t in trace)},{'name':'monitoring available','pass':not errors},{'name':'low traffic has no admission rejection','pass':all(not t['rejected_503'] for r in results if r['name']=='low-traffic' for t in r['traffic'].values())}]
  return {'profile':profile,'variant':variant,'repeat':repeat,'phases':results,'trace':trace,'sampling_errors':errors,'checks':checks,'passed':all(c['pass'] for c in checks)}
 finally:stop.set();thread.join(5);s.stop_app()
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default='podman');p.add_argument('--profiles',nargs='+',choices=PROFILES,default=PROFILES);p.add_argument('--variants',nargs='+',choices=list(SETTINGS),default=list(SETTINGS));p.add_argument('--repeats',type=int,default=3);p.add_argument('--smoke',action='store_true');p.add_argument('--output',type=pathlib.Path,default=ROOT/'results/adaptation-results.json');a=p.parse_args()
 phases=PHASES[:3] if a.smoke else PHASES;report={'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'methodology':{'scope':'Local database-backed controller evaluation, not production capacity or a multi-host claim.','platform':platform.platform(),'settings':SETTINGS,'common':{'max':12,'reserved':4,'minimum_default':5,'critical_budget_ms':100,'report_budget_ms':500,'pool':4},'phases':phases,'signal':'Controlled global cap4 in final phase; source collector100ms, maxAge600ms. Tests coordination, not automatic detection.','measurement':'Open-loop Go arrivals; no retries; 3s client and1s SQL deadlines. Phase controls switch only after responses and SQL drain. No claim of independent per-scenario experiments.','repeats':a.repeats,'selection':'Exploratory pilot findings informed the candidate. Published comparisons use this fixed-code repeated matrix; legacy defaults are retained alongside the earlier tuned reference.','configuration_footprint':{'legacy-tuned':{'explicit_advanced_fields':4,'nondefault_advanced_fields':2,'profile_selection_fields':0},'legacy-defaults':{'explicit_advanced_fields':0,'nondefault_advanced_fields':0,'profile_selection_fields':0},'stable-defaults':{'explicit_advanced_fields':0,'nondefault_advanced_fields':0,'profile_selection_fields':1}}},'runs':[]}
 def save():a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(report,indent=2)+'\n')
 with Stack(a.projects,a.engine) as s:
  s.build();s.start_db();
  source_files={str(p.relative_to(s.projects/'gatewise')):hashlib.sha256(p.read_bytes()).hexdigest() for p in (s.projects/'gatewise').rglob('*') if p.is_file() and (p.suffix=='.go' or p.name in ('go.mod','go.sum')) and 'tests' not in p.parts}
  report['methodology']['runtime_source_sha256']=hashlib.sha256(json.dumps(source_files,sort_keys=True).encode()).hexdigest()
  report['methodology'].update(postgres=s.db_version,image_digest=s.image_digest,binaries_sha256={k:hashlib.sha256((s.projects/v).read_bytes()).hexdigest() for k,v in [('commerce','commerce-backend/bin/commerce-backend'),('proxy','gatewise/bin/gatewise'),('loadgen','commerce-backend/bin/loadgen')]})
  variants=[(p,v) for p in a.profiles for v in a.variants]
  for repeat in range(1,a.repeats+1):
   shift=(repeat-1)%len(variants)
   for profile,variant in variants[shift:]+variants[:shift]:report['runs'].append(one(s,profile,variant,repeat,phases));save()
 if not a.smoke:
  from controller_evaluation.analysis import analyze
  report.update(analyze(report["runs"]))
 report.update(finished_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),passed=all(r['passed'] for r in report['runs']) and all(c['pass'] for c in report.get('comparison_checks',[])));save();print('INVARIANTS PASS' if report['passed'] else 'FAIL',flush=True)
 if not report['passed']:raise SystemExit(1)
if __name__=='__main__':main()
