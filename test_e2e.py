#!/usr/bin/env python3
"""End-to-end test: starts puck-tak locally and acts like TAK clients.

usage: test_e2e.py /path/to/puck-tak-native /path/to/scratch-dir
"""
import hashlib
import http.client
import json
import os
import signal
import socket
import subprocess
import sys
import tempfile
import time

BIN, SCRATCH = sys.argv[1], sys.argv[2]
COT, HTTP = 18087, 18080
DATA = os.path.join(SCRATCH, "data")
PID = os.path.join(SCRATCH, "pid")
LOG = os.path.join(SCRATCH, "server.log")

passed = failed = 0


def check(name, cond, detail=""):
    global passed, failed
    if cond:
        passed += 1
        print(f"  ok    {name}")
    else:
        failed += 1
        print(f"  FAIL  {name} {detail}")


def start(extra=()):
    p = subprocess.Popen(
        [BIN, "-bind", "127.0.0.1", "-cot-port", str(COT), "-http-port", str(HTTP),
         "-data", DATA, "-log", LOG, "-pidfile", PID, *extra])
    for _ in range(50):
        try:
            socket.create_connection(("127.0.0.1", HTTP), timeout=0.2).close()
            return p
        except OSError:
            time.sleep(0.1)
    raise SystemExit("server did not start")


def stop(p):
    p.send_signal(signal.SIGTERM)
    p.wait(timeout=5)


def ev(uid, typ="a-f-G-U-C", stale_s=300, lat=31.95, lon=35.93, extra=""):
    t = time.time()
    fmt = lambda x: time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime(x)) + ".000Z"
    return (f'<event version="2.0" uid="{uid}" type="{typ}" time="{fmt(t)}" start="{fmt(t)}" '
            f'stale="{fmt(t + stale_s)}" how="m-g"><point lat="{lat}" lon="{lon}" hae="0" ce="10" le="10"/>'
            f'<detail><contact callsign="{uid}"/>{extra}</detail></event>').encode()


class Cli:
    def __init__(self):
        self.s = socket.create_connection(("127.0.0.1", COT))
        self.s.settimeout(0.4)
        self.buf = b""

    def send(self, b):
        self.s.sendall(b)

    def recv_events(self, wait=0.8):
        end = time.time() + wait
        while time.time() < end:
            try:
                d = self.s.recv(65536)
                if not d:
                    break
                self.buf += d
            except socket.timeout:
                pass
        out = []
        while True:
            i = self.buf.find(b"<event")
            if i < 0:
                break
            gt = self.buf.find(b">", i)
            if gt < 0:
                break
            if self.buf[gt - 1:gt] == b"/":  # self-closed <event .../>
                end = gt + 1
            else:
                j = self.buf.find(b"</event>", gt)
                if j < 0:
                    break
                end = j + 8
            out.append(self.buf[i:end])
            self.buf = self.buf[end:]
        return out

    def uids(self, wait=0.8):
        import re
        return [re.search(rb' uid="([^"]*)"', e).group(1).decode() for e in self.recv_events(wait)]


def req(method, path, body=None, headers=None):
    c = http.client.HTTPConnection("127.0.0.1", HTTP, timeout=10)
    c.request(method, path, body=body, headers=headers or {})
    r = c.getresponse()
    data = r.read()
    c.close()
    return r.status, dict(r.getheaders()), data


def multipart(field, filename, content, ctype="application/x-zip-compressed"):
    b = "----t" + hashlib.md5(content).hexdigest()[:10]
    body = (f'--{b}\r\nContent-Disposition: form-data; name="{field}"; filename="{filename}"\r\n'
            f'Content-Type: {ctype}\r\n\r\n').encode() + content + f"\r\n--{b}--\r\n".encode()
    return body, {"Content-Type": f"multipart/form-data; boundary={b}"}


os.makedirs(SCRATCH, exist_ok=True)
for f in (LOG, LOG + ".1", PID):
    if os.path.exists(f):
        os.remove(f)
if os.path.isdir(DATA):
    for f in os.listdir(DATA):
        os.remove(os.path.join(DATA, f))

