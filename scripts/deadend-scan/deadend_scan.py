#!/usr/bin/env python3
"""deadend-scan: find plumbing breaks (dead ends) in TOFI run records.

Standard library only. Reads eval results folders and/or a live server over
read-only HTTP GET, runs eight detectors and prints a ranked markdown report
(or JSON). See README.md next to this file.

Exit codes: 0 clean (or nothing new in a hard detector vs. baseline),
1 a hard detector (1, 4, 6) found something new, 2 usage / input error.
"""
import argparse
import glob
import hashlib
import json
import os
import re
import ssl
import sys
import unicodedata
import urllib.error
import urllib.parse
import urllib.request

# --------------------------------------------------------------------------
# Constants

DETECTORS = {
    1: ("dead_end_refusal", "Dead-end refusals", True),
    2: ("identical_retry", "Identical retries after a failure", False),
    3: ("claim_without_call", "Visual claim without a view call (heuristic)", False),
    4: ("format_leak", "Format leaks in published messages", True),
    5: ("unseen_artefact", "Image/document produced, no image viewed", False),
    6: ("quiet_completion", "Quiet or empty scheduled completion", True),
    7: ("review_loop", "Review loop then shorter/empty answer", False),
    8: ("run_error", "Provider/runtime error ended a run", False),
}
HARD = {1, 4, 6}

FAIL_STATUSES = {"validation_error", "transient_failure", "permanent_failure",
                 "uncertain_effect", "denied", "approval_expired"}
# Statuses that are a person's decision or a gate, not a plumbing break.
NOT_DEADEND_STATUSES = {"denied", "approval_expired", "need_approval", "need_information"}
# next_action values that tell the model how to carry on.
REPAIR_ACTIONS = {"repair_arguments", "retry", "refresh_schema", "refresh_tools",
                  "replan", "replan_after_input", "replan_after_restart",
                  "verify_target", "repeat_exact_call"}
LEGACY_CODES = {"unclassified_tool_failure"}
# Result keys that carry alternatives / schema / a way forward.
ALT_KEYS = {"available", "alternatives", "valid", "allowed", "servers", "choices",
            "options", "schema", "expected", "supported", "candidates",
            "suggestions", "tools", "did_you_mean", "usage", "required",
            "properties", "enum", "examples", "known", "names"}
# Code shapes of "bare missing" refusals (matched on the code, not the prose).
BARE_CODE_RE = re.compile(r"(^|_)(not_found|unavailable|not_configured|unknown|missing|no_such)(_|$)")

REVIEW_CODES = {"answer_review", "answer_revise"}

# Calls that put an image in front of the model (tool -> normalised actions).
VIEW_ACTIONS = {
    "computer_files": {"view"},
    "computer_browser": {"snapshot", "screenshot"},
    "computer_desktop": {"capture", "snapshot", "screenshot"},
}
IMAGE_EXT = {"png", "jpg", "jpeg", "gif", "webp", "svg"}
# Documents whose look matters. Spreadsheets and archives are data, not layout.
DOC_EXT = {"pdf", "doc", "docx", "ppt", "pptx", "html", "htm"}
IMAGE_DOC_EXT = IMAGE_EXT | DOC_EXT

# Detector 3 signals. A visual object term and a "looked at" term within
# CLAIM_WINDOW characters of each other in the final answer, no negator
# near them. Plain substrings, case-insensitive; English and CJK only
# because those are the languages the product answers in.
CLAIM_OBJECTS = ["screenshot", "preview", "rendered", "render", "layout", "visual",
                 "looks ", "截图", "截屏", "预览", "渲染", "排版", "版式", "视觉",
                 "目视", "看起来", "效果图"]
CLAIM_VERBS = ["viewed", "looked at", "inspected", "checked", "verified", "reviewed",
               "examined", "confirmed", "i saw", "visually", "看过", "查看了", "检查了",
               "核对了", "确认了", "验证了", "目检", "过目", "看了", "检查过", "核对过"]
