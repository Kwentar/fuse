"""Stage/activate only the project binary; never reboot the server."""
import argparse
import hashlib
import json
from server_check import ROOT, connect
from deploy_initial import command, UNIT

def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('action',choices=['stage','activate'])
    args=parser.parse_args()
    with connect() as ssh:
        with ssh.open_sftp() as s:
            with s.open('/opt/p2pmessenger/.project-marker') as f:
                if f.read()!=b'p2pmessenger-mvp-0.1.0\n':
                    raise RuntimeError('Project ownership marker mismatch')
            if args.action=='stage':
                binary=ROOT/'.local/release/bootstrap-node'
                digest=hashlib.sha256(binary.read_bytes()).hexdigest()
                progress=[0]
                def callback(sent,total):
                    if sent-progress[0]>=5*1024*1024 or sent==total:
                        progress[0]=sent
                        print(json.dumps({'upload_bytes':sent,'total':total}),flush=True)
                s.put(str(binary),'/opt/p2pmessenger/bootstrap-node.next',callback=callback)
                s.chmod('/opt/p2pmessenger/bootstrap-node.next',0o755)
                remote=command(ssh,'sha256sum /opt/p2pmessenger/bootstrap-node.next').split()[0]
                if remote!=digest:
                    raise RuntimeError('Binary checksum mismatch')
                print(json.dumps({'stage':'verified','sha256':digest}),flush=True)
            else:
                s.stat('/opt/p2pmessenger/bootstrap-node.next')
                s.posix_rename('/opt/p2pmessenger/bootstrap-node','/opt/p2pmessenger/bootstrap-node.previous')
                s.posix_rename('/opt/p2pmessenger/bootstrap-node.next','/opt/p2pmessenger/bootstrap-node')
                command(ssh,'systemctl restart '+UNIT)
                command(ssh,'systemctl is-active '+UNIT)
                print(json.dumps({'service':'active','server_rebooted':False,'other_services_changed':False}),flush=True)

if __name__=='__main__':
    try:
        main()
    except Exception as error:
        print(json.dumps({'error_type':type(error).__name__,'reason':str(error) if isinstance(error,RuntimeError) else 'details suppressed'}))
        raise SystemExit(1)