p = start(["-max-upload-mb", "1", "-max-store-mb", "2"])
try:
    print("CoT relay")
    a, b = Cli(), Cli()
    time.sleep(0.2)
    e1 = ev("ANDROID-A")
    a.send(e1[:50])
    time.sleep(0.15)
    a.send(e1[50:])
    got = b.recv_events()
    check("fragmented event arrives whole at other client", got == [e1])
    check("sender does not get its own event back", a.recv_events(0.3) == [])

    e2, e3 = ev("ANDROID-A", typ="b-t-f"), b'<event version="2.0" uid="SELF" type="b-m-p-s-m" time="2026-01-01T00:00:00Z" start="2026-01-01T00:00:00Z" stale="2026-01-01T00:05:00Z" how="h-g-i-g-o"/>'
    a.send(b'<?xml version="1.0"?>\n' + e2 + b"\n" + e3)
    got = b.recv_events()
    check("two events in one write are split and both delivered", got == [e2, e3], got)

    a.send(b"<events><junk/></events>" + ev("AFTER-JUNK"))
    check("junk like <events> is skipped", b.uids() == ["AFTER-JUNK"])

    a.send(ev("PING", typ="t-x-c-t"))
    check("ping events are not relayed", b.recv_events(0.4) == [])

    big = ev("BIG", extra="<remarks>" + "x" * 200000 + "</remarks>")
    a.send(big)
    check("200 KB event relayed intact", b.recv_events(1.5) == [big])

    print("Position cache")
    a.send(ev("PAST", stale_s=-60))
    a.send(ev("MARKER-1", typ="b-m-p-w", stale_s=3600))
    a.send(ev("CHAT", typ="b-t-f"))
    time.sleep(0.3)
    c = Cli()
    seen = sorted(c.uids(1.0))
    check("late joiner receives live positions/markers", "ANDROID-A" in seen and "MARKER-1" in seen, seen)
    check("late joiner does not get stale or chat events", "PAST" not in seen and "CHAT" not in seen, seen)
    a.send(ev("DEL", typ="t-x-d-d", extra='<link uid="MARKER-1" relation="none" type="b-m-p-w"/>'))
    time.sleep(0.3)
    d = Cli()
    check("delete event removes marker from cache", "MARKER-1" not in d.uids(1.0))
    for x in (a, b, c, d):
        x.s.close()

    print("Data packages")
    zipish = os.urandom(5000)
    h = hashlib.sha256(zipish).hexdigest()
    body, hdr = multipart("assetfile", "plan.zip", zipish)
    st, _, txt = req("POST", f"/Marti/sync/missionupload?hash={h}&filename=plan.zip&creatorUid=ANDROID-A", body, hdr)
    check("missionupload ok and returns metadata url", st == 200 and txt.decode().endswith(f"/Marti/api/sync/metadata/{h}/tool"), (st, txt))

    st, _, txt = req("GET", "/Marti/sync/search?keyword=missionpackage&tool=public")
    res = json.loads(txt)
    check("search lists the package with expected fields", st == 200 and res["resultCount"] == 1 and res["results"][0]["Hash"] == h
          and res["results"][0]["Name"] == "plan.zip" and res["results"][0]["Size"] == 5000 and res["results"][0]["CreatorUid"] == "ANDROID-A", res)
    check("keyword filter works", json.loads(req("GET", "/Marti/sync/search?keyword=nomatch")[2])["resultCount"] == 0
          and json.loads(req("GET", "/Marti/sync/search?keyword=plan")[2])["resultCount"] == 1)

    st, hd, data = req("GET", f"/Marti/sync/content?hash={h}")
    check("download by hash is byte-identical", st == 200 and data == zipish)
    check("download names the file", "plan.zip" in hd.get("Content-Disposition", ""), hd)
    st, _, _ = req("HEAD", f"/Marti/sync/content?hash={h}")
    check("HEAD content returns 200", st == 200)
    st, _, _ = req("HEAD", f"/Marti/sync/content?hash={'0' * 64}")
    check("HEAD unknown hash returns 404", st == 404)
    st, _, data = req("GET", f"/Marti/api/sync/metadata/{h}/tool")
    check("download via metadata path", st == 200 and data == zipish)
    st, _, data = req("GET", f"/Marti/sync/missionquery?hash={h}")
    check("download via missionquery", st == 200 and data == zipish)
    uid = res["results"][0]["UID"]
    st, _, data = req("GET", f"/Marti/sync/content?uid={uid}")
    check("download by uid", st == 200 and data == zipish)
    st, hd, data = req("GET", f"/Marti/sync/content?hash={h}", headers={"Range": "bytes=10-19"})
    check("range request supported", st == 206 and data == zipish[10:20])

    raw = b"hello raw upload"
    st, _, txt = req("POST", "/Marti/sync/upload?name=note.txt&creatorUid=WINTAK-1", raw, {"Content-Type": "text/plain"})
    j = json.loads(txt)
    check("raw upload returns the documented JSON keys", st == 200 and sorted(j) == sorted(
        ["UID", "SubmissionDateTime", "MIMEType", "SubmissionUser", "PrimaryKey", "Hash"]) and j["Hash"] == hashlib.sha256(raw).hexdigest(), j)

    body, hdr = multipart("assetfile", "plan.zip", zipish)
    req("POST", f"/Marti/sync/missionupload?hash={h}&filename=plan-renamed.zip&creatorUid=X", body, hdr)
    n = json.loads(req("GET", "/Marti/sync/search")[2])["resultCount"]
    check("re-uploading identical bytes does not duplicate", n == 2, n)

    st, _, txt = req("PUT", f"/Marti/api/sync/metadata/{h}/tool", b"private")
    check("privacy PUT returns Okay", st == 200 and txt == b"Okay")
    n = json.loads(req("GET", "/Marti/sync/search")[2])["resultCount"]
    check("private packages are hidden from search", n == 1, n)
    req("PUT", f"/Marti/api/sync/metadata/{h}/tool", b"public")

    st, hd, txt = req("GET", "/")
    check("web page lists packages", st == 200 and b"note.txt" in txt and b"puck-tak" in txt)
    body, hdr = multipart("assetfile", "from browser.txt", b"via web form", "text/plain")
    st, hd, _ = req("POST", "/Marti/sync/upload?redirect=1&creatorUid=web", body, hdr)
    check("web form upload redirects home", st == 303 and hd.get("Location") == "/", (st, hd))

    st, _, txt = req("GET", "/Marti/api/nonexistent")
    check("unknown path returns 404 JSON", st == 404 and b"not found" in txt)
    st, _, _ = req("GET", "/Marti/sync/content?hash=../../etc/passwd")
    check("path traversal in hash is rejected", st == 404)

    st, _, _ = req("POST", "/Marti/sync/upload?name=big.bin", os.urandom(2 << 20), {"Content-Type": "application/octet-stream"})
    check("upload over the size limit returns 413", st == 413, st)
    for i in range(3):
        st, _, _ = req("POST", f"/Marti/sync/upload?name=f{i}.bin", os.urandom(900 * 1024), {"Content-Type": "application/octet-stream"})
    check("upload past the storage quota returns 507", st == 507, st)
    leftovers = [f for f in os.listdir(DATA) if f.endswith(".tmp")]
    check("no temp files left after failed uploads", not leftovers, leftovers)

    print("Persistence")
    before = json.loads(req("GET", "/Marti/sync/search")[2])["resultCount"]
    stop(p)
    p = start(["-max-upload-mb", "1", "-max-store-mb", "2"])
    after = json.loads(req("GET", "/Marti/sync/search")[2])["resultCount"]
    check("packages survive a restart", before == after and after >= 3, (before, after))
    st, _, data = req("GET", f"/Marti/sync/content?hash={h}")
    check("file still downloads after restart", st == 200 and data == zipish)
    stop(p)
    check("pidfile removed on clean shutdown", not os.path.exists(PID))

    print("Waiting for a bind address")
    open(LOG, "w").close()
    w = subprocess.Popen([BIN, "-bind", "10.255.255.1", "-cot-port", str(COT), "-http-port", str(HTTP),
                          "-data", DATA, "-log", LOG])
    time.sleep(1.5)
    check("server waits (and stays alive) when the bind address is not up yet",
          w.poll() is None and "waiting for 10.255.255.1" in open(LOG).read())
    w.terminate()
    w.wait(timeout=5)

    print("Daemon mode")
    out = subprocess.run([BIN, "-daemon", "-bind", "127.0.0.1", "-cot-port", str(COT), "-http-port", str(HTTP),
                          "-data", DATA, "-log", LOG, "-pidfile", PID], capture_output=True, text=True, timeout=10)
    time.sleep(0.8)
    pid = int(open(PID).read())
    alive = True
    try:
        os.kill(pid, 0)
    except OSError:
        alive = False
    check("-daemon returns immediately and leaves a running server", out.returncode == 0 and alive, out)
    st, _, _ = req("GET", "/")
    check("daemonised server answers HTTP", st == 200)
    os.kill(pid, signal.SIGTERM)
    time.sleep(0.5)
finally:
    try:
        stop(p)
    except Exception:
        pass

print(f"\n{passed} passed, {failed} failed")
sys.exit(1 if failed else 0)
