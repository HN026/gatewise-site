"""Publish measured controller evidence without a GitHub-sized raw JSON file."""
import argparse,gzip,hashlib,json,pathlib,statistics
ROOT=pathlib.Path(__file__).resolve().parents[1]
def main():
 p=argparse.ArgumentParser();p.add_argument('--report',type=pathlib.Path,required=True);a=p.parse_args();raw=a.report.read_bytes();d=json.loads(raw)
 assert d.get('finished_at') and len(d['runs'])==18 and all(r['passed'] for r in d['runs'])
 for profile in ['middleware-adaptive','proxy-adaptive']:
  for variant in ['legacy-tuned','legacy-defaults','stable-defaults']:assert {r['repeat'] for r in d['runs'] if r['profile']==profile and r['variant']==variant}=={1,2,3}
 checks=[c for r in d['runs'] for c in r['checks']]+d['comparison_checks'];out=ROOT/'public/data';compressed=gzip.compress(raw,mtime=0);(out/'adaptation-results.json.gz').write_bytes(compressed)
 summary={k:d[k] for k in ['groups','rows','criteria','comparison_checks','run_summaries','methodology','started_at','finished_at']}
 summary.update(source_sha256=hashlib.sha256(raw).hexdigest(),gzip_sha256=hashlib.sha256(compressed).hexdigest(),passed=d['passed'],runs=18,sent=sum(p['sent'] for r in d['runs'] for p in r['phases'] if p['rps']),checks=len(checks),passed_checks=sum(c['pass'] for c in checks),missed=sum(p['dropped_schedules'] for r in d['runs'] for p in r['phases'] if p['rps']),transport_errors=sum(t['transport_errors'] for r in d['runs'] for p in r['phases'] if p['rps'] for t in p['traffic'].values()))
 (ROOT/'src/data/adaptation.json').write_text(json.dumps(summary,indent=2)+'\n')
 import matplotlib;matplotlib.use('Agg');import matplotlib.pyplot as plt
 plt.rcParams.update({'font.family':'DejaVu Sans','font.size':10,'text.color':'#dae8df','axes.labelcolor':'#bccfc2','xtick.color':'#a9bfb0','ytick.color':'#cbdcd1','axes.edgecolor':'#45604f','figure.facecolor':'#101b16','axes.facecolor':'#101b16','savefig.facecolor':'#101b16'})
 colors={'legacy-tuned':'#829d8e','legacy-defaults':'#bdab79','stable-defaults':'#76d9bc'};groups=d['groups'];labels=[g['profile'].split('-')[0].title()+' / '+g['variant'].replace('-',' ') for g in groups]
 fig,axes=plt.subplots(1,3,figsize=(17,7))
 for i,(key,label) in enumerate([('periodic_adjustments','Exact adjustments / periodic sequence'),('periodic_variation','Sampled absolute slot changes'),('useful_critical','Critical successes within 100 ms from schedule')]):
  ax=axes[i];values=[g[key] for g in groups];errs=[]
  for g in groups:
   vals=[r[key] for r in d['run_summaries'] if r['profile']==g['profile'] and r['variant']==g['variant']];errs.append([g[key]-min(vals),max(vals)-g[key]])
  ax.barh(range(len(groups)),values,color=[colors[g['variant']] for g in groups],xerr=[[e[0] for e in errs],[e[1] for e in errs]],error_kw={'ecolor':'#dae8df','capsize':3},height=.65);ax.invert_yaxis();ax.set_yticks(range(len(groups)),labels if i==0 else []);ax.set_xlabel(label);ax.grid(axis='x',alpha=.12);ax.set_axisbelow(True);ax.spines[['top','right']].set_visible(False)
 fig.suptitle('Controller movement and useful work',x=.025,ha='left',fontsize=17,y=.97);fig.text(.025,.025,'Real PostgreSQL · same offered traffic · three repeats per deployment and configuration\nMedians with min/max repeat whiskers, not confidence intervals. Fewer adjustments are useful only if completed work and recovery remain acceptable.',color='#a9bfb0');fig.tight_layout(rect=(0,.12,1,.92));fig.savefig(ROOT/'public/images/adaptation-comparison.png',dpi=150);plt.close(fig)
 fig,axes=plt.subplots(3,1,figsize=(16,10),sharex=True)
 for variant in colors:
  run=next(r for r in d['runs'] if r['profile']=='middleware-adaptive' and r['variant']==variant and r['repeat']==1);pts=[p for p in run['trace'] if p['service']=='orders'];x=[p['seconds'] for p in pts]
  for ax,key in zip(axes,['limit','p90_ratio']):ax.plot(x,[p[key] for p in pts],color=colors[variant],label=variant,linewidth=1.3)
  axes[2].plot(x,[p['pool']['InUse'] for p in pts],color=colors[variant],label=variant,linewidth=1)
  if variant=='stable-defaults':
   for phase in run['phases']:
    if phase['name'] in ['capacity-loss','gradual-25','mixed-cost','signal-cap']:axes[0].axvspan(phase['started'],phase['started']+phase['seconds'],color='#bdab79',alpha=.12);axes[0].text(phase['started'],12.4,phase['name'],fontsize=8,rotation=25)
 axes[0].set_ylabel('Adaptive baseline slots');axes[0].set_ylim(4,14);axes[1].set_ylabel('Raw aggregate p90 / budget');axes[2].set_ylabel('SQL connections in use');axes[2].set_xlabel('Seconds since this run started (sequential phases include control and drain time)')
 for ax in axes:ax.grid(alpha=.12);ax.spines[['top','right']].set_visible(False)
 axes[0].legend(loc='lower left',ncol=3);fig.suptitle('Orders: one recorded middleware run per configuration',x=.06,ha='left',fontsize=17);fig.text(.06,.015,'Traces are sampled about every 100 ms. Separate runs have slightly different phase timings; shading marks the stable run only.\nSignal cap 4 is an effective limit below the controller floor 5; baseline alone does not show the signal ceiling.',fontsize=10,color='#a9bfb0');fig.tight_layout(rect=(0,.07,1,.95));fig.savefig(ROOT/'public/images/adaptation-trace.png',dpi=150);plt.close(fig)
 provenance={'source_sha256':summary['source_sha256'],'gzip_sha256':summary['gzip_sha256'],'runtime_source_sha256':d['methodology']['runtime_source_sha256'],'started_at':d['started_at'],'finished_at':d['finished_at'],'aggregation':'Rates, successful p95 and motion counts are medians of three run-level outcomes. Rejection/error percentages pool request counts. SQL wait is total duration / acquisitions that waited, for all services. Chart whiskers are observed min/max, not confidence intervals.','recovery':'First pair of consecutive full1s bins with >=95% critical requests successfully completed within100ms of schedule and no server/transport errors. Zero means the pair starts with the phase. Censored runs remain counted separately.','settling':'Worst service time until sampled baseline stays within one slot of its final1s median; at least1s observed. Missing/censored values retained in per-run summaries.','criteria':d['criteria'],'comparison_checks':d['comparison_checks'],'limitations':'Local sequential workload, exploratory selection before fixed confirmatory matrix, no long-term or multi-host qualification, no measurement of human tuning effort. Legacy defaults already require zero advanced overrides. Priority feedback uses histogram bucket estimates. No SQL contention provider or tenant-sharing policy enabled in this comparison. Controlled signal input tests coordination, not detection.','matplotlib':matplotlib.__version__,'images':['adaptation-comparison.png','adaptation-trace.png']}
 (out/'adaptation-provenance.json').write_text(json.dumps(provenance,indent=2)+'\n');print(summary['sent'],'requests;',summary['passed_checks'],'/',summary['checks'],'checks; passed=',summary['passed'],flush=True)
if __name__=='__main__':main()
