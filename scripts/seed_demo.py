"""Create the demo bots through the backend (logs in as the analyst).

    python scripts/seed_demo.py [--api http://localhost:8080] [--n 50]
"""
import argparse
import json
import time
import urllib.request


def call(url, body=None, token=None):
    req = urllib.request.Request(url, data=json.dumps(body).encode() if body is not None else None,
                                 headers={"Content-Type": "application/json",
                                          **({"Authorization": f"Bearer {token}"} if token else {})})
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.loads(r.read())


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--api", default="http://localhost:8080")
    ap.add_argument("--n", type=int, default=50)
    ap.add_argument("--password", default="analyst")
    a = ap.parse_args()
    token = call(f"{a.api}/api/login", {"username": "analyst", "password": a.password})["token"]
    call(f"{a.api}/api/sim/seed", {"n": a.n}, token)
    while True:
        st = call(f"{a.api}/api/sim/status", token=token)
        print(f"\r{st['state']:8s} {st['message'][:70]:70s}", end="", flush=True)
        if st["state"] in ("idle", "error"):
            break
        time.sleep(2)
    print()


if __name__ == "__main__":
    main()
