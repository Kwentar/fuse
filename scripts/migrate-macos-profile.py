#!/usr/bin/env python3
"""Relocate a stopped profile, keeping a separate backup and the source intact."""
import argparse,ctypes,fcntl,hashlib,json,os,pathlib,shutil,stat,tempfile,time

p=argparse.ArgumentParser()
p.add_argument('source',type=pathlib.Path)
p.add_argument('destination',type=pathlib.Path)
args=p.parse_args()
source=args.source.absolute();destination=args.destination.absolute()
if source==destination:raise SystemExit('Source equals destination')
if not (source/'identity.key').is_file():raise SystemExit('Source identity.key is missing')
if destination.exists():raise SystemExit('Destination exists; refusing to overwrite a profile')
lock=os.open(source/'node.lock',os.O_RDWR|os.O_CREAT|os.O_NOFOLLOW,0o600)
try:fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
except BlockingIOError:raise SystemExit('Stop the old core before relocation')
destination.parent.mkdir(parents=True,exist_ok=True)
backups=destination.parent/(destination.name+' Backups')
backups.mkdir(mode=0o700,exist_ok=True)
if stat.S_IMODE(backups.stat().st_mode)!=0o700:raise SystemExit('Unsafe backup directory permissions')
backup=pathlib.Path(tempfile.mkdtemp(prefix=time.strftime('%Y%m%d-%H%M%S')+'-before-relocation-',dir=backups))
stage=pathlib.Path(tempfile.mkdtemp(prefix='.p2pm-migration-',dir=destination.parent))
lib=ctypes.CDLL(None,use_errno=True)
lib.clonefile.argtypes=[ctypes.c_char_p,ctypes.c_char_p,ctypes.c_int]
def copy_file(src,dst):
    if lib.clonefile(os.fsencode(src),os.fsencode(dst),1)!=0:shutil.copyfile(src,dst,follow_symlinks=False)
    os.chmod(dst,0o600)
    return dst
def copy_profile(target):
    count=0
    for base,dirs,files in os.walk(source,followlinks=False):
        rel=pathlib.Path(base).relative_to(source)
        for name in dirs:
            path=pathlib.Path(base)/name
            if path.is_symlink():raise RuntimeError('Symbolic link in profile; refusing migration')
            (target/rel/name).mkdir(mode=0o700)
        for name in files:
            if name in ['node.lock','control.sock']:continue
            path=pathlib.Path(base)/name
            if not stat.S_ISREG(path.lstat().st_mode):raise RuntimeError('Unexpected file type in profile')
            copy_file(path,target/rel/name);count+=1
    return count
def durable_json(path,value):
    fd,tmp=tempfile.mkstemp(prefix='.migration-',dir=path.parent)
    with os.fdopen(fd,'w') as f:json.dump(value,f,ensure_ascii=False,indent=2);f.write('\n');f.flush();os.fsync(f.fileno())
    os.replace(tmp,path)
try:
    count=copy_profile(backup);copy_profile(stage)
    rewritten=0
    for path in (stage/'files').glob('*.json'):
        row=json.loads(path.read_text())
        changed=False
        for field in ['path','export_path']:
            value=row.get(field,'')
            if value.startswith(str(source)+os.sep):row[field]=str(destination)+value[len(str(source)):];changed=True
        if changed:durable_json(path,row);rewritten+=1
    key_hash=lambda path:hashlib.sha256(path.read_bytes()).digest()
    if not key_hash(source/'identity.key')==key_hash(stage/'identity.key')==key_hash(backup/'identity.key'):raise RuntimeError('Identity verification failed')
    durable_json(stage/'profile-relocation.json',{'source':str(source),'backup':str(backup),'date':time.strftime('%Y-%m-%dT%H:%M:%S%z')})
    os.rename(stage,destination)
    fd=os.open(destination.parent,os.O_RDONLY);os.fsync(fd);os.close(fd)
    print(json.dumps({'status':'copied','files':count,'relocated_file_records':rewritten,'identity_key_unchanged':True,'source_preserved':True,'destination':str(destination),'backup':str(backup)},ensure_ascii=False))
finally:os.close(lock)
