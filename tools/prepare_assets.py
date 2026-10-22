"""Build public diagrams and charts from canonical diagram models and measured data."""
import argparse
import html
import json
import pathlib
import statistics
import xml.etree.ElementTree as ET

ROOT = pathlib.Path(__file__).resolve().parents[1]


def diagram(name, nodes, edges, width, height):
    output = ROOT / 'public/diagrams'
    output.mkdir(parents=True, exist_ok=True)
    mx = ET.Element('mxfile', host='app.diagrams.net')
    page = ET.SubElement(mx, 'diagram', name=name)
    model = ET.SubElement(page, 'mxGraphModel', pageWidth=str(width), pageHeight=str(height))
    graph = ET.SubElement(model, 'root')
    ET.SubElement(graph, 'mxCell', id='0')
    ET.SubElement(graph, 'mxCell', id='1', parent='0')
    svg = [f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {width} {height}" role="img" aria-label="{name}">', '<defs><marker id="arrow" markerWidth="7" markerHeight="7" refX="6" refY="3.5" orient="auto"><path d="M0 0L7 3.5L0 7" fill="#91ab9a"/></marker></defs>', f'<rect width="{width}" height="{height}" fill="#101a13"/>']
    for index, (source, target, label) in enumerate(edges):
        a, b = nodes[source], nodes[target]
        color = '#ef8d7d' if 'reject' in target else '#91ab9a'
        if abs((a[0]+a[2]/2)-(b[0]+b[2]/2)) < 5 and b[1] > a[1]:
            x = a[0]+a[2]/2
            path = f'M{x} {a[1]+a[3]} V{b[1]-3}'
            lx, ly = x+8, (a[1]+a[3]+b[1])/2
            anchor = 'start'
        elif b[0] < a[0]:
            x1, x2 = a[0]+a[2]/2, b[0]+b[2]/2
            path = f'M{x1} {a[1]+a[3]} V{height-30} H{x2} V{b[1]+b[3]+3}'
            lx, ly, anchor = (x1+x2)/2, height-38, 'middle'
        else:
            x1, y1, x2, y2 = a[0]+a[2], a[1]+a[3]/2, b[0], b[1]+b[3]/2
            mid = (x1+x2)/2
            path = f'M{x1} {y1} H{mid} V{y2} H{x2-3}'
            lx, ly, anchor = mid, (y1+y2)/2-9, 'middle'
        svg.append(f'<path d="{path}" fill="none" stroke="{color}" stroke-width="1.4" marker-end="url(#arrow)"/>')
        if label:
            svg.append(f'<text x="{lx}" y="{ly}" fill="{color}" text-anchor="{anchor}" font-family="Arial,sans-serif" font-size="11">{html.escape(label)}</text>')
        cell = ET.SubElement(graph, 'mxCell', id=f'edge-{index}', value=label, edge='1', parent='1', source=source, target=target, style='edgeStyle=orthogonalEdgeStyle;rounded=0;endArrow=block;strokeColor='+color+';fontColor=#afc3b6;')
        ET.SubElement(cell, 'mxGeometry', relative='1', **{'as': 'geometry'})
    for key, (x, y, w, h, title, subtitle, accent) in nodes.items():
        stroke = '#6faf91' if accent else '#4a6051'
        svg += [f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="2" fill="#142019" stroke="{stroke}"/>', f'<text x="{x+w/2}" y="{y+25}" fill="#dce7df" text-anchor="middle" font-family="Arial,sans-serif" font-size="14">{html.escape(title)}</text>', f'<text x="{x+w/2}" y="{y+46}" fill="#a1b7a8" text-anchor="middle" font-family="Arial,sans-serif" font-size="11">{html.escape(subtitle)}</text>']
        cell = ET.SubElement(graph, 'mxCell', id=key, value=title+'\n'+subtitle, vertex='1', parent='1', style='rounded=0;whiteSpace=wrap;html=0;fillColor=#142019;strokeColor='+stroke+';fontColor=#dce7df;fontSize=14;')
        ET.SubElement(cell, 'mxGeometry', x=str(x), y=str(y), width=str(w), height=str(h), **{'as': 'geometry'})
    svg.append('</svg>')
    (output / f'{name}.drawio').write_text(ET.tostring(mx, encoding='unicode'))
    (output / f'{name}.svg').write_text('\n'.join(svg))


def charts(report_path):
    report = json.loads(pathlib.Path(report_path).read_text())
    assert report['passed'] and len(report['results']) == 15
    public = ROOT / 'public/data'
    public.mkdir(parents=True, exist_ok=True)
    report.pop('hostname', None)
    report['config'] = {k: v for k, v in report['config'].items() if 'binary' not in k and k != 'output_directory'}
    report['binary_sha256'] = {pathlib.Path(k).name: v for k, v in report['binary_sha256'].items()}
    for run in report['results']:
        for resource in run['resources']:
            resource.pop('pid', None)
    (public / 'benchmark-results.json').write_text(json.dumps(report, indent=2)+'\n')
    deployment_names = ['direct', 'proxy-fixed', 'proxy-adaptive', 'middleware-fixed', 'middleware-adaptive']
    measured = []
    for name in deployment_names:
        runs = [r for r in report['results'] if r['deployment'] == name]
        phases = {}
        for phase in ['baseline', 'pressure', 'recovery']:
            samples = []
            for run in runs:
                p = next(p for p in run['phases'] if p['name'] == phase)
                traffic = list(p['traffic'].values())
                sent = sum(t['sent'] for t in traffic)
                samples.append({'repeat': run['repeat'], 'timely': sum(t['successful_within_100ms_per_second'] for t in traffic), 'p95': p['traffic']['critical']['success_latency']['p95_ms'], 'active': p['cumulative_peak_backend_active'], 'rejected': 100*sum(t['rejected_503'] for t in traffic)/sent, 'goodput': sum(t['successful_per_second'] for t in traffic), 'limit': p['end_limit']})
            phases[phase] = {'samples': samples, **{key: statistics.median(s[key] for s in samples) for key in ['timely', 'p95', 'active', 'rejected', 'goodput', 'limit']}}
        measured.append({'name': name, 'phases': phases})
    summary = {'date': report['started_at'][:10], 'cpu': 'Apple M4 Pro', 'go': report['go_version'], 'os': report['os'], 'arch': report['arch'], 'repeats': 3, 'sent': sum(t['sent'] for r in report['results'] for p in r['phases'] for t in p['traffic'].values()), 'scheduled': sum(t['scheduled'] for r in report['results'] for p in r['phases'] for t in p['traffic'].values()), 'dropped': sum(t['dropped_schedules'] for r in report['results'] for p in r['phases'] for t in p['traffic'].values()), 'deployments': measured}
    (ROOT / 'src/data/benchmarks.json').write_text(json.dumps(summary, indent=2)+'\n')
    rows = []
    for i, d in enumerate(measured):
        value = d['phases']['pressure']['timely']
        y = 100+i*62
        color = '#72dfc3' if 'adaptive' in d['name'] else '#647a70'
        rows += [f'<text x="32" y="{y+22}" fill="#b8c7bb" font-family="monospace" font-size="13">{d["name"]}</text>',f'<rect x="238" y="{y}" width="{value/22*440:.2f}" height="30" rx="3" fill="{color}"/>',f'<text x="{250+value/22*440:.2f}" y="{y+21}" fill="#eff4e9" font-family="monospace" font-size="13">{value:.2f}</text>']
    (ROOT / 'public/diagrams/benchmark-goodput.svg').write_text('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 800 460"><rect width="800" height="460" fill="#101715"/><text x="32" y="42" fill="#72dfc3" font-family="Arial" font-size="22">Successful work within 100ms</text><text x="32" y="68" fill="#a6b8ad" font-family="monospace" font-size="12">Local pressure phase · requests/second · median of 3 repeats</text>'+''.join(rows)+'<text x="32" y="441" fill="#a6b8ad" font-family="monospace" font-size="11">Gatewise · 6 Nov 2026 · synthetic workload, not production traffic</text></svg>')


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--results', required=True)
    args = parser.parse_args()
    diagram('overload', {
        'raw': (20, 30, 150, 64, 'Without admission', '120 requests/s', False),
        'raw-handlers': (400, 30, 160, 64, 'Active handlers', 'Average ~60 in flight', False),
        'raw-dep': (610, 30, 160, 64, 'Slow dependency', 'Mean duration 500ms', False),
        'bounded': (20, 175, 150, 64, 'With admission', '120 requests/s', False),
        'guard': (210, 175, 150, 64, 'Concurrency guard', 'L = 20', True),
        'handlers': (400, 175, 160, 64, 'Active handlers', 'At most 20 admitted', False),
        'dep': (610, 175, 160, 64, 'Slow dependency', 'Mean duration 500ms', False),
        'reject': (210, 290, 150, 64, 'HTTP 503', 'Excess arrivals', False),
    }, [('raw','raw-handlers',''),('raw-handlers','raw-dep',''),('bounded','guard',''),('guard','handlers',''),('handlers','dep',''),('guard','reject','full')], 800, 385)
    diagram('proxy-architecture', {
        'clients': (20, 105, 120, 64, 'HTTP clients', 'Any language', False),
        'router': (180, 105, 150, 64, 'Route selection', 'Longest segment prefix', False),
        'users-guard': (395, 30, 160, 64, 'Users guard + pool', 'Per-service budget', True),
        'catalog-guard': (395, 180, 160, 64, 'Catalog guard + pool', 'Independent budget', True),
        'users': (625, 30, 150, 64, 'Users backend', 'HTTP upstream', False),
        'catalog': (625, 180, 150, 64, 'Catalog backend', 'HTTP upstream', False),
    }, [('clients','router',''),('router','users-guard',''),('router','catalog-guard',''),('users-guard','users',''),('catalog-guard','catalog','')], 800, 275)
    diagram('middleware-architecture', {
        'router': (20, 105, 165, 64, 'Application router', 'Assign route priority', False),
        'guard': (250, 105, 180, 64, 'Shared Service', 'One admission budget', True),
        'critical': (535, 30, 240, 64, 'Critical handler', 'Uses total capacity', False),
        'background': (535, 180, 240, 64, 'Background handler', 'Also checks background quota', False),
    }, [('router','guard',''),('guard','critical',''),('guard','background','')], 800, 275)
    diagram('admission-decision', {
        'request': (20, 30, 130, 64, 'Request', 'Priority assigned', False),
        'total': (215, 30, 220, 64, 'A >= L ?', 'Total capacity check', True),
        'reject-total': (555, 30, 220, 64, 'Reject 503', 'concurrency_limit', False),
        'background': (215, 165, 220, 64, 'Background quota full?', 'background AND B >= L - R', True),
        'reject-background': (555, 165, 220, 64, 'Reject 503', 'critical_capacity_reserved', False),
        'admit': (215, 300, 220, 64, 'Admit and execute', 'Increment A; increment B if background', True),
    }, [('request','total',''),('total','reject-total','yes'),('total','background','no'),('background','reject-background','yes'),('background','admit','no')], 800, 390)
    diagram('feedback-loop', {
        'admission': (20, 30, 185, 64, 'Admission at limit L', 'Record generation', True),
        'sample': (300, 30, 210, 64, 'Handler completes', 'Release slot; record feedback', False),
        'adjust': (600, 30, 180, 64, 'Evaluate window', 'Hold / decrease / increase L', True),
    }, [('admission','sample',''),('sample','adjust',''),('adjust','admission','New limit applies to subsequent requests')], 800, 190)
    charts(args.results)
