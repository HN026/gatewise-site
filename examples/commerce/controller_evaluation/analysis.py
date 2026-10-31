"""Fixed aggregation, recovery definitions and confirmatory comparison criteria."""
import json,pathlib,statistics
CRITERIA=json.loads(pathlib.Path(__file__).with_name('criteria.json').read_text())
RECOVERY=set(CRITERIA['recovery_phases'])
def median(values):
 values=[x for x in values if x is not None];return statistics.median(values) if values else None
def useful(traffic,seconds):return traffic['successful_within_100ms_from_schedule_per_offered_second']*seconds
def recovery_bin(phase):
 bins=phase.get('timeline',[])
 def good(b):
  if b['seconds']<.99:return False
  t=b['traffic']['critical'];return t['sent']>0 and t['successful_within_100ms_from_schedule_per_offered_second']*b['seconds']>=.95*t['sent'] and not t['backend_5xx'] and not t['transport_errors']
 return next((a['start_seconds'] for a,b in zip(bins,bins[1:]) if good(a) and good(b)),None)
def motion(trace,predicate):
 total=0;reversals=0
 for role in ['orders','catalog','inventory']:
  points=[x for x in trace if x['service']==role and predicate(x['phase'])];last=None;direction=0
  for point in points:
   value=point['limit']
   if last is not None and value!=last:
    total+=abs(value-last);new=1 if value>last else -1
    if direction and direction!=new:reversals+=1
    direction=new
   last=value
 return total,reversals
def settling(run,phase):
 role_times=[]
 for role in ['orders','catalog','inventory']:
  pts=[p for p in run['trace'] if p['service']==role and p['phase']==phase['name'] and phase['started']<=p['seconds']<=phase['started']+phase['seconds']]
  if not pts:return None
  final=median(p['limit'] for p in pts if p['seconds']>=phase['started']+phase['seconds']-1)
  if final is None:return None
  t=next((p['seconds']-phase['started'] for i,p in enumerate(pts) if pts[-1]['seconds']-p['seconds']>=1 and all(abs(q['limit']-final)<=1 for q in pts[i:])),None)
  if t is None:return None
  role_times.append(t)
 return max(role_times)
def run_summary(run):
 phases=[p for p in run['phases'] if p['rps']];variation,reversals=motion(run['trace'],lambda _:True);pv,pr=motion(run['trace'],lambda n:n.startswith('periodic'))
 noninjected=[p for p in phases if not p['failure_every']];sent=sum(p['traffic']['critical']['sent'] for p in noninjected)
 return {'profile':run['profile'],'variant':run['variant'],'repeat':run['repeat'],'useful_critical':sum(useful(p['traffic']['critical'],p['seconds']) for p in phases),'adjustments':sum(sum(p['adjustments'].values()) for p in phases),'periodic_adjustments':sum(sum(p['adjustments'].values()) for p in phases if p['name'].startswith('periodic')),'limit_variation':variation,'limit_reversals':reversals,'periodic_variation':pv,'periodic_reversals':pr,'noninjected_error_rate':sum(p['traffic']['critical']['backend_5xx'] for p in noninjected)/sent if sent else 0,'recovery_bins':{p['name']:recovery_bin(p) for p in phases if p['name'] in RECOVERY},'settling_seconds':{p['name']:settling(run,p) for p in phases if p['name'] in RECOVERY}}
