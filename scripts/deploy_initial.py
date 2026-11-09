"""Install only the new project binary/unit. Refuse to overwrite existing paths.

No packages, firewall changes, reboots, or operations on other services.
"""
import ipaddress
import json
import time
from server_check import ROOT, connect, credentials

REMOTE = '/opt/p2pmessenger'
UNIT = 'p2pmessenger-bootstrap.service'


def command(client, text):
    _, out, err = client.exec_command(text, timeout=30)
    result = out.read(65536).decode('utf-8', 'replace')
    error = err.read(65536).decode('utf-8', 'replace')
    if out.channel.recv_exit_status() != 0:
        raise RuntimeError('Remote project operation failed: ' + text.split()[0])
    return result


def main():
    public_ip = str(ipaddress.ip_address(credentials()['ip']))
    config = json.loads((ROOT / 'configs/bootstrap.example.json').read_text())
    config['announce_addrs'] = [f'/ip4/{public_ip}/tcp/4001']
    with connect() as client:
        command(client, 'test ! -e /opt/p2pmessenger')
        command(client, 'test ! -e /etc/systemd/system/p2pmessenger-bootstrap.service')
        with client.open_sftp() as sftp:
            sftp.mkdir(REMOTE, 0o755)
            with sftp.file(REMOTE + '/.project-marker', 'w') as f:
                f.write('p2pmessenger-mvp-0.1.0\n')
            sftp.put(str(ROOT / 'dist/bootstrap-node'), REMOTE + '/bootstrap-node')
            sftp.chmod(REMOTE + '/bootstrap-node', 0o755)
            with sftp.file(REMOTE + '/config.json', 'w') as f:
                f.write(json.dumps(config, indent=2) + '\n')
            sftp.chmod(REMOTE + '/config.json', 0o644)
            sftp.put(str(ROOT / 'deploy/bootstrap-node.service'), '/etc/systemd/system/' + UNIT)
            sftp.chmod('/etc/systemd/system/' + UNIT, 0o644)
        command(client, 'systemctl daemon-reload')
        command(client, 'systemctl enable --now ' + UNIT)
        record = None
        for _ in range(15):
            logs = command(client, 'journalctl -u ' + UNIT + ' -n 30 --no-pager -o cat')
            for line in logs.splitlines():
                try:
                    data = json.loads(line)
                    if 'peer_id' in data and 'addresses' in data:
                        record = data
                except ValueError:
                    pass
            if record:
                break
            time.sleep(1)
        if not record:
            raise RuntimeError('Project startup record not found')
        command(client, 'systemctl is-active ' + UNIT)
        address = f'/ip4/{public_ip}/tcp/4001/p2p/{record["peer_id"]}'
        defaults = {'bootstrap_nodes': [address], 'relay_nodes': [address]}
        (ROOT / '.local').mkdir(exist_ok=True)
        (ROOT / '.local/default-network.json').write_text(json.dumps(defaults, indent=2) + '\n')
        (ROOT / '.local/deployment.json').write_text(json.dumps({'unit': UNIT, 'peer_id': record['peer_id'], 'status': 'active', 'default_network': defaults}, indent=2))
        print(json.dumps({'deployment': 'active', 'peer_id': record['peer_id'], 'port': 4001,
                          'server_rebooted': False, 'packages_installed': False,
                          'other_services_changed': False}))


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print(json.dumps({'deployment': 'failed', 'error_type': type(error).__name__, 'reason': str(error) if isinstance(error, RuntimeError) else 'details suppressed'}))
        raise SystemExit(1)
