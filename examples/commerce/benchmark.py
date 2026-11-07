#!/usr/bin/env python3
"""Real SQL commerce experiment: open-loop arrivals, isolated databases, no application retries."""
import argparse,datetime,hashlib,json,pathlib,platform,subprocess,threading,time,uuid
from run import ROOT,Stack,api,request
PROFILES=['direct','proxy-fixed','proxy-adaptive','middleware-fixed','middleware-adaptive']
# name, seconds, initial/final RPS, business slots of five, report multiplier, SQL delay ms, failure every N
PHASES=[('baseline',5,200,200,4,1,0,0),('ramp',10,200,800,4,5,10,0),('pressure',15,1200,1200,1,30,40,0),('burst',5,2400,2400,1,30,40,0),('intermittent-errors',10,800,800,4,1,5,4),('recovery',15,1200,1200,4,1,0,0),('sustained',60,1500,1500,4,1,0,0),('low-traffic',5,10,10,4,1,0,0)]
def metrics(base):
    code,data,_=request(base,'/metrics');out={}
    if code!=200:raise RuntimeError('metrics unavailable')
    for line in data.decode().splitlines():
        if line.startswith('#'):continue
        parts=line.split()
        if len(parts)==2 and parts[0].startswith('gatewise_'):
            try:out[parts[0]]=float(parts[1])
            except ValueError:pass
    return out

def metric(values,name):return next((v for k,v in values.items() if k==name or k.startswith(name+'{')),0)


def execute(stack,name,seconds,start,end,slots,run_id):
    config={'urls':stack.urls,'seconds':seconds,'start_rps':start,'end_rps':end,'business_slots':slots,'run_id':run_id+'-'+name}
    result=subprocess.run([str(stack.projects/'commerce-backend/bin/loadgen')],input=json.dumps(config),capture_output=True,text=True,check=True)
    return json.loads(result.stdout)

def one_run(stack,profile,repeat,scale):
    stack.start_app(profile);trace=[];stop=threading.Event();phase_name=['warmup'];sampling_errors=[];run_started=time.monotonic()
    def sample():
        while not stop.is_set():
            try:
                states={role:api(url,'/state') for role,url in stack.admins.items()}
                values={}
                if profile!='direct':
                    for url in stack.metrics_urls:values.update(metrics(url))
                for role,state in states.items():
                    v={k:n for k,n in values.items() if 'service="'+role+'"' in k}
                    trace.append({'seconds':time.monotonic()-run_started,'unix_seconds':time.time(),'phase':phase_name[0],'service':role,'pool_in_use':state['pool']['InUse'],'pool_wait_count':state['pool']['WaitCount'],'pool_wait_seconds':state['pool']['WaitDuration']/1e9,'heap_bytes':state['heap_bytes'],'goroutines':state['goroutines'],'limit':metric(v,'gatewise_concurrency_limit') if v else None,'active':sum(n for k,n in v.items() if k.startswith('gatewise_requests_in_flight{')) if v else None,'controller_evaluated_at':metric(v,'gatewise_controller_last_evaluation_timestamp_seconds') if v else None,'controller_samples':metric(v,'gatewise_controller_last_window_samples') if v else None,'controller_p90_ratio':metric(v,'gatewise_controller_last_p90_ratio') if v else None,'controller_failure_fraction':metric(v,'gatewise_controller_last_failure_fraction') if v else None,'controller_decision':next((k for k,n in v.items() if k.startswith('gatewise_controller_last_decision{') and n==1),None)})
            except Exception as e:sampling_errors.append(type(e).__name__)
            stop.wait(.25)
    sampler=threading.Thread(target=sample,daemon=True);sampler.start();phases=[];run_id=uuid.uuid4().hex[:12]
    try:
        for name,duration,start,end,slots,multiplier,delay,failures in PHASES:
            stack.control(multiplier,delay,failures);phase_name[0]=name
            before={r:api(u,'/state')['pool'] for r,u in stack.admins.items()}
            phase_started_unix=time.time()
            result=execute(stack,name,duration*scale,start,end,slots,run_id)
            drain_started=time.monotonic();after={r:api(u,'/state')['pool'] for r,u in stack.admins.items()}
            while any(v['InUse'] for v in after.values()) and time.monotonic()-drain_started<2:
                time.sleep(.02);after={r:api(u,'/state')['pool'] for r,u in stack.admins.items()}
            result['post_response_pool_drain_seconds']=time.monotonic()-drain_started;result['started_unix_seconds']=phase_started_unix
            pools={}
            for r in before:
                waits=after[r]['WaitCount']-before[r]['WaitCount'];wait_seconds=(after[r]['WaitDuration']-before[r]['WaitDuration'])/1e9
                pools[r]={'wait_count':waits,'wait_seconds':wait_seconds,'mean_wait_ms_when_waited':wait_seconds*1000/waits if waits else 0,'in_use_after_drain':after[r]['InUse']}
            result.update(name=name,business_slots=slots,work_multiplier=multiplier,sql_delay_ms=delay,failure_every=failures,pools=pools);phases.append(result)
            print(f'{profile} repeat {repeat}: {name}, sent={result["sent"]}, critical p95={result["traffic"]["critical"]["successful_p95_ms"]}',flush=True)
        checks=[{'name':'recovery business p95 within 100ms','pass':all(t['successful_p95_ms'] is not None and t['successful_p95_ms']<100 for p in phases if p['name']=='recovery' for r,ts in p['services'].items() for priority,t in ts.items() if priority=='critical')},{'name':'low traffic has no admission rejections','pass':all(t['rejected_503']==0 for p in phases if p['name']=='low-traffic' for t in p['traffic'].values())},{'name':'all pools drained within two seconds','pass':all(v['in_use_after_drain']==0 for p in phases for v in p['pools'].values())},{'name':'monitoring available','pass':not sampling_errors},{'name':'offered traffic delivered within 1 percent','pass':all(p['dropped_schedules']<=p['scheduled']*.01 for p in phases)},{'name':'no client transport errors','pass':all(t['transport_errors']==0 for p in phases for t in p['traffic'].values())},{'name':'no unexpected HTTP statuses','pass':all(t['other_statuses']==0 for p in phases for t in p['traffic'].values())}]
        if profile!='direct':checks += [{'name':'active requests within ceiling','pass':all(s['active'] is None or s['active']<=12 for s in trace)}]
        if 'adaptive' in profile:checks += [{'name':'each controller evaluates','pass':all(any(s['service']==r and (s['controller_samples'] or 0)>=10 for s in trace) for r in stack.urls)},{'name':'each controller sheds during degradation','pass':all(any(s['service']==r and s['phase'] in ['pressure','burst'] and 5<=(s['limit'] or 0)<12 for s in trace) for r in stack.urls)},{'name':'adaptive bounds respected','pass':all(s['limit'] is None or 5<=s['limit']<=12 for s in trace)}]
        return {'deployment':profile,'repeat':repeat,'phases':phases,'trace':trace,'sampling_errors':sampling_errors,'checks':checks,'passed':all(c['pass'] for c in checks)}
    finally:stop.set();sampler.join(timeout=5);stack.stop_app()

