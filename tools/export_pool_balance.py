"""Export the separately measured v0.8.1 policy comparison; never rewrite v0.8 evidence."""
import argparse,gzip,hashlib,json,pathlib,statistics
ROOT=pathlib.Path(__file__).resolve().parents[1]
PROFILES=['middleware-fixed','proxy-fixed','middleware-adaptive','proxy-adaptive']
def main():
 p=argparse.ArgumentParser();p.add_argument('--report',type=pathlib.Path,required=True);a=p.parse_args();raw=a.report.read_bytes();d=json.loads(raw)
 assert d.get('passed') and len(d['runs'])==24 and all(r['passed'] for r in d['runs']) and all(c['pass'] for c in d['comparison_checks'])
 for profile in PROFILES:
  for policy in ['pool','balanced']:assert {r['repeat'] for r in d['runs'] if r['profile']==profile and r['policy']==policy}=={1,2,3}
 summary={'source_sha256':hashlib.sha256(raw).hexdigest(),'runs':len(d['runs']),'sent':sum(p['sent'] for r in d['runs'] for p in r['phases']),'checks':sum(len(r['checks']) for r in d['runs'])+len(d['comparison_checks']),'missed':sum(p['dropped_schedules'] for r in d['runs'] for p in r['phases']),'transport_errors':sum(t['transport_errors'] for r in d['runs'] for p in r['phases'] for t in p['traffic'].values()),'criteria':d['criteria'],'rows':d['rows'],'comparisons':d['comparison_checks']}
 out=ROOT/'public/data';(out/'pool-balance-results.json').write_bytes(raw);(out/'pool-balance-results.json.gz').write_bytes(gzip.compress(raw,mtime=0));(ROOT/'src/data/pool-balance.json').write_text(json.dumps(summary,indent=2)+'\n')
 import matplotlib;matplotlib.use('Agg');import matplotlib.pyplot as plt
 plt.rcParams.update({'font.family':'DejaVu Sans','font.size':10,'text.color':'#dae8df','axes.labelcolor':'#bccfc2','xtick.color':'#a9bfb0','ytick.color':'#cbdcd1','axes.edgecolor':'#45604f','figure.facecolor':'#101b16','axes.facecolor':'#101b16','savefig.facecolor':'#101b16'})
 colors={'pool':'#829d8e','balanced':'#76d9bc'}
 ordered=lambda phase:[next(r for r in d['rows'] if r['profile']==profile and r['policy']==policy and r['phase']==phase) for profile in PROFILES for policy in ['pool','balanced']]
 def ranges(phase,key,rows):
  values=[]
  for r in rows:
   phases=[next(p for p in run['phases'] if p['name']==phase) for run in d['runs'] if run['profile']==r['profile'] and run['policy']==r['policy']]
   vals=[p['jobs']['completed'] if key=='jobs_completed' else p['traffic']['critical']['successful_p95_ms'] if key=='p95_ms' else p['traffic']['critical']['successful_within_100ms_per_offered_second'] for p in phases]
   values.append([r[key]-min(vals),max(vals)-r[key]])
  return [[v[0] for v in values],[v[1] for v in values]]
 def plot(phase,specs,title,name):
  rows=ordered(phase);fig,axes=plt.subplots(1,len(specs),figsize=(17,7));labels=[r['profile'].replace('middleware-','Middleware / ').replace('proxy-','Proxy / ')+' · '+('occupancy' if r['policy']=='pool' else 'contention + sharing') for r in rows]
  for i,(ax,(key,label)) in enumerate(zip(axes,specs)):
   ax.barh(range(len(rows)),[r[key] or 0 for r in rows],xerr=ranges(phase,key,rows),error_kw={'ecolor':'#dae8df','capsize':3,'elinewidth':1},color=[colors[r['policy']] for r in rows],height=.65);ax.invert_yaxis();ax.set_yticks(range(len(rows)),labels if i==0 else []);ax.set_xlabel(label);ax.grid(axis='x',alpha=.12);ax.set_axisbelow(True);ax.spines[['top','right']].set_visible(False)
   if key=='timely_rps' and phase=='workers':ax.axvline(120,color='#bdab79',linestyle='--',linewidth=1)
  fig.suptitle(title,fontsize=17,x=.035,ha='left',y=.97);fig.text(.035,.025,'Real PostgreSQL · identical offered traffic · three repeats per HTTP deployment and policy\nBars: median run-level outcomes. Whiskers: smallest/largest repeat, not confidence intervals. Job counts include drain.\nRead rejection, errors, queue drops and pool waits together. This compares the combined policy; it does not isolate either component.',fontsize=10,color='#a9bfb0');fig.tight_layout(rect=(.01,.14,1,.92));fig.savefig(ROOT/'public/images'/name,dpi=150);plt.close(fig)
 plot('workers',[('jobs_completed','Background jobs completed / phase'),('timely_rps','Critical successes within 100 ms / sec'),('p95_ms','Successful critical p95 · ms')],'Background progress alongside critical-request protection','pool-balance-workers.png')
 plot('pool-exhaustion',[('jobs_completed','Background jobs completed / phase'),('timely_rps','Critical successes within 100 ms / sec'),('p95_ms','Successful critical p95 · ms')],'Actual pool contention still requires shedding work','pool-balance-external.png')
 plot('light-interactive',[('jobs_completed','Background jobs completed / phase'),('timely_rps','Critical successes within 100 ms / sec'),('p95_ms','Successful critical p95 · ms')],'Background progress after releasing external pool holders','pool-balance-recovery.png')
 (out/'pool-balance-provenance.json').write_text(json.dumps({'source_sha256':summary['source_sha256'],'aggregation':'Median run-level successful p95, useful throughput and job counts; pooled rejection percentages and error counts. Whiskers show min/max of three repeats, not confidence intervals. Job counts include drain; job production stops when the HTTP load command returns, including its response tail.','comparison':'Combined contention signal plus existing server-owned workload-class sharing versus retained occupancy-only policy. Not an ablation or proof that either change alone produces the result.','criteria':d['criteria'],'limitations':'Short local workload, no admission queue, no preemption or starvation bound. Initial detection requires a new pool wait after provider construction; historical totals are ignored. Full-after-wait remains pressured until nonfull/quiet. A pressure cap1 and critical reservation1 still block background under real contention. These workload classes use the tenant dimension, not an additional independent customer isolation layer.','matplotlib':matplotlib.__version__,'images':['pool-balance-workers.png','pool-balance-external.png','pool-balance-recovery.png']},indent=2)+'\n');print(summary['sent'],'requests;',summary['checks'],'checks',flush=True)
if __name__=='__main__':main()
