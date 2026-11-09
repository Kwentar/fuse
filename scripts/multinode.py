"""Three temporary Linux processes + separate Windows clients, real DHT/relay.

Uses only this project's existing binary and its own transient systemd units.
The default relay remains running throughout this test.
"""
import ipaddress
import json
import os
from pathlib import Path
import shlex
import subprocess
import time
import uuid
from server_check import ROOT, connect, credentials
from deploy_initial import command

FLAGS = subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0


def main():
    tag = uuid.uuid4().hex[:8]
    folder = ROOT / '.local' / ('multinode-' + tag)
    folder.mkdir()
    units, remote_configs, clients, handles = [], [], [], []
    report = {'test': 'three_linux_processes_two_windows_processes', 'checks': {}}
    public = str(ipaddress.ip_address(credentials()['ip']))
    ssh = connect()

    def check(name, data=True):
        report['checks'][name] = data
        (folder / 'results.json').write_text(json.dumps(report, indent=2))
        print(json.dumps({'check': name, 'result': data}), flush=True)

    def server(name, port, neighbors, short=False):
        unit = f'p2pmessenger-test-{tag}-{name}'
        config = json.loads((ROOT / 'configs/bootstrap.example.json').read_text())
        config['data_dir'] = f'/var/lib/p2pm-bootstrap/test-{tag}/{name}'
        config['listen_addrs'] = [f'/ip4/0.0.0.0/tcp/{port}']
        config['announce_addrs'] = [f'/ip4/{public}/tcp/{port}']
        config['bootstrap_nodes'] = neighbors
        if short:
            config['relay']['circuit_duration'] = '5s'
            config['relay']['circuit_bytes_per_direction'] = 262144
        remote = f'/opt/p2pmessenger/test-{tag}-{name}.json'
        with ssh.open_sftp() as sftp:
            with sftp.open(remote, 'w') as f:
                f.write(json.dumps(config))
            sftp.chmod(remote, 0o644)
        remote_configs.append(remote)
        command(ssh, 'systemd-run --quiet --unit=' + unit + ' --property=DynamicUser=yes --property=User=p2pmessenger --property=StateDirectory=p2pm-bootstrap --property=StateDirectoryMode=0700 --property=UMask=0077 --property=NoNewPrivileges=yes --property=PrivateTmp=yes --property=ProtectSystem=strict --property=MemoryMax=512M --property=RuntimeMaxSec=600 /opt/p2pmessenger/bootstrap-node --config ' + remote)
        units.append(unit)
        for _ in range(20):
            logs = command(ssh, f'journalctl -u {unit} -n 10 --no-pager -o cat')
            for line in logs.splitlines():
                try:
                    data = json.loads(line)
                    if 'peer_id' in data:
                        return unit, f'/ip4/{public}/tcp/{port}/p2p/{data["peer_id"]}'
                except ValueError:
                    pass
            time.sleep(0.5)
        raise AssertionError('Test server did not start: ' + name)

    def cli(who, *args, text=None):
        r = subprocess.run([str(ROOT / 'dist/messenger.exe'), '--data-dir', str(folder / who), '--json', *args], input=None if text is None else text.encode(), stdout=subprocess.PIPE, stderr=subprocess.PIPE, creationflags=FLAGS, timeout=33)
        if r.returncode:
            raise AssertionError(f'{who}/{args[0]} failed: ' + r.stderr.decode()[:1000] + r.stdout.decode()[:1000])
        return [json.loads(line) for line in r.stdout.decode().splitlines()]

    def configure(who, bootstrap, relays):
        data = json.loads((folder / who / 'config.json').read_text())
        data['bootstrap_nodes'], data['relay_nodes'], data['publish_interval'] = bootstrap, relays, '3s'
        (folder / who / 'config.json').write_text(json.dumps(data))

    def start(who):
        handle = (folder / (who + '.log')).open('ab'); handles.append(handle)
        p = subprocess.Popen([str(ROOT / 'dist/messenger.exe'), '--data-dir', str(folder / who), 'run'], stdout=handle, stderr=handle, creationflags=FLAGS)
        clients.append(p)
        return p

    def ready(who, reservations=1):
        end = time.monotonic()+60; status = None
        while time.monotonic()<end:
            try:
                status=cli(who,'status')[0]
                if status['state']=='online' and status['reservations']>=reservations:
                    return status
            except AssertionError:
                pass
            time.sleep(1)
        raise AssertionError(f'{who} unavailable: {status}')

    def send(who, target, text):
        reply = cli(who, 'send', target, '--stdin', text=text)[0]
        assert reply['status']=='delivered' and reply['route']=='relay', reply
        return reply

    try:
        b0unit,b0=server('b0',14001,[])
        s1unit,s1=server('s1',14002,[b0])
        s2unit,s2=server('s2',14003,[b0,s1],True)
        a=cli('a','init')[0]['peer_id'];b=cli('b','init')[0]['peer_id']
        configure('a',[b0,s1],[s1]);configure('b',[b0,s2],[s2])
        pa,pb=start('a'),start('b');ready('a');ready('b')
        assert not (folder/'a'/'peer-cache.json').exists() or b not in json.loads((folder/'a'/'peer-cache.json').read_text())['contacts']
        contact=cli('a','find',b)[0]
        assert contact['source']=='dht' and all('/tcp/14003/' in addr for addr in contact['addresses'])
        send('a',b,'Через другой DHT-сервер и другой relay')
        check('different_relays_signed_discovery')
        time.sleep(6)
        send('a',b,'Новое соединение после лимита circuit 5s')
        check('circuit_duration_reconnect')
        command(ssh,'systemctl stop '+b0unit)
        old_seq=contact['seq'];pb.terminate();pb.wait(timeout=15)
        pb=start('b');ready('b')
        end=time.monotonic()+30
        while time.monotonic()<end:
            fresh=cli('a','find',b)[0]
            if fresh['seq']>old_seq:
                break
            time.sleep(1)
        assert fresh['source']=='dht' and fresh['seq']>old_seq
        send('a',b,'Bootstrap B0 выключен, новая запись найдена')
        send('b',a,'Ответ без первоначального bootstrap')
        check('bootstrap_stopped_new_sequence_discovered')
        # Restart A with only surviving infrastructure; its identity persists.
        pa.terminate();pa.wait(timeout=15);configure('a',[s1],[s1]);pa=start('a');ready('a')
        assert cli('a','id')[0]['peer_id']==a
        send('a',b,'Перезапуск клиента без B0')
        check('client_restart_without_initial_bootstrap')
        pb.terminate();pb.wait(timeout=15);configure('b',[s1,s2],[s2,s1]);pb=start('b');ready('b',2)
        command(ssh,'systemctl stop '+s2unit)
        begin=time.monotonic();end=begin+60
        while time.monotonic()<end:
            status=cli('b','status')[0]
            if status['state']=='online' and status['reservations']==1 and all('/tcp/14003/' not in addr for addr in status['contact_addresses']):
                break
            time.sleep(1)
        assert status['reservations']==1 and all('/tcp/14003/' not in addr for addr in status['contact_addresses'])
        cli('a','find',b)
        send('a',b,'Relay S2 выключен, используем S1')
        check('relay_failover',{'recovery_seconds':round(time.monotonic()-begin,2)})
        check('completed')
    except Exception as error:
        report['failure']=str(error)
        (folder/'results.json').write_text(json.dumps(report,indent=2))
        raise
    finally:
        for p in clients:
            if p.poll() is None:p.terminate()
            p.wait(timeout=15)
        for h in handles:h.close()
        for unit in reversed(units):
            _,out,_=ssh.exec_command('systemctl stop '+unit,timeout=20)
            out.channel.recv_exit_status()
        with ssh.open_sftp() as sftp:
            for path in remote_configs:
                sftp.remove(path)
        # Remove only the generated test subtree, after all of its units stop.
        cleanup="import pathlib,shutil; p=pathlib.Path('/var/lib/p2pm-bootstrap/test-"+tag+"'); r=p.resolve(); assert r.name=='test-"+tag+"' and str(r.parent)=='/var/lib/private/p2pm-bootstrap'; shutil.rmtree(r) if r.exists() else None"
        command(ssh,'python3 -c '+shlex.quote(cleanup))
        ssh.close()
        print(json.dumps({'report':str(folder/'results.json')}),flush=True)


if __name__=='__main__':
    main()
