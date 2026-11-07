#!/usr/bin/env python3
"""Noisy-neighbor experiment against real, isolated PostgreSQL services."""
import argparse,datetime,hashlib,json,pathlib,platform,subprocess,threading,time,uuid
from run import ROOT,Stack,api
from comparison import ADAPTERS
from benchmark import metrics,metric
# Fixed mixture: all critical business operations, 60/20/20 service distribution.
PHASES=[('baseline',3,8,False,0),('noisy-neighbor',12,480,False,40),('identity-churn',4,400,True,0),('recovery',4,8,False,0)]
def one_run(stack,adapter,repeat,scale):
    adapter.start(stack);trace=[];errors=[];stop=threading.Event();phase=['startup'];start=time.monotonic()
    def sample():
        while not stop.is_set():
            try:
                states={r:api(u,'/state') for r,u in stack.admins.items()};values={}
                if adapter.profile not in ['direct','semaphore']:
                    for url in stack.metrics_urls:values.update(metrics(url))
                for role,state in states.items():
                    v={k:n for k,n in values.items() if 'service="'+role+'"' in k}
                    trace.append({'seconds':time.monotonic()-start,'phase':phase[0],'service':role,'pool_in_use':state['pool']['InUse'],'pool_wait_count':state['pool']['WaitCount'],'pool_wait_seconds':state['pool']['WaitDuration']/1e9,'heap_bytes':state['heap_bytes'],'goroutines':state['goroutines'],'limit':metric(v,'gatewise_concurrency_limit') if v else None,'active':sum(n for k,n in v.items() if k.startswith('gatewise_requests_in_flight{')) if v else None,'tenants_tracked':metric(v,'gatewise_tenants_tracked') if adapter.tenants else None,'tenant_leases':sum(n for k,n in v.items() if k.startswith('gatewise_tenant_requests_in_flight{')) if adapter.tenants else None})
            except Exception as e:errors.append(type(e).__name__)
            stop.wait(.25)
    worker=threading.Thread(target=sample,daemon=True);worker.start();phases=[];run_id=uuid.uuid4().hex[:10]
    try:
        for name,duration,noisy,churn,delay in PHASES:
            stack.control(delay=delay);phase[0]=name
            before={r:api(u,'/state')['pool'] for r,u in stack.admins.items()}
            cfg={'urls':stack.urls,'seconds':duration*scale,'start_rps':noisy+24,'end_rps':noisy+24,'business_slots':5,'run_id':run_id+'-'+name,'tenants':[{'id':'noisy','weight':noisy}]+[{'id':'ordinary-'+str(i),'weight':8} for i in range(1,4)],'unique_noisy':churn}
            process=subprocess.run([str(stack.projects/'commerce-backend/bin/loadgen')],input=json.dumps(cfg),text=True,capture_output=True,check=True);result=json.loads(process.stdout)
            until=time.monotonic()+2;after={r:api(u,'/state')['pool'] for r,u in stack.admins.items()}
            while any(s['InUse'] for s in after.values()) and time.monotonic()<until:time.sleep(.02);after={r:api(u,'/state')['pool'] for r,u in stack.admins.items()}
            result.update(name=name,sql_delay_ms=delay,pools={r:{'wait_count':after[r]['WaitCount']-before[r]['WaitCount'],'wait_seconds':(after[r]['WaitDuration']-before[r]['WaitDuration'])/1e9,'in_use_after_drain':after[r]['InUse']} for r in after});phases.append(result)
            normal=[v for k,v in result['tenant_traffic'].items() if k.startswith('ordinary-')];print(adapter.name,repeat,name,'sent',result['sent'],'ordinary timely/s',sum(v['successful_within_100ms_per_offered_second'] for v in normal),flush=True)
        checks=[{'name':'exact scheduled-arrival accounting','pass':all(p['sent']+p['dropped_schedules']==p['scheduled'] and p['dropped_schedules']==0 for p in phases)},{'name':'no client transport errors','pass':all(v['transport_errors']==0 for p in phases for v in p['traffic'].values())},{'name':'dispatch p95 below 10ms','pass':all((v['dispatch_p95_ms'] or 0)<10 for p in phases for v in p['tenant_traffic'].values())},{'name':'no unexpected statuses','pass':all(v['other_statuses']==0 for p in phases for v in p['traffic'].values())},{'name':'database connections drained','pass':all(s['in_use_after_drain']==0 for p in phases for s in p['pools'].values())},{'name':'monitoring available','pass':not errors}]
        if adapter.tenants:
            attack=next(p for p in phases if p['name']=='noisy-neighbor');churn=next(p for p in phases if p['name']=='identity-churn');recover=next(p for p in phases if p['name']=='recovery')
            checks += [{'name':'registry bounded','pass':all(0<=s['tenants_tracked']<=32 for s in trace)},{'name':'noisy tenant is explicitly limited','pass':attack['tenant_traffic']['noisy']['rejection_reasons'].get('tenant_concurrency_limit',0)>0},{'name':'every ordinary tenant has useful work under attack','pass':all(attack['tenant_traffic']['ordinary-'+str(i)]['successful_within_100ms_per_offered_second']>0 for i in range(1,4))},{'name':'ordinary successful p95 below 100ms under attack','pass':all(attack['tenant_traffic']['ordinary-'+str(i)]['successful_p95_ms'] is not None and attack['tenant_traffic']['ordinary-'+str(i)]['successful_p95_ms']<100 for i in range(1,4))},{'name':'identity churn hits explicit registry bound','pass':churn['tenant_traffic']['churn']['rejection_reasons'].get('tenant_registry_full',0)>0},{'name':'ordinary tenants continue during churn','pass':all(churn['tenant_traffic']['ordinary-'+str(i)]['successful']>0 for i in range(1,4))},{'name':'ordinary recovery has no rejection or 5xx','pass':all(v['rejected_503']==0 and v['backend_5xx']==0 for k,v in recover['tenant_traffic'].items() if k.startswith('ordinary-'))}]
        return {'adapter':adapter.metadata(),'repeat':repeat,'phases':phases,'trace':trace,'sampling_errors':errors,'checks':checks,'passed':all(c['pass'] for c in checks)}
    finally:stop.set();worker.join(timeout=5);stack.stop_app()
