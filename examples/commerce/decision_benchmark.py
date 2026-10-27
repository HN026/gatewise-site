#!/usr/bin/env python3
"""Compare cached signal enforcement with observe/disabled modes against real PostgreSQL."""
import argparse,datetime,hashlib,json,pathlib,platform,subprocess,threading,time,uuid
from run import ROOT,Stack,api
from benchmark import metrics,metric
PROFILES=['middleware-fixed','proxy-fixed','middleware-adaptive','proxy-adaptive']
# Controlled observations deliberately separate engine delivery/combination from sensor accuracy.
PHASES=[('healthy',1.5,60,0,.2,False,False),('pressure',2,600,40,.95,False,False),('conflict',1,120,0,.2,True,False),('unavailable',1.5,600,40,.2,False,True),('recovery',1.5,60,0,.2,False,False)]
def one(s,profile,strategy,repeat):
 s.start_app(profile,decisions='' if strategy=='disabled' else strategy);trace=[];errors=[];stop=threading.Event();phase=['startup'];started=time.monotonic()
 def sample():
  while not stop.is_set():
   try:
    values={}
    for u in s.metrics_urls:values.update(metrics(u))
    for role,u in s.admins.items():
     state=api(u,'/state');v={k:n for k,n in values.items() if 'service="'+role+'"' in k}
     trace.append({'seconds':time.monotonic()-started,'phase':phase[0],'service':role,'baseline_limit':metric(v,'gatewise_concurrency_limit'),'active':sum(n for k,n in v.items() if k.startswith('gatewise_requests_in_flight{')),'pool_wait_count':state['pool']['WaitCount'],'pool_wait_seconds':state['pool']['WaitDuration']/1e9,'signal_states':{k:n for k,n in v.items() if k.startswith('gatewise_signal_state{') and n},'proposals':{k:n for k,n in v.items() if k.startswith('gatewise_signal_proposals_total{')}})
   except Exception as e:errors.append(type(e).__name__)
   stop.wait(.2)
 worker=threading.Thread(target=sample,daemon=True);worker.start();phases=[]
 try:
  for name,duration,rate,delay,value,secondary,failed in PHASES:
   s.control(delay=delay)
   if strategy!='disabled':
    for u in s.admins.values():
     api(u,'/signals/primary',{'value':value,'state':'measured','error':failed},'PUT');api(u,'/signals/secondary',{'value':.95 if secondary else .2,'state':'measured'},'PUT')
   time.sleep(.35);phase[0]=name;before={r:api(u,'/state')['pool'] for r,u in s.admins.items()}
   cfg={'urls':s.urls,'seconds':duration,'start_rps':rate,'end_rps':rate,'business_slots':5,'run_id':uuid.uuid4().hex[:12]}
   out=subprocess.run([str(s.projects/'commerce-backend/bin/loadgen')],input=json.dumps(cfg),capture_output=True,text=True,check=True);result=json.loads(out.stdout)
   deadline=time.monotonic()+2;after={r:api(u,'/state')['pool'] for r,u in s.admins.items()}
   while any(v['InUse'] for v in after.values()) and time.monotonic()<deadline:time.sleep(.02);after={r:api(u,'/state')['pool'] for r,u in s.admins.items()}
   result.update(name=name,offered_rps=rate,sql_delay_ms=delay,pools={r:{'wait_count':after[r]['WaitCount']-before[r]['WaitCount'],'wait_seconds':(after[r]['WaitDuration']-before[r]['WaitDuration'])/1e9,'in_use_after_drain':after[r]['InUse']} for r in after});phases.append(result)
   print(profile,strategy,repeat,name,result['sent'],sum(v['successful'] for v in result['traffic'].values()),flush=True)
  checks=[{'name':'scheduled arrivals delivered','pass':all(p['sent']==p['scheduled'] and not p['dropped_schedules'] for p in phases)}, {'name':'no transport errors','pass':all(not v['transport_errors'] for p in phases for v in p['traffic'].values())},{'name':'known statuses and rejection reasons','pass':all(not v['other_statuses'] and not v['rejection_reasons'].get('other_503',0) for p in phases for v in p['traffic'].values())},{'name':'dispatch p95 below 10ms','pass':all((v['dispatch_p95_ms'] or 0)<10 for p in phases for v in p['traffic'].values())},{'name':'SQL connections drained','pass':all(v['in_use_after_drain']==0 for p in phases for v in p['pools'].values())},{'name':'baseline service bounds retained','pass':all(0<=v['active']<=12 for v in trace)},{'name':'healthy recovery has no rejections or errors','pass':all(not v['rejected_503'] and not v['backend_5xx'] for v in phases[-1]['traffic'].values())},{'name':'monitoring available','pass':not errors}]
  if strategy=='enforce':
   checks += [{'name':'reject overrides healthy source','pass':all(v['successful']==0 and v['rejection_reasons'].get('signal_pressure',0)==v['sent'] for v in phases[2]['traffic'].values())},{'name':'cap restricts overloaded work','pass':sum(v['rejection_reasons'].get('signal_concurrency_limit',0) for v in phases[1]['traffic'].values())>0},{'name':'failed source fallback restricts work','pass':sum(v['rejection_reasons'].get('signal_concurrency_limit',0) for v in phases[3]['traffic'].values())>0}]
  else:checks += [{'name':'no signal enforcement','pass':not any(n for p in phases for v in p['traffic'].values() for k,n in v['rejection_reasons'].items() if k.startswith('signal_'))}]
  return {'profile':profile,'strategy':strategy,'repeat':repeat,'phases':phases,'trace':trace,'sampling_errors':errors,'checks':checks,'passed':all(c['pass'] for c in checks)}
 finally:stop.set();worker.join(timeout=5);s.stop_app()
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default='podman');p.add_argument('--repeats',type=int,default=3);p.add_argument('--profiles',nargs='+',choices=PROFILES,default=PROFILES);p.add_argument('--strategies',nargs='+',choices=['disabled','observe','enforce'],default=['disabled','observe','enforce']);p.add_argument('--output',type=pathlib.Path,default=ROOT/'results/decision-results.json');a=p.parse_args()
 if not 1<=a.repeats<=10:p.error('repeats must be 1..10')
 report={'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'methodology':{'scope':'Local real PostgreSQL services with controlled pressure providers; this validates decision combination and delivery, not automatic resource detection or production capacity','hardware':platform.platform(),'phases':PHASES,'mix':'All critical, 60% orders / 20% catalog / 20% inventory; four SQL connections per service; no retries; open-loop scheduled arrivals','signal_policy':'Threshold .8, max age 600ms, primary cap4/unknown cap2, secondary reject/unknown allow; poll100ms deadline50ms; settle350ms before counted load','repeats':a.repeats,'application_processes_max':4},'runs':[]}
 with Stack(a.projects,a.engine) as s:
  s.build();s.start_db();report['methodology'].update(postgres=s.db_version,image_digest=s.image_digest,go_version=subprocess.check_output(['go','version'],text=True).strip(),loadgen_sha256=hashlib.sha256((s.projects/'commerce-backend/bin/loadgen').read_bytes()).hexdigest(),commerce_binary_sha256=hashlib.sha256((s.projects/'commerce-backend/bin/commerce-backend').read_bytes()).hexdigest(),proxy_binary_sha256=hashlib.sha256((s.projects/'gatewise/bin/gatewise').read_bytes()).hexdigest())
  variants=[(profile,strategy) for profile in a.profiles for strategy in a.strategies]
  for repeat in range(1,a.repeats+1):
   shift=(repeat-1)%len(variants)
   for profile,strategy in variants[shift:]+variants[:shift]:
    report['runs'].append(one(s,profile,strategy,repeat));a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(report,indent=2)+'\n')
 report.update(finished_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),passed=all(r['passed'] for r in report['runs']));a.output.write_text(json.dumps(report,indent=2)+'\n');print('PASS' if report['passed'] else 'FAIL',a.output,flush=True)
 if not report['passed']:raise SystemExit(1)
if __name__=='__main__':main()
