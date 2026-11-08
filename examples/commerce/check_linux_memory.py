#!/usr/bin/env python3
"""Check the real memory provider in an isolated 256MiB Linux container."""
import argparse,json,os,pathlib,subprocess,tempfile,uuid
from run import ROOT,IMAGE
PROBE='''package main
import("context";"encoding/json";"os";"runtime";"github.com/HN026/gatewise/regulator")
func main(){m,_:=regulator.NewMemory(regulator.MemoryConfig{});_,err:=m.Sample(context.Background());if err!=nil{panic(err)};s:=m.Snapshot();if s.Source!="cgroup_v2" || s.LimitBytes!=256<<20 || s.UsedBytes==0{panic("unexpected cgroup accounting")};g,_:=(regulator.Goroutines{}).Sample(context.Background());json.NewEncoder(os.Stdout).Encode(map[string]any{"passed":true,"os":runtime.GOOS,"arch":runtime.GOARCH,"memory":s,"goroutines":g.Value,"container_limit_bytes":256<<20})}
'''
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--projects',type=pathlib.Path,default=ROOT);p.add_argument('--engine',default='podman');p.add_argument('--image',default=IMAGE);p.add_argument('--output',type=pathlib.Path,default=ROOT/'results/resource-platform-validation.json');a=p.parse_args()
 arch=subprocess.check_output([a.engine,'info','--format','{{.Host.Arch}}' if a.engine.endswith('podman') else '{{.Architecture}}'],text=True).strip();arch={'aarch64':'arm64','x86_64':'amd64'}.get(arch,arch)
 if arch not in ['arm64','amd64']:p.error('this check supports arm64/amd64 Linux container engines')
 name='gatewise-runtime-'+uuid.uuid4().hex[:10]
 with tempfile.TemporaryDirectory(prefix='gatewise-runtime-build-') as folder:
  folder=pathlib.Path(folder);(folder/'go.mod').write_text('module example.com/gatewise-linux-probe\n\ngo 1.22\n\nrequire github.com/HN026/gatewise v0.7.0\nreplace github.com/HN026/gatewise => '+str((a.projects/'gatewise').resolve())+'\n');(folder/'main.go').write_text(PROBE)
  env={**os.environ,'GOWORK':'off'};subprocess.run(['go','mod','tidy'],cwd=folder,env=env,check=True);subprocess.run(['go','build','-o',str(folder/'probe'),'.'],cwd=folder,env={**env,'CGO_ENABLED':'0','GOOS':'linux','GOARCH':arch},check=True)
  try:
   raw=subprocess.check_output([a.engine,'run','--rm','--name',name,'--network','none','--memory','256m','--cpus','1','--env','GOMEMLIMIT=off','-v',str(folder/'probe')+':/probe:ro','--entrypoint','/probe',a.image],text=True);result=json.loads(raw);assert result['passed'];a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(result,indent=2)+'\n');print(raw)
  finally:subprocess.run([a.engine,'rm','-f',name],capture_output=True)
if __name__=='__main__':main()
