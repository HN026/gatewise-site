"""Policy adapters share one application, arrival generator and measurement protocol.

An additional implementation supplies start(stack) and populated urls/backends/
admins/metrics_urls on the stack. Its own configuration and revision belong in
metadata. Do not relabel an independently written implementation as Cinnamon.
"""
from dataclasses import dataclass
@dataclass(frozen=True)
class PolicyAdapter:
    name: str
    profile: str
    tenants: bool = False
    def start(self, stack):
        return stack.start_app(self.profile, self.tenants)
    def metadata(self):
        return {'name':self.name,'profile':self.profile,'tenant_isolation':self.tenants,'placement':'proxy' if self.profile.startswith('proxy-') else 'in-process'}
ADAPTERS=[PolicyAdapter('direct','direct'),PolicyAdapter('semaphore','semaphore')]
for profile in ['proxy-fixed','proxy-adaptive','middleware-fixed','middleware-adaptive']:
    ADAPTERS += [PolicyAdapter(profile,profile),PolicyAdapter(profile+'-tenants',profile,True)]
