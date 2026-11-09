"""Package the commerce example and two independent Go module snapshots."""
import argparse,hashlib,json,pathlib,shutil,zipfile
ROOT=pathlib.Path(__file__).resolve().parents[1]
def main():
 p=argparse.ArgumentParser();p.add_argument('--sync-sources',action='store_true');p.add_argument('--projects',type=pathlib.Path,default=ROOT.parent);a=p.parse_args();example=ROOT/'examples/commerce'
 if a.sync_sources:
  for name in ['gatewise','commerce-backend']:
   target=example/name
   if target.exists():shutil.rmtree(target)
   for source in sorted((a.projects/name).rglob('*')):
    if not source.is_file():continue
    rel=source.relative_to(a.projects/name)
    if any(x in {'.git','bin','results','__pycache__','node_modules'} for x in rel.parts):continue
    if source.suffix not in {'.go','.mod','.sum','.json','.sh','.py','.yaml','.yml','.svg'} and source.name not in {'LICENSE','README.md','.gitignore','.env.example','Dockerfile','.dockerignore'}:continue
    out=target/rel;out.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(source,out)
 contents={}
 for source in sorted(example.rglob('*')):
  if not source.is_file():continue
  rel=source.relative_to(example)
  if any(x in {'.git','bin','results','__pycache__'} for x in rel.parts) or source.name=='.env':continue
  if source.name=='manifest-sha256.json':continue
  contents[rel.as_posix()]=source.read_bytes()
 manifest={name:hashlib.sha256(data).hexdigest() for name,data in contents.items()};contents['manifest-sha256.json']=(json.dumps(manifest,indent=2)+'\n').encode()
 output=ROOT/'public/downloads';output.mkdir(exist_ok=True)
 archive=output/'gatewise-demo.zip'
 with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED) as z:
  for name,data in contents.items():
   info=zipfile.ZipInfo('gatewise-demo/'+name);info.external_attr=(0o100755 if name.endswith('.sh') else 0o100644)<<16;info.compress_type=zipfile.ZIP_DEFLATED;z.writestr(info,data)
 digest=hashlib.sha256(archive.read_bytes()).hexdigest();(output/'gatewise-demo.sha256').write_text(digest+'  gatewise-demo.zip\n')
 with zipfile.ZipFile(output/'postman.zip','w',zipfile.ZIP_DEFLATED) as z:
  for source in sorted((example/'postman').glob('*')):
   if source.is_file():z.write(source,source.name)
 (ROOT/'src/data/demo.json').write_text(json.dumps({'files':len(contents),'bytes':archive.stat().st_size,'sha256':digest},indent=2)+'\n')
 print('Packaged',len(contents),'commerce example files')
if __name__=='__main__':main()
