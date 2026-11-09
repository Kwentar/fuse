"""Read-only deployment preflight; credentials remain in server_check.connect."""
import json
from server_check import connect

COMMAND = r'''python3 - <<'PY'
import json, os, platform, shutil, socket, subprocess
ports = {}
for port in (4001, 14001, 14002, 14003):
    sock = socket.socket()
    try:
        sock.bind(('0.0.0.0', port))
        ports[str(port)] = 'available'
    except OSError:
        ports[str(port)] = 'in_use'
    finally:
        sock.close()
print(json.dumps({'os': platform.platform(), 'root': os.geteuid() == 0,
                  'systemd': os.path.isdir('/run/systemd/system'),
                  'ports': ports, 'existing_project': os.path.exists('/opt/p2pmessenger'),
                  'disk_free_mb': shutil.disk_usage('/').free // 1048576}))
PY'''

try:
    with connect() as client:
        _, out, err = client.exec_command(COMMAND, timeout=15)
        data = out.read(16384)
        if out.channel.recv_exit_status() != 0:
            raise RuntimeError('Inventory failed')
        print(json.dumps(json.loads(data)))
except Exception as error:
    print(json.dumps({'error_type': type(error).__name__}))
    raise SystemExit(1)