CLAIM_NEGATORS = ["not ", "n't", "never", "unable", "without", "没", "未", "无法", "不能"]
CLAIM_WINDOW = 40

# Known tool argument shapes for format-leak detection. Observed shapes in the
# scanned data are added at run time (see build_tool_shapes).
KNOWN_TOOL_KEYS = {"say": {"purpose", "text"}}

SECRET_PATTERNS = [
    (re.compile(r"(?i)(authorization|cookie|set-cookie)\s*[:=]\s*[^\n]{4,}"), r"\1: [masked]"),
    (re.compile(r"(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}"), r"\1 [masked]"),
    (re.compile(r"(?i)([\"']?(?:api[_-]?key|token|secret|password|passwd|credential|session)[\"']?\s*[:=]\s*[\"']?)[^\s\"',}]{4,}"), r"\1[masked]"),
    (re.compile(r"\b(sk|pk|rk|ghp|gho|xox[abp]|AKIA)[-_A-Za-z0-9]{12,}"), "[masked]"),
    (re.compile(r"\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*"), "[masked-jwt]"),
    (re.compile(r"\b[A-Fa-f0-9]{40,}\b"), "[masked-hex]"),
    (re.compile(r"(?=[A-Za-z0-9+_-]*\d)(?=[A-Za-z0-9+_-]*[A-Za-z])\b[A-Za-z0-9+_-]{48,}={0,2}"), "[masked-long]"),
]
SNIPPET_MAX = 240


# --------------------------------------------------------------------------
# Small helpers

def mask(text):
    text = text if isinstance(text, str) else str(text)
    for pat, rep in SECRET_PATTERNS:
        text = pat.sub(rep, text)
    return text


def snippet(text, limit=SNIPPET_MAX):
    text = mask(" ".join(str(text).split()))
    return text if len(text) <= limit else text[:limit - 1] + "…"


def parse_json(text):
    if not isinstance(text, str):
        return None
    t = text.strip()
    if t.startswith("Tool error:"):
        t = t[len("Tool error:"):].strip()
    if not t or t[0] not in "{[":
        return None
    try:
        return json.loads(t)
    except ValueError:
        return None


def parse_outcome(text):
    """The control envelope of internal/tooloutcome, or None."""
    obj = parse_json(text)
    if isinstance(obj, dict) and obj.get("version") == 1 and obj.get("status") and "execution_certainty" in obj:
        return obj
    return None


def args_hash(args):
    obj = parse_json(args) if isinstance(args, str) else args
    if obj is None:
        raw = args if isinstance(args, str) else repr(args)
    else:
        raw = json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False)
    return hashlib.sha256(raw.encode("utf-8", "replace")).hexdigest()[:16]


def is_invisible(text):
    """True when nothing a reader can see remains (Cf/Cc/space only)."""
    for ch in text:
        cat = unicodedata.category(ch)
        if cat in ("Cf", "Cc", "Zs", "Zl", "Zp") or ch.isspace():
            continue
        return False
    return True


def norm_head(text, n=60):
    t = " ".join(str(text).split())[:n * 2]
    t = re.sub(r"[0-9a-f]{8}-[0-9a-f-]{27,}", "#", t, flags=re.I)
    t = re.sub(r"\d+", "#", t)
    return t[:n]


def tool_action(call):
    """Normalised action of a computer_* call (last dotted segment)."""
    obj = call.get("args_obj")
    if isinstance(obj, dict):
        a = obj.get("action")
        if isinstance(a, str):
            return a.split(".")[-1].lower()
    return ""


def make_call(name, args, result, status="", outcome=None):
    obj = parse_json(args) if isinstance(args, str) else args
    if outcome is None:
        outcome = parse_outcome(result)
    return {"name": name, "args": args if isinstance(args, str) else json.dumps(args, ensure_ascii=False),
            "args_obj": obj, "result": result or "", "status": status, "outcome": outcome}


