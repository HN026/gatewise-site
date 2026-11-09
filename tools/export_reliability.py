#!/usr/bin/env python3
"""Export only the completed, fixed-source reliability validation."""
import gzip,hashlib,json,pathlib
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
import numpy as np
ROOT=pathlib.Path(__file__).resolve().parents[1]
source=ROOT/'examples/commerce/results/reliability-results.json';raw=source.read_bytes();d=json.loads(raw)
assert d['passed'] and not d['exploratory'] and len(d['runs'])==4
assert all(sum(p['seconds'] for p in r['phases'])==180 for r in d['runs'])
rows=[];runtime=[]
for run in d['runs']:
 for p in run['phases']:
  for priority,t in p['traffic'].items():
   rows.append({'profile':run['profile'],'phase':p['name'],'priority':priority,'sent':t['sent'],'success_rps':t['successful']/p['seconds'],'timely_rps':t['successful_within_100ms_from_schedule_per_offered_second'],'p95_ms':t['successful_p95_ms'],'refused_percent':100*t['rejected_503']/t['sent'] if t['sent'] else 0,'server_error_percent':100*t['backend_5xx']/t['sent'] if t['sent'] else 0,'deadline_percent':100*t['client_deadline_errors']/t['sent'] if t['sent'] else 0,'pool_wait_seconds':p['pool_wait_seconds']})
 runtime.append({'profile':run['profile'],'max_backend_heap_mib':max(t['heap_bytes'] for t in run['trace'])/1024**2,'max_proxy_rss_mib':max((t['proxy_rss_bytes'] or 0 for t in run['trace']),default=0)/1024**2 or None,'max_idle_backend_goroutines':max(t['goroutines'] for t in run['idle'].values()),'checks':len(run['checks']),'passed_checks':sum(c['pass'] for c in run['checks'])})
summary={'source_sha256':hashlib.sha256(raw).hexdigest(),'started_at':d['started_at'],'finished_at':d['finished_at'],'core_ref':d['methodology']['core_ref'],'sent':sum(p['sent'] for r in d['runs'] for p in r['phases']),'runs':4,'seconds_per_run':180,'passed_checks':sum(c['pass'] for r in d['runs'] for c in r['checks']),'checks':sum(len(r['checks']) for r in d['runs']),'rows':rows,'runtime':runtime,'criteria':d['criteria']}
(ROOT/'src/data/reliability.json').write_text(json.dumps(summary,indent=2)+'\n');(ROOT/'public/data/reliability-results.json.gz').write_bytes(gzip.compress(raw,mtime=0))
plt.rcParams.update({'figure.facecolor':'#101b16','axes.facecolor':'#101b16','savefig.facecolor':'#101b16','text.color':'#dce8df','axes.labelcolor':'#b1c4b8','xtick.color':'#b1c4b8','ytick.color':'#b1c4b8','axes.edgecolor':'#496451','font.size':10,'axes.spines.top':False,'axes.spines.right':False})
colors=['#72dfc3','#ef907f','#a6bcf4','#deb978'];profiles=[r['profile'] for r in d['runs']];names={p:p.replace('middleware-','Middleware / ').replace('proxy-','Proxy / ') for p in profiles};phases=[p['name'] for p in d['runs'][0]['phases']];xs=np.arange(len(phases));fig,axes=plt.subplots(2,1,figsize=(12,7),layout='constrained')
for i,p in enumerate(profiles):
 selected=[next(r for r in rows if r['profile']==p and r['phase']==phase and r['priority']=='critical') for phase in phases]
 for ax,key in zip(axes,['timely_rps','refused_percent']):ax.bar(xs+(i-1.5)*.19,[r[key] for r in selected],.18,label=names[p],color=colors[i]);ax.grid(axis='y',alpha=.15);ax.set_axisbelow(True)
axes[0].set_ylabel('Critical successes within 100 ms / sec');axes[1].set_ylabel('Critical refused (%)');axes[0].legend(ncols=2,frameon=False,fontsize=9,labelcolor='#dce8df')
for ax in axes:ax.set_xticks(xs,[p.replace('-','\n') for p in phases],fontsize=9)
fig.suptitle('Sustained overload: timely completions and refusals',fontsize=16);fig.savefig(ROOT/'public/images/reliability-outcomes.png',dpi=150);plt.close(fig)
fig,axes=plt.subplots(2,1,figsize=(12,6.5),layout='constrained')
for i,run in enumerate(d['runs']):
 trace=[t for t in run['trace'] if t['service']=='orders'];x=[t['seconds'] for t in trace]
 for ax,key in zip(axes,['limit','waiting']):ax.step(x,[t[key] for t in trace],where='post',label=names[run['profile']],color=colors[i],alpha=.85);ax.grid(alpha=.15)
axes[0].set_ylabel('Orders execution limit');axes[0].set_ylim(0,5);axes[0].legend(ncols=2,frameon=False,fontsize=9,labelcolor='#dce8df');axes[1].set_ylabel('Orders waiting requests');axes[1].set_xlabel('Elapsed seconds in each independent run');axes[1].set_ylim(0,18)
fig.suptitle('Execution and waiting remain separate through pressure and recovery',fontsize=15);fig.savefig(ROOT/'public/images/reliability-limits.png',dpi=150);plt.close(fig)
print('Exported',summary['sent'],'requests and',summary['passed_checks'],'checks')
