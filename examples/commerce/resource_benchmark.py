#!/usr/bin/env python3
"""Exercise actual runtime pressure, planned SQL writes and hot keys with unrelated traffic."""
import argparse,datetime,hashlib,json,pathlib,platform,subprocess,threading,time,uuid
from run import ROOT,Stack,api
from benchmark import metrics,metric
PROFILES=['middleware-fixed','proxy-fixed','middleware-adaptive','proxy-adaptive']
PHASES=[('healthy','healthy',1,20,20),('memory','allocation',2,240,120),('goroutines','fanout',2,240,120),('writes','write',3,240,75),('hot-keys','key_write',3,240,75),('recovery','healthy',1,20,20)]
def one(s,profile,strategy,repeat):
 s.start_app(profile,resources=strategy);trace=[];errors=[];stop=threading.Event();phase=['startup'];started=time.monotonic()
 def sample():
  while not stop.is_set():
   try:
    values={}
    for u in s.metrics_urls:values.update(metrics(u))
    for role,u in s.admins.items():
     ordinary=api(u,'/state');resource=api(u,'/resource/state');v={k:n for k,n in values.items() if 'service="'+role+'"' in k}
     trace.append({'seconds':time.monotonic()-started,'phase':phase[0],'service':role,'baseline_limit':metric(v,'gatewise_concurrency_limit'),'active':sum(n for k,n in v.items() if k.startswith('gatewise_requests_in_flight{')),'resource':resource,'pool_wait_seconds':ordinary['pool']['WaitDuration']/1e9,'pool_wait_count':ordinary['pool']['WaitCount'],'signal_states':{k:n for k,n in v.items() if k.startswith('gatewise_signal_state{') and n},'signal_proposals':{k:n for k,n in v.items() if k.startswith('gatewise_signal_proposals_total{')},'budgets':{k:n for k,n in v.items() if k.startswith('gatewise_resource_')},'source_failures':{k:n for k,n in v.items() if k.startswith('gatewise_signal_collection_failures_total{') and n}})
   except Exception as e:errors.append(type(e).__name__)
   stop.wait(.1)
 worker=threading.Thread(target=sample,daemon=True);worker.start();phases=[]
 try:
  for name,work,duration,rate,hold in PHASES:
   # Release unused runtime pages explicitly between phases, equally for every
   # strategy. This is fixture cleanup, not an automatic recovery guarantee.
   for u in s.admins.values():api(u,'/resource/control',{'hold_ms':hold,'collect':True,'reset_peaks':True},'PUT')
   time.sleep(.2);phase[0]=name;before={r:api(u,'/state')['pool'] for r,u in s.admins.items()}
   cfg={'urls':s.urls,'seconds':duration,'start_rps':rate,'end_rps':rate,'business_slots':5,'resource_work':work,'run_id':uuid.uuid4().hex[:12]}
   out=subprocess.run([str(s.projects/'commerce-backend/bin/loadgen')],input=json.dumps(cfg),capture_output=True,text=True,check=True);result=json.loads(out.stdout)
   deadline=time.monotonic()+3;states={r:api(u,'/resource/state') for r,u in s.admins.items()};after={r:api(u,'/state')['pool'] for r,u in s.admins.items()}
   while (any(v['InUse'] for v in after.values()) or any(v['allocation_live_bytes'] or v['workers_active'] or v['write_bytes_active'] for v in states.values())) and time.monotonic()<deadline:
    time.sleep(.02);states={r:api(u,'/resource/state') for r,u in s.admins.items()};after={r:api(u,'/state')['pool'] for r,u in s.admins.items()}
   result.update(name=name,work=work,offered_rps=rate,hold_ms=hold,resource_after=states,pools={r:{'wait_count':after[r]['WaitCount']-before[r]['WaitCount'],'wait_seconds':(after[r]['WaitDuration']-before[r]['WaitDuration'])/1e9,'in_use_after_drain':after[r]['InUse']} for r in after});phases.append(result)
   groups=result['tenant_traffic'];print(profile,strategy,repeat,name,result['sent'],'probe',groups.get('probe',{}).get('successful'),'pressure',groups.get('pressure',{}).get('successful'),flush=True)
  checks=[{'name':'scheduled arrivals delivered','pass':all(p['sent']==p['scheduled'] and not p['dropped_schedules'] for p in phases)},{'name':'no transport errors','pass':all(not v['transport_errors'] for p in phases for v in p['traffic'].values())},{'name':'known statuses and rejection reasons','pass':all(not v['other_statuses'] and not v['rejection_reasons'].get('other_503',0) for p in phases for v in p['traffic'].values())},{'name':'generator dispatch p95 below 20ms','pass':all((v['dispatch_p95_ms'] or 0)<20 for p in phases for v in p['traffic'].values())},{'name':'work and database connections drain','pass':all(not v['in_use_after_drain'] for p in phases for v in p['pools'].values()) and all(not v['allocation_live_bytes'] and not v['workers_active'] and not v['write_bytes_active'] and not v['active_keys'] for p in phases for v in p['resource_after'].values())},{'name':'healthy work and recovery have no false-positive rejection/error','pass':all(not v['rejected_503'] and not v['backend_5xx'] for p in [phases[0],phases[-1]] for v in p['traffic'].values())},{'name':'unrelated probes make progress in every phase','pass':all(p['tenant_traffic']['probe']['successful']>0 for p in phases)},{'name':'monitoring available','pass':not errors}]
  if strategy=='enforce':
   checks += [{'name':'concurrent planned bytes remain within budget','pass':all(v['resource']['write_bytes_peak']<=262144 for v in trace if v['phase']=='writes') and all(v['write_bytes_peak']<=262144 for v in phases[3]['resource_after'].values())},{'name':'each hot key admits at most one concurrent operation','pass':all(v['key_peak_concurrent']<=1 for v in phases[4]['resource_after'].values())},{'name':'key tracking remains bounded','pass':all(n<=32 for v in trace for k,n in v['budgets'].items() if k.startswith('gatewise_resource_keys_tracked{'))},{'name':'real memory pressure observed','pass':any('signal="memory"' in k and 'state="pressure"' in k for v in trace if v['phase']=='memory' for k in v['signal_states'])},{'name':'real goroutine pressure observed','pass':any('signal="goroutines"' in k and 'state="pressure"' in k for v in trace if v['phase']=='goroutines' for k in v['signal_states'])},{'name':'write budget actually sheds work','pass':sum(v['rejection_reasons'].get('write_bytes_limit',0) for v in phases[3]['traffic'].values())>0},{'name':'key budget actually sheds work','pass':sum(v['rejection_reasons'].get('hot_key_limit',0) for v in phases[4]['traffic'].values())>0}]
  else:checks += [{'name':'no resource policy enforcement','pass':not any(n for p in phases for v in p['traffic'].values() for k,n in v['rejection_reasons'].items() if k.startswith(('signal_','write_bytes_','hot_key_','resource_')))}]
  return {'profile':profile,'strategy':strategy,'repeat':repeat,'phases':phases,'trace':trace,'sampling_errors':errors,'checks':checks,'passed':all(c['pass'] for c in checks)}
 finally:stop.set();worker.join(timeout=5);s.stop_app()
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default='podman');p.add_argument('--repeats',type=int,default=3);p.add_argument('--profiles',nargs='+',choices=PROFILES,default=PROFILES);p.add_argument('--strategies',nargs='+',choices=['disabled','observe','enforce'],default=['disabled','observe','enforce']);p.add_argument('--output',type=pathlib.Path,default=ROOT/'results/resource-results.json');a=p.parse_args()
 if not 1<=a.repeats<=10:p.error('repeats must be 1..10')
 report={'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'methodology':{'scope':'One local host, three real services and PostgreSQL; actual Go managed-memory/goroutine samples, logical byte reservations and key contention. No production capacity or OOM-prevention guarantee.','hardware':platform.platform(),'phases':PHASES,'mix':'80% pressure operations and 20% unrelated probe work; all routes critical. For hot keys, probes write 16 rotating cold keys. Other probes perform ordinary database reads. Orders receives 60%, catalog/inventory 20% each.','memory':'8MiB per pressure request, 128MiB Go-managed budget, threshold .65, 50ms sampling, cap2; runtime/OS pages explicitly collected between phases for fixture cleanup','goroutines':'32 child goroutines per pressure request, count threshold180, 50ms sampling, cap2; count is not a CPU/scheduler measurement','writes':'128KiB actual persisted value per operation; 256KiB concurrent planned-byte budget; cost computed from validated plan, not request body length','keys':'16KiB persisted writes; one hot key, 16 cold keys; per-key cap1, registry32, TTL2s, overflow shared','measurement':'Open-loop arrivals, no retries, 3s clients, 4096 generator slots; runtime pressure is sampled and can overshoot/oscillate. TTL measured separately.','repeats':a.repeats,'application_processes_max':4},'runs':[]}
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