def call_failed(call):
    """(failed, outcome, legacy_uncoded)."""
    o = call.get("outcome")
    st = call.get("status") or ""
    res = call.get("result") or ""
    if o:
        if o.get("status") in FAIL_STATUSES or st == "failed":
            return True, o, o.get("code") in LEGACY_CODES
        return False, o, False
    if st == "failed" or res.startswith("Tool error:"):
        return True, None, True
    return False, None, False


def is_view_call(call):
    acts = VIEW_ACTIONS.get(call["name"])
    return bool(acts) and tool_action(call) in acts and not call_failed(call)[0]


# --------------------------------------------------------------------------
# Loaders: eval results

def load_eval_dir(path):
    """Yield normalised runs (one per task) from one eval results folder."""
    rj = os.path.join(path, "results.json")
    ddir = os.path.join(path, "deliverables")
    if not os.path.isfile(rj) or not os.path.isdir(ddir):
        return
    with open(rj, encoding="utf-8") as f:
        results = json.load(f)
    label = os.path.basename(os.path.normpath(path))
    for model, entries in (results.get("by_model") or {}).items():
        for e in entries:
            stem = "%s-%s-r%s" % (e.get("task_id"), model, e.get("run_index"))
            cpath = os.path.join(ddir, stem + ".context.json")
            if not os.path.isfile(cpath):
                continue
            with open(cpath, encoding="utf-8") as f:
                ctx = json.load(f)
            calls = []
            for t in ctx.get("tool_texts") or []:
                name, _, rest = t.partition(" ")
                a, _, res = rest.partition("\n")
                calls.append(make_call(name, a, res))
            texts = []
            for m in ctx.get("messages") or []:
                texts.append({"content": m.get("text") or "", "role": "assistant",
                              "attachments": m.get("attachments") or [], "run_id": ""})
            if not texts and ctx.get("reply_text"):
                texts.append({"content": ctx["reply_text"], "role": "assistant", "attachments": [], "run_id": ""})
            yield {
                "id": "%s/%s" % (label, stem),
                "source": "eval:" + label,
                "title": e.get("task_id"),
                "runs": [{"id": s.get("id"), "kind": s.get("kind"), "status": s.get("status"),
                          "error": s.get("error") or "", "final_origin": s.get("final_origin") or ""}
                         for s in (e.get("run_statuses") or [])],
                "calls": calls,
                "messages": texts,
                "ctx_reply": ctx.get("reply_text") or "",
                "per_run_messages": False,
            }


def find_eval_dirs(spec):
    out = []
    for p in sorted(glob.glob(spec)) or [spec]:
        if os.path.isfile(os.path.join(p, "results.json")):
            out.append(p)
        elif os.path.isdir(p):
            for sub in sorted(os.listdir(p)):
                sp = os.path.join(p, sub)
                if os.path.isfile(os.path.join(sp, "results.json")):
                    out.append(sp)
    return out


# --------------------------------------------------------------------------
# Loaders: live server (read-only GET)

def parse_cookie_file(path, host):
    pairs = []
    with open(path, encoding="utf-8") as f:
        for line in f:
            line = line.rstrip("\n")
            if line.startswith("#HttpOnly_"):
                line = line[len("#HttpOnly_"):]
            elif line.startswith("#") or not line.strip():
                continue
            parts = line.split("\t")
            if len(parts) < 7:
                continue
            dom = parts[0].lstrip(".")
            if dom and not (host == dom or host.endswith("." + dom)):
                continue
            pairs.append("%s=%s" % (parts[5], parts[6]))
    return "; ".join(pairs)


