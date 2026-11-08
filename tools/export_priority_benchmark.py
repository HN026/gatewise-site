"""Publish only a complete passed priority-chain experiment and its measured chart."""
import argparse,gzip,hashlib,json,pathlib,statistics
ROOT=pathlib.Path(__file__).resolve().parents[1]
def main():
 p=argparse.ArgumentParser();p.add_argument('--report',type=pathlib.Path,required=True);a=p.parse_args();raw=a.report.read_bytes();d=json.loads(raw)
 assert d.get('passed') and len(d['runs'])==12 and all(r['passed'] for r in d['runs']), 'Need 12 passed runs'
 records=[]
 for profile in ['middleware-fixed','proxy-fixed','middleware-adaptive','proxy-adaptive']:
  runs=[r for r in d['runs'] if r['profile']==profile]
  assert len(runs)==3
  for phase in ['baseline','downstream-pressure','deeper-pressure','recovery']:
   for tier in ['urgent','normal','batch']:
    rows=[next(p for p in r['phases'] if p['name']==phase)['traffic'][tier] for r in runs]
    seconds=next(p for p in runs[0]['phases'] if p['name']==phase)['offered_seconds']
    records.append({'profile':profile,'phase':phase,'tier':tier,'timely_rps':statistics.median(v['successful_within_100ms_per_offered_second'] for v in rows),'timely_percent':statistics.median(v['successful_within_100ms_per_offered_second']*seconds/v['sent']*100 for v in rows),'p95_ms':statistics.median(v['successful_p95_ms'] for v in rows if v['successful_p95_ms'] is not None) if any(v['successful_p95_ms'] is not None for v in rows) else None,'rejected_percent':sum(v['rejected_503'] for v in rows)/sum(v['sent'] for v in rows)*100,'other_5xx_percent':sum(v['backend_5xx'] for v in rows)/sum(v['sent'] for v in rows)*100})
 rows=[v for r in d['runs'] for p in r['phases'] for v in p['traffic'].values()]
 summary={'sha256':hashlib.sha256(raw).hexdigest(),'runs':len(d['runs']),'checks':sum(len(r['checks']) for r in d['runs']),'sent':sum(p['sent'] for r in d['runs'] for p in r['phases']),'missed':sum(p['dropped_schedules'] for r in d['runs'] for p in r['phases']),'transport_errors':sum(v['transport_errors'] for v in rows),'verified_successful_flows':sum(v['chain_verified'] for v in rows),'same_epoch_cohort_comparisons':sum(v['cohort_comparisons'] for v in rows),'cohort_mismatches':sum(v['cohort_mismatches'] for v in rows),'rows':records}
 out=ROOT/'public/data';out.mkdir(exist_ok=True);(out/'priority-results.json').write_bytes(raw);(out/'priority-results.json.gz').write_bytes(gzip.compress(raw,mtime=0));(ROOT/'src/data/priority.json').write_text(json.dumps(summary,indent=2)+'\n')
 import matplotlib;matplotlib.use('Agg');import matplotlib.pyplot as plt
 plt.rcParams.update({'font.family':'DejaVu Sans','font.size':10,'text.color':'#dae8df','axes.labelcolor':'#bccfc2','xtick.color':'#a9bfb0','ytick.color':'#cbdcd1','axes.edgecolor':'#45604f','figure.facecolor':'#101b16','axes.facecolor':'#101b16','savefig.facecolor':'#101b16'})
 fig,axes=plt.subplots(2,2,figsize=(16,13));colors={'urgent':'#76d9bc','normal':'#9db7e5','batch':'#bc9973'}
 for i,phase in enumerate(['downstream-pressure','deeper-pressure']):
  selected=[r for r in records if r['phase']==phase];labels=[r['profile'].replace('middleware-','Middleware / ').replace('proxy-','Proxy / ')+' · '+r['tier'] for r in selected]
  for j,(field,title) in enumerate([('timely_percent','Successful within 100ms / offered requests'),('rejected_percent','Rejected 503 / offered requests')]):
   ax=axes[i,j];ax.barh(range(len(selected)),[r[field] for r in selected],color=[colors[r['tier']] for r in selected],height=.65);ax.set_yticks(range(len(selected)),labels);ax.invert_yaxis();ax.set_xlim(0,100);ax.set_xlabel('Percent');ax.set_title(('40ms SQL delay · 480 RPS' if i==0 else '80ms SQL delay · 720 RPS')+'\n'+title,pad=14);ax.grid(axis='x',alpha=.12);ax.set_axisbelow(True);ax.spines[['top','right']].set_visible(False)
 fig.suptitle('Priority through orders → catalog: useful work and rejection cost',fontsize=19,x=.05,ha='left',y=.98)
 fig.text(.05,.035,'20% urgent / 20% normal / 60% batch · three repetitions per mode · 128 rotating cohorts · local PostgreSQL\nUseful-work bars: median run-level percentage; rejection bars: pooled counts. Successful p95 and other 5xx are reported in the table.',fontsize=10,color='#a9bfb0')
 fig.tight_layout(rect=(.01,.065,1,.94));image=ROOT/'public/images/priority-chain.png';fig.savefig(image,dpi=150);plt.close(fig)
 (out/'priority-chart-provenance.json').write_text(json.dumps({'source_sha256':summary['sha256'],'runs':12,'aggregation':'Median run-level timely percent; pooled 503 counts; per-tier successful p95 is median of run percentiles, not a pooled percentile','matplotlib':matplotlib.__version__,'image':'priority-chain.png'},indent=2)+'\n');print('Published',summary['sent'],'requests;',summary['checks'],'checks;',summary['verified_successful_flows'],'verified flows')
if __name__=='__main__':main()
