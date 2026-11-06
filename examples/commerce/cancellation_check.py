#!/usr/bin/env python3
"""Real PostgreSQL: cooperative vs deliberately uncancelable checkout work."""
import argparse,concurrent.futures,datetime,json,pathlib,time,uuid
from run import ROOT,Stack,api,request
from comparison_recorded import refs,drain
from queue_benchmark import values,state

def one(s,profile,ignore):
 s.start_app(profile,execution_limit=4,reserved=1)
 def checkout(timeout):
  try:return request(s.urls['orders'],'/orders/checkout',{'amount_cents':1000},headers={'Idempotency-Key':uuid.uuid4().hex},timeout=timeout)[0]
  except (TimeoutError,OSError):return 'client_timeout'
 api(s.admins['orders'],'/control',{'work_multiplier':1,'sql_delay_ms':300,'failure_every':0,'ignore_cancellation':ignore},'PUT')
 checks=[];waves=[];trace=[]
 def check(name,passed):checks.append({'name':name,'pass':bool(passed)})
 for wave in range(3):
  with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:outcomes=list(pool.map(lambda _:checkout(.04),range(4)))
  if not ignore:drain(s)
  backend=refs(s)['orders'];snapshot=state(values(s),'orders');ps=api(s.admins['orders'],'/state')['pool']
  waves.append({'wave':wave+1,'outcomes':outcomes,'backend':backend,'proxy_or_middleware':snapshot,'pool':ps});trace.append(snapshot)
  if not ignore:check('cooperative work drains after wave'+str(wave+1),backend['active']==0)
  elif profile.startswith('middleware-'):check('middleware holds actual handler lifetime wave'+str(wave+1),backend['peak']<=4 and backend['active']<=4)
 check('Gatewise sampled request ceiling4',all(t['active']<=4 for t in trace))
 check('SQL pool ceiling4',all(w['pool']['InUse']<=4 for w in waves))
 if ignore and profile.startswith('proxy-'):check('remote activity can exceed proxy request ceiling',max(w['backend']['active'] for w in waves)>=8)
 if ignore and profile.startswith('middleware-'):check('new requests refused while canceled handlers still work',any(503 in w['outcomes'] for w in waves[1:]))
 check('client deadlines exercised',any('client_timeout' in w['outcomes'] for w in waves))
 drain(s);check('all actual work and SQL connections released',refs(s)['orders']['active']==0 and api(s.admins['orders'],'/state')['pool']['InUse']==0)
 api(s.admins['orders'],'/control',{'work_multiplier':1,'sql_delay_ms':0,'failure_every':0,'ignore_cancellation':False},'PUT');check('healthy checkout recovers',checkout(3)==201)
 result={'profile':profile,'ignore_cancellation':ignore,'client_timeout_ms':40,'sql_delay_ms':300,'sql_timeout_ms':1000,'waves':waves,'checks':checks,'passed':all(c['pass'] for c in checks)};print(profile,'ignore='+str(ignore),'peak',max(w['backend']['peak'] for w in waves),'PASS' if result['passed'] else 'FAIL',flush=True);s.stop_app();return result

def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default='podman');p.add_argument('--output',type=pathlib.Path,default=ROOT/'results/cancellation-results.json');a=p.parse_args()
 report={'release':'v0.11.1','started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'scope':'Controlled realSQL failure-mode validation, not throughput or a promise about arbitrary upstream cancellation. IgnoreCancellation strips the caller cancellation only for fixture checkout; existing1s SQL timeout remains authoritative. Three waves of4checkouts; no retries. Middleware holds slots until actual handler return; proxy slots protect proxy request lifetime.','runs':[]}
 with Stack(a.projects,a.engine) as s:
  s.build();s.start_db()
  for profile in ['middleware-fixed','proxy-fixed']:
   for ignore in [False,True]:report['runs'].append(one(s,profile,ignore))
 report.update(passed=all(r['passed'] for r in report['runs']),finished_at=datetime.datetime.now(datetime.timezone.utc).isoformat());a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(report,indent=2)+'\n')
 if not report['passed']:raise SystemExit(1)
if __name__=='__main__':main()
