#!/usr/bin/env python3
"""Run the isolated commerce example. This module is also used by benchmark.py."""
import argparse, contextlib, datetime, json, os, pathlib, shutil, signal, socket, subprocess, tempfile, time, urllib.request, uuid
ROOT=pathlib.Path(__file__).resolve().parent
IMAGE='docker.io/library/postgres:18.6-alpine'

def port():
    with socket.socket() as s:s.bind(('127.0.0.1',0));return s.getsockname()[1]

def request(base,path,body=None,method=None,headers=None,timeout=3):
    import urllib.error
    data=None if body is None else json.dumps(body).encode()
    h={'Content-Type':'application/json',**(headers or {})}
    req=urllib.request.Request(base+path,data=data,headers=h,method=method)
    try:
        with urllib.request.urlopen(req,timeout=timeout) as r:return r.status,r.read(),dict(r.headers)
    except urllib.error.HTTPError as e:return e.code,e.read(),dict(e.headers)

def api(base,path,body=None,method=None):
    code,data,_=request(base,path,body,method)
    if code!=200:raise RuntimeError(f'{path}: HTTP {code}: {data[:200]!r}')
    return json.loads(data)

class Stack:
    def __init__(self,projects,engine='podman',image=IMAGE):
        self.projects=pathlib.Path(projects).resolve();self.engine=engine;self.image=image
        self.temp=pathlib.Path(tempfile.mkdtemp(prefix='gatewise-postgres-'))
        self.name='gatewise-commerce-'+uuid.uuid4().hex[:10];self.volume=self.name+'-data'
        self.children=[];self.logs=[];self.container=False;self.volume_created=False
    def command(self,*args,**kw):
        return subprocess.run([self.engine,*args],check=True,capture_output=True,text=True,**kw).stdout.strip()
    def build(self):
        for module in ['gatewise','commerce-backend']:
            subprocess.run([os.environ.get('GO_BIN','go'),'build','-o',f'bin/{module}',f'./cmd/{module}'],cwd=self.projects/module,env={**os.environ,'GOWORK':'off'},check=True)
        subprocess.run([os.environ.get("GO_BIN","go"),"build","-o","bin/loadgen","./cmd/loadgen"],cwd=self.projects/"commerce-backend",env={**os.environ,"GOWORK":"off"},check=True)
    def start_db(self):
        self.dbport=port();self.command('volume','create',self.volume);self.volume_created=True
        self.command('run','--rm','-d','--name',self.name,'-p',f'127.0.0.1:{self.dbport}:5432','-e','POSTGRES_USER=gatewise','-e','POSTGRES_PASSWORD=gatewise-local','-e','POSTGRES_DB=gatewise','-v',f'{self.volume}:/var/lib/postgresql',self.image)
        self.container=True
        deadline=time.monotonic()+45
        while time.monotonic()<deadline:
            try:
                self.command('exec',self.name,'pg_isready','-h','127.0.0.1','-U','gatewise',timeout=3)
                self.db_version=self.command('exec',self.name,'psql','-U','gatewise','-d','gatewise','-Atc','SHOW server_version')
                self.image_digest=self.command('image','inspect',self.image,'--format','{{index .RepoDigests 0}}')
                return
            except subprocess.SubprocessError:time.sleep(.2)
        raise RuntimeError('PostgreSQL did not become ready')
    def start_standby(self):
        if getattr(self,'standby_created',False):return
        self.network=self.name+'-replication';self.standby=self.name+'-standby';self.standby_volume=self.standby+'-data';self.replica_port=port()
        self.command('network','create',self.network);self.network_created=True
        self.command('network','connect','--alias','primary',self.network,self.name)
        self.sql("CREATE ROLE gatewise_replicator WITH REPLICATION LOGIN PASSWORD 'gatewise-local'")
        self.sql("ALTER SYSTEM SET wal_keep_size='64MB'")
        self.command('exec',self.name,'sh','-c',"echo 'host replication gatewise_replicator all scram-sha-256' >> /var/lib/postgresql/18/docker/pg_hba.conf")
        self.sql('SELECT pg_reload_conf()')
        self.command('volume','create',self.standby_volume);self.standby_volume_created=True
        self.command('run','--rm','-v',self.standby_volume+':/var/lib/postgresql',self.image,'sh','-c','mkdir -p /var/lib/postgresql/18/docker && chown -R postgres:postgres /var/lib/postgresql')
        self.command('run','--rm','--user','postgres','--network',self.network,'-v',self.standby_volume+':/var/lib/postgresql',self.image,'pg_basebackup','-D','/var/lib/postgresql/18/docker','-R','-X','stream','-d','host=primary user=gatewise_replicator password=gatewise-local application_name=gatewise_standby',timeout=90)
        self.command('run','-d','--name',self.standby,'--network',self.network,'-p',f'127.0.0.1:{self.replica_port}:5432','-e','PGDATA=/var/lib/postgresql/18/docker','-v',self.standby_volume+':/var/lib/postgresql',self.image,'postgres','-c','hot_standby=on','-c','wal_receiver_status_interval=1s')
        self.standby_created=True;self.wait_standby()
    def standby_sql(self,text):
        return self.command('exec',self.standby,'psql','-U','gatewise','-d','gatewise','-v','ON_ERROR_STOP=1','-Atc',text)
    def wait_standby(self):
        until=time.monotonic()+30
        while time.monotonic()<until:
            try:
                if self.standby_sql('SELECT pg_is_in_recovery()')=='t' and self.sql("SELECT count(*) FROM pg_stat_replication WHERE application_name='gatewise_standby' AND state='streaming'")=='1':return
            except subprocess.SubprocessError:pass
            time.sleep(.1)
        raise RuntimeError('Physical standby not ready')
    def pause_replay(self):
        self.standby_sql('SELECT pg_wal_replay_pause()')
        until=time.monotonic()+5
        while time.monotonic()<until:
            if self.standby_sql('SELECT pg_get_wal_replay_pause_state()')=='paused':return
            time.sleep(.05)
        raise RuntimeError('Replay pause not acknowledged')
    def resume_replay(self):self.standby_sql('SELECT pg_wal_replay_resume()')
    def stop_standby(self):self.command('stop','-t','3',self.standby)
    def restart_standby(self):self.command('start',self.standby);self.wait_standby()
    def sql(self,text):return self.command('exec',self.name,'psql','-U','gatewise','-d','gatewise','-v','ON_ERROR_STOP=1','-Atc',text)
    def start_process(self,name,args,env):
        log=(self.temp/(name+'.log')).open('w');self.logs.append(log)
        p=subprocess.Popen(args,env=env,stdout=log,stderr=log);self.children.append(p);return p
    def ready(self,url,p):
        until=time.monotonic()+20
        while time.monotonic()<until:
            if p.poll() is not None:raise RuntimeError('Service stopped; logs: '+str(self.temp))
            try:
                if request(url,'/healthz',timeout=.5)[0]==200:return
            except Exception:pass
            time.sleep(.05)
        raise RuntimeError('Service readiness timed out; logs: '+str(self.temp))
    def start_app(self,profile,tenants=False,priorities=False,untrusted_entry=False,sharing=0,decisions="",resources="",operations="",adaptive_settings=None,route_budgets=False,queue_settings=None,execution_limit=12,reserved=4):
        self.queue_settings=queue_settings
        self.execution_limit=execution_limit
        self.reserved=reserved
        self.controller_profile=(adaptive_settings or {}).get("profile","legacy")
        self.operation_mode=operations
        if operations:self.start_standby()
        self.resource_mode=resources
        self.decision_mode=decisions
        self.priorities_enabled=priorities
        self.tenants_enabled=tenants
        if sharing:tenants=True;self.tenants_enabled=True
        self.sharing_ceiling=sharing
        self.stop_app();self.databases=[];self.backends={};self.admins={};self.urls={}
        mode=profile.removeprefix('middleware-') if profile.startswith('middleware-') else ('semaphore' if profile=='semaphore' else 'direct')
        env={k:v for k,v in os.environ.items() if not k.startswith(('GATEWISE_','COMMERCE_'))}
        proxy_port=port() if profile.startswith('proxy-') else None
        addresses={role:(port(),port()) for role in ['orders','catalog','inventory']}
        catalog_url=f'http://127.0.0.1:{proxy_port or addresses["catalog"][0]}'
        for role in ['orders','catalog','inventory']:
            database='gw_'+uuid.uuid4().hex;self.sql('CREATE DATABASE '+database);self.databases.append(database)
            business,admin=addresses[role];self.backends[role]=f'http://127.0.0.1:{business}';self.admins[role]=f'http://127.0.0.1:{admin}'
            service_env={**env,'DATABASE_URL':f'postgres://gatewise:gatewise-local@127.0.0.1:{self.dbport}/{database}?sslmode=disable'}
            args=[str(self.projects/'commerce-backend/bin/commerce-backend'),'-role',role,'-listen',f'127.0.0.1:{business}','-admin-listen',f'127.0.0.1:{admin}','-mode',mode]
            args += ['-max-concurrent',str(execution_limit),'-reserved-critical',str(reserved)]
            if queue_settings:
                tier='urgent' if priorities else 'critical'
                args += ['-queue-waiting',str(queue_settings['max_waiting']),'-queue-wait',queue_settings['max_wait'],'-queue-aging',queue_settings.get('aging_after','0s'),'-queue-reserved',str(queue_settings.get('reserved',{}).get(tier,0))]
            if adaptive_settings is not None and mode=='adaptive':
                flags={'min_concurrent':'-min-concurrent','initial_concurrent':'-initial-concurrent','min_samples':'-min-samples','interval':'-adaptive-interval','target_latency':'-target-latency'}
                resolved={'min_concurrent':0,'initial_concurrent':0,'min_samples':0,'interval':'0s','target_latency':'100ms',**adaptive_settings}
                for key,flag in flags.items():args += [flag,str(resolved[key])]
                if resolved.get('profile'):args += ['-adaptive-profile',resolved['profile']]
            if route_budgets:args += ['-route-budgets']
            if operations and role=='orders':args += ['-operations',operations,'-replica-url',f'postgres://gatewise:gatewise-local@127.0.0.1:{self.replica_port}/{database}?sslmode=disable']
            if resources:args += ["-resources",resources]
            if decisions:args += ["-decisions",decisions]
            if priorities:
                args += ['-priorities','-catalog-url',catalog_url]
                if untrusted_entry and role=='orders' and not profile.startswith('proxy-'):args += ['-priority-trusted-proxies','']
                if role=='catalog':args += ['-max-concurrent','6','-initial-concurrent','6']
            if tenants and mode!='direct':args += ['-tenant-limit','2','-tenant-max-tracked','32','-tenant-idle-ttl','2s']
            if sharing and mode!='direct':args += ['-tenant-sharing-ceiling',str(sharing)]
            process=self.start_process(role+'-'+profile,args,service_env);self.ready(self.backends[role],process)
        self.urls=dict(self.backends);self.metrics_urls=list(self.backends.values())
        if profile.startswith('proxy-'):
            proxy=proxy_port;cfg={'listen_addr':f'127.0.0.1:{proxy}','services':{},'routes':[]}
            if queue_settings:cfg['read_timeout']='5s'
            for role in self.backends:
                service={'upstream_url':self.backends[role],'max_concurrent':execution_limit,'reserved_critical':reserved}
                if profile=='proxy-adaptive':service.update(mode='adaptive',adaptive={'min_concurrent':5,'initial_concurrent':12,'interval':'500ms','min_samples':10,'target_latency':'100ms'})
                if adaptive_settings is not None and profile=='proxy-adaptive':service['adaptive']=adaptive_settings
                if queue_settings:service['queue']=queue_settings
                if priorities:
                    service.pop('reserved_critical');service['max_concurrent']=6 if role=='catalog' else 12
                    if profile=='proxy-adaptive' and role=='catalog':service['adaptive']['initial_concurrent']=6
                    service['priorities']={'tiers':[{'name':'urgent','reserved':2},{'name':'normal','reserved':1},{'name':'batch','reserved':0}],'trusted_proxies':['127.0.0.1/32','::1/128'],'cohorts':{'buckets':128,'rotation':'2s','seed':'demo-only-cohort-seed'}}
                if priorities and untrusted_entry and role=='orders':service['priorities']['trusted_proxies']=[]
                if tenants:service['tenants']={'max_concurrent':2,'max_tracked':32,'idle_ttl':'2s','header':'X-Demo-Tenant','trusted_proxies':['127.0.0.1/32','::1/128']}
                if decisions:
                    service['decisions']={'mode':decisions,'signals':[{'name':name,'threshold':.8,'max_age':'600ms','on_unknown':'limit' if name=='primary' else 'allow',**({'unknown_limit':2} if name=='primary' else {}),'pressure':{'max_concurrent':4} if name=='primary' else {'reject':True},'source':{'url':self.admins[role]+'/signals/'+name,'interval':'100ms','timeout':'50ms'}} for name in ['primary','secondary']]}
                if resources and resources!='disabled':
                    service['decisions']={'mode':resources,'cost_metadata':{'trusted_peers':['127.0.0.1/32','::1/128']},'signals':[{'name':name,'scope':{'kind':'resource','key':'allocation' if name=='memory' else 'fanout'},'threshold':.65 if name=='memory' else 180,'max_age':'1s','on_unknown':'limit','unknown_limit':2,'pressure':{'max_concurrent':2},'source':{'url':self.admins[role]+'/resource/signals/'+name,'interval':'50ms','timeout':'40ms'}} for name in ['memory','goroutines']],'budgets':[{'name':'writes','resource':'write','max_write_bytes':262144},{'name':'keys','resource':'key_write','keys':{'max_concurrent':1,'max_tracked':32,'idle_ttl':'2s','missing':'reject','overflow':'shared'}}]}
                if sharing:service['tenants']['sharing']={'max_concurrent':sharing,'demand_ttl':'500ms'}
                cfg['services'][role]=service
                if resources:
                    cfg['routes'] += [{'path_prefix':'/'+role+'/resource/'+kind,'service':role,'priority':'critical','resource':kind} for kind in ['allocation','fanout','write','key_write']]
                cfg['routes'] += [{'path_prefix':'/'+role+'/reports','service':role,'priority':'background'},{'path_prefix':'/'+role,'service':role,'priority':'critical'}]
            if priorities:
                cfg['routes'] += [{'path_prefix':'/orders/flows/'+tier,'service':'orders','priority':tier} for tier in ['urgent','normal','batch']]
                for route in cfg['routes']:
                    if route['path_prefix']=='/catalog':route['priority']='batch'
            if route_budgets:
                for route in cfg['routes']:
                    if '/reports' in route['path_prefix']:route['latency_target']='500ms'
            config=self.temp/'proxy.json';config.write_text(json.dumps(cfg));process=self.start_process(profile,[str(self.projects/'gatewise/bin/gatewise'),'-config',str(config),'-env-file',''],env)
            public=f'http://127.0.0.1:{proxy}';self.ready(public,process);self.urls={role:public for role in self.backends};self.metrics_urls=[public]
        self.profile=profile;return self
    def control(self,multiplier=1,delay=0,failures=0):
        return {role:api(url,'/control',{'work_multiplier':multiplier,'sql_delay_ms':delay,'failure_every':failures},'PUT') for role,url in self.admins.items()}
    def environment(self):
        values=[{'key':'queue_enabled','value':bool(getattr(self,'queue_settings',None)),'enabled':True},{'key':'execution_limit','value':str(getattr(self,'execution_limit',12)),'enabled':True}]+[{'key':'controller_profile','value':getattr(self,'controller_profile','legacy'),'enabled':True},{'key':'operation_mode','value':getattr(self,'operation_mode',''),'enabled':True},{'key':'resource_mode','value':getattr(self,'resource_mode',''),'enabled':True}]+[{'key':'decision_mode','value':getattr(self,'decision_mode',''),'enabled':True}]+[{'key':role+'_url','value':url,'enabled':True} for role,url in self.urls.items()]+[{'key':role+'_admin','value':url,'enabled':True} for role,url in self.admins.items()]+[{'key':role+'_backend','value':url,'enabled':True} for role,url in self.backends.items()]+[{'key':'tenant_header','value':'X-Demo-Tenant','enabled':True},{'key':'tenant_max_tracked','value':'32','enabled':True},{'key':'expected_mode','value':self.profile.removeprefix('proxy-').removeprefix('middleware-'),'enabled':True}]
        path=self.temp/'Commerce.postman_environment.json';path.write_text(json.dumps({'name':'Gatewise commerce example','values':values,'_postman_variable_scope':'environment'},indent=2));return path
    def stop_app(self):
        for p in self.children:
            if p.poll() is None:p.terminate()
        for p in self.children:
            try:p.wait(timeout=7)
            except subprocess.TimeoutExpired:p.kill();p.wait()
        self.children=[]
        for log in self.logs:log.close()
        self.logs=[]
        for database in getattr(self,'databases',[]):self.sql('DROP DATABASE IF EXISTS '+database+' WITH (FORCE)')
        self.databases=[]
    def close(self):
        try:
            if self.container:self.stop_app()
        finally:
            if getattr(self,'standby_created',False):subprocess.run([self.engine,'rm','-f',self.standby],capture_output=True);self.standby_created=False
            if getattr(self,'standby_volume_created',False):subprocess.run([self.engine,'volume','rm',self.standby_volume],capture_output=True);self.standby_volume_created=False
            if getattr(self,'network_created',False):
                subprocess.run([self.engine,'network','disconnect',self.network,self.name],capture_output=True)
                subprocess.run([self.engine,'network','rm',self.network],capture_output=True);self.network_created=False
            if self.container:subprocess.run([self.engine,'rm','-f',self.name],capture_output=True);self.container=False
            if self.volume_created:subprocess.run([self.engine,'volume','rm',self.volume],capture_output=True);self.volume_created=False
    def __enter__(self):return self
    def __exit__(self,*_):self.close()

