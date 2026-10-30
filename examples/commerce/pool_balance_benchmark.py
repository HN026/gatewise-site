#!/usr/bin/env python3
"""Compare the retained v0.8 pool policy against an opt-in contention/sharing policy."""
import argparse,datetime,hashlib,json,pathlib,platform,statistics,subprocess,time
from run import ROOT,Stack
from operation_benchmark import PROFILES,one
PHASES=[('healthy',1,30),('workers',4,120),('pool-exhaustion',4,120),('light-interactive',3,1),('recovery',1,30)]
CRITERIA={'critical_timely_ratio_min':.9,'background_completion_ratio_min':1.5,'background_completed_min_per_run':8,'critical_p95_budget_ms':100,'pool_critical_timely_ratio_min':.9,'pool_p95_relative_max':1.25,'pool_p95_slack_ms':25,'light_background_ratio_min':.9}
def comparison(runs):
 rows=[];checks=[]
 for profile in sorted({r['profile'] for r in runs}):
  grouped={}
  for policy in ['pool','balanced']:
   selected=[r for r in runs if r['profile']==profile and r['policy']==policy];summary={}
   for name,seconds,_ in PHASES:
    phases=[next(p for p in r['phases'] if p['name']==name) for r in selected];traffic=[p['traffic']['critical'] for p in phases];sent=sum(t['sent'] for t in traffic);p95=[t['successful_p95_ms'] for t in traffic if t['successful_p95_ms'] is not None]
    summary[name]={'profile':profile,'policy':policy,'phase':name,'timely_rps':statistics.median(t['successful_within_100ms_per_offered_second'] for t in traffic),'success_rps':statistics.median(t['successful']/seconds for t in traffic),'p95_ms':statistics.median(p95) if p95 else None,'rejected_percent':sum(t['rejected_503'] for t in traffic)/sent*100,'errors':sum(t['backend_5xx'] for t in traffic),'sent':sent,'jobs_completed':statistics.median(p['jobs']['completed'] for p in phases),'jobs_rejected':statistics.median(p['jobs']['rejected'] for p in phases),'jobs_dropped':statistics.median(p['jobs']['dropped'] for p in phases),'jobs_failed':sum(p['jobs']['failed'] for p in phases),'jobs_canceled':sum(p['jobs']['canceled'] for p in phases),'wait_seconds':sum(p['pool_wait_seconds'] for p in phases),'wait_count':sum(p['pool_wait_count'] for p in phases)}
    rows.append(summary[name])
   grouped[policy]=summary
  old,new=grouped['pool'],grouped['balanced']
  def check(name,ok):checks.append({'profile':profile,'name':name,'pass':bool(ok)})
  a,b=old['workers'],new['workers']
  check('critical timely throughput retains at least90%',b['timely_rps']>=a['timely_rps']*CRITERIA['critical_timely_ratio_min'])
  check('critical successful p95 stays within100ms',b['p95_ms'] is not None and b['p95_ms']<=CRITERIA['critical_p95_budget_ms'])
  check('background completions improve at least50% with at least8 per run',b['jobs_completed']>=max(CRITERIA['background_completed_min_per_run'],a['jobs_completed']*CRITERIA['background_completion_ratio_min']))
  check('no extra critical deadline/server errors',b['errors']<=a['errors'])
  a,b=old['pool-exhaustion'],new['pool-exhaustion']
  check('external pool pressure retains at least90% useful critical throughput',b['timely_rps']>=a['timely_rps']*CRITERIA['pool_critical_timely_ratio_min'])
  check('external pool successful p95 regression bounded',b['p95_ms'] is not None and a['p95_ms'] is not None and b['p95_ms']<=a['p95_ms']*CRITERIA['pool_p95_relative_max']+CRITERIA['pool_p95_slack_ms'])
  check('no extra errors under external pool pressure',b['errors']<=a['errors'])
  check('background recovery retains at least90% completions',new['light-interactive']['jobs_completed']>=old['light-interactive']['jobs_completed']*CRITERIA['light_background_ratio_min'])
 return rows,checks
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default='podman');p.add_argument('--profiles',nargs='+',choices=PROFILES,default=PROFILES);p.add_argument('--repeats',type=int,default=3);p.add_argument('--output',type=pathlib.Path,default=ROOT/'results/pool-balance-results.json');a=p.parse_args()
 if not 1<=a.repeats<=10:p.error('repeats must be1..10')
 report={'release':'v0.8.1','started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'criteria':CRITERIA,'methodology':{'platform':platform.platform(),'phases':PHASES,'scope':'Local PostgreSQL contention and recovery, not production capacity or a fairness guarantee.','pool':'Retained v0.8 rule: utilization >=.75 sets shared cap1 with critical reservation1; all background work blocked while binding.','balanced':'WaitCount deltas activate 0/1 contention signal; recovery needs200ms with no new waits and a nonfull pool. Existing sharing gives server-owned interactive/batch classes shares3:1, borrowing ceiling4, demandTTL200ms; service4/reserved1 unchanged.','sampling':'100ms; maxAge1s; timeout50ms; same unknown cap1. No new admission queue, retry, preemption or controller algorithm.','jobs':'200 offers/sec,6 workers, bounded fixture queue16; 120ms writes vs20ms critical transactions. Rejections/drops/cancellation are retained. Counts include drain.','traffic':'Independent arrivals, database deadline1s, client3s. Same offered traffic and fresh databases; rotating policy/profile order.','repeats':a.repeats},'runs':[]}
 def save():a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(report,indent=2)+'\n')
 with Stack(a.projects,a.engine) as s:
  s.build();s.start_db();s.start_standby();report['methodology'].update(postgres=s.db_version,image_digest=s.image_digest,binaries_sha256={k:hashlib.sha256((s.projects/v).read_bytes()).hexdigest() for k,v in [('commerce','commerce-backend/bin/commerce-backend'),('proxy','gatewise/bin/gatewise'),('loadgen','commerce-backend/bin/loadgen')]})
  variants=[(p,m) for p in a.profiles for m in ['pool','balanced']]
  for repeat in range(1,a.repeats+1):
   order=variants[(repeat-1)%len(variants):]+variants[:(repeat-1)%len(variants)]
   for profile,policy in order:report['runs'].append(one(s,profile,policy,repeat,PHASES));save()
 report['rows'],report['comparison_checks']=comparison(report['runs']);report.update(finished_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),passed=all(r['passed'] for r in report['runs']) and all(c['pass'] for c in report['comparison_checks']));save();print('PASS' if report['passed'] else 'FAIL',[(c['profile'],c['name']) for c in report['comparison_checks'] if not c['pass']],flush=True)
 if not report['passed']:raise SystemExit(1)
if __name__=='__main__':main()
