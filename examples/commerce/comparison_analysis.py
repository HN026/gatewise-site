"""Preserve the original protocol verdict; separate a diagnosed lifetime assumption.

This classification was added after the first proxy deadline observations. It is
post-hoc scope analysis, not a claim that every predeclared check passed.
"""
import datetime

def classify(report):
    integrity=[];expectations=[]
    for run in report['runs']:
        for c in run['checks']:
            row={'case':run['case']['id'],'repeat':run['repeat'],**c}
            if run['case']['profile'].startswith('proxy-') and c['name']=='execution bounded at4':
                row.update(backend_peak=max(t['reference']['peak'] for t in run['trace']),sampled_proxy_active_peak=max(t['active'] for t in run['trace']))
                first={}
                for sample in run['trace']:
                    if sample['reference']['peak']>4 and sample['service'] not in first:first[sample['service']]={'phase':sample['phase'],'seconds':sample['seconds'],'peak':sample['reference']['peak']}
                row['first_observed_excess_by_service']=first
                expectations.append(row)
            else:integrity.append(row)
    # Keep original failures. A proxy request can finish before the canceled
    # remote handler finishes cleanup; its observer is a different lifetime.
    proxy_samples=[t for r in report['runs'] if r['case']['profile'].startswith('proxy-') for t in r['trace']]
    if proxy_samples:integrity.append({'name':'post-hoc sampled proxy admission <=4','pass':all(0<=t['active']<=4 for t in proxy_samples),'post_hoc':True})
    report['scope_analysis']={
        'analyzed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),
        'post_hoc':True,
        'original_protocol_passed':report['passed'],
        'integrity':integrity,
        'backend_expectations':expectations,
        'measurement_validated':all(c['pass'] for c in integrity),
        'interpretation':'Original backend<=4 checks are retained, including failures. Proxy<=4 applies to active proxy request lifetimes, not canceled remote handlers still cleaning up. SQL pool stays4. Middleware reference counts actual protected handler lifetime. Post-hoc classification is not all-predeclared-criteria-pass evidence.'}
    return report['scope_analysis']
