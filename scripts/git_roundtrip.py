"""The git round trip.

Clone a project's branch with the real git binary, change something, push it
back, and check that the working tree on the server followed — then freeze the
project and check that the push is refused with a reason.

    python3 scripts/git_roundtrip.py [--url http://127.0.0.1:5000]
"""

import argparse
import base64
import os
import subprocess
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from sweep import Client  # noqa: E402

ap = argparse.ArgumentParser()
ap.add_argument("--url", default="http://127.0.0.1:5000")
ap.add_argument("--user", default="offlinebot")
ap.add_argument("--password", default=os.environ.get("HP_PASSWORD", "set-HP_PASSWORD"))
args = ap.parse_args()

API = args.url
USER, PW = args.user, args.password
c = Client(API)
c.token = c.call("POST", "/api/auth/login", {"username": USER, "password": PW})["accessToken"]
# Deleting needs the password again, so confirm it once up front — otherwise the
# cleanup at the end fails and leaves the test's group behind.
c.call("POST", "/api/auth/step-up", {"password": PW})

g = c.call("POST", "/api/groups", {"title": "pushtest"})
p = c.call("POST", "/api/projects", {"title": "pushed", "groupId": g["slug"], "preset": "data"})
auth = "Authorization: Basic " + base64.b64encode(f"{USER}:{PW}".encode()).decode()
url = f"{API}/git/{g['slug']}.git"

# A shell that has the server's configuration in its environment would hand
# GIT_DIR to every git we run here, and that git would work on the server's
# repositories instead of the clone.
CLEAN_ENV = {k: v for k, v in os.environ.items()
             if k not in ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY")}


def git(*args, cwd=None):
    return subprocess.run(["git", "-c", f"http.extraHeader={auth}", *args],
                          cwd=cwd, capture_output=True, text=True, env=CLEAN_ENV)

ok = True
def check(cond, what):
    global ok
    print(("  ok  " if cond else "  FAIL") + "  " + what)
    if not cond: ok = False

with tempfile.TemporaryDirectory() as tmp:
    r = git("clone", "--quiet", "-b", p["slug"], "--single-branch", url, tmp + "/w")
    check(r.returncode == 0, f"clone the project branch ({r.stderr.strip()[:120]})")
    w = tmp + "/w"
    open(w + "/from-push.txt", "w").write("this arrived by push\n")
    os.makedirs(w + "/deep", exist_ok=True)
    open(w + "/deep/nested.md", "w").write("# nested\n")
    subprocess.run(["git", "config", "user.email", "t@t"], cwd=w, env=CLEAN_ENV)
    subprocess.run(["git", "config", "user.name", "tester"], cwd=w, env=CLEAN_ENV)
    subprocess.run(["git", "add", "-A"], cwd=w, env=CLEAN_ENV)
    subprocess.run(["git", "commit", "-qm", "from the outside"], cwd=w, env=CLEAN_ENV)
    r = git("push", "--quiet", "origin", p["slug"], cwd=w)
    check(r.returncode == 0, f"push into the project branch ({r.stderr.strip()[:160]})")

    listing = c.call("GET", f"/api/projects/{p['id']}/files")
    names = [e["name"] for e in listing["entries"]]
    check("from-push.txt" in names, "the working tree on the server followed the push")
    check("deep" in names, "nested folders arrived too")

    # A frozen project refuses a push even with valid credentials.
    c.call("PATCH", f"/api/projects/{p['id']}", {"readOnly": True})
    open(w + "/second.txt", "w").write("no\n")
    subprocess.run(["git", "add", "-A"], cwd=w, env=CLEAN_ENV)
    subprocess.run(["git", "commit", "-qm", "second"], cwd=w, env=CLEAN_ENV)
    r = git("push", "origin", p["slug"], cwd=w)
    check(r.returncode != 0, "a push into a read-only project is refused")
    check("read-only" in (r.stderr + r.stdout).lower() or "refused" in (r.stderr + r.stdout).lower(),
          f"and says why ({r.stderr.strip()[:160]})")
    c.call("PATCH", f"/api/projects/{p['id']}", {"readOnly": False})

c.call("DELETE", f"/api/projects/{p['id']}?confirm={p['slug']}")
c.call("DELETE", f"/api/groups/{g['slug']}?confirm={g['slug']}")
# Anything the Client itself refused is a failure too — including a cleanup that
# did not go through.
# --------------------------------------------- who may clone, on its own
# The repository is its own question: a public group whose repository is
# closed, and a private group whose repository is open, both have to behave.
pub = c.call("POST", "/api/groups", {"title": "clone-visibility", "visibility": "public"})
if pub:
    open_p = c.call("POST", "/api/projects",
                    {"title": "open", "groupId": pub["slug"], "preset": "data", "visibility": "public"})
    c.call("PUT", f"/api/projects/{open_p['id']}/files/content", {"path": "a.txt", "content": "hi"})

    def anon_clone() -> bool:
        into = tempfile.mkdtemp()
        env = {k: v for k, v in os.environ.items() if k not in ("GIT_DIR", "GIT_WORK_TREE")}
        env["GIT_TERMINAL_PROMPT"] = "0"
        done = subprocess.run(
            ["git", "clone", "--quiet", "-b", open_p["slug"], "--single-branch",
             f"{args.url}/git/{pub['slug']}.git", into + "/x"],
            capture_output=True, text=True, env=env)
        return done.returncode == 0

    check(anon_clone(), "a public group hands its repository out")
    c.call("PATCH", f"/api/groups/{pub['slug']}", {"gitVisibility": "private"})
    check(not anon_clone(), "and stops when the repository alone is made private")
    c.call("PATCH", f"/api/groups/{pub['slug']}", {"gitVisibility": "public", "visibility": "private"})
    check(anon_clone(), "a private group can still hand its repository out")

    c.call("DELETE", f"/api/projects/{open_p['id']}?confirm={open_p['slug']}")
    c.call("DELETE", f"/api/groups/{pub['slug']}?confirm={pub['slug']}&withProjects=true")


import sweep  # noqa: E402

if sweep.failures:
    ok = False
    print("\nrequests that failed:")
    for f in sweep.failures:
        print("  x " + f)

print("\npush round trip:", "green" if ok else "RED")
sys.exit(0 if ok else 1)
