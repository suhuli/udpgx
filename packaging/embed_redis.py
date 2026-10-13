"""Generate private, build-tagged defaults; never include the generated file in source archives.
Usage: python packaging/embed_redis.py .private/redis-default.json
The encoded data is only to avoid accidental display, NOT a confidentiality guarantee.
"""
import base64,json,pathlib,sys
root=pathlib.Path(__file__).resolve().parents[1]
profile=json.loads(pathlib.Path(sys.argv[1]).read_text())
assert profile.get('host') and profile.get('password')
payload=base64.b64encode(json.dumps(profile,separators=(',',':')).encode()).decode()
out=root/'core/internal/config/builtin_private.go'
out.write_text('//go:build builtinredis\n\npackage config\n\nimport("encoding/base64";"encoding/json")\n\nfunc builtinRedis()(Redis,bool){\n b,e:=base64.StdEncoding.DecodeString("'+payload+'");if e!=nil{return Redis{},false};var r Redis;if json.Unmarshal(b,&r)!=nil{return Redis{},false};return r,r.Host!=""\n}\n')
out.chmod(0o600)
print('Private Redis default embedded. Address and password omitted from build output.')
