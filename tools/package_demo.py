"""Package source-only demo modules outside the Gatewise library repository."""
import argparse
import hashlib
import json
import pathlib
import shutil
import zipfile

ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULES = ['gatewise', 'demo-backend', 'catalog-backend', 'middleware-backend', 'benchmark-backend']


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--projects', type=pathlib.Path, default=ROOT.parent)
    parser.add_argument('--sync-sources', action='store_true', help='refresh checked-in demo snapshots from sibling source modules')
    args = parser.parse_args()
    contents = {}
    for name in MODULES:
        module = (args.projects if args.sync_sources else ROOT/'examples/demo')/name
        if not (module/'go.mod').exists():
            raise SystemExit(f'Missing independent module: {module}')
        for p in sorted(module.rglob('*')):
            if not p.is_file():
                continue
            rel = p.relative_to(module)
            if any(part in {'bin', '.git', 'node_modules', 'results', 'docs', 'postman', '__pycache__'} for part in rel.parts):
                continue
            if p.suffix not in {'.go', '.mod', '.sum', '.json', '.sh', '.yaml', '.yml'} and p.name not in {'.env.example', 'LICENSE'}:
                continue
            contents[f'{name}/{rel.as_posix()}'] = p.read_bytes()
    if args.sync_sources:
        for name in MODULES:
            destination = ROOT/'examples/demo'/name
            if destination.exists():
                shutil.rmtree(destination)
        for name, data in contents.items():
            destination = ROOT/'examples/demo'/name
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_bytes(data)
            if name.endswith('.sh'):
                destination.chmod(0o755)
    postman = ROOT/'examples/demo/postman'
    for name in ['Gatewise.postman_collection.json', 'Gatewise-performance.postman_collection.json', 'Gatewise-Local.postman_environment.json', 'README.md']:
        contents[f'postman/{name}'] = (postman/name).read_bytes()
    contents['run.py'] = (ROOT/'examples/demo/run.py').read_bytes()
    contents['START-HERE.md'] = (ROOT/'examples/demo/README.md').read_bytes()
    manifest = {name: hashlib.sha256(data).hexdigest() for name, data in contents.items()}
    contents['manifest-sha256.json'] = (json.dumps(manifest, indent=2)+'\n').encode()
    output = ROOT/'public/downloads'
    output.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(output/'postman.zip', 'w', zipfile.ZIP_DEFLATED) as postman_zip:
        for name in ['Gatewise.postman_collection.json', 'Gatewise-performance.postman_collection.json', 'Gatewise-Local.postman_environment.json', 'README.md']:
            postman_zip.write(postman/name, name)
    archive = output/'gatewise-demo.zip'
    with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as z:
        for name, data in contents.items():
            info = zipfile.ZipInfo('gatewise-demo/'+name)
            info.external_attr = (0o100755 if name.endswith('.sh') else 0o100644) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            z.writestr(info, data)
    with zipfile.ZipFile(archive) as z:
        assert z.testzip() is None
    (output/'gatewise-demo.sha256').write_text(hashlib.sha256(archive.read_bytes()).hexdigest()+'  gatewise-demo.zip\n')
    (ROOT/'src/data/demo.json').write_text(json.dumps({'bytes': archive.stat().st_size, 'files': len(contents), 'sha256': hashlib.sha256(archive.read_bytes()).hexdigest()}, indent=2)+'\n')
    print(f'Packaged {len(contents)} source files: {archive}')


if __name__ == '__main__':
    main()
