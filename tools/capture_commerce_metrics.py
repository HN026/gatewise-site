"""Capture native dashboard PNGs from the real PostgreSQL commerce services.
This separate monitoring run is not one of the repeated performance runs.
"""
import argparse,base64,datetime,json,pathlib,subprocess,sys,time,urllib.parse,urllib.request
SITE=pathlib.Path(__file__).resolve().parents[1];sys.path.insert(0,str(SITE/'examples/commerce'))
from run import Stack,api,request
from benchmark import execute
from monitor import Monitoring

def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=SITE/'examples/commerce');p.add_argument('--engine',default='podman');a=p.parse_args();record={'source':'Separate local monitoring validation of the PostgreSQL commerce workload; not a repeated performance run','deployments':[],'screenshots':[]}
 with Stack(a.projects,a.engine) as stack:
  stack.build();stack.start_db()
  for profile in ['proxy-adaptive','middleware-adaptive']:
   stack.start_app(profile);monitor=Monitoring(stack,render=True)
   try:
    monitor.start();time.sleep(4);targets=api(monitor.prometheus,'/api/v1/targets')['data']['activeTargets'];assert len(targets)==(4 if profile.startswith('proxy') else 3) and all(t['health']=='up' for t in targets)
    start=int(time.time()*1000);deployment={'profile':profile,'phases':[],'series':{}}
    for phase,seconds,rps,slots,multiplier,delay,errors in [('baseline',10,600,4,1,0,0),('pressure',20,1200,1,30,40,0),('SQL-errors',8,800,4,1,5,4),('recovery',20,1500,4,1,0,0)]:
     stack.control(multiplier,delay,errors);result=execute(stack,phase,seconds,rps,rps,slots,'metrics-'+profile);result.update(name=phase,work_multiplier=multiplier,sql_delay_ms=delay,failure_every=errors);deployment['phases'].append(result)
     assert result['dropped_schedules']==0 and all(t['transport_errors']==0 for t in result['traffic'].values());print(profile,phase,'sent',result['sent'],flush=True)
    time.sleep(3);end=int(time.time()*1000)
    for query in ['gatewise_concurrency_limit','gatewise_requests_rejected_total','gatewise_controller_last_p90_ratio','gatewise_controller_last_failure_fraction','gatewise_controller_last_window_samples','gatewise_controller_last_decision','commerce_db_pool_wait_seconds_total','commerce_db_pool_wait_count_total','commerce_db_pool_in_use','go_memstats_heap_alloc_bytes','go_goroutines','rate(commerce_db_pool_wait_seconds_total[10s]) / rate(commerce_db_pool_wait_count_total[10s])']:
     result=api(monitor.prometheus,'/api/v1/query_range?'+urllib.parse.urlencode({'query':query,'start':start/1000,'end':end/1000,'step':1}));assert result['status']=='success' and result['data']['result'],query;deployment['series'][query]=result['data']['result']
    captures=[(None,'commerce-metrics-'+('proxy' if profile.startswith('proxy') else 'middleware')+'.png')]
    if profile.startswith('proxy'):captures += [(1,'commerce-metrics-limit.png'),(6,'commerce-metrics-pool.png')]
    for panel,filename in captures:
     params={'from':start,'to':end,'width':1600,'height':2000,'theme':'dark','tz':'UTC','kiosk':'tv'}
     if panel:params.update(panelId=panel,width=1200,height=440)
     path=('/render/d-solo/' if panel else '/render/d/')+'gatewise-commerce/commerce?'+urllib.parse.urlencode(params)
     req=urllib.request.Request(monitor.grafana+path,headers={'Authorization':'Basic '+base64.b64encode(b'admin:gatewise-local').decode()})
     with urllib.request.urlopen(req,timeout=120) as response:image=response.read()
     assert image.startswith(b'\x89PNG\r\n\x1a\n');(SITE/'public/images'/filename).write_bytes(image);record['screenshots'].append({'file':filename,'profile':profile,'from_ms':start,'to_ms':end,'bytes':len(image),'source':'Grafana native rendering endpoint'});print('CAPTURED',filename,len(image),flush=True)
    deployment.update(from_ms=start,to_ms=end);record['deployments'].append(deployment);(SITE/'public/data/commerce-metrics-capture.json').write_text(json.dumps(record,indent=2)+'\n')
    if profile.startswith('proxy'):(SITE/'public/data/commerce-metrics-sample.prom').write_bytes(request(stack.metrics_urls[0],'/metrics')[1])
   finally:monitor.close();stack.stop_app()
  record.update(captured_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),postgres_version=stack.db_version,postgres_image=stack.image_digest,renderer_image=stack.command('image','inspect','docker.io/grafana/grafana-image-renderer:latest','--format','{{index .RepoDigests 0}}'))
 (SITE/'public/data/commerce-metrics-capture.json').write_text(json.dumps(record,indent=2)+'\n');print('Saved dashboard PNGs and source time series',flush=True)
if __name__=='__main__':main()