class Remote:
    """Read-only client: the only network call is _get, and it is a GET."""

    def __init__(self, base, cookie_file, insecure=False, timeout=30):
        self.base = base.rstrip("/")
        host = urllib.parse.urlparse(self.base).hostname or ""
        self.cookie = parse_cookie_file(cookie_file, host) if cookie_file else ""
        self.timeout = timeout
        self.ctx = None
        if insecure:
            self.ctx = ssl.create_default_context()
            self.ctx.check_hostname = False
            self.ctx.verify_mode = ssl.CERT_NONE

    def _get(self, path, params=None):
        url = self.base + path
        if params:
            url += "?" + urllib.parse.urlencode({k: v for k, v in params.items() if v not in (None, "")})
        req = urllib.request.Request(url, method="GET", headers={"Accept": "application/json"})
        if self.cookie:
            req.add_header("Cookie", self.cookie)
        for attempt in (0, 1):
            try:
                with urllib.request.urlopen(req, timeout=self.timeout, context=self.ctx) as r:
                    return json.loads(r.read().decode("utf-8", "replace"))
            except urllib.error.HTTPError as e:
                if e.code == 404:
                    return None
                if attempt:
                    raise
            except (urllib.error.URLError, TimeoutError, ValueError):
                if attempt:
                    raise
        return None

    def conversations(self):
        d = self._get("/api/conversations", {"include_archived": "1"})
        return (d or {}).get("conversations") or []

    def runs(self, conv, since):
        out, before, before_id = [], "", ""
        while True:
            d = self._get("/api/conversations/%s/runs" % conv,
                          {"limit": 200, "before": before, "before_id": before_id}) or {}
            page = d.get("runs") or []
            out.extend(page)
            if not d.get("has_more") or not page:
                break
            last = page[-1]
            before, before_id = last.get("created_at", ""), last.get("id", "")
            if since and before < since:
                break
        return [r for r in out if not since or (r.get("created_at") or "") >= since]

    def messages(self, conv, since):
        out, before = [], 0
        while True:
            d = self._get("/api/conversations/%s/messages" % conv,
                          {"limit": 200, "before_seq": before or ""}) or {}
            page = d.get("messages") or []
            out.extend(page)
            if not d.get("has_more") or not page:
                break
            before = min(m.get("seq", 0) for m in page)
            if since and all((m.get("created_at") or "") < since for m in page):
                break
        out.sort(key=lambda m: m.get("seq", 0))
        return out

    def tools(self, conv, run_id):
        out, off = [], 0
        while off <= 1000:
            d = self._get("/api/conversations/%s/tools" % conv,
                          {"run_id": run_id, "offset": off, "limit": 25}) or {}
            page = d.get("activities") or []
            out.extend(page)
            if not d.get("has_more") or not page:
                break
            off += len(page)
        return out


def load_remote(remote, since="", exclude_prefix="", max_conversations=0, log=None):
    convs = remote.conversations()
    if since:
        convs = [c for c in convs if (c.get("updated_at") or "") >= since]
    if exclude_prefix:
        convs = [c for c in convs if not (c.get("name") or "").startswith(exclude_prefix)]
    if max_conversations:
        convs = convs[:max_conversations]
    for c in convs:
        cid = c["id"]
        if log:
            log("conversation %s %s" % (cid, snippet(c.get("name") or "", 40)))
        runs = remote.runs(cid, since)
        if not runs:
            continue
        by_run = {}
        for m in remote.messages(cid, since):
            by_run.setdefault(m.get("run_id") or "", []).append(m)
        for r in runs:
            calls = [make_call(a.get("name") or "", a.get("arguments") or "", a.get("result") or "",
                               a.get("status") or "", a.get("outcome"))
                     for a in remote.tools(cid, r["id"])]
            mm = []
            for m in by_run.get(r["id"], []):
                if m.get("role") != "assistant" or m.get("notice") or m.get("card"):
                    continue
                mm.append({"content": m.get("content") or "", "role": "assistant",
                           "attachments": m.get("attachments") or [], "run_id": r["id"]})
            yield {
                "id": r["id"], "source": "live:" + cid, "title": c.get("name") or "",
                "runs": [{"id": r["id"], "kind": r.get("kind"), "status": r.get("status"),
                          "error": r.get("error") or "", "final_origin": r.get("final_origin") or ""}],
                "calls": calls, "messages": mm, "ctx_reply": "", "per_run_messages": True,
            }


# --------------------------------------------------------------------------
# Detectors. Each returns a list of finding dicts.

