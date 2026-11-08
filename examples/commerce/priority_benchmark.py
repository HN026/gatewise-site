#!/usr/bin/env python3
"""Measure real orders-to-catalog priority propagation and per-tier overload outcomes."""
import argparse,datetime,hashlib,json,pathlib,platform,subprocess,time,uuid
from run import ROOT,Stack,api,request
PROFILES=['middleware-fixed','proxy-fixed','middleware-adaptive','proxy-adaptive']
PHASES=[('baseline',3,60,0),('downstream-pressure',8,480,40),('deeper-pressure',6,720,80),('recovery',4,60,0)]
def one(stack,profile,repeat):
 stack.start_app(profile,priorities=True);phases=[];probes=[]
 for name,seconds,rps,delay in PHASES:
  stack.control(delay=0)
  # One unmeasured health/rotation probe between phases; no retries in measured traffic.
  code,b,_=request(stack.urls['orders'],'/orders/flows/batch',headers={'X-Gatewise-Priority':'v1;urgent;'+'a'*64})
  assert code==200,(profile,name,code,b)
  probe=json.loads(b);probes.append({'phase':name,'upstream':probe['upstream'],'downstream':probe['downstream']['priority']})
  api(stack.admins['catalog'],'/control',{'work_multiplier':1,'sql_delay_ms':delay,'failure_every':0},'PUT')
  cfg={'urls':stack.urls,'seconds':seconds,'start_rps':rps,'end_rps':rps,'business_slots':5,'run_id':uuid.uuid4().hex[:10],'priority_flows':True}
  out=subprocess.run([str(stack.projects/'commerce-backend/bin/loadgen')],input=json.dumps(cfg),capture_output=True,text=True,check=True)
  result=json.loads(out.stdout);result.update(name=name,sql_delay_ms=delay)
  until=time.monotonic()+2
  while time.monotonic()<until:
   states={role:api(url,'/state') for role,url in stack.admins.items()}
   if all(s['pool']['InUse']==0 for s in states.values()):break
   time.sleep(.02)
  result['pool_in_use_after_drain']={role:s['pool']['InUse'] for role,s in states.items()};phases.append(result)
  print(profile,repeat,name,'sent',result['sent'],'tiers',[(t,v['successful'],v['rejected_503']) for t,v in result['traffic'].items()],flush=True)
 checks=[{'name':'all scheduled arrivals delivered','pass':all(p['sent']==p['scheduled'] and p['dropped_schedules']==0 for p in phases)},
 {'name':'no transport errors or unexpected statuses','pass':all(v['transport_errors']==0 and v['other_statuses']==0 for p in phases for v in p['traffic'].values())},
 {'name':'every successful flow preserved importance','pass':all(v['chain_verified']==v['successful'] for p in phases for v in p['traffic'].values())},
 {'name':'cohorts agree within the same epoch','pass':all(v['cohort_mismatches']==0 for p in phases for v in p['traffic'].values()) and sum(v['cohort_comparisons'] for p in phases for v in p['traffic'].values())>0},
 {'name':'every tier makes progress in both pressure levels','pass':all(v['successful']>0 for p in phases if 'pressure' in p['name'] for v in p['traffic'].values())},
 {'name':'pressure produces explicit cohort rejection','pass':any(v['rejection_reasons'].get('cohort_limited',0)>0 for p in phases for v in p['traffic'].values())},
 {'name':'SQL connections drain','pass':all(n==0 for p in phases for n in p['pool_in_use_after_drain'].values())},
 {'name':'baseline and recovery successful p95 below 100ms','pass':all(v['successful_p95_ms'] is not None and v['successful_p95_ms']<100 for p in phases if p['name'] in ['baseline','recovery'] for v in p['traffic'].values())},
 {'name':'generator dispatch p95 below 10ms','pass':all((v['dispatch_p95_ms'] or 0)<10 for p in phases for v in p['traffic'].values())},
 {'name':'stable probe rotates between epochs','pass':len({p['upstream']['Epoch'] for p in probes})>=2 and len({p['upstream']['Bucket'] for p in probes})>=2}]
 return {'profile':profile,'repeat':repeat,'phases':phases,'rotation_probes':probes,'checks':checks,'passed':all(c['pass'] for c in checks)}
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default='podman');p.add_argument('--repeats',type=int,default=3);p.add_argument('--profiles',nargs='+',choices=PROFILES,default=PROFILES);p.add_argument('--output',type=pathlib.Path,default=ROOT/'results/priority-results.json');a=p.parse_args()
 if not 1<=a.repeats<=10:p.error('repeats must be 1..10')
 report={'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'methodology':{'scope':'Single local host with real PostgreSQL; no production capacity claim','chain':'orders -> catalog through Go middleware or one shared proxy; three business processes plus optional proxy','mix':'20% urgent, 20% normal, 60% batch; 64 stable synthetic cohort keys supplied by trusted loopback generator','limits':'orders/inventory 12, catalog 6; reservations urgent 2, normal 1, batch 0; SQL pools 4; adaptive minimum 5, starting at service ceiling','cohorts':'128 buckets; 2s UTC rotation; one unmeasured stable-key probe between phases; no synchronized-clock guarantee','generator':'Go open-loop, 4096 in-flight slots, 3s client timeout, no application retries; dispatch and arrival latency retained','phases':PHASES,'priority_trust':'Loopback is deliberately trusted in this isolated demo; remote-peer forgery is covered by Go integration tests'},'runs':[]}
 with Stack(a.projects,a.engine) as s:
  s.build();s.start_db();report.update(platform=platform.system()+'/'+platform.machine(),go_version=subprocess.check_output(['go','version'],text=True).strip(),postgres_version=s.db_version,postgres_image=s.image_digest,binary_sha256={n:hashlib.sha256((a.projects/n/'bin'/n).read_bytes()).hexdigest() for n in ['gatewise','commerce-backend']},generator_sha256=hashlib.sha256((a.projects/'commerce-backend/bin/loadgen').read_bytes()).hexdigest())
  for repeat in range(1,a.repeats+1):
   shift=(repeat-1)%len(a.profiles)
   for profile in a.profiles[shift:]+a.profiles[:shift]:
    report['runs'].append(one(s,profile,repeat));a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(report,indent=2)+'\n')
 report.update(finished_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),passed=all(r['passed'] for r in report['runs']));a.output.write_text(json.dumps(report,indent=2)+'\n');print('Report',a.output,'passed',report['passed'],flush=True)
 if not report['passed']:raise SystemExit(1)
if __name__=='__main__':main()
