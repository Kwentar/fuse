"""Separate Windows processes; real public DHT/relay; no shared contact directory.

Run after build: python scripts/integration.py [--soak-seconds 905]
The only shared input is the configured infrastructure and exchanged Peer IDs.
"""
from pathlib import Path
import argparse
import json
import os
import platform
import subprocess
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
EXE = ROOT / 'dist/messenger.exe'
FLAGS = subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--soak-seconds', type=int, default=0)
    args = parser.parse_args()
    folder = ROOT / '.local' / ('integration-' + uuid.uuid4().hex[:10])
    folder.mkdir(parents=True)
    processes = []
    logs = []
    results = {'os': platform.platform(), 'started': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()), 'checks': {}, 'client_direct_listeners': False}

    def cli(who, *arguments, text=None, expected=0):
        command = [str(EXE), '--data-dir', str(folder / who), '--json', *arguments]
        response = subprocess.run(command, input=None if text is None else text.encode('utf-8'), stdout=subprocess.PIPE, stderr=subprocess.PIPE, creationflags=FLAGS, timeout=33)
        if response.returncode != expected:
            raise AssertionError(f'{who}/{arguments[0]} exit={response.returncode}: ' + response.stderr.decode('utf-8', 'replace')[:1000] + response.stdout.decode('utf-8', 'replace')[:1000])
        return [json.loads(line) for line in response.stdout.decode('utf-8').splitlines() if line]

    def start(who):
        log = (folder / (who + '-process.log')).open('ab')
        logs.append(log)
        proc = subprocess.Popen([str(EXE), '--data-dir', str(folder / who), 'run'], stdout=log, stderr=log, creationflags=FLAGS)
        processes.append(proc)
        return proc

    def ready(who, maximum=65):
        end = time.monotonic() + maximum
        last = None
        while time.monotonic() < end:
            try:
                last = cli(who, 'status')[0]
                if last['state'] == 'online' and last['reservations'] > 0:
                    return last
            except AssertionError:
                pass
            time.sleep(1)
        raise AssertionError(f'{who} not ready: {last}')

    def check(name, value=True):
        results['checks'][name] = value
        (folder / 'results.json').write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding='utf-8')
        print(json.dumps({'check': name, 'result': value}), flush=True)

    try:
        a = cli('a', 'init')[0]['peer_id']
        b = cli('b', 'init')[0]['peer_id']
        for who, identity in [('a', a), ('b', b)]:
            for _ in range(3):
                assert cli(who, 'init')[0]['peer_id'] == identity
                assert cli(who, 'id')[0]['peer_id'] == identity
        check('identity_stability')
        pa, pb = start('a'), start('b')
        sa, sb = ready('a'), ready('b')
        assert sa['config']['direct_listen_addrs'] == [] and sb['config']['direct_listen_addrs'] == []
        check('autorelay_ready')
        cli('a', 'run', expected=6)
        check('second_run_refused')
        found = cli('a', 'find', b)[0]
        assert found['source'] == 'dht' and found['addresses']
        check('signed_dht_discovery')
        for sender, target in [('a', b), ('b', a)]:
            reply = cli(sender, 'send', target, '--stdin', text='Привет через защищённый relay\nВторая строка\tтест')[0]
            assert reply['status'] == 'delivered' and reply['route'] == 'relay' and reply['ack'] == 'stored'
            repeated = cli(sender, 'retry', reply['message_id'])[0]
            assert repeated['status'] == 'delivered'
        check('bidirectional_cyrillic_relay_ack')
        escaped = '\x00' * 65536
        r = cli('a', 'send', b, '--stdin', text=escaped)[0]
        assert r['status'] == 'delivered'
        rows = cli('b', 'history', a)
        assert [x['message']['text'] for x in rows if x['message_id'] == r['message_id']] == [escaped]
        check('escaped_64k_network_ipc_history')
        # A killed receiver rebuilds the index and retains its identity on restart.
        pb.terminate(); pb.wait(timeout=15)
        assert cli('b', 'id')[0]['peer_id'] == b
        offline = cli('b', 'history', a)
        assert any(x['message_id'] == r['message_id'] for x in offline)
        pb = start('b'); ready('b')
        again = cli('a', 'send', b, '--stdin', text='После перезапуска получателя')[0]
        assert again['status'] == 'delivered'
        check('receiver_restart_and_offline_history')
        never = cli('never-online', 'init')[0]['peer_id']
        begin = time.monotonic()
        missing = cli('a', 'send', never, '--stdin', text='Unavailable contact test', expected=3)[0]
        elapsed = time.monotonic() - begin
        assert elapsed <= 30 and missing['status'] == 'not_sent'
        check('unavailable_contact', {'seconds': round(elapsed, 3), 'status': missing['status'], 'code': missing['code']})
        if args.soak_seconds:
            started = time.monotonic(); rounds = 0; count = {'a': 0, 'b': 0}
            payload = 'я' * 16384
            while time.monotonic() - started < args.soak_seconds:
                for sender, target in [('a', b), ('b', a)]:
                    delivered = cli(sender, 'send', target, '--stdin', text=payload)[0]
                    assert delivered['status'] == 'delivered' and delivered['route'] == 'relay'
                    count[sender] += len(payload.encode('utf-8'))
                rounds += 1
                if rounds % 10 == 0:
                    print(json.dumps({'soak_elapsed': round(time.monotonic() - started), 'bytes_per_direction': count}), flush=True)
                time.sleep(min(10, max(0, args.soak_seconds - (time.monotonic() - started))))
            assert min(count.values()) > 1048576
            check('long_exchange', {'seconds': round(time.monotonic()-started, 2), 'bytes_per_direction': count})
        check('completed')
    except Exception as error:
        results['failure'] = str(error)
        (folder / 'results.json').write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding='utf-8')
        raise
    finally:
        for proc in processes:
            if proc.poll() is None:
                proc.terminate()
            proc.wait(timeout=15)
        for log in logs:
            log.close()
        print(json.dumps({'report': str(folder / 'results.json')}), flush=True)


if __name__ == '__main__':
    main()
