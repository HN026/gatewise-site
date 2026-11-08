#!/usr/bin/env python3
"""Exercise actual trusted/untrusted HTTP entries during downstream congestion."""
import argparse,concurrent.futures,datetime,json,pathlib,threading,time
from run import ROOT,Stack,request
PROFILES=['middleware-fixed','proxy-fixed','middleware-adaptive','proxy-adaptive']
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default='podman');p.add_argument('--output',type=pathlib.Path,default=ROOT/'results/priority-validation.json');a=p.parse_args();runs=[]
 with Stack(a.projects,a.engine) as s:
  s.build();s.start_db()
  for profile in PROFILES:
   for untrusted in [False,True]:
    s.start_app(profile,priorities=True,untrusted_entry=untrusted);base=s.urls['orders'];checks=[];results=[]
    def call(path='/orders/flows/urgent',headers=None):return request(base,path,headers=headers)
    status,b,_=call();flow=json.loads(b);checks.append({'name':'missing metadata uses server priority through chain','pass':status==200 and flow['upstream']['Tier']=='urgent' and flow['downstream']['priority']['Tier']=='urgent'})
    s.control(delay=40);stop=threading.Event();background=[]
    def fill():
     while not stop.is_set():
      code,body,_=call('/orders/flows/batch');background.append(code)
      if code==503:time.sleep(.003)
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
     futures=[pool.submit(fill) for _ in range(8)];time.sleep(.25)
     try:
      for label,value in [('valid urgent promotion','v1;urgent;'+'a'*64),('malformed','bad'),('unknown tier','v1;admin;-'),('invalid cohort','v1;urgent;bad')]:
       status,b,_=call('/orders/flows/batch',{'X-Gatewise-Priority':value,'X-Forwarded-For':'127.0.0.1','Forwarded':'for=127.0.0.1'})
       expected=403 if untrusted else (None if label=='valid urgent promotion' else 400)
       ok=status==expected if expected else status in [200,503]
       if not untrusted and label=='valid urgent promotion' and status==200:
        v=json.loads(b);ok=ok and v['upstream']['Tier']=='urgent' and v['downstream']['priority']['Tier']=='urgent'
       checks.append({'name':label+' under pressure','pass':ok});results.append({'case':label,'status':status,'reason':json.loads(b).get('reason')})
     finally:stop.set()
     for f in futures:f.result()
    checks += [{'name':'real background load reached service','pass':len(background)>0 and all(c in [200,503] for c in background)},{'name':'background made useful progress','pass':200 in background}]
    runs.append({'profile':profile,'entry_trust':'none' if untrusted else 'loopback','results':results,'background_statuses':{str(c):background.count(c) for c in set(background)},'checks':checks,'passed':all(c['pass'] for c in checks)});print(profile,'untrusted',untrusted,'passed',runs[-1]['passed'],results,flush=True)
 report={'recorded_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'scope':'Real localhost HTTP; orders entry explicitly trusts loopback or nobody; catalog still trusts its upstream. 8 concurrent background flow clients during 40ms SQL delay. Forwarded headers never establish peer trust.','runs':runs,'checks':sum(len(r['checks']) for r in runs),'passed':all(r['passed'] for r in runs)};a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(report,indent=2)+'\n');print('Passed',report['passed'],'checks',report['checks'])
 if not report['passed']:raise SystemExit(1)
if __name__=='__main__':main()
