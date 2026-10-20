#!/usr/bin/env python3
"""Offline eval harness for what a Bot makes visible (docs/agent-plan/structured-output.md, section 7).

Why Python stdlib: the harness only needs HTTP, sqlite3 (read-only), subprocess and JSON, all in the
standard library, so it adds no Go package, build tag or dependency to the product tree; the repo
already ships Python tooling (scripts/, deploy/self-host/). The unit under test is the real server
binary, started exactly as the kill tests start it.

Run (from the repo root):

    TOFI_EVAL_KEY_FILE=~/.tofi-test-key TOFI_EVAL_ANTHROPIC_WORKSPACE=wrkspc_... \\
        python3 eval/run_eval.py --models claude-haiku-5-5 --runs 1

Options: --models a,b,c   --runs N (fresh data dir and fresh Bots per run)   --probes id1,id2 or
category names   --grader-model   --out DIR   --with-computer (does not provision a computer; it
only stops skipping probes marked requires_computer)   --no-grade (collect only).

Credentials: the key is read only from the file named by TOFI_EVAL_KEY_FILE and sent only to the
server's own provider-key endpoint and to the grader request header. It is never printed or written.
"""
import argparse
import datetime
import json
import os
import re
import shutil
import signal
import socket
import sqlite3
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)

# List prices per 1M tokens (input, output). The two non-Haiku entries are the prices in
# internal/provider/models.go. Haiku 5.5 has no table entry; Haiku 4.5's price stands in (ESTIMATE).
PRICES = {
    "claude-haiku-5-5": (1.00, 5.00, "estimate: Haiku 4.5 list price as stand-in"),
    "claude-sonnet-5-5": (2.00, 10.00, "list price from internal/provider/models.go"),
    "claude-opus-5-5": (4.00, 20.00, "list price from internal/provider/models.go"),
}
PERSONA_ORDER = ["assistant", "technews", "analogy"]

LOOKUP_RE = r"web|browse|fetch|lookup|call_mcp_tool|search(?!_history)"  # search_history reads this chat, it is not a lookup
KEY = ""  # loaded once; used only in memory


def scrub(text):
    text = str(text)
    if KEY:
        text = text.replace(KEY, "[redacted]")
    return text


def log(msg):
    print(scrub(msg), flush=True)


def load_key():
    path = os.environ.get("TOFI_EVAL_KEY_FILE", "")
    if not path:
        sys.exit("TOFI_EVAL_KEY_FILE must name the API key file")
    with open(os.path.expanduser(path), "r") as f:
        key = f.read().strip()
    if not key or any(c.isspace() for c in key):
        sys.exit("key file must hold one key")
    return key


# ---------------------------------------------------------------- server

