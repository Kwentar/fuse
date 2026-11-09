"""Read credentials only in-process; never print them or raw SSH exceptions."""
from pathlib import Path
import json
import re
import sys

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / ".tools" / "python"))
import paramiko


def credentials():
    values = {}
    for line in (ROOT / ".env_server").read_text(encoding="utf-8-sig").splitlines():
        match = re.match(r"^\s*(ip|user|pass)\s*=\s*(.*?)\s*$", line)
        if not match:
            continue
        key, value = match.groups()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        values[key] = value
    if any(not values.get(key) for key in ("ip", "user", "pass")):
        raise ValueError("Missing credential field")
    return values


def connect():
    values = credentials()
    state = ROOT / ".local"
    state.mkdir(exist_ok=True)
    hosts = state / "ssh_known_hosts"
    client = paramiko.SSHClient()
    client.load_system_host_keys()
    if hosts.exists():
        client.load_host_keys(str(hosts))

    class PinOnFirstUse(paramiko.MissingHostKeyPolicy):
        def missing_host_key(self, ssh, hostname, key):
            ssh.get_host_keys().add(hostname, key.get_name(), key)
            ssh.save_host_keys(str(hosts))

    client.set_missing_host_key_policy(PinOnFirstUse())
    client.connect(values["ip"], username=values["user"], password=values["pass"],
                   timeout=10, auth_timeout=10, banner_timeout=10,
                   look_for_keys=False, allow_agent=False)
    return client


def main():
    try:
        with connect() as client:
            _, stdout, stderr = client.exec_command("printf 'SSH_OK\\n'; uname -s -m", timeout=10)
            output = stdout.read(4096).decode("utf-8", "replace").strip()
            status = stdout.channel.recv_exit_status()
            if status != 0 or not output.startswith("SSH_OK\n"):
                raise RuntimeError("Probe failed")
            print(json.dumps({"ssh": "ok", "remote": output.splitlines()[1],
                              "remote_changes": False, "host_key": "pinned"}))
        return 0
    except Exception as error:
        print(json.dumps({"ssh": "failed", "error_type": type(error).__name__}))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
