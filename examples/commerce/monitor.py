"""Optional local Prometheus/Grafana infrastructure for the printed demo targets."""
import json,os,pathlib,secrets,shutil,subprocess,time,urllib.parse
from run import api,port,request
class Monitoring:
 def __init__(self,stack,render=False):
  self.stack=stack;self.name=stack.name+'-monitor';self.render=render;self.token=secrets.token_hex(24);self.directory=stack.temp/('observability-'+secrets.token_hex(4));self.started=False
 def start(self):
  s=self.stack;shutil.copytree(s.projects/'gatewise/deploy/observability',self.directory)
  dashboard=pathlib.Path(__file__).parent/'observability/commerce.json';shutil.copy2(dashboard,self.directory/'grafana/dashboards/commerce.json')
  urls=sorted(set(list(s.backends.values())+s.metrics_urls));targets='\n'.join('      - targets: [host.docker.internal:'+str(urllib.parse.urlsplit(url).port)+']\n        labels: {application: '+next((role for role,u in s.backends.items() if u==url),'gateway')+'}' for url in urls)
  (self.directory/'prometheus.yml').write_text('global:\n  scrape_interval: 1s\n  evaluation_interval: 1s\nscrape_configs:\n  - job_name: commerce\n    static_configs:\n'+targets+'\n')
  self.env={**os.environ,'CONTAINER_ENGINE':s.engine,'STACK_NAME':self.name,'PROMETHEUS_PORT':str(port()),'GRAFANA_PORT':str(port())};self.prometheus='http://127.0.0.1:'+self.env['PROMETHEUS_PORT'];self.grafana='http://127.0.0.1:'+self.env['GRAFANA_PORT']
  self.started=True
  if self.render:
   s.command('network','create',self.name)
   s.command('run','-d','--name',self.name+'-renderer','--network',self.name,'--network-alias','renderer','--memory','2g','--cpus','2','-e','AUTH_TOKEN='+self.token,'docker.io/grafana/grafana-image-renderer:latest')
   f=self.directory/'run.sh';f.write_text(f.read_text().replace('-e GF_ANALYTICS_REPORTING_ENABLED=false',f'-e GF_RENDERING_SERVER_URL=http://renderer:8081/render -e GF_RENDERING_CALLBACK_URL=http://{self.name}-grafana:3000/ -e GF_RENDERING_RENDERER_TOKEN={self.token} -e GF_ANALYTICS_REPORTING_ENABLED=false'))
  subprocess.run(['sh',str(self.directory/'run.sh'),'start'],env=self.env,check=True,capture_output=True)
  for url,path in [(self.prometheus,'/api/v1/status/buildinfo'),(self.grafana,'/api/health')]:
   until=time.monotonic()+90
   while time.monotonic()<until:
    try:
     if request(url,path,timeout=2)[0]==200:break
    except Exception:pass
    time.sleep(.3)
   else:raise RuntimeError('Monitoring readiness timeout')
  return self
 def close(self):
  if not self.started:return
  s=self.stack;subprocess.run(['sh',str(self.directory/'run.sh'),'stop'],env=self.env,capture_output=True,timeout=45)
  if self.render:subprocess.run([s.engine,'rm','-f',self.name+'-renderer'],capture_output=True)
  subprocess.run([s.engine,'network','rm',self.name],capture_output=True)
  for suffix in ['prometheus-data','grafana-data']:subprocess.run([s.engine,'volume','rm',self.name+'-'+suffix],capture_output=True)
  self.started=False
