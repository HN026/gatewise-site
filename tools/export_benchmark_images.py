"""Export PNG figures from the recorded overload benchmark report. Requires Matplotlib."""
import hashlib
import json
import pathlib
import statistics

import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
import numpy as np

ROOT = pathlib.Path(__file__).resolve().parents[1]
SOURCE = ROOT/'public/data/benchmark-results.json'


def main():
    report = json.loads(SOURCE.read_text())
    assert report['passed'] and len(report['results']) == 15
    names = ['direct','proxy-fixed','proxy-adaptive','middleware-fixed','middleware-adaptive']
    labels = ['Direct','Proxy / fixed','Proxy / adaptive','Middleware / fixed','Middleware / adaptive']
    colors = ['#ed8a75','#728c7c','#72dfc3','#728c7c','#72dfc3']
    background = '#101a14'
    plt.rcParams.update({'figure.facecolor':background,'axes.facecolor':background,'axes.edgecolor':'#40564a','axes.labelcolor':'#b8ccc0','text.color':'#e0ebe4','xtick.color':'#9eb6a7','ytick.color':'#c3d5c9','grid.color':'#304237','font.family':'DejaVu Sans','font.size':11,'savefig.facecolor':background})
    samples = {}
    for name in names:
        runs = [x for x in report['results'] if x['deployment'] == name]
        samples[name] = {}
        for phase in ['baseline','pressure','recovery']:
            values = [next(p for p in r['phases'] if p['name']==phase) for r in runs]
            samples[name][phase] = {
                'timely':[sum(t['successful_within_100ms_per_second'] for t in p['traffic'].values()) for p in values],
                'p95':[p['traffic']['critical']['success_latency']['p95_ms'] for p in values],
                'active':[p['cumulative_peak_backend_active'] for p in values],
                'limit':[p['end_limit'] for p in values],
            }
    output = ROOT/'public/images'
    output.mkdir(parents=True,exist_ok=True)
    fig, axes = plt.subplots(1,2,figsize=(14,6.7),gridspec_kw={'wspace':.43})
    fig.suptitle('Gatewise: measured behavior under overload',x=.07,ha='left',fontsize=19,fontweight='bold',y=.98)
    fig.text(.07,.91,'Localhost · Apple M4 Pro · 3 independent runs per deployment · pressure phase',color='#9eb6a7',fontsize=11)
    for ax, key, title, unit in [(axes[0],'timely','Successful work within 100ms','Successful requests / second'),(axes[1],'p95','Successful critical-request p95','Milliseconds')]:
        positions = np.arange(5)
        medians = [statistics.median(samples[name]['pressure'][key]) for name in names]
        ax.barh(positions,medians,height=.55,color=colors,zorder=2)
        ax.set_yticks(positions,labels if key=='timely' else labels)
        ax.invert_yaxis()
        ax.set_title(title,loc='left',fontsize=13,pad=18)
        ax.set_xlabel(unit,labelpad=12)
        maximum=max(medians)
        ax.set_xlim(0,maximum*1.27)
        ax.grid(axis='x',alpha=.6,zorder=0)
        for i,name in enumerate(names):
            values=samples[name]['pressure'][key]
            ax.scatter(values,[i-.12,i,i+.12],s=27,facecolors=background,edgecolors='#e0ebe4',linewidths=1,zorder=3)
            ax.text(maximum*1.17,i,f'{medians[i]:.2f}',va='center',fontsize=10)
        ax.spines[['top','right','left']].set_visible(False)
        ax.tick_params(axis='y',length=0,pad=9)
    fig.subplots_adjust(top=.79,bottom=.23,left=.16,right=.95)
    fig.text(.07,.095,'Bars: median of 3 runs. Points: individual runs. P95 values are not pooled percentiles.',fontsize=10,color='#a6beaf')
    fig.text(.07,.055,'Pressure: 400 scheduled RPS, capacity 2. Direct completed more requests, but often exceeded the 100ms budget.',fontsize=10,color='#a6beaf')
    fig.savefig(output/'benchmark-overload.png',dpi=160)
    plt.close(fig)

    fig, axes=plt.subplots(1,2,figsize=(14,6.6),gridspec_kw={'wspace':.38})
    fig.suptitle('Concurrency bounds and adaptive recovery',x=.07,ha='left',fontsize=19,fontweight='bold',y=.97)
    ax=axes[0]
    medians=[statistics.median(samples[n]['pressure']['active']) for n in names]
    ax.barh(np.arange(5),medians,color=colors,height=.55)
    ax.set_yticks(np.arange(5),labels);ax.invert_yaxis();ax.set_xlabel('Cumulative observed peak active handlers')
    ax.set_title('Peak backend work through the pressure phase',loc='left',fontsize=12,pad=20)
    for i,v in enumerate(medians):ax.text(v+3,i,f'{v:.0f}',va='center',fontsize=11)
    ax.set_xlim(0,max(medians)*1.15);ax.grid(axis='x',alpha=.6);ax.set_axisbelow(True)
    ax=axes[1]
    for name,color,marker,style in [('proxy-adaptive','#72dfc3','o','-'),('middleware-adaptive','#ef9b80','x','--')]:
        values=[statistics.median(samples[name][phase]['limit']) for phase in ['baseline','pressure','recovery']]
        ax.plot([0,1,2],values,color=color,marker=marker,markersize=8,linewidth=2,linestyle=style,label=name)
    ax.set_xticks([0,1,2],['Baseline','Pressure','Recovery']);ax.set_ylim(0,7);ax.set_yticks(range(0,8));ax.set_ylabel('Admission limit');ax.set_title('Measured phase-end admission limits',loc='left',fontsize=12,pad=20);ax.grid(alpha=.6);ax.legend(loc='lower right',frameon=False,fontsize=10,labelcolor='#c5d8cb')
    for ax in axes:ax.spines[['top','right']].set_visible(False)
    fig.subplots_adjust(top=.80,bottom=.23,left=.16,right=.96)
    fig.text(.07,.10,'Fixed ceiling: 6. Critical reservation: 1. Adaptive minimum: 2. Each deployment uses its own backend.',fontsize=10,color='#a6beaf')
    fig.text(.07,.06,'Recovery lines connect phase-end medians, not a continuous time series. Both adaptive series overlap at 6 → 4 → 6.',fontsize=10,color='#a6beaf')
    fig.savefig(output/'benchmark-concurrency.png',dpi=160)
    plt.close(fig)
    fig, axes = plt.subplots(1,2,figsize=(14,6.9),gridspec_kw={'wspace':.28})
    fig.suptitle('Measured process CPU time and resident memory',x=.07,ha='left',fontsize=18,fontweight='bold',y=.97)
    for ax,field,scale,title,unit in [(axes[0],'cpu_seconds_entire_child_lifetime',1,'CPU time over the whole child lifetime','CPU seconds'),(axes[1],'observed_peak_rss_kib',1024,'Observed per-process peak RSS','MiB')]:
        for role,offset,color,label in [('backend',-.17,'#7d9786','Backend / application'),('proxy',.17,'#72dfc3','Proxy process')]:
            values=[]
            positions=[]
            for i,name in enumerate(names):
                measured=[v[field]/scale for run in report['results'] if run['deployment']==name for v in run['resources'] if v['role']==role]
                if measured:
                    positions.append(i+offset)
                    values.append(statistics.median(measured))
            ax.bar(positions,values,width=.31,color=color,label=label,zorder=2)
        ax.set_xticks(np.arange(5),['Direct','Proxy\nfixed','Proxy\nadaptive','Middleware\nfixed','Middleware\nadaptive'])
        ax.set_ylabel(unit)
        ax.set_title(title,loc='left',fontsize=12,pad=17)
        ax.grid(axis='y',alpha=.6,zorder=0)
        ax.spines[['top','right']].set_visible(False)
        ax.set_ylim(0,ax.get_ylim()[1]*1.22)
        ax.legend(loc='upper left',frameon=False,fontsize=9,labelcolor='#c5d8cb')
    fig.subplots_adjust(top=.81,bottom=.25,left=.08,right=.97)
    fig.text(.07,.11,'Median per-role values across 3 runs. CPU includes startup, warm-up, traffic, control requests and logging.',fontsize=10,color='#a6beaf')
    fig.text(.07,.065,'RSS is sampled every 250ms and is an observed lower bound. It is not heap size or evidence of a leak.',fontsize=10,color='#a6beaf')
    fig.savefig(output/'benchmark-resources.png',dpi=160)
    plt.close(fig)
    metadata={'source':'15-run overload benchmark report','source_sha256':hashlib.sha256(SOURCE.read_bytes()).hexdigest(),'runs':15,'repetitions_per_deployment':3,'matplotlib':matplotlib.__version__,'images':['benchmark-overload.png','benchmark-concurrency.png','benchmark-resources.png'],'aggregation':'Median of run-level values; no pooled percentile calculation'}
    (ROOT/'public/data/chart-provenance.json').write_text(json.dumps(metadata,indent=2)+'\n')
    print('Exported measured benchmark PNGs with source provenance')


if __name__ == '__main__':
    main()
