#!/usr/bin/env python3
"""Real SQL/worker contention and physical-standby fault validation. Local, not a production capacity claim."""
import argparse,datetime,hashlib,json,pathlib,platform,subprocess,threading,time,uuid
from run import ROOT,Stack,api,request
PROFILES=['middleware-fixed','proxy-fixed','middleware-adaptive','proxy-adaptive']
POLICIES=['disabled','fixed','pool']
def wait(fn,timeout=10):
 end=time.monotonic()+timeout
 while time.monotonic()<end:
  v=fn()
  if v:return v
  time.sleep(.05)
 raise RuntimeError('condition timed out')
def state(s):return api(s.admins['orders'],'/operations/state')
def control(s,enabled=False):return api(s.admins['orders'],'/operations/control',{'enabled':enabled,'delay_ms':120,'jobs':0},'PUT')
def drain(s):
 control(s)
 return wait(lambda: (v if not v['queue_length'] and not v['worker']['Active'] and not v['storage']['Active'] and not v['pool']['InUse'] else None) if (v:=state(s)) else None,5)
def load(s,seconds,rate):
 cfg={'urls':s.urls,'seconds':seconds,'start_rps':rate,'end_rps':rate,'business_slots':5,'operation_work':True,'run_id':uuid.uuid4().hex}
 return json.loads(subprocess.run([str(s.projects/'commerce-backend/bin/loadgen')],input=json.dumps(cfg),capture_output=True,text=True,check=True).stdout)
def one(s,profile,policy,repeat,phase_config=None):
 s.start_app(profile,operations=policy);trace=[];errors=[];stop=threading.Event();phase=['startup'];start=time.monotonic()
 def sample():
  while not stop.is_set():
   try:trace.append({'seconds':time.monotonic()-start,'phase':phase[0],**state(s)})
   except Exception as e:errors.append(type(e).__name__)
   stop.wait(.1)
 worker=threading.Thread(target=sample,daemon=True);worker.start();phases=[]
 try:
  for name,seconds,rate in (phase_config or [('healthy',1,30),('workers',3,120),('pool-exhaustion',3,120),('recovery',1,30)]):
   phase[0]=name
   if name=='workers':control(s,True)
   if name=='pool-exhaustion':
    drain(s);api(s.admins['orders'],'/operations/pool',{'count':3},'POST');control(s,True)
   if name=='light-interactive':
    control(s);api(s.admins['orders'],'/operations/pool',method='DELETE');drain(s);control(s,True)
   if name=='recovery':
    control(s);api(s.admins['orders'],'/operations/pool',method='DELETE');drain(s)
   before=state(s);result=load(s,seconds,rate)
   control(s)
   if name=='pool-exhaustion':api(s.admins['orders'],'/operations/pool',method='DELETE')
   after=drain(s)
   result.update(name=name,seconds=seconds,offered_rps=rate,before=before,after=after,pool_wait_count=after['pool']['WaitCount']-before['pool']['WaitCount'],pool_wait_seconds=(after['pool']['WaitDuration']-before['pool']['WaitDuration'])/1e9,jobs={k:after['jobs'][k]-before['jobs'][k] for k in after['jobs']})
   phases.append(result);print(profile,policy,repeat,name,result['traffic'],flush=True)
  checks=[{'name':'scheduled arrivals delivered','pass':all(p['sent']==p['scheduled'] and not p['dropped_schedules'] for p in phases)}, {'name':'no transport or unknown-status failures','pass':all(not t['transport_errors'] and not t['other_statuses'] and not t['rejection_reasons'].get('other_503',0) for p in phases for t in p['traffic'].values())},{'name':'healthy and recovery successful without rejection','pass':all(t['successful']==t['sent'] for p in [phases[0],phases[-1]] for t in p['traffic'].values())},{'name':'all connections and work drain','pass':all(not p['after']['pool']['InUse'] and not p['after']['storage']['Active'] and not p['after']['worker']['Active'] for p in phases)},{'name':'generator p95 dispatch below20ms','pass':all((t['dispatch_p95_ms'] or 0)<20 for p in phases for t in p['traffic'].values())},{'name':'worker queue bounded','pass':all(t['queue_length']<=16 and t['worker']['Active']<=6 for t in trace)},{'name':'monitoring available','pass':not errors}]
  if policy!='disabled':checks.append({'name':'database permit ceiling respected','pass':all(t['storage']['Active']<=4 for t in trace)})
  if policy in ('pool','balanced'):checks.append({'name':'pool pressure actually measured','pass':any(x['State']=='pressure' for t in trace for x in t['storage_signals']['Signals'])})
  return {'profile':profile,'policy':policy,'repeat':repeat,'phases':phases,'trace':trace,'sampling_errors':errors,'checks':checks,'passed':all(c['pass'] for c in checks)}
 finally:stop.set();worker.join(5);s.stop_app()
