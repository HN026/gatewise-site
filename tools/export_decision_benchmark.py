"""Export measured signal-engine evidence; preserve the complete report and hashes."""
import argparse,gzip,hashlib,json,pathlib,re,statistics
ROOT=pathlib.Path(__file__).resolve().parents[1]
PROFILES=['middleware-fixed','proxy-fixed','middleware-adaptive','proxy-adaptive'];STRATEGIES=['disabled','observe','enforce']
def main():
 p=argparse.ArgumentParser();p.add_argument('--report',type=pathlib.Path,required=True);p.add_argument('--overhead',type=pathlib.Path,required=True);a=p.parse_args();raw=a.report.read_bytes();d=json.loads(raw)
 assert d.get('passed') and len(d['runs'])==36 and all(r['passed'] for r in d['runs'])
 rows=[]
 for profile in PROFILES:
  for strategy in STRATEGIES:
   runs=[r for r in d['runs'] if r['profile']==profile and r['strategy']==strategy];assert len(runs)==3
   for name in ['healthy','pressure','conflict','unavailable','recovery']:
    phases=[next(p for p in r['phases'] if p['name']==name) for r in runs];traffic=[p['traffic']['critical'] for p in phases];sent=sum(v['sent'] for v in traffic);waits=sum(v['wait_count'] for p in phases for v in p['pools'].values());waittime=sum(v['wait_seconds'] for p in phases for v in p['pools'].values())
    rows.append({'profile':profile,'strategy':strategy,'phase':name,'success_rps':statistics.median(v['successful']/p['offered_seconds'] for p,v in zip(phases,traffic)),'timely_rps':statistics.median(v['successful_within_100ms_per_offered_second'] for v in traffic),'p95_ms':statistics.median(v['successful_p95_ms'] for v in traffic if v['successful_p95_ms'] is not None) if any(v['successful_p95_ms'] is not None for v in traffic) else None,'rejected_percent':sum(v['rejected_503'] for v in traffic)/sent*100,'errors_percent':sum(v['backend_5xx'] for v in traffic)/sent*100,'mean_wait_ms':waittime/waits*1000 if waits else 0,'pool_wait_seconds':statistics.median(sum(v['wait_seconds'] for v in p['pools'].values()) for p in phases)})
 summary={'source_sha256':hashlib.sha256(raw).hexdigest(),'runs':len(d['runs']),'checks':sum(len(r['checks']) for r in d['runs']),'sent':sum(p['sent'] for r in d['runs'] for p in r['phases']),'missed':sum(p['dropped_schedules'] for r in d['runs'] for p in r['phases']),'transport_errors':sum(v['transport_errors'] for r in d['runs'] for p in r['phases'] for v in p['traffic'].values()),'rows':rows}
 overhead=a.overhead.read_text();bench={}
 for name,ns,byte,alloc in re.findall(r'^(Benchmark\S+?)-\d+\s+\d+\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op',overhead,re.M):bench.setdefault(name,[]).append({'ns':float(ns),'bytes':int(byte),'allocs':int(alloc)})
 summary['overhead']=[{'name':k,'ns_median':statistics.median(v['ns'] for v in values),'ns_min':min(v['ns'] for v in values),'ns_max':max(v['ns'] for v in values),'bytes':values[0]['bytes'],'allocs':values[0]['allocs']} for k,values in bench.items()]
 assert len(summary['overhead'])==6 and all(len(v)==3 for v in bench.values())
 out=ROOT/'public/data';(out/'decision-results.json').write_bytes(raw);(out/'decision-results.json.gz').write_bytes(gzip.compress(raw,mtime=0));(out/'decision-overhead.txt').write_text(overhead);(ROOT/'src/data/decisions-benchmark.json').write_text(json.dumps(summary,indent=2)+'\n')
 import matplotlib;matplotlib.use('Agg');import matplotlib.pyplot as plt
 plt.rcParams.update({'font.family':'DejaVu Sans','font.size':10,'text.color':'#dae8df','axes.labelcolor':'#bccfc2','xtick.color':'#a9bfb0','ytick.color':'#cbdcd1','axes.edgecolor':'#45604f','figure.facecolor':'#101b16','axes.facecolor':'#101b16','savefig.facecolor':'#101b16'})
 selected=[r for r in rows if r['phase']=='pressure'];colors={'disabled':'#829d8e','observe':'#bdab79','enforce':'#76d9bc'};labels=[r['profile'].replace('middleware-','Middleware / ').replace('proxy-','Proxy / ')+' · '+r['strategy'] for r in selected]
 fig,axes=plt.subplots(1,2,figsize=(16,8))
 for ax,key,title,xlabel in [(axes[0],'timely_rps','Requests completed within 100 ms','Successful requests / sec'),(axes[1],'mean_wait_ms','Time spent waiting for a SQL connection','Mean wait among acquisitions that waited · ms')]:
  ax.barh(range(len(selected)),[r[key] for r in selected],color=[colors[r['strategy']] for r in selected],height=.65);ax.set_yticks(range(len(selected)),labels);ax.invert_yaxis();ax.set_title(title,pad=18);ax.set_xlabel(xlabel);ax.grid(axis='x',alpha=.12);ax.set_axisbelow(True);ax.spines[['top','right']].set_visible(False)
 fig.suptitle('When an external signal asks Gatewise to reduce admission',fontsize=17,x=.035,ha='left',y=.97)
 fig.text(.035,.03,'Real PostgreSQL · 600 requests/sec · all critical · 40 ms SQL delay · four connections per service · three repeats\nDisabled and observe retain hard service limits. Enforce adds a signal cap of 4. Read successful p95 and rejection rates together in the table.',fontsize=10,color='#a9bfb0');fig.tight_layout(rect=(.01,.1,1,.9));fig.savefig(ROOT/'public/images/decision-pressure.png',dpi=150);plt.close(fig)
 run=next(r for r in d['runs'] if r['profile']=='proxy-adaptive' and r['strategy']=='enforce' and r['repeat']==1);points=[v for v in run['trace'] if v['service']=='orders'];fig,axes=plt.subplots(3,1,figsize=(14,9),sharex=True)
 axes[0].plot([v['seconds'] for v in points],[v['baseline_limit'] for v in points],color='#bdab79',label='adaptive baseline');axes[0].plot([v['seconds'] for v in points],[v['active'] for v in points],color='#76d9bc',label='admitted work in flight');axes[0].set_ylabel('Concurrent requests');axes[0].legend(loc='upper right',frameon=False,labelcolor='#dae8df')
 for sig,color in [('primary','#76d9bc'),('secondary','#9db7e5')]:
  xs=[];ys=[]
  for v in points:
   matches=[k for k,n in v['signal_states'].items() if 'signal="'+sig+'"' in k and n==1];state=re.search(r'state="(\w+)"',matches[0]).group(1) if matches else 'missing';xs.append(v['seconds']);ys.append({'healthy':0,'pressure':1,'unknown':2,'stale':3,'missing':4}[state])
  axes[1].step(xs,ys,where='post',label=sig,color=color)
 axes[1].set_yticks(range(5),['healthy','pressure','unknown','stale','missing']);axes[1].set_ylabel('Cached source state');axes[1].legend(loc='upper right',frameon=False,labelcolor='#dae8df')
 axes[2].plot([v['seconds'] for v in points],[v['pool_wait_seconds'] for v in points],color='#76d9bc');axes[2].set_ylabel('Cumulative SQL\npool wait · sec');axes[2].set_xlabel('Elapsed seconds, including setup and drain')
 starts={}
 for v in points:starts.setdefault(v['phase'],v['seconds'])
 for name,at in starts.items():
  if name=='startup':continue
  for ax in axes:ax.axvline(at,color='#829d8e',alpha=.4,linewidth=.8)
  axes[0].text(at,1.03,name,transform=axes[0].get_xaxis_transform(),fontsize=9)
 for ax in axes:ax.grid(alpha=.1);ax.spines[['top','right']].set_visible(False)
 fig.suptitle('One recorded run: pressure, conflicting signals, failure and recovery',fontsize=16,x=.04,ha='left');fig.text(.04,.02,'Adaptive proxy · orders · enforce · repeat 1 · samples about every 200 ms; brief transitions and request peaks can be missed.\nBaseline is the adaptive controller limit, not the additional signal ceiling. Source states are measured metrics from controlled providers.',fontsize=10,color='#a9bfb0');fig.tight_layout(rect=(0,.07,1,.93));fig.savefig(ROOT/'public/images/decision-trace.png',dpi=150);plt.close(fig)
 (out/'decision-chart-provenance.json').write_text(json.dumps({'source_sha256':summary['source_sha256'],'overhead_sha256':hashlib.sha256(overhead.encode()).hexdigest(),'aggregation':'Median run-level goodput/successful p95; pooled rejection/error percentages; pooled SQL wait duration divided by waited acquisitions. Three repetitions per deployment/strategy.','trace':'proxy-adaptive / enforce / repetition 1 / orders; ~200 ms samples; cached state one-hot metrics mapped to categorical y values','overhead':'Go benchmarks, three repetitions, HTTP recorder and immediately completing handler for middleware; cached Evaluate alone for 1/8/32 rules; no contention or network included.','matplotlib':matplotlib.__version__,'images':['decision-pressure.png','decision-trace.png']},indent=2)+'\n');print('Exported',summary['sent'],'requests;',summary['checks'],'checks')
if __name__=='__main__':main()
