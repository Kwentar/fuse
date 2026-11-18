#!/usr/bin/env python3
"""Real macOS subprocess acceptance. Never labels a second Mac process as Windows.

python3 scripts/acceptance-macos.py --mode lan --large --network-config .local/network.json
python3 scripts/acceptance-macos.py --mode relay --network-config .local/network.json
All temporary identities/data are removed; only measurements are reported.
"""
import argparse,hashlib,json,os,pathlib,platform,shutil,signal,subprocess,tempfile,threading,time

ROOT=pathlib.Path(__file__).resolve().parents[1]
parser=argparse.ArgumentParser()
parser.add_argument('--mode',choices=['lan','relay'],default='lan')
parser.add_argument('--large',action='store_true')
parser.add_argument('--peer-core',type=pathlib.Path)
parser.add_argument('--core',type=pathlib.Path)
parser.add_argument('--report',default='acceptance')
parser.add_argument('--network-config',type=pathlib.Path,required=True,help='Local JSON containing bootstrap_nodes and relay_nodes; never committed')
args=parser.parse_args()
network=json.loads(args.network_config.read_text())
if not all(isinstance(network.get(k),list) and network[k] and all(isinstance(v,str) for v in network[k]) for k in ['bootstrap_nodes','relay_nodes']):
    parser.error('network config must contain nonempty bootstrap_nodes and relay_nodes')
exe=(args.core or ROOT/'dist/macos-0.4.1-arm64/Fuse.app/Contents/Helpers/messenger').resolve()
peer=(args.peer_core or exe).resolve()
work=pathlib.Path(tempfile.mkdtemp(prefix='p2pm-',dir='/tmp'))
processes={};logs=[];peak={'a':0,'b':0};stop_sample=threading.Event()
report={'host':platform.platform(),'mode':args.mode,'endpoint_a':'macOS port 0.4.1','endpoint_b':'alternate core supplied with --peer-core' if peer!=exe.resolve() else 'macOS port 0.4.1','windows_tested':False,'results':[]}

def emit(item):
    report['results'].append(item)
    print(json.dumps(item,ensure_ascii=False),flush=True)

def call(who,*command,input=None,check=True):
    binary=exe if who=='a' else peer
    result=subprocess.run([str(binary),'--data-dir',str(work/who),'--json',*command],input=input,text=True,capture_output=True,timeout=45)
    rows=[json.loads(line) for line in result.stdout.splitlines() if line.strip()]
    if check and result.returncode:
        raise RuntimeError(f'{who} {command[0]} failed {result.returncode}: {result.stderr} {result.stdout}')
    return rows if check else (result,rows)

def start(who):
    binary=exe if who=='a' else peer
    out=open(work/(who+'.out'),'ab');err=open(work/(who+'.err'),'ab');logs.extend([out,err])
    processes[who]=subprocess.Popen([str(binary),'--data-dir',str(work/who),'--json','run'],stdout=out,stderr=err)
    end=time.monotonic()+120
    while time.monotonic()<end:
        if processes[who].poll() is not None:raise RuntimeError('daemon exited: '+(work/(who+'.err')).read_text())
        try:
            status=call(who,'status')[0]
            if status['state']=='online':return status
        except RuntimeError:pass
        time.sleep(.3)
    raise RuntimeError(f'{who} did not become online')

def stop(who):
    p=processes.get(who)
    if p and p.poll() is None:
        p.send_signal(signal.SIGINT)
        p.wait(timeout=20)

def digest(path):
    h=hashlib.sha256()
    with open(path,'rb') as f:
        while True:
            block=f.read(1024*1024)
            if not block:break
            h.update(block)
    return h.hexdigest()

def record(who,id):return next(r for r in call(who,'files') if r['file_id']==id)

def wait_file(who,id,timeout=900):
    end=time.monotonic()+timeout;routes=set()
    while time.monotonic()<end:
        r=record(who,id)
        if r.get('route'):routes.add(r['route'])
        if r['status']=='delivered':return r,sorted(routes)
        if r['status']=='paused':raise RuntimeError('file paused: '+str(r))
        time.sleep(.12 if args.mode=='lan' else .5)
    raise RuntimeError('file timeout: '+str(r))

def send_file(sender,receiver,path,**metadata):
    started=time.monotonic();sent=call(sender,'--','send-file',ids[receiver],str(path))[0]
    final,routes=wait_file(sender,sent['file_id'])
    received=record(receiver,sent['file_id'])
    expected=digest(path);actual=digest(received['path'])
    assert final['progress']==path.stat().st_size and received['status']=='stored'
    assert final['sha256']==expected==actual and final['route']==args.mode,(final,expected,actual)
    emit({'test':'file','direction':sender+'→'+receiver,'bytes':path.stat().st_size,'route':final['route'],'observed_routes':routes,'sha256':actual,'elapsed_seconds':round(time.monotonic()-started,2),'result':'PASS',**metadata})
    return sent

def sample():
    while not stop_sample.wait(.2):
        for who,p in list(processes.items()):
            if p.poll() is None:
                result=subprocess.run(['ps','-o','rss=','-p',str(p.pid)],capture_output=True,text=True)
                if result.stdout.strip():peak[who]=max(peak[who],int(result.stdout.strip())*1024)