def replication(s,profile):
 s.start_app(profile,operations='pool');u=s.urls['orders'];a=s.admins['orders'];events=[];checks=[]
 def record(name):
  v=state(s);code,data,_=request(u,'/orders/operations/replica');body=json.loads(data);events.append({'phase':name,'state':v,'http_status':code,'body':body});return code,body,v
 def check(name,ok):checks.append({'name':name,'pass':bool(ok)})
 def healthy():
  v=state(s);return v['replication']['Available'] and v['replication']['LagBytes']<65536 and request(u,'/orders/operations/replica')[0]==200
 try:
  wait(healthy);code,_,_=record('healthy');check('healthy follower accepts read',code==200)
  # No writes: byte position and heartbeat remain valid even if replay_lag is NULL.
  time.sleep(4);code,_,v=record('idle');check('idle caught-up standby is healthy',code==200 and v['replication']['Available'] and v['replication']['LagBytes']<65536)
  s.pause_replay()
  for _ in range(48):check('primary write while replay paused',request(u,'/orders/operations/transaction',{},'POST')[0]==200)
  wait(lambda:state(s)['replication']['LagBytes']>=65536)
  wait(lambda:request(u,'/orders/operations/replica')[0]==503)
  code,b,v=record('paused-backlog');check('real WAL backlog rejects follower reads',code==503 and b.get('reason')=='signal_pressure' and v['replication']['LagBytes']>=65536)
  t=time.monotonic();s.resume_replay();wait(healthy);replay_recovery=time.monotonic()-t;code,_,_=record('replay-recovered');check('replay recovery accepts read',code==200)
  s.stop_standby();wait(lambda:not state(s)['replication']['Available']);wait(lambda:request(u,'/orders/operations/replica')[0]==503)
  code,b,_=record('offline');check('missing standby rejects as unknown',code==503 and b.get('reason')=='signal_unavailable');check('primary remains usable with offline standby',request(u,'/orders/operations/transaction',{},'POST')[0]==200)
  t=time.monotonic();s.restart_standby();wait(healthy);offline_recovery=time.monotonic()-t;code,_,_=record('reconnected');check('reconnected standby recovers',code==200)
  check('stream opens',request(a,'/operations/stream',{},'POST')[0]==200);v=state(s);check('stream holds SQL permit beyond HTTP return',v['stream_open'] and v['storage']['Active']==1 and v['pool']['InUse']==1)
  api(a,'/operations/stream',method='DELETE');v=state(s);check('explicit stream close releases connection and permit',not v['stream_open'] and not v['storage']['Active'] and not v['pool']['InUse'])
  api(a,'/operations/pool',{'count':4},'POST');code=request(u,'/orders/operations/transaction',{},'POST')[0];v=state(s);check('pool wait times out without leaking permit',code==504 and not v['storage']['Active']);api(a,'/operations/pool',method='DELETE');check('pool recovery accepts transaction',request(u,'/orders/operations/transaction',{},'POST')[0]==200)
  return {'profile':profile,'events':events,'checks':checks,'replay_recovery_seconds':replay_recovery,'offline_recovery_seconds':offline_recovery,'passed':all(c['pass'] for c in checks)}
 finally:
  # Owned fixture only. Restore before dropping databases for next profile.
  try:s.restart_standby()
  except Exception:pass
  try:s.resume_replay()
  except Exception:pass
  s.stop_app()
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default='podman');p.add_argument('--repeats',type=int,default=3);p.add_argument('--profiles',nargs='+',choices=PROFILES,default=PROFILES);p.add_argument('--policies',nargs='+',choices=POLICIES,default=POLICIES);p.add_argument('--skip-replication',action='store_true');p.add_argument('--output',type=pathlib.Path,default=ROOT/'results/operation-results.json');a=p.parse_args()
 report={'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'methodology':{'scope':'Local SQL/worker contention and physical PostgreSQL standby; not production qualification or cluster-wide enforcement.','hardware':platform.platform(),'application_processes_max':4,'database_pool':4,'database_limit':4,'reserved_critical':1,'worker_limit':6,'queue_capacity':16,'background_sql_ms':120,'critical_sql_ms':20,'pool_signal':'100ms sampling, InUse/MaxOpen >= .75 limits background resource to1 (critical reservation1); external consumers hold3/4 connections','replication':'Physical streaming standby; real replay pause creates >64KiB WAL backlog, actual stop creates unknown; 1s heartbeat, max reply age3s, sample100ms, cache maxAge1s, collector timeout80ms','measurement':'Independent arrivals; 3s clients, database work context1s, no retries. Four phases 1/3/3/1s. p95 on successful requests shown with rejection and timeout rates.','repeats':a.repeats},'runs':[],'replication':[]}
 def save():a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(report,indent=2)+'\n')
 with Stack(a.projects,a.engine) as s:
  s.build();s.start_db();s.start_standby();report['methodology'].update(postgres=s.db_version,image_digest=s.image_digest,go_version=subprocess.check_output([__import__('os').environ.get('GO_BIN','go'),'version'],text=True).strip(),binaries_sha256={n:hashlib.sha256((s.projects/path).read_bytes()).hexdigest() for n,path in [('loadgen','commerce-backend/bin/loadgen'),('commerce','commerce-backend/bin/commerce-backend'),('proxy','gatewise/bin/gatewise')]})
  variants=[(x,y) for x in a.profiles for y in a.policies]
  for repeat in range(1,a.repeats+1):
   shift=(repeat-1)%len(variants)
   for profile,policy in variants[shift:]+variants[:shift]:report['runs'].append(one(s,profile,policy,repeat));save()
  if not a.skip_replication:
   for profile in a.profiles:report['replication'].append(replication(s,profile));save()
 report.update(finished_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),passed=all(r['passed'] for r in report['runs']+report['replication']));save();print('PASS' if report['passed'] else 'FAIL',a.output,flush=True)
 if not report['passed']:raise SystemExit(1)
if __name__=='__main__':main()
