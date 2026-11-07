"""Produce reproducible figures from the complete commerce measurement report."""
import argparse,collections,gzip,hashlib,json,pathlib,re,statistics
ROOT=pathlib.Path(__file__).resolve().parents[1]
NAMES=['direct','proxy-fixed','proxy-adaptive','middleware-fixed','middleware-adaptive']
LABELS=['Direct','Proxy / fixed','Proxy / adaptive','Middleware / fixed','Middleware / adaptive']
COLORS=['#ed8a75','#789486','#72dfc3','#789486','#72dfc3']
def median(v):
 v=[x for x in v if x is not None];return statistics.median(v) if v else None
def export_controller(path):
 d=json.loads(path.read_text());assert d['passed'] and len(d['runs'])==6 and d['methodology']['phase_scale']==1
 raw=(json.dumps(d,separators=(',',':'))+'\n').encode();(ROOT/'public/data/controller-validation.json').write_bytes(raw)
 summary={'source_sha256':hashlib.sha256(raw).hexdigest(),'runs':len(d['runs']),'checks':sum(len(v['checks']) for v in d['runs']),'profiles':[]}
 for name in ['proxy-adaptive','middleware-adaptive']:
  runs=[v for v in d['runs'] if v['deployment']==name];assert len(runs)==3 and all(v['passed'] for v in runs);row={'deployment':name}
  for phase in ['pressure','burst','recovery']:row[phase+'_p95_ms']=statistics.median(next(p for p in v['phases'] if p['name']==phase)['traffic']['critical']['successful_p95_ms'] for v in runs)
  summary['profiles'].append(row)
 (ROOT/'src/data/controller-validation.json').write_text(json.dumps(summary,indent=2)+'\n')