class Server:
    def __init__(self, binary, data_dir, log_path):
        self.binary, self.data_dir, self.log_path = binary, data_dir, log_path
        self.port = self._free_port()
        self.base = "http://127.0.0.1:%d" % self.port
        self.proc = None
        self.db_path = None
        self._ro = None

    @staticmethod
    def _free_port():
        s = socket.socket()
        s.bind(("127.0.0.1", 0))
        port = s.getsockname()[1]
        s.close()
        return port

    def start(self):
        env = {k: v for k, v in os.environ.items() if not k.startswith("TOFI_")}
        env["TOFI_DATA_DIR"] = self.data_dir
        env["TOFI_LISTEN"] = "127.0.0.1:%d" % self.port
        logf = open(self.log_path, "wb")
        self.proc = subprocess.Popen([self.binary], env=env, stdout=logf, stderr=subprocess.STDOUT)
        deadline = time.time() + 30
        while time.time() < deadline:
            if self.proc.poll() is not None:
                raise RuntimeError("server exited early; see " + self.log_path)
            try:
                self.get("/api/bots")
                break
            except Exception:
                time.sleep(0.3)
        else:
            raise RuntimeError("server did not come up")
        self.db_path = self._find_db()

    def _find_db(self):
        deadline = time.time() + 20
        while time.time() < deadline:
            for dirpath, _, files in os.walk(self.data_dir):
                for name in files:
                    if name.endswith((".db", ".sqlite", ".sqlite3")):
                        p = os.path.join(dirpath, name)
                        try:
                            c = sqlite3.connect("file:%s?mode=ro" % p, uri=True, timeout=5)
                            ok = c.execute("SELECT 1 FROM sqlite_master WHERE name='runs'").fetchone()
                            c.close()
                        except sqlite3.Error:
                            continue
                        if ok:
                            return p
            time.sleep(0.5)
        raise RuntimeError("could not locate the runs database under the data dir")

    def db(self):
        if self._ro is None:
            self._ro = sqlite3.connect("file:%s?mode=ro" % self.db_path, uri=True, timeout=10)
            self._ro.row_factory = sqlite3.Row
        return self._ro

    def stop(self):
        if self._ro is not None:
            self._ro.close()
        if self.proc and self.proc.poll() is None:
            self.proc.send_signal(signal.SIGTERM)
            try:
                self.proc.wait(timeout=15)
            except subprocess.TimeoutExpired:
                self.proc.kill()

    def request(self, method, path, body=None, timeout=60):
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(self.base + path, data=data, method=method,
                                     headers={"Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                raw = resp.read()
        except urllib.error.HTTPError as e:
            detail = scrub(e.read().decode("utf-8", "replace"))[:400]
            raise RuntimeError("%s %s -> HTTP %d %s" % (method, path, e.code, detail))
        return json.loads(raw) if raw else {}

    def get(self, path):
        return self.request("GET", path)

    def put(self, path, body):
        return self.request("PUT", path, body)

    def post(self, path, body):
        return self.request("POST", path, body)


# ---------------------------------------------------------------- setup

def configure_provider_and_model(srv, key, workspace, model):
    # Same call Settings -> Models makes: PUT /api/providers/anthropic/key, then PUT /api/model-settings.
    srv.put("/api/providers/anthropic/key", {"key": key, "workspace_id": workspace})
    srv.put("/api/model-settings", {"model": model, "reasoning_effort": ""})
    got = srv.get("/api/model-settings")
    if got.get("model") != model:
        raise RuntimeError("global model not applied: %r" % got)


def wait_idle(srv, timeout=240, quiet_polls=4, min_wait=0.0):
    """Wait until no run is active for a few consecutive polls. Returns False on timeout."""
    deadline = time.time() + timeout
    started = time.time()
    quiet = 0
    while time.time() < deadline:
        n = srv.db().execute("SELECT COUNT(*) FROM runs WHERE status IN ('queued','running','waiting')").fetchone()[0]
        quiet = quiet + 1 if n == 0 and time.time() - started >= min_wait else 0
        if quiet >= quiet_polls:
            return True
        time.sleep(1.0)
    return False


def post_message(srv, conv_id, text, recipient=None):
    body = {"content": text, "client_message_id": str(uuid.uuid4())}
    if recipient:
        body["recipient_bot_id"] = recipient
    return srv.post("/api/conversations/%s/messages" % conv_id, body)


def create_persona_bot(srv, persona, brief):
    bot = srv.post("/api/bots", {"onboarding": True, "client_creation_id": str(uuid.uuid4()), "locale": "en"})
    bot_id, conv = bot["id"], bot["dm_conversation_id"]
    post_message(srv, conv, brief)
    nudges = ["That sounds right. Go ahead and set yourself up.",
              "Yes, that is what I meant. Please finish the setup now."]
    for attempt in range(len(nudges) + 1):
        wait_idle(srv, timeout=180)
        row = srv.db().execute("SELECT name,instructions FROM bots WHERE id=?", (bot_id,)).fetchone()
        if row and (row["instructions"] or "").strip():
            return {"persona": persona, "id": bot_id, "dm": conv, "name": row["name"],
                    "instructions": row["instructions"], "onboarding_turns": attempt + 1}
        if attempt < len(nudges):
            post_message(srv, conv, nudges[attempt])
    raise RuntimeError("Bot %s never wrote its profile through onboarding" % persona)


# A group mention needs distinct names; a model may pick the same name for two personas.
FALLBACK_NAMES = {"assistant": "Atlas", "technews": "Quill", "analogy": "Lumen"}


def ensure_distinct_names(srv, bots):
    seen = set()
    for persona in PERSONA_ORDER:
        bot = bots[persona]
        if bot["name"].strip().lower() in seen:
            new = FALLBACK_NAMES[persona]
            srv.request("PATCH", "/api/bots/%s" % bot["id"], {"name": new})
            log("  persona %-9s renamed %r -> %r (duplicate name)" % (persona, bot["name"], new))
            bot["name"] = new
        seen.add(bot["name"].strip().lower())


def create_world(srv, personas):
    bots = {}
    for name in PERSONA_ORDER:
        bots[name] = create_persona_bot(srv, name, personas[name]["brief"])
        log("  persona %-9s -> Bot %r (%d onboarding turns)" % (name, bots[name]["name"], bots[name]["onboarding_turns"]))
    ensure_distinct_names(srv, bots)
    group = srv.post("/api/groups", {"name": "Eval trio", "bot_ids": [bots[n]["id"] for n in PERSONA_ORDER]})
    return bots, group["id"]


# ---------------------------------------------------------------- probes

def fill(text, bots):
    def bot(m):
        return bots[m.group(1)]["name"]
    text = re.sub(r"\{bot:(\w+)\}", bot, text)

    def when(m):
        t = datetime.datetime.utcnow() + datetime.timedelta(minutes=int(m.group(1)))
        return t.strftime("%H:%M")
    return re.sub(r"\{utc\+(\d+)m\}", when, text)


def norm(s):
    return " ".join((s or "").split())


def select_probes(all_probes, spec, with_computer):
    wanted = None if not spec else set(x.strip() for x in spec.split(",") if x.strip())
    out = []
    for p in all_probes:
        if p.get("requires_computer") and not with_computer:
            continue
        if wanted is None or p["id"] in wanted or p["category"] in wanted:
            out.append(p)
    return out


def run_probe(srv, probe, bots, group_id):
    target = probe["target"]
    if target == "group":
        conv = group_id
    else:
        conv = bots[target.split(":", 1)[1]]["dm"]
    db = srv.db()
    seen = set(r[0] for r in db.execute("SELECT id FROM runs"))
    seq0 = db.execute("SELECT COALESCE(MAX(seq),0) FROM messages WHERE conversation_id=?", (conv,)).fetchone()[0]
    usage0 = db.execute("SELECT COALESCE(MAX(id),0) FROM usage_calls").fetchone()[0]
    timed_out, error = False, ""
    turn_seq0 = seq0
    for i, turn in enumerate(probe["turns"]):
        if i == len(probe["turns"]) - 1:
            turn_seq0 = db.execute("SELECT COALESCE(MAX(seq),0) FROM messages WHERE conversation_id=?", (conv,)).fetchone()[0]
        try:
            post_message(srv, conv, fill(turn, bots))
        except RuntimeError as e:
            error = str(e)
            break
        if not wait_idle(srv, timeout=240):
            timed_out = True
            break
    if probe.get("await_schedule") and not error and not timed_out:
        deadline = time.time() + probe.get("wait_seconds", 240)
        fired = False
        while time.time() < deadline:
            rows = [r for r in db.execute("SELECT id,status FROM runs WHERE kind='schedule'") if r[0] not in seen]
            if rows and all(r[1] not in ("queued", "running", "waiting") for r in rows):
                fired = True
                break
            time.sleep(2)
        if not fired:
            error = "scheduled run did not fire and finish within the wait window"
        wait_idle(srv, timeout=120)
    return collect(srv, probe, conv, seen, turn_seq0 if not probe.get("await_schedule") else seq0, usage0, timed_out, error, bots)


def collect(srv, probe, conv, seen_runs, seq0, usage0, timed_out, error, bots=None):
    db = srv.db()
    new_runs = [dict(r) for r in db.execute(
        "SELECT id,conversation_id,bot_id,kind,status,error,final_origin,parent_run_id FROM runs ORDER BY created_at")
        if r["id"] not in seen_runs]
    run_ids = [r["id"] for r in new_runs]
    names = {r["id"]: r["name"] for r in db.execute("SELECT id,name FROM bots")}
    for r in new_runs:
        r["bot"] = names.get(r["bot_id"], r["bot_id"])
        r["error"] = (r["error"] or "")[:300]
    msgs = db.execute(
        "SELECT seq,kind,sender_bot_id,content,notice_data,role,run_id,created_at FROM messages "
        "WHERE conversation_id=? AND seq>? AND role='assistant' ORDER BY seq", (conv, seq0)).fetchall()
    visible = [{"seq": m["seq"], "kind": m["kind"], "sender": names.get(m["sender_bot_id"], m["sender_bot_id"]),
                "text": m["content"], "at": m["created_at"]} for m in msgs if m["kind"] in ("", "segment") and (m["content"] or "").strip()]
    all_notices = db.execute(
        "SELECT content,notice_data FROM messages WHERE kind='notice' AND run_id IN (%s)" %
        ",".join("?" * len(run_ids)), run_ids).fetchall() if run_ids else []
    briefs = []
    for m in all_notices:
        try:
            if json.loads(m["notice_data"] or "{}").get("type") == "handoff" and (m["content"] or "").strip():
                briefs.append(m["content"])
        except ValueError:
            pass
    # Rows the chat would render although they are working text: a progress row
    # of a run that has no final message (the UI folds progress only under a final).
    finals = {m["run_id"] for m in msgs if m["kind"] == "" and (m["content"] or "").strip()}
    stray_progress = [m["content"] for m in msgs if m["kind"] == "progress" and m["run_id"] not in finals and (m["content"] or "").strip()]
    seen_text, dups = {}, 0
    for v in visible:
        t = norm(v["text"])
        if t in seen_text:
            dups += 1
        seen_text[t] = True
    brief_visible = False
    for b in briefs:
        nb = norm(b)
        if len(nb) >= 20 and any(nb in norm(v["text"]) or (len(norm(v["text"])) >= 20 and norm(v["text"]) in nb) for v in visible):
            brief_visible = True
    usage = {"calls": 0, "input_tokens": 0, "output_tokens": 0, "search_usd": 0.0, "searches": 0}
    tools, tool_starts = [], []
    if run_ids:
        q = ",".join("?" * len(run_ids))
        # Server-side search fees are their own rows (kind web_search): money, not model calls.
        u = db.execute("SELECT COUNT(*),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0) FROM usage_calls "
                       "WHERE kind<>'web_search' AND run_id IN (%s)" % q, run_ids).fetchone()
        sr = db.execute("SELECT COALESCE(SUM(equivalent_usd),0) FROM usage_calls WHERE kind='web_search' AND run_id IN (%s)" % q, run_ids).fetchone()
        usage = {"calls": u[0], "input_tokens": u[1], "output_tokens": u[2], "search_usd": sr[0], "searches": round(sr[0] / 0.01)}
        tool_rows = db.execute("SELECT name,started_at FROM tool_activities WHERE run_id IN (%s) ORDER BY rowid" % q, run_ids).fetchall()
        tools = [r[0] for r in tool_rows]
        tool_starts = [(r[0], r[1]) for r in tool_rows]
    # looked_up_first: a lookup-like tool started before the first visible reply was posted
    # (local mode has no web, so an attempted call that fails still counts).
    looked_up_first = None
    if probe.get("record_lookup"):
        first_at = min((v["at"] for v in visible), default=None)
        looked_up_first = any(re.search(LOOKUP_RE, n, re.I) and (first_at is None or st < first_at) for n, st in tool_starts)
    lo, hi = probe["expected_visible"]
    topic_mention = None
    if probe.get("topic_regex"):
        topic_mention = any(re.search(probe["topic_regex"], v["text"], re.I) for v in visible)
    answer_covers = None
    if probe.get("answer_must_mention"):
        alltext = " ".join(v["text"] for v in visible)
        answer_covers = bool(visible) and all(
            (re.search(m[3:], alltext, re.I) if m.startswith("re:") else m.lower() in alltext.lower())
            for m in (fill(x, bots) for x in probe["answer_must_mention"]))
    # Of the Bots the probe's message mentions, how many posted a visible reply.
    mentioned_answered = None
    if probe.get("mentioned") and bots:
        spoke = {v["sender"] for v in visible}
        mentioned_answered = {"mentioned": len(probe["mentioned"]),
                              "answered": sum(1 for k in probe["mentioned"] if bots[k]["name"] in spoke)}
    return {
        "id": probe["id"], "category": probe["category"], "target": probe["target"],
        "mentioned_answered": mentioned_answered,
        "turns": probe["turns"], "owes_answer": probe["owes_answer"],
        "runs": new_runs, "origins": [r["final_origin"] for r in new_runs],
        "visible": visible, "visible_count": len(visible), "expected_visible": [lo, hi],
        "visible_count_ok": lo <= len(visible) <= hi, "exact_duplicates": dups,
        "stray_progress_rows": len(stray_progress), "stray_progress_text": stray_progress[:3],
        "handoff_briefs": briefs, "handoff_brief_visible": brief_visible,
        "usage": usage, "tools": tools,
        "lookup_attempted": any(re.search(LOOKUP_RE, t, re.I) for t in tools) if probe.get("record_lookup") else None,
        "looked_up_first": looked_up_first,
        "lookup_calls": sum(1 for t in tools if re.search(LOOKUP_RE, t, re.I)),
        "timed_out": timed_out, "error": error,
        "topic_mention": topic_mention, "answer_covers": answer_covers,
    }


# ---------------------------------------------------------------- grader

def anthropic_call(key, workspace, model, system, user, max_tokens=3000):
    body = json.dumps({"model": model, "max_tokens": max_tokens, "system": system,
                       "messages": [{"role": "user", "content": user}]}).encode()
    headers = {"content-type": "application/json", "x-api-key": key, "anthropic-version": "2023-06-01",
               "anthropic-workspace-id": workspace}
    for attempt in range(5):
        req = urllib.request.Request("https://api.anthropic.com/v1/messages", data=body, headers=headers, method="POST")
        try:
            with urllib.request.urlopen(req, timeout=180) as resp:
                return json.loads(resp.read())
        except urllib.error.HTTPError as e:
            detail = scrub(e.read().decode("utf-8", "replace"))[:300]
            if e.code in (429, 500, 502, 503, 529) and attempt < 4:
                time.sleep(2 ** attempt * 2)
                continue
            raise RuntimeError("grader HTTP %d %s" % (e.code, detail))
        except urllib.error.URLError as e:
            if attempt < 4:
                time.sleep(2 ** attempt * 2)
                continue
            raise RuntimeError("grader unreachable: " + scrub(e))


def extract_json(text):
    text = text.strip()
    text = re.sub(r"^```(?:json)?\s*|\s*```$", "", text)
    start, end = text.find("{"), text.rfind("}")
    return json.loads(text[start:end + 1])


def grade(key, workspace, grader_model, rubric, res, bots_by_name):
    if not res["visible"]:
        return {"replies": [], "step_narration_visible": False, "coordination_visible": False,
                "delegation_requirements_visible": False, "trailing_unverified_caveat": False,
                "routine_closing_offer": False, "revived_prior_topic": False, "member_brief_visible": False, "asked_back": False, "scope_refusal": False, "verified_without_lookup": False, "asked_instead_of_lookup": False, "_usage": {"input_tokens": 0, "output_tokens": 0}}
    roles = []
    for name, b in bots_by_name.items():
        roles.append("%s: %s" % (b["name"], norm(b["instructions"])[:1200]))
    where = "a group chat with three Bots" if res["target"] == "group" else "a direct chat with one Bot"
    lines = ["Setting: %s." % where, "Bot roles:", *roles, "",
             "What the person wrote (last turn is the one being answered):"]
    for t in res["turns"]:
        lines.append("PERSON: " + t)
    lines += ["", "Visible replies, in order:"]
    for i, v in enumerate(res["visible"]):
        lines.append("[%d] %s: %s" % (i, v["sender"], v["text"]))
    if res.get("looked_up_first") is not None:
        lines += ["", "A lookup tool was called before the first visible reply: " + ("yes" if res["looked_up_first"] else "no")]
    lines += ["", "Tools called during the run (names only, in order; empty means no tool was called): " + (", ".join(res["tools"]) or "(none)")]
    if res["handoff_briefs"]:
        lines += ["", "Handoff briefs one Bot gave another (internal assignment text):"]
        lines += ["- " + b for b in res["handoff_briefs"]]
    out = anthropic_call(key, workspace, grader_model, rubric, "\n".join(lines))
    text = "".join(b.get("text", "") for b in out.get("content", []) if b.get("type") == "text")
    parsed = extract_json(text)
    parsed["_usage"] = {"input_tokens": out.get("usage", {}).get("input_tokens", 0),
                        "output_tokens": out.get("usage", {}).get("output_tokens", 0)}
    return parsed


# ---------------------------------------------------------------- aggregate

NO_LOOKUP_CATEGORIES = {"dm_smalltalk", "group_greeting", "group_unaddressed"}
DIMS = ["natural_speech", "mood", "stance", "length", "machine_tells"]
FLAGS = ["step_narration_visible", "coordination_visible", "delegation_requirements_visible",
         "trailing_unverified_caveat", "routine_closing_offer", "revived_prior_topic", "member_brief_visible", "asked_back", "scope_refusal", "verified_without_lookup", "asked_instead_of_lookup"]


def rate(num, den):
    return round(num / den, 4) if den else None


def aggregate(results):
    """results: list of per-probe dicts (with 'grade'). Returns a dict of numbers for the set."""
    runs = [r for p in results for r in p["runs"]]
    owed = [p for p in results if p["owes_answer"]]
    owed_runs = [r for p in owed for r in p["runs"] if r["kind"] != "schedule" or True]
    origin_counts = {}
    for r in runs:
        key = r["final_origin"] or "(empty)"
        origin_counts[key] = origin_counts.get(key, 0) + 1
    graded = [p for p in results if p.get("grade") and p["visible"]]
    scores = {d: [] for d in DIMS}
    for p in graded:
        for rep in p["grade"].get("replies", []):
            for d in DIMS:
                if isinstance(rep.get(d), (int, float)):
                    scores[d].append(rep[d])
    all_scores = [s for d in DIMS for s in scores[d]]
    lookups = [p for p in results if p["lookup_attempted"] is not None]
    usage = {"calls": sum(p["usage"]["calls"] for p in results),
             "input_tokens": sum(p["usage"]["input_tokens"] for p in results),
             "output_tokens": sum(p["usage"]["output_tokens"] for p in results),
             "search_usd": sum(p["usage"].get("search_usd", 0.0) for p in results),
             "searches": sum(p["usage"].get("searches", 0) for p in results)}
    out = {
        "probes": len(results), "runs": len(runs),
        "origin_counts": origin_counts,
        "adherence_say_rate": rate(sum(1 for r in owed_runs if r["final_origin"] == "say"), len(owed_runs)),
        "visible_count_ok_rate": rate(sum(1 for p in results if p["visible_count_ok"]), len(results)),
        "probes_with_exact_duplicates": sum(1 for p in results if p["exact_duplicates"] > 0),
        "exact_duplicate_messages": sum(p["exact_duplicates"] for p in results),
        "handoff_brief_visible": sum(1 for p in results if p["handoff_brief_visible"]),
        "probes_with_stray_progress": sum(1 for p in results if p.get("stray_progress_rows")),
        "handoff_probes": sum(1 for p in results if p["handoff_briefs"]),
        "lookup_attempted": sum(1 for p in lookups if p["lookup_attempted"]), "lookup_probes": len(lookups),
        "looked_up_first": sum(1 for p in lookups if p.get("looked_up_first")),
        "needless_lookup_calls": sum(p.get("lookup_calls", 0) for p in results if p["category"] in NO_LOOKUP_CATEGORIES),
        "needless_lookup_probes": sum(1 for p in results if p["category"] in NO_LOOKUP_CATEGORIES and p.get("lookup_calls", 0) > 0),
        "no_lookup_category_probes": sum(1 for p in results if p["category"] in NO_LOOKUP_CATEGORIES),
        "failed_or_timed_out": sum(1 for p in results if p["timed_out"] or p["error"] or any(r["status"] != "done" for r in p["runs"])),
        "graded_probes": len(graded),
        "avg_score": round(sum(all_scores) / len(all_scores), 3) if all_scores else None,
        "avg_by_dimension": {d: round(sum(v) / len(v), 3) if v else None for d, v in scores.items()},
        "flag_rates": {f: rate(sum(1 for p in graded if p["grade"].get(f)), len(graded)) for f in FLAGS},
        "usage": usage,
        "topic_mention": {"count": sum(1 for p in results if p.get("topic_mention")), "probes": sum(1 for p in results if p.get("topic_mention") is not None)},
        "answer_covers": {"count": sum(1 for p in results if p.get("answer_covers")), "probes": sum(1 for p in results if p.get("answer_covers") is not None)},
        "mentioned_answered": {"answered": sum(p["mentioned_answered"]["answered"] for p in results if p.get("mentioned_answered")),
                               "mentioned": sum(p["mentioned_answered"]["mentioned"] for p in results if p.get("mentioned_answered"))},
    }
    return out


def cost_usd(model, usage):
    pin, pout, _ = PRICES.get(model, (None, None, "no price"))
    if pin is None:
        return None
    return round(usage["input_tokens"] / 1e6 * pin + usage["output_tokens"] / 1e6 * pout + usage.get("search_usd", 0.0), 4)


def pct(x):
    return "n/a" if x is None else "%.0f%%" % (100 * x)


def write_summary(path, report):
    L = ["# Structured-output eval summary", "",
         "Generated %s. Probes: %d. Runs per model: %d. Grader: %s." % (
             report["generated_at"], report["probe_count"], report["runs_per_model"], report["grader_model"]), ""]
    L += ["## Per model", "",
          "| model | say rate | visible count ok | probes with exact dups | narration | coordination | delegation reqs | trailing caveat | closing offer | topic revival | member brief | scope refusal | verified w/o lookup | avg score (0-2) | model calls | tokens in/out | est. $ |",
          "|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|"]
    for model, m in report["models"].items():
        a, fr = m["overall"], m["overall"]["flag_rates"]
        L.append("| %s | %s | %s | %d | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %d | %d / %d | %s |" % (
            model, pct(a["adherence_say_rate"]), pct(a["visible_count_ok_rate"]), a["probes_with_exact_duplicates"],
            pct(fr["step_narration_visible"]), pct(fr["coordination_visible"]), pct(fr["delegation_requirements_visible"]),
            pct(fr["trailing_unverified_caveat"]), pct(fr["routine_closing_offer"]), pct(fr["revived_prior_topic"]), pct(fr["member_brief_visible"]), pct(fr["scope_refusal"]), pct(fr["verified_without_lookup"]),
            a["avg_score"], a["usage"]["calls"], a["usage"]["input_tokens"], a["usage"]["output_tokens"],
            "n/a" if m["cost_usd_est"] is None else "$%.3f" % m["cost_usd_est"]))
    L.append("")
    for model, m in report["models"].items():
        L += ["### %s" % model, "",
              "Final origin counts (all runs): %s." % json.dumps(m["overall"]["origin_counts"], sort_keys=True),
              "Price basis: %s. Setup (persona onboarding) tokens in/out: %d / %d, included in the cost below but not in the probe table." % (
                  PRICES.get(model, (0, 0, "unknown"))[2], m["setup_usage"]["input_tokens"], m["setup_usage"]["output_tokens"]),
              "Total run tokens in/out (setup + probes): %d / %d; est. $%s." % (
                  m["total_usage"]["input_tokens"], m["total_usage"]["output_tokens"], m["total_cost_usd_est"]),
              "Grader tokens in/out: %d / %d." % (m["grader_usage"]["input_tokens"], m["grader_usage"]["output_tokens"]),
              "Lookup attempted: %d of %d lookup probes (before the first reply: %d). Needless lookup calls on small-talk probes: %d calls in %d of %d probes. verified_without_lookup %s, asked_instead_of_lookup %s. Handoff brief visible: %d of %d probes with a brief." % (
                  m["overall"]["lookup_attempted"], m["overall"]["lookup_probes"], m["overall"]["looked_up_first"],
                  m["overall"]["needless_lookup_calls"], m["overall"]["needless_lookup_probes"], m["overall"]["no_lookup_category_probes"],
                  pct(m["overall"]["flag_rates"]["verified_without_lookup"]), pct(m["overall"]["flag_rates"]["asked_instead_of_lookup"]),
                  m["overall"]["handoff_brief_visible"], m["overall"]["handoff_probes"]),
              "Mentioned Bots that answered: %d of %d." % (m["overall"]["mentioned_answered"]["answered"], m["overall"]["mentioned_answered"]["mentioned"]),
              "Failed, errored or timed-out probes: %d. Probes with working text a chat would render (progress row, no final): %d." % (m["overall"]["failed_or_timed_out"], m["overall"]["probes_with_stray_progress"]), "",
              "| category | probes | say rate | count ok | dup probes | narration | coordination | delegation | caveat | offer | revival | brief | scope refusal | avg score |",
              "|---|---|---|---|---|---|---|---|---|---|---|---|---|---|"]
        for cat, a in sorted(m["by_category"].items()):
            fr = a["flag_rates"]
            L.append("| %s | %d | %s | %s | %d | %s | %s | %s | %s | %s | %s | %s | %s | %s |" % (
                cat, a["probes"], pct(a["adherence_say_rate"]), pct(a["visible_count_ok_rate"]),
                a["probes_with_exact_duplicates"], pct(fr["step_narration_visible"]), pct(fr["coordination_visible"]),
                pct(fr["delegation_requirements_visible"]), pct(fr["trailing_unverified_caveat"]),
                pct(fr["routine_closing_offer"]), pct(fr["revived_prior_topic"]), pct(fr["member_brief_visible"]), pct(fr["scope_refusal"]), a["avg_score"]))
        L.append("")
    with open(path, "w") as f:
        f.write("\n".join(L) + "\n")


# ---------------------------------------------------------------- main

def main():
    global KEY
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--models", default="claude-haiku-5-5")
    ap.add_argument("--runs", type=int, default=1)
    ap.add_argument("--probes", default="", help="comma list of probe ids or category names")
    ap.add_argument("--grader-model", default="claude-opus-5-5")
    ap.add_argument("--out", default="")
    ap.add_argument("--with-computer", action="store_true")
    ap.add_argument("--no-grade", action="store_true")
    ap.add_argument("--binary", default="", help="prebuilt tofi binary (default: go build ./cmd/tofi)")
    args = ap.parse_args()

    KEY = load_key()
    workspace = os.environ.get("TOFI_EVAL_ANTHROPIC_WORKSPACE", "")
    if not re.match(r"^wrkspc_[A-Za-z0-9]+$", workspace):
        sys.exit("TOFI_EVAL_ANTHROPIC_WORKSPACE must be a wrkspc_ id")

    probes_doc = json.load(open(os.path.join(HERE, "probes.json")))
    personas = json.load(open(os.path.join(HERE, "personas.json")))
    rubric = open(os.path.join(HERE, "rubric.md")).read()
    probes = select_probes(probes_doc["probes"], args.probes, args.with_computer)
    if not probes:
        sys.exit("no probes selected")
    models = [m.strip() for m in args.models.split(",") if m.strip()]

    stamp = datetime.datetime.utcnow().strftime("%Y%m%dT%H%M%SZ")
    out_dir = args.out or os.path.join(HERE, "results", stamp)
    os.makedirs(out_dir, exist_ok=True)
    work = tempfile.mkdtemp(prefix="tofi-eval-")
    binary = args.binary or os.path.join(work, "tofi")
    if not args.binary:
        log("building ./cmd/tofi")
        subprocess.check_call(["go", "build", "-o", binary, "./cmd/tofi"], cwd=ROOT)

    report = {"generated_at": datetime.datetime.utcnow().isoformat() + "Z", "probe_count": len(probes),
              "runs_per_model": args.runs, "grader_model": args.grader_model, "models": {}}
    try:
        for model in models:
            per_probe, setup_usage, grader_usage = [], {"input_tokens": 0, "output_tokens": 0}, {"input_tokens": 0, "output_tokens": 0}
            total_usage = {"input_tokens": 0, "output_tokens": 0}
            for run_index in range(args.runs):
                log("== %s run %d/%d" % (model, run_index + 1, args.runs))
                data_dir = os.path.join(work, "data-%s-%d" % (model, run_index))
                os.makedirs(data_dir)
                srv = Server(binary, data_dir, os.path.join(out_dir, "server-%s-%d.log" % (model, run_index)))
                srv.start()
                try:
                    configure_provider_and_model(srv, KEY, workspace, model)
                    bots, group_id = create_world(srv, personas)
                    u = srv.db().execute("SELECT COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0) FROM usage_calls").fetchone()
                    setup_usage["input_tokens"] += u[0]
                    setup_usage["output_tokens"] += u[1]
                    for probe in probes:
                        log("  probe %s" % probe["id"])
                        res = run_probe(srv, probe, bots, group_id)
                        res["run_index"] = run_index
                        if not args.no_grade:
                            try:
                                res["grade"] = grade(KEY, workspace, args.grader_model, rubric, res, bots)
                                gu = res["grade"].pop("_usage")
                                grader_usage["input_tokens"] += gu["input_tokens"]
                                grader_usage["output_tokens"] += gu["output_tokens"]
                            except Exception as e:  # keep the run; record that grading failed
                                res["grade"] = None
                                res["grade_error"] = scrub(e)[:300]
                        per_probe.append(res)
                    u = srv.db().execute("SELECT COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0) FROM usage_calls").fetchone()
                    total_usage["input_tokens"] += u[0]
                    total_usage["output_tokens"] += u[1]
                finally:
                    srv.stop()
            by_cat = {}
            for p in per_probe:
                by_cat.setdefault(p["category"], []).append(p)
            overall = aggregate(per_probe)
            report["models"][model] = {
                "overall": overall,
                "by_category": {c: aggregate(v) for c, v in by_cat.items()},
                "setup_usage": setup_usage, "grader_usage": grader_usage, "total_usage": total_usage,
                "cost_usd_est": cost_usd(model, overall["usage"]),
                "total_cost_usd_est": cost_usd(model, total_usage),
                "price_basis": PRICES.get(model, (None, None, "no price"))[2],
                "probes": per_probe,
            }
        with open(os.path.join(out_dir, "results.json"), "w") as f:
            json.dump(report, f, indent=2, ensure_ascii=False)
        write_summary(os.path.join(out_dir, "summary.md"), report)
        # Safety net: the key must be absent from everything written.
        for name in os.listdir(out_dir):
            with open(os.path.join(out_dir, name), "rb") as f:
                if KEY.encode() in f.read():
                    log("WARNING: key material found in %s; deleting" % name)
                    os.remove(os.path.join(out_dir, name))
        log("wrote %s" % out_dir)
    finally:
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    main()