def F(det, run, tool, code, evidence, legacy=False, heuristic=False):
    return {"detector": det, "run_id": run["id"], "source": run["source"], "tool": tool,
            "code": code, "evidence": snippet(evidence), "legacy_uncoded": legacy,
            "heuristic": heuristic}


def has_repair_data(call, outcome):
    """Alternatives, a schema or a list in the result: a way forward."""
    obj = parse_json(call.get("result"))
    if isinstance(obj, dict):
        for k, v in obj.items():
            if k in ALT_KEYS and v not in (None, "", [], {}):
                return True
    text = (outcome or {}).get("message") or call.get("result") or ""
    lines = [ln for ln in text.splitlines() if re.match(r"\s*([-*•]|\d+[.)])\s+\S", ln)]
    if len(lines) >= 2:
        return True
    # Backticked or single-quoted names only: double quotes are mostly the
    # upstream JSON echoed inside a message, not a list of choices.
    quoted = set(re.findall(r"`([^`]{1,60})`|(?<![A-Za-z])'([^']{1,60})'", text))
    return len(quoted) >= 3


def detect_dead_end(run):
    out = []
    for c in run["calls"]:
        failed, o, legacy = call_failed(c)
        if not failed:
            continue
        status = (o or {}).get("status", "")
        if status in NOT_DEADEND_STATUSES:
            continue
        code = (o or {}).get("code") or "uncoded"
        if status == "uncertain_effect" and code not in LEGACY_CODES:
            continue
        if has_repair_data(c, o):
            continue
        nxt = (o or {}).get("next_action", "")
        msg = (o or {}).get("message") or c["result"]
        if o is None:
            kind = "uncoded"
            code = "uncoded:" + norm_head(msg)
        elif legacy:
            kind = "uncoded"
        else:
            bare = bool(BARE_CODE_RE.search(code))
            if nxt in REPAIR_ACTIONS and not bare:
                continue
            kind = "bare_missing" if bare else "terminal:" + (nxt or "none")
        out.append(F(1, run, c["name"], code, "[%s] %s" % (kind, msg), legacy))
    return out


def detect_identical_retry(run):
    out, failed = [], {}
    for c in run["calls"]:
        key = (c["name"], args_hash(c["args"]))
        if key in failed:
            first = failed[key]
            out.append(F(2, run, c["name"], first["code"], "%s args=%s" % (c["name"], c["args"]),
                         first["legacy"]))
        ok, o, legacy = call_failed(c)
        if ok:
            # A transient failure that says retry is meant to be repeated.
            if o and o.get("status") == "transient_failure" and o.get("next_action") == "retry":
                continue
            failed.setdefault(key, {"code": (o or {}).get("code") or "uncoded", "legacy": legacy})
    return out


def final_answer_text(run):
    """Text of the last accepted say; else the last assistant message."""
    for c in reversed(run["calls"]):
        if c["name"] == "say" and not call_failed(c)[0] and isinstance(c["args_obj"], dict):
            if c["args_obj"].get("purpose") in (None, "answer", "message"):
                t = c["args_obj"].get("text")
                if isinstance(t, str):
                    return t
    texts = [m["content"] for m in run["messages"]]
    return texts[-1] if texts else ""


def find_claim(text):
    low = text.lower()
    objs = [(m.start(), m.end()) for o in CLAIM_OBJECTS for m in re.finditer(re.escape(o), low)]
    verbs = [(m.start(), m.end()) for v in CLAIM_VERBS for m in re.finditer(re.escape(v), low)]
    for os_, oe in objs:
        for vs, ve in verbs:
            gap = 0 if (os_ < ve and vs < oe) else (vs - oe if vs >= oe else os_ - ve)
            if gap > CLAIM_WINDOW:
                continue
            lo, hi = min(os_, vs), max(oe, ve)
            if any(n in low[max(0, lo - 12):hi] for n in CLAIM_NEGATORS):
                continue
            return text[max(0, lo - 30):hi + 30]
    return ""


def has_view_call(run):
    return any(is_view_call(c) for c in run["calls"])