def main():
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default='podman');p.add_argument('--repeats',type=int,default=3);p.add_argument('--phase-scale',type=float,default=1);p.add_argument('--adapters',nargs='+',choices=[a.name for a in ADAPTERS]);p.add_argument('--output',type=pathlib.Path,default=ROOT/'results/tenant-results.json');a=p.parse_args()
    if not 1<=a.repeats<=10 or not .2<=a.phase_scale<=3:p.error('repeats 1..10, phase-scale .2..3')
    adapters=[adapter for adapter in ADAPTERS if not a.adapters or adapter.name in a.adapters]
    report={'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'methodology':{'phase_scale':a.phase_scale,'phases':PHASES,'scope':'Local native HTTP services, PostgreSQL container VM; no production capacity claim','generator':'Go open-loop, no application retries, 4096 in-flight generator slots, 3s client timeout','priorities':'All requests critical; identical 60/20/20 service proportions','ordinary_tenants':3,'ordinary_rps_each':8,'tenant_limit':2,'tenant_registry_bound':32,'idle_ttl_seconds':2,'service_max':12,'service_reserved':4,'adaptive_min':5,'pool_connections_each':4,'latency_budget_ms':100,'identity':'Synthetic loopback-only trusted header, not production authentication','reference_semaphore':'12 execution slots, immediate rejection, no reservation; all requests critical'},'runs':[]}
    with Stack(a.projects,a.engine) as stack:
        stack.build();stack.start_db();report.update(postgres_version=stack.db_version,postgres_image=stack.image_digest,platform=platform.system()+'/'+platform.machine(),go_version=subprocess.check_output(['go','version'],text=True).strip(),binary_sha256={n:hashlib.sha256((a.projects/n/'bin'/n).read_bytes()).hexdigest() for n in ['gatewise','commerce-backend']})
        for repeat in range(1,a.repeats+1):
            shift=(repeat-1)%len(adapters)
            for adapter in adapters[shift:]+adapters[:shift]:
                report['runs'].append(one_run(stack,adapter,repeat,a.phase_scale));a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(report,indent=2)+'\n')
    report.update(finished_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),passed=all(r['passed'] for r in report['runs']));a.output.write_text(json.dumps(report,indent=2)+'\n');print('Report',a.output,'passed',report['passed'],flush=True)
    if not report['passed']:raise SystemExit(1)
if __name__=='__main__':main()
