#!/usr/bin/env python3
"""Verify the distributed artifact, not the developer checkout; no workspace publication."""
import hashlib,json,pathlib,re,subprocess,sys,tempfile,zipfile
import argparse,shutil
parser=argparse.ArgumentParser(description='Validate the exact downloadable demo and all saved Postman profiles against isolated PostgreSQL.')
parser.add_argument('--engine',default=shutil.which('podman') or shutil.which('docker') or 'docker')
args=parser.parse_args()
site=pathlib.Path(__file__).resolve().parents[1];(site/'examples/commerce/results').mkdir(parents=True,exist_ok=True);archive=site/'public/downloads/gatewise-demo.zip' ;digest=hashlib.sha256(archive.read_bytes()).hexdigest();assert digest==(site/'public/downloads/gatewise-demo.sha256').read_text().split()[0]
folder=pathlib.Path(tempfile.mkdtemp(prefix='gatewise-v1-verify-'))
with zipfile.ZipFile(archive) as z:assert z.testzip() is None;z.extractall(folder)
root=folder/'gatewise-demo';sys.path.insert(0,str(root));from run import Stack,api
manifest=json.loads((root/'manifest-sha256.json').read_text())
for name,h in manifest.items():assert hashlib.sha256((root/name).read_bytes()).hexdigest()==h
print('PASS extracted ZIP manifest:',len(manifest),'entries',flush=True);profiles=[]
with Stack(root,args.engine) as stack:
 stack.build();stack.start_db();cases=[(p,False,False,0,'','') for p in ['direct','proxy-fixed','proxy-adaptive','middleware-fixed','middleware-adaptive']]
 for p in ['proxy-fixed','proxy-adaptive','middleware-fixed','middleware-adaptive']:
  cases += [(p,True,False,0,'',''),(p,False,True,0,'',''),(p,True,False,4,'',''),(p,False,False,0,'enforce',''),(p,False,False,0,'observe','')]
  cases += [(p,False,False,0,'',r) for r in ['enforce','observe','disabled']]
 cases=[(*c,'') for c in cases]+[(p,False,False,0,'','',o) for p in ['proxy-fixed','proxy-adaptive','middleware-fixed','middleware-adaptive'] for o in ['disabled','fixed','pool','balanced']]
 cases=[(*c,None,False) for c in cases]
 for p in ['middleware-adaptive','proxy-adaptive']:
  cases.append((p,False,False,0,'','','',{},True))
  for decisions in ['', 'enforce']:cases.append((p,False,False,0,decisions,'','',{'profile':'stable'},True))
 cases=[(*c,None) for c in cases]
 queue={'max_waiting':16,'max_wait':'40ms','aging_after':'20ms','reserved':{'critical':4}}
 cases=[(p,False,False,0,'','','',None,False,queue) for p in ['middleware-fixed','proxy-fixed','middleware-adaptive','proxy-adaptive']]+cases
 cases=[(p,False,False,0,'','','',None,False,None) for p in ['semaphore','rate']]+cases
 for profile in ['middleware-fixed','proxy-fixed','middleware-adaptive','proxy-adaptive']:
  stack.start_app(profile);collection=root/'postman/Cancellation.postman_collection.json';env=stack.environment()
  result=subprocess.run(['postman','collection','run',str(collection),'-e',str(env),'--no-report-events','--timeout-request','5000','--timeout-script','15000'],capture_output=True,text=True);(folder/(profile+'-cancellation.log')).write_text(result.stdout+result.stderr)
  clean=re.sub(r'\x1b\[[0-9;]*[A-Za-z]','',result.stdout);lines=[l for l in clean.splitlines() if 'assertions' in l];counts=re.findall(r'\d+',lines[-1]) if lines else [];print(profile+'-cancellation',result.returncode,lines[-1:],flush=True);assert result.returncode==0
  assert all(api(url,'/state')['pool']['InUse']==0 for url in stack.admins.values())
  profiles.append({'profile':profile,'cancellation_controls':True,'assertions':int(counts[0]),'failed_assertions':int(counts[1]),'exit_code':result.returncode,'pool_connections_drained':True,'collection_sha256':hashlib.sha256(collection.read_bytes()).hexdigest()});stack.stop_app()
 for profile,tenants,priorities,sharing,decisions,resources,operations,adaptive_settings,route_budgets,queue_settings in cases:
  stack.start_app(profile,tenants,priorities,sharing=sharing,decisions=decisions,resources=resources,operations=operations,adaptive_settings=adaptive_settings,route_budgets=route_budgets,queue_settings=queue_settings,execution_limit=4 if profile in ['semaphore','rate'] else 12,reference_rate=1,reference_burst=1);collection=root/'postman'/('Reference.postman_collection.json' if profile in ['semaphore','rate'] else 'Queue.postman_collection.json' if queue_settings else 'Adaptive.postman_collection.json' if (adaptive_settings or {}).get('profile')=='stable' else 'Operations.postman_collection.json' if operations else 'Resources.postman_collection.json' if resources else 'Signals.postman_collection.json' if decisions else 'Sharing.postman_collection.json' if sharing else 'Priorities.postman_collection.json' if priorities else 'Tenants.postman_collection.json' if tenants else 'Commerce.postman_collection.json');env=stack.environment();label=profile+('-operations-'+operations if operations else '-resources-'+resources if resources else '-'+decisions if decisions else '-sharing' if sharing else '-priorities' if priorities else '-tenants' if tenants else '')
  label+=('-queue' if queue_settings else '')
  label+=('-stable' if (adaptive_settings or {}).get('profile')=='stable' else '-legacy-defaults' if adaptive_settings is not None else '')
  result=subprocess.run(['postman','collection','run',str(collection),'-e',str(env),'--no-report-events','--timeout-request','5000','--timeout-script','15000'],capture_output=True,text=True);(folder/(label+'.log')).write_text(result.stdout+result.stderr)
  clean=re.sub(r'\x1b\[[0-9;]*[A-Za-z]','',result.stdout);lines=[l for l in clean.splitlines() if 'assertions' in l];counts=re.findall(r'\d+',lines[-1]) if lines else [];print(label,'exit',result.returncode,lines[-1:],flush=True);assert result.returncode==0,str(folder/(label+'.log'))
  assert all(api(url,'/state')['pool']['InUse']==0 for url in stack.admins.values());
  if operations:
   state=api(stack.admins['orders'],'/operations/state');assert state['storage']['Active']==0 and state['replica']['Active']==0 and state['worker']['Active']==0 and state['pool']['InUse']==0
  profiles.append({'profile':profile,'tenants_enabled':tenants,'priorities_enabled':priorities,'sharing_ceiling':sharing,'decision_mode':decisions,'resource_mode':resources,'operation_mode':operations,'adaptive_settings':adaptive_settings,'route_budgets':route_budgets,'queue_settings':queue_settings,'assertions':int(counts[0]),'failed_assertions':int(counts[1]),'exit_code':result.returncode,'pool_connections_drained':True,'collection_sha256':hashlib.sha256(collection.read_bytes()).hexdigest()});stack.stop_app()
subprocess.run(['postman','status'],cwd=site,check=False)
report={'release':'v1.0.0','target':'Freshly extracted source-only demo with real PostgreSQL','archive_sha256':digest,'profiles':profiles,'passed':all(p['exit_code']==0 and p['failed_assertions']==0 for p in profiles)};(site/'examples/commerce/results/release-contract-results.json').write_text(json.dumps(report,indent=2)+'\n');print('PASS',sum(p['assertions'] for p in profiles),'assertions across',len(profiles),'profiles',flush=True)