def main():
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default=os.environ.get('CONTAINER_ENGINE','podman'));p.add_argument('--profile',choices=['direct','proxy-fixed','proxy-adaptive','middleware-fixed','middleware-adaptive'],default='middleware-adaptive');p.add_argument('--controller-profile',choices=['legacy','stable'],default='',help='use controller defaults with selected profile; adaptive deployments only');p.add_argument('--queue',action='store_true',help='bounded waiting:16 slots,40ms maximum,20ms aging,4 critical slots');p.add_argument('--max-concurrent',type=int,default=12);p.add_argument('--reserved-critical',type=int,default=4);p.add_argument('--route-budgets',action='store_true',help='example500ms report latency budget');p.add_argument('--operations',choices=['disabled','fixed','pool','balanced'],default='',help='SQL/worker/physical-standby example');p.add_argument('--resources',choices=['disabled','enforce','observe'],default='',help='real resource regulators and pressure operations');p.add_argument('--decisions',choices=['enforce','observe'],default='',help='controlled providers for decision-engine validation');p.add_argument('--tenants',action='store_true',help='enable example tenant limits: 2 per tenant, 32 identities, 2s idle TTL');p.add_argument('--sharing',type=int,default=0,help='enable tenant sharing with this borrowing ceiling (example: 4)');p.add_argument('--priorities',action='store_true',help='enable priority tiers, cohorts and orders-to-catalog flow');p.add_argument('--monitor',action='store_true',help='start optional local Prometheus and Grafana');p.add_argument('--verify',action='store_true',help='run the Postman contract and clean up');a=p.parse_args()
    if a.queue and a.profile=='direct':p.error('--queue requires middleware or proxy')
    if a.queue and a.priorities:p.error('run focused queue and priority demo contracts separately')
    if a.controller_profile and not a.profile.endswith('adaptive'):p.error('--controller-profile requires an adaptive deployment')
    if a.operations and (a.resources or a.decisions or a.tenants or a.sharing or a.priorities):p.error('--operations is a focused scenario')
    if a.resources and (a.profile=='direct' or a.decisions or a.tenants or a.sharing or a.priorities):p.error('--resources needs a standalone proxy/middleware validation profile')
    if a.decisions and (a.profile=='direct' or a.tenants or a.sharing or a.priorities):p.error('--decisions needs an independent proxy/middleware validation profile')
    if a.sharing and a.profile=='direct':p.error('--sharing needs proxy or middleware admission')
    if a.sharing and a.priorities:p.error('run sharing and priority demo contracts separately')
    if a.priorities and a.tenants:p.error('run the focused priority and tenant contracts separately')
    if a.priorities and a.profile=='direct':p.error('--priorities needs proxy or middleware admission')
    if a.tenants and a.profile=='direct':p.error('--tenants needs proxy or middleware admission')
    with Stack(a.projects,a.engine) as stack:
        stack.build();stack.start_db();stack.start_app(a.profile,a.tenants,a.priorities,sharing=a.sharing,decisions=a.decisions,resources=a.resources,operations=a.operations,adaptive_settings={"profile":a.controller_profile} if a.controller_profile else None,route_budgets=a.route_budgets,queue_settings={'max_waiting':16,'max_wait':'40ms','aging_after':'20ms','reserved':{'critical':4}} if a.queue else None,execution_limit=a.max_concurrent,reserved=a.reserved_critical);environment=stack.environment()
        print(json.dumps({'services':stack.urls,'backends':stack.backends,'metrics':[url+'/metrics' for url in stack.metrics_urls],'controls':stack.admins,'postman_environment':str(environment),'profile':a.profile}),flush=True)
        monitoring=None
        try:
            if a.monitor:
                from monitor import Monitoring
                monitoring=Monitoring(stack);monitoring.start();print(json.dumps({'grafana':monitoring.grafana+'/d/gatewise-commerce','prometheus':monitoring.prometheus}),flush=True)
            if a.verify:
                subprocess.run(['postman','collection','run',str(ROOT/'postman'/('Queue.postman_collection.json' if a.queue else 'Adaptive.postman_collection.json' if a.controller_profile=='stable' else 'Operations.postman_collection.json' if a.operations else 'Resources.postman_collection.json' if a.resources else 'Signals.postman_collection.json' if a.decisions else 'Sharing.postman_collection.json' if a.sharing else 'Priorities.postman_collection.json' if a.priorities else ('Tenants.postman_collection.json' if a.tenants else 'Commerce.postman_collection.json'))),'-e',str(environment),'--no-report-events','--timeout-request','5000','--timeout-script','15000'],check=True)
                subprocess.run(['postman','status'],cwd=ROOT,check=False)
            else:
                print('Ctrl+C stops this example and removes its PostgreSQL volume.',flush=True)
                try:
                    while True:time.sleep(.5)
                except KeyboardInterrupt:pass
        finally:
            if monitoring:monitoring.close()
if __name__=='__main__':main()