def detect_claim(run):
    if has_view_call(run):
        return []
    claim = find_claim(final_answer_text(run))
    if not claim:
        return []
    return [F(3, run, "", "visual_claim_no_view", claim, heuristic=True)]


def build_tool_shapes(runs):
    shapes = {k: set(v) for k, v in KNOWN_TOOL_KEYS.items()}
    for r in runs:
        for c in r["calls"]:
            if isinstance(c["args_obj"], dict):
                shapes.setdefault(c["name"], set()).update(c["args_obj"].keys())
    return shapes


def detect_format_leak(run, shapes):
    out = []
    for m in run["messages"]:
        text = m.get("content") or ""
        if not text:
            if not m.get("attachments"):
                out.append(F(4, run, "", "empty_message", "(empty assistant message)"))
            continue
        if is_invisible(text):
            out.append(F(4, run, "", "invisible_only", repr(text[:40])))
            continue
        body = text.strip()
        fence = re.match(r"^```(?:json)?\s*(.*?)\s*```$", body, re.S)
        if fence:
            body = fence.group(1)
        obj = parse_json(body) if body.startswith("{") else None
        if isinstance(obj, dict) and obj:
            keys = set(obj)
            hit = ""
            if "purpose" in keys and "text" in keys:
                hit = "say"
            else:
                for tool, tk in shapes.items():
                    if len(keys) >= 2 and keys <= tk:
                        hit = tool
                        break
            if hit:
                out.append(F(4, run, hit, "json_tool_arguments", body))
                continue
        idx = text.find("[message_id=")
        # A marker at the very start is the quote-reply syntax the UI parses.
        if idx > len(text) - len(text.lstrip()):
            out.append(F(4, run, "", "message_id_marker", text[max(0, idx - 30):idx + 60]))
    return out


def produced_artefacts(run):
    found = []
    for c in run["calls"]:
        if call_failed(c)[0]:
            continue
        if c["name"] == "publish_file":
            res = parse_json(c["result"])
            res = res if isinstance(res, dict) else {}
            argname = c["args_obj"].get("name", "") if isinstance(c["args_obj"], dict) else ""
            name = res.get("name") or argname or ""
            mime = res.get("mime", "")
            ext = name.rsplit(".", 1)[-1].lower() if "." in name else ""
            if ext in IMAGE_DOC_EXT or mime.startswith("image/") or mime == "application/pdf":
                found.append(name or mime)
        elif c["name"] == "generate_image":
            found.append("generate_image")
    for m in run["messages"]:
        for a in m.get("attachments") or []:
            if isinstance(a, str):  # eval records keep attachment names only
                a = {"name": a}
            name = a.get("name") or ""
            ext = name.rsplit(".", 1)[-1].lower() if "." in name else ""
            if ext in IMAGE_DOC_EXT or (a.get("mime") or "").startswith("image/"):
                found.append(name)
    return found


def detect_unseen(run):
    if has_view_call(run):
        return []
    files = produced_artefacts(run)
    if not files:
        return []
    exts = {n.rsplit(".", 1)[-1].lower() for n in files if "." in n}
    image = bool(exts & IMAGE_EXT) or "generate_image" in files
    return [F(5, run, "publish_file", "image_not_viewed" if image else "document_not_viewed",
              "produced: %s" % ", ".join(sorted(set(files))[:5]))]


def detect_quiet(run):
    out = []
    sched = [r for r in run["runs"] if r.get("kind") == "schedule" and r.get("status") in ("done", "")]
    for r in sched:
        if r.get("final_origin") in ("none", "implicit"):
            out.append(F(6, run, "", "final_origin_" + r["final_origin"],
                         "schedule run %s ended with final_origin=%s" % (r.get("id"), r["final_origin"])))
            continue
        texts = [m["content"] for m in run["messages"]] if run["per_run_messages"] else [run["ctx_reply"]]
        if not any(t and not is_invisible(t) for t in texts):
            out.append(F(6, run, "", "answer_empty_or_invisible",
                         "schedule run %s recorded no visible answer" % r.get("id")))
    return out