def main():
 p=argparse.ArgumentParser();p.add_argument('--report',type=pathlib.Path,required=True);p.add_argument('--controller-report',type=pathlib.Path);a=p.parse_args();
 if a.controller_report:export_controller(a.controller_report)
 report=json.loads(a.report.read_text());assert report['passed'] and len(report['runs'])==15 and report['methodology']['phase_scale']==1
 assert all(r['passed'] for r in report['runs']);dispatch=max(t['dispatch_p95_ms'] for r in report['runs'] for p in r['phases'] for ts in p['services'].values() for t in ts.values() if t['dispatch_p95_ms'] is not None);assert dispatch<=10, 'Generator dispatch p95 exceeds the 10ms publication gate';out=ROOT/'public';raw=(json.dumps(report,separators=(',',':'))+'\n').encode();(out/'data/commerce-results.json').write_bytes(raw)
 with (out/'data/commerce-results.json.gz').open('wb') as f:
  with gzip.GzipFile(fileobj=f,mode='wb',mtime=0) as z:z.write(raw)
 summary={'source_sha256':hashlib.sha256(raw).hexdigest(),'maximum_service_dispatch_p95_ms':dispatch,'started_at':report['started_at'],'finished_at':report['finished_at'],'postgres_version':report['postgres_version'],'platform':report['platform'],'go_version':report['go_version'],'hardware':report['hardware'],'run_count':len(report['runs']),'checks':sum(len(r['checks']) for r in report['runs']),'scheduled':sum(p['scheduled'] for r in report['runs'] for p in r['phases']),'sent':sum(p['sent'] for r in report['runs'] for p in r['phases']),'dropped':sum(p['dropped_schedules'] for r in report['runs'] for p in r['phases']),'transport_errors':sum(t['transport_errors'] for r in report['runs'] for p in r['phases'] for t in p['traffic'].values()),'comparisons':[],'service_comparisons':[],'phases':[],'recovery':[],'resources':[],'controller_decisions':[]}
 def aggregate(phases):
  ts=[p['traffic']['critical'] for p in phases];sent=sum(t['sent'] for t in ts)
  return {'critical_p95_ms':median([t['successful_p95_ms'] for t in ts]),'critical_timely_per_second':median([t['successful_within_100ms_per_offered_second'] for t in ts]),'critical_success_percent':100*sum(t['successful'] for t in ts)/sent,'critical_rejected_percent':100*sum(t['rejected_503'] for t in ts)/sent,'critical_backend_errors_percent':100*sum(t['backend_5xx'] for t in ts)/sent,'mean_pool_wait_ms':median([1000*sum(v['wait_seconds'] for v in p['pools'].values())/sum(v['wait_count'] for v in p['pools'].values()) if sum(v['wait_count'] for v in p['pools'].values()) else 0 for p in phases])}
 for name,label in zip(NAMES,LABELS):
  runs=[r for r in report['runs'] if r['deployment']==name];assert len(runs)==3 and {r['repeat'] for r in runs}=={1,2,3};pressure=[next(p for p in r['phases'] if p['name']=='pressure') for r in runs]
  summary['comparisons'].append({'deployment':name,'label':label,**aggregate(pressure)})
  for phase in [p['name'] for p in runs[0]['phases']]:summary['phases'].append({'deployment':name,'phase':phase,**aggregate([next(p for p in r['phases'] if p['name']==phase) for r in runs])})
  for role in ['orders','catalog','inventory']:
   ts=[p['services'][role]['critical'] for p in pressure];sent=sum(t['sent'] for t in ts)
   summary['service_comparisons'].append({'deployment':name,'service':role,'p95_ms':median([t['successful_p95_ms'] for t in ts]),'timely_per_second':median([t['successful_within_100ms_per_offered_second'] for t in ts]),'rejected_percent':100*sum(t['rejected_503'] for t in ts)/sent,'backend_errors_percent':100*sum(t['backend_5xx'] for t in ts)/sent})
   peaks=[max(s['heap_bytes'] for s in r['trace'] if s['service']==role)/1024**2 for r in runs];goroutines=[max(s['goroutines'] for s in r['trace'] if s['service']==role) for r in runs]
   summary['resources'].append({'deployment':name,'service':role,'median_peak_heap_mib':median(peaks),'median_peak_goroutines':median(goroutines)})
   if 'adaptive' not in name:continue
   recovery=[];end_limits=[];decisions=collections.Counter()
   for r in runs:
    phase=next(p for p in r['phases'] if p['name']=='recovery');samples=[s for s in r['trace'] if s['service']==role and s['phase']=='recovery'];healthy=[s for s in samples if (s['controller_evaluated_at'] or 0)>=phase['started_unix_seconds'] and s['controller_p90_ratio']<=.8 and s['controller_failure_fraction']==0]
    recovery.append(healthy[0]['unix_seconds']-phase['started_unix_seconds'] if healthy else None);end_limits.append(samples[-1]['limit'] if samples else None)
    seen=set()
    for s in r['trace']:
     stamp=s['controller_evaluated_at'];key=s['controller_decision']
     if s['service']!=role or not stamp or stamp in seen or not key:continue
     seen.add(stamp);decision=re.search(r'decision="([^"]+)"',key);reason=re.search(r'reason="([^"]+)"',key)
     if decision and reason:decisions[(decision[1],reason[1])]+=1
   summary['recovery'].append({'deployment':name,'service':role,'healthy_feedback_seconds':recovery,'median_healthy_feedback_seconds':median(recovery),'recovery_end_limits':end_limits})
   for (decision,reason),count in sorted(decisions.items()):summary['controller_decisions'].append({'deployment':name,'service':role,'decision':decision,'reason':reason,'observed_windows':count})
 (ROOT/'src/data/commerce.json').write_text(json.dumps(summary,indent=2)+'\n')
 import matplotlib;matplotlib.use('Agg')
 import matplotlib.pyplot as plt
 import numpy as np
 bg='#101a14';plt.rcParams.update({'figure.facecolor':bg,'axes.facecolor':bg,'axes.edgecolor':'#40564a','axes.labelcolor':'#b8ccc0','text.color':'#e0ebe4','xtick.color':'#9eb6a7','ytick.color':'#c3d5c9','grid.color':'#304237','font.family':'DejaVu Sans','font.size':11,'savefig.facecolor':bg})
 def bars(ax,field,title,unit,phase="pressure"):
  values=[]
  for i,name in enumerate(NAMES):
   samples=[next(p for p in r['phases'] if p['name']==phase)['traffic']['critical'][field] for r in report['runs'] if r['deployment']==name];value=median(samples);values.append(value or 0)
   ax.barh(i,value or 0,color=COLORS[i],height=.55,zorder=2)
   for offset,v in zip([-.12,0,.12],samples):
    if v is not None:ax.scatter(v,i+offset,s=25,facecolors=bg,edgecolors='#e0ebe4',zorder=3)
  maximum=max(values+[1]);ax.set_xlim(0,maximum*1.3)
  for i,v in enumerate(values):ax.text(maximum*1.18,i,f'{v:.1f}',va='center',fontsize=10)
  ax.set_yticks(range(5),LABELS);ax.invert_yaxis();ax.set_title(title,loc='left',fontsize=12,pad=18);ax.set_xlabel(unit,labelpad=12);ax.grid(axis='x',alpha=.6,zorder=0)
 fig,axes=plt.subplots(1,2,figsize=(14,7.4),gridspec_kw={'wspace':.45});fig.suptitle('Commerce APIs: critical work under database overload',x=.07,ha='left',fontsize=18,fontweight='bold',y=.97)
 fig.text(.07,.90,'Orders writes · catalog reads · inventory reservations · separate pools · 3 repeats per deployment',color='#9eb6a7',fontsize=10)
 bars(axes[0],'successful_within_100ms_per_offered_second','Successful business work within 100ms','Requests / offered second');bars(axes[1],'successful_p95_ms','Successful critical-request p95','Milliseconds')
 for ax in axes:ax.spines[['top','right','left']].set_visible(False);ax.tick_params(axis='y',length=0,pad=8)
 fig.subplots_adjust(top=.78,bottom=.24,left=.16,right=.95);fig.text(.07,.12,'Pressure: 1,200 RPS total; 20% business / 80% reports; explicit 40ms SQL delay and 30× report aggregation.',fontsize=10,color='#a6beaf');fig.text(.07,.075,'Bars: median run-level values. Points: individual runs. Successful p95 excludes 503 and backend errors.',fontsize=10,color='#a6beaf');fig.text(.07,.035,'Local experiment. Read rejection rates alongside latency. These measurements do not establish production capacity.',fontsize=10,color='#a6beaf');fig.savefig(out/'images/commerce-critical.png',dpi=160);plt.close(fig)
 fig,axes=plt.subplots(1,2,figsize=(14,7.4),gridspec_kw={'wspace':.45});fig.suptitle('Commerce APIs: SQL pool waiting and critical outcomes',x=.07,ha='left',fontsize=18,fontweight='bold',y=.97)
 values=[r['mean_pool_wait_ms'] for r in summary['comparisons']];ax=axes[0];ax.barh(range(5),values,color=COLORS,height=.55);ax.set_yticks(range(5),LABELS);ax.invert_yaxis();ax.set_xlabel('Milliseconds / waiting acquisition');ax.set_title('SQL pool wait, among acquisitions that waited',loc='left',fontsize=11,pad=18);ax.grid(axis='x',alpha=.6);ax.set_axisbelow(True)
 ax=axes[1];left=np.zeros(5)
 for field,label,color in [('critical_success_percent','Successful','#72dfc3'),('critical_rejected_percent','Rejected 503','#b99672'),('critical_backend_errors_percent','Other 5xx','#ed8a75')]:
  v=np.array([r[field] for r in summary['comparisons']]);ax.barh(range(5),v,left=left,color=color,height=.55,label=label)
  for i,(n,l) in enumerate(zip(v,left)):
   if n>=8:ax.text(l+n/2,i,f'{n:.0f}%',ha='center',va='center',fontsize=9,color='#0c1710')
  left+=v
 ax.set_yticks(range(5),LABELS);ax.invert_yaxis();ax.set_xlim(0,100);ax.set_xlabel('Percentage of sent critical requests');ax.set_title('Critical outcomes pooled across three pressure runs',loc='left',fontsize=11,pad=18);fig.legend(*ax.get_legend_handles_labels(),loc='lower center',bbox_to_anchor=(.73,.14),ncol=3,frameon=False,fontsize=9)
 for ax in axes:ax.spines[['top','right','left']].set_visible(False);ax.tick_params(axis='y',length=0,pad=8)
 fig.subplots_adjust(top=.8,bottom=.26,left=.16,right=.97);fig.text(.07,.105,'Wait bars: median of per-run pooled means across three services. Outcome proportions use pooled request counts.',fontsize=10,color='#a6beaf');fig.text(.07,.06,'Wait time includes pool acquisition that can end in cancellation. Rejections do not enter successful latency.',fontsize=10,color='#a6beaf');fig.savefig(out/'images/commerce-pool-outcomes.png',dpi=160);plt.close(fig)
 fig,axes=plt.subplots(1,2,figsize=(14,7.4),gridspec_kw={'wspace':.45});fig.suptitle('Commerce APIs: sustained real-SQL traffic',x=.07,ha='left',fontsize=18,fontweight='bold',y=.97)
 fig.text(.07,.90,'60 seconds at 1,500 RPS · 80% business / 20% reports · no injected delay or SQL errors',color='#9eb6a7',fontsize=10)
 bars(axes[0],'successful_within_100ms_per_offered_second','Successful critical work within 100ms','Requests / offered second','sustained');bars(axes[1],'successful_p95_ms','Successful critical-request p95','Milliseconds','sustained')
 for ax in axes:ax.spines[['top','right','left']].set_visible(False);ax.tick_params(axis='y',length=0,pad=8)
 fig.subplots_adjust(top=.78,bottom=.24,left=.16,right=.95);fig.text(.07,.12,'Real orders writes, product reads, atomic stock reservations and report aggregation share bounded SQL pools.',fontsize=10,color='#a6beaf');fig.text(.07,.075,'Bars: median run-level measurements. Points: individual repeats. Inspect the report for rejection and error rates.',fontsize=10,color='#a6beaf');fig.text(.07,.035,'A one-minute local run is not a production capacity measurement or long-duration soak test.',fontsize=10,color='#a6beaf');fig.savefig(out/'images/commerce-sustained.png',dpi=160);plt.close(fig)
 run=next(r for r in report['runs'] if r['deployment']=='proxy-adaptive' and r['repeat']==1);fig,axes=plt.subplots(4,1,figsize=(14,11),sharex=True,gridspec_kw={'hspace':.35});fig.suptitle('Adaptive proxy: three independent service controllers',x=.08,ha='left',fontsize=18,fontweight='bold',y=.98)
 for role,color in [('orders','#72dfc3'),('catalog','#b99672'),('inventory','#99b9ef')]:
  trace=[s for s in run['trace'] if s['service']==role];times=[s['seconds'] for s in trace]
  for ax,field in zip(axes,['limit','pool_wait_count','controller_p90_ratio','controller_failure_fraction']):ax.plot(times,[s[field] for s in trace],color=color,label=role,linewidth=1.3)
 axes[0].axhline(5,color='#789486',ls='--');axes[0].axhline(12,color='#789486',ls='--');axes[0].legend(frameon=False,ncol=3,loc='upper right',fontsize=9)
 for ax,label in zip(axes,['Admission limit','Cumulative\nSQL pool waits','Last-window\np90 / target','Last-window\nfailure fraction']):ax.set_ylabel(label);ax.grid(alpha=.4);ax.spines[['top','right']].set_visible(False)
 axes[2].axhline(1,color='#ed8a75',ls='--');axes[3].set_xlabel('Elapsed seconds including drain and monitoring')
 for phase in [p['name'] for p in run['phases']]:
  points=[s['seconds'] for s in run['trace'] if s['phase']==phase]
  if not points:continue
  for ax in axes:ax.axvline(min(points),color='#789486',alpha=.3,linewidth=.8)
  axes[0].text((min(points)+max(points))/2,1.03,phase.replace('intermittent-errors','SQL errors').replace('low-traffic','low'),transform=axes[0].get_xaxis_transform(),ha='center',fontsize=8,rotation=0)
 fig.subplots_adjust(top=.88,bottom=.13,left=.1,right=.97);fig.text(.08,.055,'Proxy-adaptive repeat 1; sampled observations, not averages. Window values persist until the next evaluation.',fontsize=10,color='#a6beaf');fig.text(.08,.025,'Recovery restores healthy feedback; the limit increases only if healthy work also saturates the current budget.',fontsize=10,color='#a6beaf');fig.savefig(out/'images/commerce-controller.png',dpi=160);plt.close(fig)
 provenance={'source_sha256':summary['source_sha256'],'images':['commerce-critical.png','commerce-pool-outcomes.png','commerce-controller.png','commerce-sustained.png'],'aggregation':'Run-level medians; outcome proportions pooled counts; controller trace proxy-adaptive repeat 1; never pooled latency percentiles','matplotlib':matplotlib.__version__}
 (out/'data/commerce-chart-provenance.json').write_text(json.dumps(provenance,indent=2)+'\n');print(json.dumps(summary['comparisons'],indent=2));print('Exported',summary['sent'],'requests;',summary['checks'],'checks')
if __name__=='__main__':main()
