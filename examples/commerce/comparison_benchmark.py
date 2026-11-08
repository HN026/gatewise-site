#!/usr/bin/env python3
"""Run the frozen comparison and retain explicit lifetime qualification failures.

The collection protocol is unchanged. Scope analysis was added after observing
proxy cancellation cleanup; it never rewrites the original checks or verdict.
"""
import argparse,hashlib,json,pathlib
import comparison_protocol as protocol
from comparison_analysis import classify

def main():
    parser=argparse.ArgumentParser(add_help=False)
    parser.add_argument('--analyze-only',type=pathlib.Path)
    parser.add_argument('--output',type=pathlib.Path,default=protocol.ROOT/'results/comparison-results.json')
    args,_=parser.parse_known_args()
    output=args.analyze_only or args.output
    if not args.analyze_only:
        try:protocol.main()
        except SystemExit as error:
            if error.code not in (0,1):raise
    report=json.loads(output.read_text());analysis=classify(report)
    folder=pathlib.Path(__file__).resolve().parent
    report['methodology']['analysis_sha256']=hashlib.sha256((folder/'comparison_analysis.py').read_bytes()).hexdigest()
    report['methodology']['wrapper_sha256']=hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest()
    output.write_text(json.dumps(report,indent=2)+'\n')
    failed=sum(not c['pass'] for c in analysis['backend_expectations'])
    print('Original protocol verdict:',report['passed'],'; failed remote-handler expectations:',failed)
    print('Measurements validated:',analysis['measurement_validated'],'; post-hoc scope analysis explicitly retained')
    if not analysis['measurement_validated']:raise SystemExit(1)
if __name__=='__main__':main()