def detect_review_loop(run):
    refused = []
    for c in run["calls"]:
        o = call_failed(c)[1]
        if c["name"] == "say" and o and o.get("code") in REVIEW_CODES:
            t = c["args_obj"].get("text") if isinstance(c["args_obj"], dict) else ""
            refused.append((o["code"], t if isinstance(t, str) else ""))
    if not refused:
        return []
    final = final_answer_text(run)
    longest = max(len(t) for _, t in refused)
    accepted = any(c["name"] == "say" and not call_failed(c)[0] for c in run["calls"])
    empty = is_invisible(final) or not accepted
    if empty or (longest >= 40 and len(final) < 0.5 * longest):
        return [F(7, run, "say", refused[-1][0] + ("_empty" if empty else "_shorter"),
                  "refused drafts=%d longest=%d final=%d chars" % (len(refused), longest, len(final)))]
    return []


def err_code(err):
    m = re.search(r"\[([a-z][a-z0-9_]{2,})\]", err)
    if m:
        return m.group(1)
    m = re.search(r"HTTP (\d{3})", err)
    if m:
        t = re.search(r'"type":\s*"([a-z_]+)"', err)
        return "http_%s%s" % (m.group(1), "_" + t.group(1) if t else "")
    head = err.split(":")[0].strip()
    return re.sub(r"\W+", "_", head.lower())[:40] or "unknown"


def detect_run_error(run):
    out = []
    for r in run["runs"]:
        if r.get("status") == "failed":
            err = r.get("error") or ""
            out.append(F(8, run, "", err_code(err) if err else "no_error_text", err or "(no error text)"))
    return out


def scan(runs):
    runs = list(runs)
    shapes = build_tool_shapes(runs)
    findings = []
    for run in runs:
        findings += detect_dead_end(run)
        findings += detect_identical_retry(run)
        findings += detect_claim(run)
        findings += detect_format_leak(run, shapes)
        findings += detect_unseen(run)
        findings += detect_quiet(run)
        findings += detect_review_loop(run)
        findings += detect_run_error(run)
    return len(runs), findings


# --------------------------------------------------------------------------
# Grouping, report, baseline

def group(findings):
    groups = {}
    for f in findings:
        key = (f["detector"], f["tool"], f["code"])
        g = groups.setdefault(key, {"detector": f["detector"], "tool": f["tool"], "code": f["code"],
                                    "count": 0, "runs": [], "evidence": f["evidence"],
                                    "legacy_uncoded": f["legacy_uncoded"], "heuristic": f["heuristic"]})
        g["count"] += 1
        if f["run_id"] not in g["runs"]:
            g["runs"].append(f["run_id"])
    out = sorted(groups.values(), key=lambda g: (g["detector"], -g["count"], g["tool"], g["code"]))
    for g in out:
        g["fingerprint"] = "%d|%s|%s" % (g["detector"], g["tool"], g["code"])
    return out


def build_summary(n_runs, findings, groups, baseline=None):
    per = {}
    for d, (name, title, hard) in DETECTORS.items():
        fs = [f for f in findings if f["detector"] == d]
        per[d] = {"name": name, "title": title, "hard": hard, "findings": len(fs),
                  "groups": len([g for g in groups if g["detector"] == d]),
                  "runs": len({f["run_id"] for f in fs})}
    known = baseline if baseline is not None else set()
    new = [g["fingerprint"] for g in groups if g["detector"] in HARD and g["fingerprint"] not in known]
    return {"runs_scanned": n_runs, "per_detector": per, "new_hard": new}