def analyze(runs):
 summaries=[run_summary(r) for r in runs];rows=[];comparisons=[];groups=[]
 for profile in sorted({r['profile'] for r in runs}):
  variants=sorted({r['variant'] for r in runs if r['profile']==profile})
  for variant in variants:
   selected=[r for r in runs if r['profile']==profile and r['variant']==variant];rs=[r for r in summaries if r['profile']==profile and r['variant']==variant]
   groups.append({'profile':profile,'variant':variant,**{key:median(r[key] for r in rs) for key in ['useful_critical','adjustments','periodic_adjustments','limit_variation','limit_reversals','periodic_variation','periodic_reversals','noninjected_error_rate']},'recovery_bins':{key:median(r['recovery_bins'][key] for r in rs) for key in RECOVERY},'recovery_censored':{key:sum(r['recovery_bins'][key] is None for r in rs) for key in RECOVERY},'settling_seconds':{key:median(r['settling_seconds'][key] for r in rs) for key in RECOVERY}})
   names=[p['name'] for p in selected[0]['phases'] if p['rps']]
   for name in names:
    phases=[next(p for p in r['phases'] if p['name']==name) for r in selected]
    for role in ['all','orders','catalog','inventory']:
     for priority in ['critical','background']:
      traffic=[p['traffic'][priority] if role=='all' else p['services'][role][priority] for p in phases];sent=sum(t['sent'] for t in traffic);waitcount=sum(p['pool_wait_count'] for p in phases);waitsec=sum(p['pool_wait_seconds'] for p in phases)
      rows.append({'profile':profile,'variant':variant,'phase':name,'service':role,'priority':priority,'sent':sent,'success_rps':median(t['successful']/p['seconds'] for t,p in zip(traffic,phases)),'timely_rps':median(t['successful_within_100ms_per_offered_second'] for t in traffic),'scheduled_timely_rps':median(t['successful_within_100ms_from_schedule_per_offered_second'] for t in traffic),'p95_ms':median(t['successful_p95_ms'] for t in traffic),'rejected_percent':sum(t['rejected_503'] for t in traffic)/sent*100 if sent else 0,'errors_percent':sum(t['backend_5xx'] for t in traffic)/sent*100 if sent else 0,'pool_mean_wait_ms':waitsec/waitcount*1000 if waitcount else 0})
  if 'stable-defaults' not in variants:continue
  stable=next(g for g in groups if g['profile']==profile and g['variant']=='stable-defaults')
  baselines=[g for g in groups if g['profile']==profile and g['variant']!='stable-defaults']
  def check(name,ok,baseline=None):comparisons.append({'profile':profile,'baseline':baseline,'name':name,'pass':bool(ok)})
  for baseline in baselines:
   label=baseline['variant'];check('overall useful critical work retained95%',stable['useful_critical']>=CRITERIA['overall_useful_ratio_min']*baseline['useful_critical'],label)
   check('noninjected critical error rate remains bounded',stable['noninjected_error_rate']<=baseline['noninjected_error_rate']+CRITERIA['noninjected_error_rate_extra_max'],label)
   for name in CRITERIA['key_phases']:
    a=next(r for r in rows if r['profile']==profile and r['variant']==label and r['phase']==name and r['service']=='all' and r['priority']=='critical');b=next(r for r in rows if r['profile']==profile and r['variant']=='stable-defaults' and r['phase']==name and r['service']=='all' and r['priority']=='critical')
    check(name+' useful critical work retained90%',b['scheduled_timely_rps']>=CRITERIA['degradation_useful_ratio_min']*a['scheduled_timely_rps'],label)
    check(name+' critical p95 regression bounded',b['p95_ms'] is not None and a['p95_ms'] is not None and b['p95_ms']<=max(CRITERIA['p95_budget_floor_ms'],a['p95_ms']*CRITERIA['degradation_p95_ratio_max']),label)
   for name in RECOVERY:
    a=next(r for r in rows if r['profile']==profile and r['variant']==label and r['phase']==name and r['service']=='all' and r['priority']=='critical');b=next(r for r in rows if r['profile']==profile and r['variant']=='stable-defaults' and r['phase']==name and r['service']=='all' and r['priority']=='critical')
    check(name+' useful critical work retained95%',b['scheduled_timely_rps']>=CRITERIA['recovery_useful_ratio_min']*a['scheduled_timely_rps'],label)
    old,new=baseline['recovery_bins'][name],stable['recovery_bins'][name]
    check(name+' qualifying recovery not lost/delayed beyond one bin',old is None or new is not None and new<=old+CRITERIA['recovery_extra_bin_seconds_max'],label)
  if baselines:
   check('periodic adjustment count reduced at least20%',stable['periodic_adjustments']<=CRITERIA['periodic_adjustment_ratio_max']*min(b['periodic_adjustments'] for b in baselines))
   check('periodic sampled variation does not regress beyond10%',stable['periodic_variation']<=CRITERIA['periodic_variation_ratio_max']*min(b['periodic_variation'] for b in baselines))
 return {'run_summaries':summaries,'groups':groups,'rows':rows,'comparison_checks':comparisons,'criteria':CRITERIA}
