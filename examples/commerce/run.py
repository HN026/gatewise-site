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
    def start_app(self,profile):
        self.stop_app();self.databases=[];self.backends={};self.admins={};self.urls={}
        mode=profile.removeprefix('middleware-') if profile.startswith('middleware-') else 'direct'
        env={k:v for k,v in os.environ.items() if not k.startswith(('GATEWISE_','COMMERCE_'))}
        for role in ['orders','catalog','inventory']:
            database='gw_'+uuid.uuid4().hex;self.sql('CREATE DATABASE '+database);self.databases.append(database)
            business,admin=port(),port();self.backends[role]=f'http://127.0.0.1:{business}';self.admins[role]=f'http://127.0.0.1:{admin}'
            service_env={**env,'DATABASE_URL':f'postgres://gatewise:gatewise-local@127.0.0.1:{self.dbport}/{database}?sslmode=disable'}
            args=[str(self.projects/'commerce-backend/bin/commerce-backend'),'-role',role,'-listen',f'127.0.0.1:{business}','-admin-listen',f'127.0.0.1:{admin}','-mode',mode]
            process=self.start_process(role+'-'+profile,args,service_env);self.ready(self.backends[role],process)
        self.urls=dict(self.backends);self.metrics_urls=list(self.backends.values())
        if profile.startswith('proxy-'):
            proxy=port();cfg={'listen_addr':f'127.0.0.1:{proxy}','services':{},'routes':[]}
            for role in self.backends:
                service={'upstream_url':self.backends[role],'max_concurrent':12,'reserved_critical':4}
                if profile=='proxy-adaptive':service.update(mode='adaptive',adaptive={'min_concurrent':5,'initial_concurrent':12,'interval':'500ms','min_samples':10,'target_latency':'100ms'})
                cfg['services'][role]=service
                cfg['routes'] += [{'path_prefix':'/'+role+'/reports','service':role,'priority':'background'},{'path_prefix':'/'+role,'service':role,'priority':'critical'}]
            config=self.temp/'proxy.json';config.write_text(json.dumps(cfg));process=self.start_process(profile,[str(self.projects/'gatewise/bin/gatewise'),'-config',str(config),'-env-file',''],env)
            public=f'http://127.0.0.1:{proxy}';self.ready(public,process);self.urls={role:public for role in self.backends};self.metrics_urls=[public]
        self.profile=profile;return self
    def control(self,multiplier=1,delay=0,failures=0):
        return {role:api(url,'/control',{'work_multiplier':multiplier,'sql_delay_ms':delay,'failure_every':failures},'PUT') for role,url in self.admins.items()}
    def environment(self):
        values=[{'key':role+'_url','value':url,'enabled':True} for role,url in self.urls.items()]+[{'key':role+'_admin','value':url,'enabled':True} for role,url in self.admins.items()]+[{'key':role+'_backend','value':url,'enabled':True} for role,url in self.backends.items()]+[{'key':'expected_mode','value':self.profile.removeprefix('proxy-').removeprefix('middleware-'),'enabled':True}]
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
            if self.container:subprocess.run([self.engine,'rm','-f',self.name],capture_output=True);self.container=False
            if self.volume_created:subprocess.run([self.engine,'volume','rm',self.volume],capture_output=True);self.volume_created=False
    def __enter__(self):return self
    def __exit__(self,*_):self.close()

def main():
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default=os.environ.get('CONTAINER_ENGINE','podman'));p.add_argument('--profile',choices=['direct','proxy-fixed','proxy-adaptive','middleware-fixed','middleware-adaptive'],default='middleware-adaptive');p.add_argument('--monitor',action='store_true',help='start optional local Prometheus and Grafana');p.add_argument('--verify',action='store_true',help='run the Postman contract and clean up');a=p.parse_args()
    with Stack(a.projects,a.engine) as stack:
        stack.build();stack.start_db();stack.start_app(a.profile);environment=stack.environment()
        print(json.dumps({'services':stack.urls,'backends':stack.backends,'metrics':[url+'/metrics' for url in stack.metrics_urls],'controls':stack.admins,'postman_environment':str(environment),'profile':a.profile}),flush=True)
        monitoring=None
        try:
            if a.monitor:
                from monitor import Monitoring
                monitoring=Monitoring(stack);monitoring.start();print(json.dumps({'grafana':monitoring.grafana+'/d/gatewise-commerce','prometheus':monitoring.prometheus}),flush=True)
            if a.verify:
                subprocess.run(['postman','collection','run',str(ROOT/'postman/Commerce.postman_collection.json'),'-e',str(environment),'--no-report-events','--timeout-request','5000','--timeout-script','15000'],check=True)
                subprocess.run(['postman','status'],cwd=ROOT,check=False)
            else:
                print('Ctrl+C stops this example and removes its PostgreSQL volume.',flush=True)
                try:
                    while True:time.sleep(.5)
                except KeyboardInterrupt:pass
        finally:
            if monitoring:monitoring.close()
if __name__=='__main__':main()