def render_markdown(summary, groups, top=8, title="Dead-end scan"):
    L = ["# %s" % title, "", "Runs scanned: %d" % summary["runs_scanned"], "",
         "| # | Detector | Gate | Findings | Groups | Runs |", "|---|---|---|---|---|---|"]
    for d, p in summary["per_detector"].items():
        L.append("| %d | %s | %s | %d | %d | %d |" % (d, p["title"], "hard" if p["hard"] else "soft",
                                                      p["findings"], p["groups"], p["runs"]))
    L += ["", "New hard findings vs baseline: %s" % (", ".join(summary["new_hard"]) or "none")]
    for d, (_, dtitle, _) in DETECTORS.items():
        gs = [g for g in groups if g["detector"] == d]
        if not gs:
            continue
        L += ["", "## %d. %s" % (d, dtitle), ""]
        for g in sorted(gs, key=lambda g: -g["count"])[:top]:
            tags = (["legacy-uncoded"] if g["legacy_uncoded"] else []) + (["heuristic"] if g["heuristic"] else [])
            L.append("- **%s / %s** x%d%s" % (g["tool"] or "(run)", g["code"], g["count"],
                                              " (%s)" % ", ".join(tags) if tags else ""))
            L.append("  - runs: %s%s" % (", ".join(g["runs"][:5]), " ..." if len(g["runs"]) > 5 else ""))
            L.append("  - evidence: `%s`" % g["evidence"].replace("`", "'"))
    return "\n".join(L) + "\n"


def load_baseline(path):
    with open(path, encoding="utf-8") as f:
        d = json.load(f)
    return set(d.get("fingerprints") or [])


def main(argv=None):
    ap = argparse.ArgumentParser(description="Scan TOFI run records for plumbing breaks (read-only).")
    ap.add_argument("--eval", action="append", default=[], metavar="PATH",
                    help="eval results folder (results.json + deliverables/) or a root of such folders; globs ok; repeatable")
    ap.add_argument("--remote", help="https://host:port of a live server (GET only)")
    ap.add_argument("--cookie-file", help="Netscape cookie jar for --remote")
    ap.add_argument("--insecure", action="store_true", help="skip TLS verification for --remote")
    ap.add_argument("--since", default="", help="ISO time; only conversations/runs at or after it")
    ap.add_argument("--exclude-title-prefix", default="", help="skip conversations whose name starts with this")
    ap.add_argument("--max-conversations", type=int, default=0)
    ap.add_argument("--top", type=int, default=8, help="groups shown per detector")
    ap.add_argument("--json", action="store_true", help="print JSON instead of markdown")
    ap.add_argument("--baseline", help="JSON file of known hard-detector fingerprints")
    ap.add_argument("--write-baseline", help="write current hard-detector fingerprints to this file")
    a = ap.parse_args(argv)
    if not a.eval and not a.remote:
        ap.error("give --eval PATH and/or --remote URL")
    try:
        baseline = load_baseline(a.baseline) if a.baseline else None
    except (OSError, ValueError) as e:
        print("cannot read baseline: %s" % e, file=sys.stderr)
        return 2

    runs = []
    try:
        for spec in a.eval:
            dirs = find_eval_dirs(spec)
            if not dirs:
                print("no eval results under %s" % spec, file=sys.stderr)
                return 2
            for d in dirs:
                runs.extend(load_eval_dir(d))
        if a.remote:
            rem = Remote(a.remote, a.cookie_file, a.insecure)
            runs.extend(load_remote(rem, a.since, a.exclude_title_prefix, a.max_conversations,
                                    log=lambda s: print(s, file=sys.stderr)))
    except (OSError, ValueError, urllib.error.URLError) as e:
        print("input error: %s" % e, file=sys.stderr)
        return 2

    n, findings = scan(runs)
    groups = group(findings)
    summary = build_summary(n, findings, groups, baseline)
    if a.json:
        print(json.dumps({"summary": summary, "groups": groups, "findings": findings},
                         ensure_ascii=False, indent=1))
    else:
        sys.stdout.write(render_markdown(summary, groups, a.top))
    if a.write_baseline:
        with open(a.write_baseline, "w", encoding="utf-8") as f:
            json.dump({"fingerprints": sorted(g["fingerprint"] for g in groups if g["detector"] in HARD)}, f, indent=1)
    return 1 if summary["new_hard"] else 0


if __name__ == "__main__":
    sys.exit(main())
