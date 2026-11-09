"""Read the project's startup record and save its relay address locally."""
import ipaddress
import json
from server_check import ROOT, connect, credentials
from deploy_initial import command, UNIT

def sync():
    with connect() as client:
        command(client, 'systemctl is-active ' + UNIT)
        logs = command(client, 'journalctl -u ' + UNIT + ' -n 100 --no-pager -o cat')
        record = None
        for line in logs.splitlines():
            try:
                data = json.loads(line)
                if 'peer_id' in data and 'addresses' in data:
                    record = data
            except ValueError:
                pass
        if not record:
            raise RuntimeError('No successful project startup record')
        public_ip = str(ipaddress.ip_address(credentials()['ip']))
        address = f'/ip4/{public_ip}/tcp/4001/p2p/{record["peer_id"]}'
        defaults = {'bootstrap_nodes': [address], 'relay_nodes': [address]}
        (ROOT / '.local').mkdir(exist_ok=True)
        (ROOT / '.local/default-network.json').write_text(json.dumps(defaults, indent=2) + '\n')
        (ROOT / '.local/deployment.json').write_text(json.dumps({'unit': UNIT, 'peer_id': record['peer_id'], 'status': 'active', 'default_network': defaults}, indent=2))
        print(json.dumps({'deployment': 'active', 'peer_id': record['peer_id'], 'port': 4001, 'packages_installed': False}))

if __name__ == '__main__':
    sync()