try:
    ids={who:call(who,'init')[0]['peer_id'] for who in ['a','b']}
    for who in ['a','b']:
        config_path=work/who/'config.json';c=json.loads(config_path.read_text())
        c.update({k:network[k] for k in ['bootstrap_nodes','relay_nodes']})
        c['smart_routing']=True;c['allow_private_addresses']=False;c['publish_interval']='2s'
        c['direct_listen_addrs']=['/ip4/0.0.0.0/tcp/0'] if args.mode=='lan' else ['/ip4/127.0.0.1/tcp/0']
        config_path.write_text(json.dumps(c));os.chmod(config_path,0o600)
        assert call(who,'id')[0]['peer_id']==ids[who]
        start(who)
    sampler=threading.Thread(target=sample,daemon=True);sampler.start()
    emit({'test':'profile','id_survives_restart':True,'permissions':'0700 directory / 0600 private files','result':'PASS'})
    for who,other in [('a','b'),('b','a')]:call(who,'--','add-contact',ids[other],'--json $(literal) Боб 🐈')
    call('a','--','add-contact',ids['b'],'Обновлённое имя')
    assert len(call('a','contacts'))==1
    invalid,_=call('a','--','add-contact','invalid','Bad',check=False);assert invalid.returncode!=0
    own,_=call('a','--','add-contact',ids['a'],'Self',check=False);assert own.returncode!=0
    duplicate,_=call('a','run',check=False);assert duplicate.returncode!=0 and 'NODE_ALREADY_RUNNING' in duplicate.stderr
    emit({'test':'contacts_and_isolation','invalid_id_rejected':True,'self_rejected':True,'duplicate_updated':True,'second_daemon_rejected':True,'result':'PASS'})
    if args.mode=='lan':time.sleep(4)  # mDNS is asynchronous after both contacts are saved.
    for sender,receiver in [('a','b'),('b','a')]:
        text='Привет с Mac 🐈\nВторая строка\n$() `кавычки` \\ "'
        r=call(sender,'send',ids[receiver],'--stdin',input=text)[0]
        assert r['status']=='delivered' and r['route']==args.mode,r
        received=[v for v in call(receiver,'history',ids[sender]) if v['message_id']==r['message_id']]
        assert len(received)==1 and received[0]['message']['text']==text
        retry=call(sender,'retry',r['message_id'])[0];assert retry['status']=='delivered'
        emit({'test':'text','direction':sender+'→'+receiver,'route':r['route'],'utf8_bytes':len(text.encode()),'same_id_retry':True,'result':'PASS'})
    limit=call('a','send',ids['b'],'--stdin',input='🐈'*16384)[0];assert limit['status']=='delivered'
    over,_=call('a','send',ids['b'],'--stdin',input='🐈'*16384+'x',check=False);assert over.returncode==2
    emit({'test':'text_limit','accepted_utf8_bytes':65536,'rejected_utf8_bytes':65537,'result':'PASS'})
    sizes=[0,1,4<<20,(4<<20)+1] if args.mode=='lan' else [32<<20]
    for sender,receiver in [('a','b'),('b','a')]:
        for size in sizes:
            path=work/('Файл 🐈 '+str(size)+'.bin')
            with open(path,'wb') as f:
                if size:
                    f.truncate(size)
                    f.seek(max(0,size-9));f.write(b'last-byte'[-min(9,size):])
            send_file(sender,receiver,path)
    # Pause with a durable offset, stop the sender, restart and resume same file_id.
    resume_path=work/'resume.bin'
    with open(resume_path,'wb') as f:f.truncate(512<<20 if args.mode=='lan' else 32<<20)
    r=call('a','send-file',ids['b'],str(resume_path))[0]
    deadline=time.monotonic()+120
    while time.monotonic()<deadline:
        partial=record('a',r['file_id'])
        if partial['progress']>=4<<20 and partial['status']!='delivered':break
        time.sleep(.03)
    assert 0<partial['progress']<partial['size'],partial
    call('a','pause-file',r['file_id'])
    for _ in range(100):
        partial=record('a',r['file_id'])
        if partial['status']=='paused':break
        time.sleep(.05)
    stop('a');start('a')
    assert call('a','id')[0]['peer_id']==ids['a']
    restarted=record('a',r['file_id']);assert restarted['progress']>=partial['progress']
    call('a','resume-file',r['file_id']);final,routes=wait_file('a',r['file_id'])
    incoming=record('b',r['file_id']);expected=digest(resume_path);actual=digest(incoming['path'])
    assert expected==actual==final['sha256'] and final['progress']==resume_path.stat().st_size
    emit({'test':'pause_restart_resume','direction':'a→b','bytes':final['size'],'confirmed_offset_before_restart':partial['progress'],'offset_after_restart':restarted['progress'],'route':final['route'],'sha256':actual,'result':'PASS'})
    if args.large:
        path=work/'10-GiB.bin'
        with open(path,'wb') as f:f.truncate(10<<30)
        for sender,receiver in [('a','b'),('b','a')]:send_file(sender,receiver,path,large=True)
    for who in ['a','b']:
        stop(who)
        assert call(who,'id')[0]['peer_id']==ids[who]
        assert call(who,'history',ids['b' if who=='a' else 'a'])
        for base,dirs,files in os.walk(work/who):
            assert (os.stat(base).st_mode&0o777)==0o700,(base,oct(os.stat(base).st_mode&0o777))
            for filename in files:
                path=pathlib.Path(base)/filename
                assert (path.stat().st_mode&0o777)==0o600,(filename,oct(path.stat().st_mode&0o777))
    emit({'test':'offline_identity_history_permissions','result':'PASS'})
    report['result']='PASS'
except Exception as error:
    report['result']='FAIL';report['error']=str(error)
    print('FAIL:',error,flush=True)
finally:
    stop_sample.set()
    for who in list(processes):
        try:stop(who)
        except Exception:
            processes[who].kill();processes[who].wait()
    for f in logs:f.close()
    report['peak_core_rss_bytes']=peak
    output=ROOT/'docs/reports/macos'/(args.report+'-'+args.mode+'.json')
    output.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
    shutil.rmtree(work)
if report['result']!='PASS':raise SystemExit(1)
