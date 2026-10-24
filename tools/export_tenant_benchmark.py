"""Export tenant isolation evidence, keeping noisy-actor costs visible."""
import argparse,gzip,hashlib,json,pathlib,statistics
ROOT=pathlib.Path(__file__).resolve().parents[1]
NAMES=['direct','semaphore','proxy-fixed','proxy-fixed-tenants','proxy-adaptive','proxy-adaptive-tenants','middleware-fixed','middleware-fixed-tenants','middleware-adaptive','middleware-adaptive-tenants']
LABELS=['Direct','Semaphore','Proxy fixed','Proxy fixed + tenants','Proxy adaptive','Proxy adaptive + tenants','Middleware fixed','Middleware fixed + tenants','Middleware adaptive','Middleware adaptive + tenants']
def median(v):return statistics.median([x for x in v if x is not None]) if any(x is not None for x in v) else None
def main():
 p=argparse.ArgumentParser();p.add_argument('--report',type=pathlib.Path,required=True);a=p.parse_args();report=json.loads(a.report.read_text());assert report['passed'] and len(report['runs'])==30 and report['methodology']['phase_scale']==1
 raw=(json.dumps(report,separators=(',',':'))+'\n').encode();(ROOT/'public/data/tenant-results.json').write_bytes(raw)
 with (ROOT/'public/data/tenant-results.json.gz').open('wb') as f:
  with gzip.GzipFile(fileobj=f,mode='wb',mtime=0) as z:z.write(raw)
 summary={'source_sha256':hashlib.sha256(raw).hexdigest(),'started_at':report['started_at'],'finished_at':report['finished_at'],'runs':len(report['runs']),'checks':sum(len(r['checks']) for r in report['runs']),'sent':sum(p['sent'] for r in report['runs'] for p in r['phases']),'missed':sum(p['dropped_schedules'] for r in report['runs'] for p in r['phases']),'transport_errors':sum(t['transport_errors'] for r in report['runs'] for p in r['phases'] for t in p['traffic'].values()),'comparisons':[]}
 for name,label in zip(NAMES,LABELS):
  runs=[r for r in report['runs'] if r['adapter']['name']==name];assert len(runs)==3 and all(r['passed'] for r in runs)
  phases=[next(p for p in r['phases'] if p['name']=='noisy-neighbor') for r in runs];ordinary=[{k:v for k,v in p['tenant_traffic'].items() if k.startswith('ordinary-')} for p in phases]
  normal_sent=sum(v['sent'] for ts in ordinary for v in ts.values());noisy_sent=sum(p['tenant_traffic']['noisy']['sent'] for p in phases)
  row={'adapter':name,'label':label,'ordinary_timely_rps':median([sum(v['successful_within_100ms_per_offered_second'] for v in ts.values()) for ts in ordinary]),'ordinary_p95_range_ms':[min(v['successful_p95_ms'] for ts in ordinary for v in ts.values() if v['successful_p95_ms'] is not None),max(v['successful_p95_ms'] for ts in ordinary for v in ts.values() if v['successful_p95_ms'] is not None)],'ordinary_rejected_percent':100*sum(v['rejected_503'] for ts in ordinary for v in ts.values())/normal_sent,'ordinary_other_5xx_percent':100*sum(v['backend_5xx'] for ts in ordinary for v in ts.values())/normal_sent,'noisy_success_rps':median([p['tenant_traffic']['noisy']['successful']/p['offered_seconds'] for p in phases]),'noisy_rejected_percent':100*sum(p['tenant_traffic']['noisy']['rejected_503'] for p in phases)/noisy_sent,'total_success_rps':median([p['traffic']['critical']['successful']/p['offered_seconds'] for p in phases]),'max_tracked':max((s['tenants_tracked'] for r in runs for s in r['trace'] if s['tenants_tracked'] is not None),default=None),'median_peak_heap_mib':median([max(s['heap_bytes'] for s in r['trace'])/1024**2 for r in runs])}
  summary['comparisons'].append(row)
 (ROOT/'src/data/tenants.json').write_text(json.dumps(summary,indent=2)+'\n')
 import matplotlib;matplotlib.use('Agg');import matplotlib.pyplot as plt
 import numpy as np
 bg='#101a14';plt.rcParams.update({'figure.facecolor':bg,'axes.facecolor':bg,'axes.edgecolor':'#40564a','axes.labelcolor':'#b8ccc0','text.color':'#e0ebe4','xtick.color':'#9eb6a7','ytick.color':'#c3d5c9','grid.color':'#304237','font.family':'DejaVu Sans','font.size':10,'savefig.facecolor':bg})
 colors=['#72dfc3' if name.endswith('-tenants') else '#ed8a75' if name=='direct' else '#789486' for name in NAMES]
 fig,axes=plt.subplots(1,2,figsize=(15,9),gridspec_kw={'wspace':.55});fig.suptitle('Tenant isolation: useful ordinary work and rejection cost',x=.06,ha='left',fontsize=18,fontweight='bold',y=.97);fig.text(.06,.91,'One noisy actor: 480 RPS · three ordinary actors: 8 RPS each · 40ms injected SQL delay · 3 repeats',fontsize=10,color='#9eb6a7')
 values=[r['ordinary_timely_rps'] for r in summary['comparisons']];ax=axes[0];ax.barh(range(10),values,color=colors,height=.6);ax.set_xlim(0,29);ax.axvline(24,color='#a9c4b5',ls='--',linewidth=1);ax.set_yticks(range(10),LABELS);ax.invert_yaxis();ax.set_title('Ordinary successful work within 100ms',loc='left',fontsize=12,pad=16);ax.set_xlabel('Requests / offered second')
 for i,v in enumerate(values):ax.text(25.6,i,f'{v:.1f}',va='center',fontsize=9)
 ax=axes[1];left=np.zeros(10)
 for field,label,color in [('ordinary_rejected_percent','Rejected 503','#b99672'),('ordinary_other_5xx_percent','Other 5xx','#ed8a75')]:
  v=np.array([r[field] for r in summary['comparisons']]);ax.barh(range(10),v,left=left,color=color,height=.6,label=label);left+=v
 ax.set_yticks(range(10),LABELS);ax.invert_yaxis();ax.set_xlim(0,100);ax.set_title('Ordinary rejected and failed requests',loc='left',fontsize=12,pad=16);ax.set_xlabel('Percent of sent ordinary requests');fig.legend(*ax.get_legend_handles_labels(),loc='lower center',bbox_to_anchor=(.74,.14),ncol=2,frameon=False)
 for ax in axes:ax.grid(axis='x',alpha=.4);ax.set_axisbelow(True);ax.spines[['top','right','left']].set_visible(False);ax.tick_params(axis='y',length=0,pad=8)
 fig.subplots_adjust(top=.8,bottom=.23,left=.19,right=.97);fig.text(.06,.09,'Left: median of run-level goodput; right: pooled request proportions. All operations critical; identical service mix.',fontsize=10,color='#a6beaf');fig.text(.06,.05,'Tenant caps can leave spare capacity unused. Read noisy-actor rejections and total throughput alongside ordinary protection.',fontsize=10,color='#a6beaf');fig.savefig(ROOT/'public/images/tenant-isolation.png',dpi=160);plt.close(fig)
 run=next(r for r in report['runs'] if r['adapter']['name']=='proxy-adaptive-tenants' and r['repeat']==1);fig,axes=plt.subplots(3,1,figsize=(14,9),sharex=True,gridspec_kw={'hspace':.35});fig.suptitle('Adaptive proxy with tenant isolation: recorded resource trace',x=.08,ha='left',fontsize=17,fontweight='bold',y=.97)
 for role,color in [('orders','#72dfc3'),('catalog','#b99672'),('inventory','#99b9ef')]:
  trace=[s for s in run['trace'] if s['service']==role];times=[s['seconds'] for s in trace]
  for ax,field in zip(axes,['tenants_tracked','tenant_leases','pool_wait_count']):ax.plot(times,[s[field] for s in trace],label=role,color=color,linewidth=1.4)
 axes[0].axhline(32,color='#ed8a75',ls='--',label='Registry bound 32');axes[0].legend(frameon=False,ncol=4,fontsize=9)
 for ax,label in zip(axes,['Retained identified entries','Tenant reservations','Cumulative SQL pool waits']):ax.set_ylabel(label);ax.grid(alpha=.4);ax.spines[['top','right']].set_visible(False)
 for phase in ['baseline','noisy-neighbor','identity-churn','recovery']:
  points=[s['seconds'] for s in run['trace'] if s['phase']==phase]
  if points:
   for ax in axes:ax.axvline(min(points),color='#789486',alpha=.4)
   axes[0].text((min(points)+max(points))/2,1.03,phase,transform=axes[0].get_xaxis_transform(),ha='center',fontsize=8)
 axes[-1].set_xlabel('Elapsed seconds including drain and observation');fig.subplots_adjust(top=.85,bottom=.15,left=.12,right=.96);fig.text(.08,.065,'One recorded run; 250ms sampling can miss peaks. Active entries are never evicted; inactive entries expire opportunistically.',fontsize=10,color='#a6beaf');fig.text(.08,.025,'Explicit synthetic identities are used in experiment reports. Gatewise itself emits no per-identity metric labels.',fontsize=10,color='#a6beaf');fig.savefig(ROOT/'public/images/tenant-tracking.png',dpi=160);plt.close(fig)
 (ROOT/'public/data/tenant-chart-provenance.json').write_text(json.dumps({'source_sha256':summary['source_sha256'],'runs':30,'aggregation':'Median run-level ordinary goodput; pooled rejection/error counts; p95 range retains per-actor/per-run percentiles; trace proxy-adaptive-tenants repeat 1','images':['tenant-isolation.png','tenant-tracking.png'],'matplotlib':matplotlib.__version__},indent=2)+'\n');print(json.dumps(summary['comparisons'],indent=2));print('Exported',summary['sent'],'requests and',summary['checks'],'checks')
if __name__=='__main__':main()