def main(default_output="commerce-results.json"):
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default='podman');p.add_argument('--repeats',type=int,default=3);p.add_argument('--phase-scale',type=float,default=1);p.add_argument('--profiles',nargs='+',choices=PROFILES,default=PROFILES);p.add_argument('--output',type=pathlib.Path,default=ROOT/'results'/default_output);a=p.parse_args()
    if not 1<=a.repeats<=10 or not .1<=a.phase_scale<=3:p.error('repeats 1..10, phase-scale 0.1..3')
    report={'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'methodology':{'services':['orders','catalog','inventory'],'databases':3,'seed_rows_per_database':20000,'pool_connections_per_service':4,'admission_max_per_service':12,'reserved_critical_per_service':4,'adaptive_min':5,'adaptive_initial':12,'adaptive_interval_ms':500,'minimum_samples':10,'latency_target_ms':100,'query_timeout_ms':1000,'service_mix':'60% orders / 20% catalog / 20% inventory','generator':'Go open-loop, 4096 in-flight slots, 3s client timeout, no application retries','scope':'local host services and PostgreSQL container VM; production-style workload, not a production capacity claim','phase_scale':a.phase_scale,'phases':PHASES},'runs':[]}
    with Stack(a.projects,a.engine) as stack:
        stack.build();stack.start_db();report['hardware']={'host_logical_cpus':__import__('os').cpu_count()};
        if platform.system()=='Darwin':
            report['hardware'].update(cpu=subprocess.check_output(['sysctl','-n','machdep.cpu.brand_string'],text=True).strip(),host_memory_bytes=int(subprocess.check_output(['sysctl','-n','hw.memsize'],text=True)))
        if a.engine=='podman':
            try:
                resources=json.loads(stack.command('machine','inspect'))[0]['Resources'];report['hardware']['postgres_vm_resources']={k:resources[k] for k in ['CPUs','Memory'] if k in resources}
            except (subprocess.SubprocessError,ValueError,KeyError,IndexError):report['hardware']['postgres_vm_resources']=None
        report.update(postgres_version=stack.db_version,postgres_image=stack.image_digest,go_version=subprocess.check_output(['go','version'],text=True).strip(),platform=platform.system()+'/'+platform.machine(),cpu_count=__import__('os').cpu_count());report['binary_sha256']={n:hashlib.sha256((a.projects/n/'bin'/n).read_bytes()).hexdigest() for n in ['gatewise','commerce-backend']}
        for repeat in range(1,a.repeats+1):
            shift=(repeat-1)%len(a.profiles);order=a.profiles[shift:]+a.profiles[:shift]
            for profile in order:
                report['runs'].append(one_run(stack,profile,repeat,a.phase_scale));a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(report,indent=2)+'\n')
    report['finished_at']=datetime.datetime.now(datetime.timezone.utc).isoformat();report['passed']=all(r['passed'] for r in report['runs']);a.output.write_text(json.dumps(report,indent=2)+'\n');print('Report:',a.output,'passed=',report['passed'],flush=True)
    if not report['passed']:raise SystemExit(1)
if __name__=='__main__':main()
