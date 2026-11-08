"""Export the complete sharing experiment without changing its measurements."""
import argparse,gzip,hashlib,json,pathlib,statistics
ROOT=pathlib.Path(__file__).resolve().parents[1]
PROFILES=['middleware-fixed','proxy-fixed','middleware-adaptive','proxy-adaptive']
STRATEGIES=['hard-cap','sharing-4','sharing-12']
def main():
 p=argparse.ArgumentParser();p.add_argument('--report',type=pathlib.Path,required=True);a=p.parse_args();raw=a.report.read_bytes();d=json.loads(raw);assert d.get('passed') and len(d['runs'])==36 and all(r['passed'] for r in d['runs'])
 records=[]
 for profile in PROFILES:
  for strategy in STRATEGIES:
   runs=[r for r in d['runs'] if r['profile']==profile and r['strategy']==strategy];assert len(runs)==3
   for phase in ['solo','returning','all-busy','solo-again','recovery']:
    phases=[next(p for p in r['phases'] if p['name']==phase) for r in runs];ordinary=[[v for k,v in p['tenant_traffic'].items() if k.startswith('ordinary-')] for p in phases];sent=sum(v['sent'] for group in ordinary for v in group);p95=[v['successful_p95_ms'] for group in ordinary for v in group if v['successful_p95_ms'] is not None];first=[max(v['first_success_seconds'] for v in group) for group in ordinary if group and all(v['first_success_seconds'] is not None for v in group)]
    records.append({'profile':profile,'strategy':strategy,'phase':phase,'total_success_rps':statistics.median(sum(v['successful'] for v in p['traffic'].values())/p['offered_seconds'] for p in phases),'total_timely_rps':statistics.median(sum(v['successful_within_100ms_per_offered_second'] for v in p['traffic'].values()) for p in phases),'ordinary_timely_rps':statistics.median(sum(v['successful_within_100ms_per_offered_second'] for v in group) for group in ordinary),'ordinary_p95_range_ms':[min(p95),max(p95)] if p95 else [],'ordinary_rejected_percent':sum(v['rejected_503'] for group in ordinary for v in group)/sent*100 if sent else None,'ordinary_errors_percent':sum(v['backend_5xx'] for group in ordinary for v in group)/sent*100 if sent else None,'all_rejected_percent':sum(v['rejected_503'] for p in phases for v in p['traffic'].values())/sum(p['sent'] for p in phases)*100,'first_all_returned_seconds':statistics.median(first) if first else None,'pool_wait_seconds':statistics.median(sum(p['pools'][r]['wait_seconds'] for r in p['pools']) for p in phases),'borrowed_successes':sum(v['allocations'].get('borrowed',0) for p in phases for v in p['traffic'].values())})
 summary={'source_sha256':hashlib.sha256(raw).hexdigest(),'runs':36,'checks':sum(len(r['checks']) for r in d['runs']),'sent':sum(p['sent'] for r in d['runs'] for p in r['phases']),'missed':sum(p['dropped_schedules'] for r in d['runs'] for p in r['phases']),'transport_errors':sum(v['transport_errors'] for r in d['runs'] for p in r['phases'] for v in p['traffic'].values()),'rows':records}
 out=ROOT/'public/data';(out/'sharing-results.json').write_bytes(raw);(out/'sharing-results.json.gz').write_bytes(gzip.compress(raw,mtime=0));(ROOT/'src/data/sharing.json').write_text(json.dumps(summary,indent=2)+'\n')
 import matplotlib;matplotlib.use('Agg');import matplotlib.pyplot as plt
 plt.rcParams.update({'font.family':'DejaVu Sans','font.size':10,'text.color':'#dae8df','axes.labelcolor':'#bccfc2','xtick.color':'#a9bfb0','ytick.color':'#cbdcd1','axes.edgecolor':'#45604f','figure.facecolor':'#101b16','axes.facecolor':'#101b16','savefig.facecolor':'#101b16'})
 fig,axes=plt.subplots(1,2,figsize=(16,8));colors={'hard-cap':'#829d8e','sharing-4':'#76d9bc','sharing-12':'#ed8b76'}
 for ax,phase,field,title in [(axes[0],'solo','total_success_rps','One busy customer\nSuccessful requests/sec'),(axes[1],'returning','ordinary_timely_rps','Returning customers\nSuccessful requests within 100 ms / sec')]:
  selected=[r for r in records if r['phase']==phase];labels=[r['profile'].replace('middleware-','Middleware / ').replace('proxy-','Proxy / ')+' · '+{'hard-cap':'cap 2','sharing-4':'share 2, ceiling 4','sharing-12':'share 2, ceiling 12'}[r['strategy']] for r in selected]
  ax.barh(range(len(selected)),[r[field] for r in selected],color=[colors[r['strategy']] for r in selected],height=.65);ax.set_yticks(range(len(selected)),labels);ax.invert_yaxis();ax.set_title(title,pad=18);ax.set_xlabel('Requests per second');ax.grid(axis='x',alpha=.12);ax.set_axisbelow(True);ax.spines[['top','right']].set_visible(False)
  if phase=='returning':ax.axvline(24,color='#bbd6c6',linestyle='--',linewidth=1);ax.set_xlim(0,25)
 fig.suptitle('Spare capacity and returning customers',fontsize=17,x=.035,ha='left',y=.97)
 fig.text(.035,.03,'Real PostgreSQL · 40ms SQL delay · three repeats per policy/deployment · four SQL connections per service\nThe return phase offers 24 requests/sec across ordinary customers. Read their p95 and rejection rates in the table; a larger ceiling is not a latency guarantee.',fontsize=10,color='#a9bfb0');fig.tight_layout(rect=(.01,.1,1,.9));fig.savefig(ROOT/'public/images/tenant-sharing.png',dpi=150);plt.close(fig)
 # One full trace illustrates the change in demand and limit. This is sampled, not a proof of exact request-time invariants.
 run=next(r for r in d['runs'] if r['profile']=='proxy-adaptive' and r['strategy']=='sharing-12' and r['repeat']==1)
 fig,axes=plt.subplots(3,1,figsize=(14,9),sharex=True);palette={'orders':'#76d9bc','catalog':'#bc9973','inventory':'#9db7e5'}
 for role,color in palette.items():
  v=[p for p in run['trace'] if p['service']==role]
  for ax,key in zip(axes,['demanding','borrowed','limit']):ax.plot([p['seconds'] for p in v],[p[key] for p in v],color=color,label=role,linewidth=1.5)
 for ax,title in zip(axes,['Customers with\nrecent demand','Borrowed reservations\nin flight','Service\nconcurrency limit']):ax.set_ylabel(title);ax.grid(alpha=.1);ax.spines[['top','right']].set_visible(False)
 axes[0].legend(ncols=3,loc='upper right',frameon=False,labelcolor='#dae8df');axes[-1].set_xlabel('Elapsed seconds, including sampling and phase cleanup')
 starts={}
 for point in run['trace']:starts.setdefault(point['phase'],point['seconds'])
 for phase,seconds in starts.items():
  if phase=='startup':continue
  for ax in axes:ax.axvline(seconds,color='#829d8e',alpha=.4,linewidth=.8)
  axes[0].text(seconds,1.03,phase,transform=axes[0].get_xaxis_transform(),fontsize=9)
 fig.suptitle('When customers return and leave: one recorded sharing run',fontsize=18,x=.04,ha='left');fig.text(.04,.02,'Adaptive proxy · ceiling 12 · repetition 1 · samples every 250ms; brief reservations and peaks may be missed.',fontsize=10,color='#a9bfb0');fig.tight_layout(rect=(0,.04,1,.93));fig.savefig(ROOT/'public/images/tenant-sharing-trace.png',dpi=150);plt.close(fig)
 (out/'sharing-chart-provenance.json').write_text(json.dumps({'source_sha256':summary['source_sha256'],'runs':36,'aggregation':'Median run-level rates; pooled rejection counts; ordinary p95 range retains individual actor/run percentiles; return time is median of run-level slowest first completion across ordinary actors','trace':'proxy-adaptive / sharing-12 / repetition 1; 250ms sampling','matplotlib':matplotlib.__version__,'images':['tenant-sharing.png','tenant-sharing-trace.png']},indent=2)+'\n');print('Exported',summary['sent'],'requests;',summary['checks'],'checks')
if __name__=='__main__':main()
