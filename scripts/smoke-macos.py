#!/usr/bin/env python3
"""Launch the actual .app twice against a temporary loopback bootstrap.

Requires a local build of cmd/bootstrap. Never uses the user's profile or servers.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--app', type=Path, default=ROOT / 'dist/macos-0.4.1-arm64/Fuse.app')
parser.add_argument('--bootstrap-core', type=Path, required=True)
args = parser.parse_args()
app = args.app.resolve()
helper = app / 'Contents/Helpers/messenger'

with tempfile.TemporaryDirectory(prefix='fuse-smoke-', dir='/tmp') as temporary:
    work = Path(temporary)
    profile = work / 'profile'
    server = gui = None
    children = []
    handles = []

    def start(command, label):
        output = open(work / (label + '.out'), 'w')
        error = open(work / (label + '.err'), 'w')
        handles.extend([output, error])
        return subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=output, stderr=error)

    def call(*command):
        result = subprocess.run([str(helper), '--data-dir', str(profile), '--json', *command],
                                capture_output=True, text=True, timeout=10, check=True)
        return [json.loads(line) for line in result.stdout.splitlines() if line.strip()]

    def owned_helpers():
        if gui is None:
            return []
        output = subprocess.check_output(['ps', '-axo', 'pid=,ppid=,command='], text=True)
        return [int(parts[0]) for line in output.splitlines()
                if len(parts := line.strip().split(None, 2)) == 3
                and int(parts[1]) == gui.pid
                and parts[2].startswith(str(helper) + ' --data-dir ' + str(profile) + ' ')
                and parts[2].endswith('--json --gui run')]

    def stop_gui():
        global children
        children = list(set(children + owned_helpers()))
        if gui is not None and gui.poll() is None:
            gui.terminate()
            gui.wait(timeout=15)
        for pid in children:
            try:
                os.kill(pid, signal.SIGINT)
            except ProcessLookupError:
                pass
        for _ in range(100):
            if not (profile / 'control.sock').exists():
                break
            time.sleep(.1)
        else:
            raise RuntimeError('Temporary helper did not stop gracefully')
        children = []

    try:
        config = json.loads((ROOT / 'configs/bootstrap.example.json').read_text())
        config.update(data_dir=str(work / 'server'), listen_addrs=['/ip4/127.0.0.1/tcp/0'],
                      announce_addrs=[], allow_private_addresses=True)
        config_path = work / 'server.json'
        config_path.write_text(json.dumps(config))
        server = start([str(args.bootstrap_core.resolve()), '--config', str(config_path)], 'server')
        for _ in range(100):
            if server.poll() is not None:
                raise RuntimeError('Temporary bootstrap exited')
            lines = (work / 'server.out').read_text().splitlines()
            if lines:
                address = json.loads(lines[0])['addresses'][0]
                break
            time.sleep(.1)
        else:
            raise RuntimeError('Temporary bootstrap did not start')

        identity = call('init')[0]['peer_id']
        key_hash = hashlib.sha256((profile / 'identity.key').read_bytes()).digest()
        config_path = profile / 'config.json'
        config = json.loads(config_path.read_text())
        assert config['bootstrap_nodes'] == config['relay_nodes'] == [], 'Unexpected bundled servers'
        config.update(bootstrap_nodes=[address], relay_nodes=[address], allow_private_addresses=True,
                      direct_listen_addrs=['/ip4/127.0.0.1/tcp/0'])
        config_path.write_text(json.dumps(config))
        for attempt in range(2):
            # Keep automatic exports isolated from the user's Downloads folder.
            (work / 'downloads').mkdir(mode=0o700, exist_ok=True)
            downloads = profile / 'downloads.json'
            downloads.write_text(json.dumps({'path': str(work / 'downloads')}))
            downloads.chmod(0o600)
            gui = start([str(app / 'Contents/MacOS/P2PMessenger'), '--profile', str(profile)], 'gui-' + str(attempt))
            for _ in range(150):
                if gui.poll() is not None:
                    raise RuntimeError('GUI exited before its helper became ready')
                children = owned_helpers()
                try:
                    state = call('status')[0]
                    if state['bootstrap'] == 'connected':
                        break
                except subprocess.CalledProcessError:
                    pass
                time.sleep(.2)
            else:
                raise RuntimeError('GUI-owned helper did not connect')
            assert children and state['peer_id'] == identity
            assert state['config']['smart_routing'] is True and 'transfers' in state
            assert hashlib.sha256((profile / 'identity.key').read_bytes()).digest() == key_hash
            marker = json.loads((profile / 'macos-version.json').read_text())
            assert (Path(marker['backup']) / 'identity.key').read_bytes() == (profile / 'identity.key').read_bytes()
            stop_gui()
            print('PASS: actual Fuse.app launch, owned helper, bootstrap, preserved ID and backup; run', attempt + 1)
    finally:
        stop_gui()
        if server is not None and server.poll() is None:
            server.send_signal(signal.SIGINT)
            server.wait(timeout=15)
        for handle in handles:
            handle.close()
