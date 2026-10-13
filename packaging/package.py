"""Legacy 0.1 WPF packager. Do not use for native 0.3 releases. Private credentials excluded."""
from pathlib import Path
import hashlib
import json
import shutil
import zipfile

root = Path(__file__).resolve().parents[1]
out = root / 'build' / 'win'
if not (out / 'NorthLink.exe').exists():
    raise SystemExit('Run dotnet publish before packaging.')
shutil.copy2(root / 'build' / 'northlink-core.exe', out / 'northlink-core.exe')
for name in ('NorthLink.Provider.exe', 'NorthLink.Client.exe'):
    shutil.copy2(out / 'NorthLink.exe', out / name)
(out / 'NorthLink.exe').unlink()
for source in ('desktop/northlink.ico', 'README.md', 'LICENSE', 'docs/NorthLink-Guide.html', 'docs/THIRD-PARTY-NOTICES.txt'):
    shutil.copy2(root / source, out / Path(source).name)
(out / 'docs').mkdir(exist_ok=True)
for name in ('ARCHITECTURE.md', 'VALIDATION.md', 'test-results.txt'):
    shutil.copy2(root / 'docs' / name, out / 'docs' / name)
info = {
    'product': 'NorthLink', 'version': '0.1.0', 'builtDate': '2026-10-13',
    'target': 'windows-amd64', 'go': '1.27.2', 'dotnetSDK': '8.0.425',
    'dotnetDesktopRuntime': '8.0.31', 'selfContained': True,
    'firstSTUN': 'stun:stun.hitv.com:3478', 'relayEnabled': False,
    'verification': 'Cross-compiled Windows artifacts; local Linux core integration and race tests passed. Windows UI/service and real NAT/RDP validation pending.'
}
(out / 'build-info.json').write_text(json.dumps(info, ensure_ascii=False, indent=2), encoding='utf-8')
files = sorted(p for p in out.rglob('*') if p.is_file() and p.name != 'SHA256SUMS.txt')
manifest = ''.join(hashlib.sha256(p.read_bytes()).hexdigest() + '  ' + p.relative_to(out).as_posix() + '\n' for p in files)
(out / 'SHA256SUMS.txt').write_text(manifest, encoding='utf-8')
windows_zip = root.parent / 'NorthLink-Win10-x64.zip'
with zipfile.ZipFile(windows_zip, 'w', zipfile.ZIP_DEFLATED, compresslevel=9) as z:
    for p in sorted(out.rglob('*')):
        if p.is_file(): z.write(p, 'NorthLink/' + p.relative_to(out).as_posix())
source_zip = root.parent / 'NorthLink-Source.zip'
excluded = {'build', 'bin', 'obj', '.git', '.cache', '__pycache__', 'node_modules', '.private'}
with zipfile.ZipFile(source_zip, 'w', zipfile.ZIP_DEFLATED, compresslevel=9) as z:
    for p in sorted(root.rglob('*')):
        rel = p.relative_to(root)
        if p.is_file() and p.name != 'builtin_private.go' and not any(part in excluded for part in rel.parts):
            z.write(p, 'northlink/' + rel.as_posix())
checksums = []
for p in (windows_zip, source_zip):
    with zipfile.ZipFile(p) as z:
        assert z.testzip() is None, 'Archive integrity failed'
        print(p.name, f'{p.stat().st_size / 1024 / 1024:.2f} MiB', len(z.infolist()), 'files')
    checksums.append(hashlib.sha256(p.read_bytes()).hexdigest() + '  ' + p.name)
(root.parent / 'NorthLink-SHA256SUMS.txt').write_text('\n'.join(checksums) + '\n', encoding='utf-8')
