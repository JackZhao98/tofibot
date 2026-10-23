import contextlib
import http.server
import io
import json
import os
import sys
import tempfile
import threading
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))
import deadend_scan as d  # noqa: E402


def outcome(status, code, next_action, message="msg", **extra):
    o = {"version": 1, "status": status, "code": code, "execution_certainty": "not_executed",
         "message": message, "next_action": next_action}
    o.update(extra)
    return json.dumps(o)


def call(name, args, result="ok", status="completed"):
    if not isinstance(args, str):
        args = json.dumps(args)
    return d.make_call(name, args, result, status)


def run(calls=(), messages=(), runs=None, per_run=True, ctx_reply=""):
    return {"id": "r1", "source": "test", "title": "t",
            "runs": runs if runs is not None else [{"id": "r1", "kind": "chat", "status": "done",
                                                    "error": "", "final_origin": "say"}],
            "calls": list(calls),
            "messages": [{"content": m, "role": "assistant", "attachments": [], "run_id": "r1"}
                         if isinstance(m, str) else m for m in messages],
            "ctx_reply": ctx_reply, "per_run_messages": per_run}


class DeadEnd(unittest.TestCase):
    def test_terminal_next_action_is_dead_end(self):
        r = run([call("computer_browser", {"action": "browser.navigate"},
                      outcome("permanent_failure", "observation_failed", "explain_blocker"))])
        f = d.detect_dead_end(r)
        self.assertEqual(len(f), 1)
        self.assertEqual(f[0]["code"], "observation_failed")
        self.assertFalse(f[0]["legacy_uncoded"])

    def test_repair_arguments_is_not_dead_end(self):
        r = run([call("x", {}, outcome("validation_error", "invalid_arguments", "repair_arguments"))])
        self.assertEqual(d.detect_dead_end(r), [])

    def test_bare_not_found_code_is_dead_end_even_with_repair(self):
        r = run([call("manage_extensions", {}, outcome("permanent_failure", "mcp_server_not_found",
                                                        "repair_arguments", "MCP server not found"))])
        self.assertEqual(len(d.detect_dead_end(r)), 1)

    def test_not_found_with_alternatives_is_fine(self):
        res = json.loads(outcome("permanent_failure", "mcp_server_not_found", "repair_arguments"))
        res["servers"] = ["alpha", "beta"]
        r = run([call("manage_extensions", {}, json.dumps(res))])
        self.assertEqual(d.detect_dead_end(r), [])

    def test_list_in_message_counts_as_alternatives(self):
        msg = "No such server. Known servers:\n- alpha\n- beta"
        r = run([call("t", {}, outcome("permanent_failure", "server_not_found", "explain_blocker", msg))])
        self.assertEqual(d.detect_dead_end(r), [])

    def test_legacy_uncoded_text_error(self):
        r = run([call("t", {}, "Tool error: MCP server not found", "failed")])
        f = d.detect_dead_end(r)
        self.assertEqual(len(f), 1)
        self.assertTrue(f[0]["legacy_uncoded"])
        self.assertTrue(f[0]["code"].startswith("uncoded:"))

    def test_unclassified_code_is_legacy(self):
        r = run([call("publish_file", {}, outcome("uncertain_effect", "unclassified_tool_failure", "verify_effect"))])
        f = d.detect_dead_end(r)
        self.assertEqual(len(f), 1)
        self.assertTrue(f[0]["legacy_uncoded"])

    def test_denial_and_approval_are_not_plumbing(self):
        r = run([call("t", {}, outcome("denied", "policy", "explain_blocker")),
                 call("t", {}, outcome("approval_expired", "x", "explain_blocker"))])
        self.assertEqual(d.detect_dead_end(r), [])

    def test_success_ignored(self):
        self.assertEqual(d.detect_dead_end(run([call("t", {}, "fine")])), [])


class IdenticalRetry(unittest.TestCase):
    def test_same_args_after_failure(self):
        bad = outcome("permanent_failure", "tool_rejected", "repair_arguments")
        r = run([call("read_attachment", {"a": 1, "b": 2}, bad),
                 call("read_attachment", {"b": 2, "a": 1}, bad)])
        f = d.detect_identical_retry(r)
        self.assertEqual(len(f), 1)
        self.assertEqual(f[0]["code"], "tool_rejected")

    def test_different_args_ok(self):
        bad = outcome("permanent_failure", "tool_rejected", "repair_arguments")
        r = run([call("t", {"a": 1}, bad), call("t", {"a": 2}, bad)])
        self.assertEqual(d.detect_identical_retry(r), [])

    def test_transient_retry_is_sanctioned(self):
        r = run([call("t", {"a": 1}, outcome("transient_failure", "busy", "retry")),
                 call("t", {"a": 1}, "ok")])
        self.assertEqual(d.detect_identical_retry(r), [])

    def test_repeat_of_success_ok(self):
        r = run([call("t", {"a": 1}), call("t", {"a": 1})])
        self.assertEqual(d.detect_identical_retry(r), [])


class ClaimWithoutCall(unittest.TestCase):
    def say(self, text):
        return call("say", {"purpose": "answer", "text": text})

    def test_claim_without_view(self):
        r = run([self.say("I checked the preview and the layout is fine.")])
        f = d.detect_claim(r)
        self.assertEqual(len(f), 1)
        self.assertTrue(f[0]["heuristic"])

    def test_claim_in_chinese(self):
        r = run([self.say("我已检查过预览图，排版没有问题。")])
        self.assertEqual(len(d.detect_claim(r)), 1)

    def test_view_call_clears_claim(self):
        r = run([call("computer_files", {"action": "files.view", "path": "a.png"}),
                 self.say("I checked the preview.")])
        self.assertEqual(d.detect_claim(r), [])

    def test_snapshot_and_capture_clear_claim(self):
        for name, act in (("computer_browser", "browser.snapshot"), ("computer_desktop", "desktop.capture")):
            r = run([call(name, {"action": act}), self.say("I viewed the screenshot.")])
            self.assertEqual(d.detect_claim(r), [], act)

    def test_failed_view_does_not_count(self):
        bad = outcome("permanent_failure", "file_not_found", "repair_arguments")
        r = run([call("computer_files", {"action": "files.view", "path": "a.png"}, bad),
                 self.say("I checked the preview.")])
        self.assertEqual(len(d.detect_claim(r)), 1)

    def test_files_read_is_not_a_view(self):
        r = run([call("computer_files", {"action": "files.read", "path": "a.png"}),
                 self.say("I checked the preview.")])
        self.assertEqual(len(d.detect_claim(r)), 1)

    def test_negation_skips(self):
        r = run([self.say("I could not check the preview.")])
        self.assertEqual(d.detect_claim(r), [])

    def test_no_claim_no_finding(self):
        self.assertEqual(d.detect_claim(run([self.say("Done, file attached.")])), [])


class FormatLeak(unittest.TestCase):
    shapes = {"say": {"purpose", "text"}, "computer_files": {"action", "path"}}

    def codes(self, *msgs):
        return [f["code"] for f in d.detect_format_leak(run(messages=msgs), self.shapes)]

    def test_say_arguments_published(self):
        self.assertEqual(self.codes('{"purpose":"answer","text":"hello"}'), ["json_tool_arguments"])

    def test_fenced_json(self):
        self.assertEqual(self.codes('```json\n{"purpose":"note","text":"x"}\n```'), ["json_tool_arguments"])

    def test_other_tool_shape(self):
        self.assertEqual(self.codes('{"action":"files.view","path":"a.png"}'), ["json_tool_arguments"])

    def test_ordinary_json_is_fine(self):
        self.assertEqual(self.codes('{"revenue": 3, "days": 7}'), [])

    def test_leading_message_id_is_quote_syntax(self):
        self.assertEqual(self.codes("[message_id=abc] Here you go"), [])

    def test_mid_text_message_id(self):
        self.assertEqual(self.codes("Here you go [message_id=abc] thanks"), ["message_id_marker"])

    def test_invisible_only(self):
        self.assertEqual(self.codes("​​ \n"), ["invisible_only"])

    def test_empty_without_attachment(self):
        self.assertEqual(self.codes(""), ["empty_message"])

    def test_empty_with_attachment_ok(self):
        m = {"content": "", "role": "assistant", "attachments": [{"name": "a.png"}], "run_id": "r1"}
        self.assertEqual(self.codes(m), [])


class UnseenArtefacts(unittest.TestCase):
    pub = staticmethod(lambda name: call("publish_file", {"path": "/w/" + name, "name": name},
                                         json.dumps({"name": name, "mime": "application/octet-stream"})))

    def test_image_not_viewed(self):
        f = d.detect_unseen(run([self.pub("cover.png")]))
        self.assertEqual([x["code"] for x in f], ["image_not_viewed"])

    def test_document_not_viewed(self):
        f = d.detect_unseen(run([self.pub("deck.pptx")]))
        self.assertEqual([x["code"] for x in f], ["document_not_viewed"])

    def test_viewed_is_fine(self):
        r = run([self.pub("cover.png"), call("computer_files", {"action": "files.view", "path": "cover.png"})])
        self.assertEqual(d.detect_unseen(r), [])

    def test_csv_and_zip_ignored(self):
        self.assertEqual(d.detect_unseen(run([self.pub("a.csv"), self.pub("b.zip"), self.pub("c.xlsx")])), [])

    def test_generate_image_counts(self):
        f = d.detect_unseen(run([call("generate_image", {"prompt": "x"})]))
        self.assertEqual([x["code"] for x in f], ["image_not_viewed"])

    def test_attachment_name_counts(self):
        m = {"content": "see", "role": "assistant", "attachments": ["x.png"], "run_id": "r1"}
        self.assertEqual(len(d.detect_unseen(run(messages=[m]))), 1)


class QuietCompletions(unittest.TestCase):
    def sched(self, origin, status="done"):
        return [{"id": "s1", "kind": "schedule", "status": status, "error": "", "final_origin": origin}]

    def test_origin_none(self):
        f = d.detect_quiet(run(runs=self.sched("none"), messages=["done"]))
        self.assertEqual([x["code"] for x in f], ["final_origin_none"])

    def test_origin_implicit(self):
        f = d.detect_quiet(run(runs=self.sched("implicit"), messages=["done"]))
        self.assertEqual([x["code"] for x in f], ["final_origin_implicit"])

    def test_empty_answer(self):
        f = d.detect_quiet(run(runs=self.sched("say"), messages=["​"]))
        self.assertEqual([x["code"] for x in f], ["answer_empty_or_invisible"])

    def test_no_message_at_all(self):
        f = d.detect_quiet(run(runs=self.sched("say")))
        self.assertEqual(len(f), 1)

    def test_good_schedule_run(self):
        self.assertEqual(d.detect_quiet(run(runs=self.sched("say"), messages=["receipt"])), [])

    def test_chat_run_with_none_not_flagged(self):
        r = run(runs=[{"id": "c", "kind": "chat", "status": "done", "error": "", "final_origin": "none"}])
        self.assertEqual(d.detect_quiet(r), [])

    def test_failed_schedule_is_detector_8(self):
        self.assertEqual(d.detect_quiet(run(runs=self.sched("none", "failed"))), [])

    def test_eval_mode_uses_reply_text(self):
        f = d.detect_quiet(run(runs=self.sched("say"), per_run=False, ctx_reply=""))
        self.assertEqual(len(f), 1)


class ReviewLoop(unittest.TestCase):
    def refused(self, text, code="answer_review"):
        return call("say", {"purpose": "answer", "text": text}, outcome("transient_failure", code, "retry"))

    def test_shorter_final(self):
        r = run([self.refused("x" * 400), call("say", {"purpose": "answer", "text": "ok"})])
        f = d.detect_review_loop(r)
        self.assertEqual([x["code"] for x in f], ["answer_review_shorter"])

    def test_empty_final(self):
        r = run([self.refused("x" * 400, "answer_revise")])
        f = d.detect_review_loop(r)
        self.assertEqual([x["code"] for x in f], ["answer_revise_empty"])

    def test_revised_same_length_ok(self):
        r = run([self.refused("x" * 400), call("say", {"purpose": "answer", "text": "y" * 380})])
        self.assertEqual(d.detect_review_loop(r), [])

    def test_no_refusal_no_finding(self):
        self.assertEqual(d.detect_review_loop(run([call("say", {"purpose": "answer", "text": "ok"})])), [])


class RunErrors(unittest.TestCase):
    def test_bracket_code(self):
        r = run(runs=[{"id": "r", "kind": "chat", "status": "failed", "final_origin": "none",
                       "error": "LLM call failed: response failed: [server_is_overloaded] try later"}])
        self.assertEqual([x["code"] for x in d.detect_run_error(r)], ["server_is_overloaded"])

    def test_http_code(self):
        r = run(runs=[{"id": "r", "kind": "chat", "status": "failed", "final_origin": "none",
                       "error": 'anthropic API error (HTTP 400): {"type":"error","error":{"type":"invalid_request_error"}}'}])
        self.assertEqual([x["code"] for x in d.detect_run_error(r)], ["http_400_error"])

    def test_done_run_ignored(self):
        self.assertEqual(d.detect_run_error(run()), [])


class Helpers(unittest.TestCase):
    def test_mask_secrets(self):
        s = d.snippet("Authorization: Bearer abcdef123456 and token=supersecretvalue sk-abcdefghijklmnop1234")
        self.assertNotIn("abcdef123456", s)
        self.assertNotIn("supersecretvalue", s)
        self.assertNotIn("sk-abcdefghijklmnop1234", s)

    def test_snippet_bounded(self):
        self.assertLessEqual(len(d.snippet("word " * 500)), d.SNIPPET_MAX)

    def test_invisible(self):
        self.assertTrue(d.is_invisible("​\x00 \n"))
        self.assertFalse(d.is_invisible("a"))

    def test_cookie_file_httponly(self):
        with tempfile.NamedTemporaryFile("w", suffix=".txt", delete=False) as f:
            f.write("# Netscape HTTP Cookie File\n#HttpOnly_.example.com\tTRUE\t/\tTRUE\t0\tsid\tabc\n"
                    "other.org\tFALSE\t/\tFALSE\t0\tx\ty\n")
        try:
            self.assertEqual(d.parse_cookie_file(f.name, "app.example.com"), "sid=abc")
        finally:
            os.unlink(f.name)


def write_eval(root, tool_texts, messages, statuses, reply="hello"):
    os.makedirs(os.path.join(root, "deliverables"))
    entry = {"task_id": "t1", "model": "default", "run_index": 0, "run_statuses": statuses}
    with open(os.path.join(root, "results.json"), "w") as f:
        json.dump({"by_model": {"default": [entry]}}, f)
    with open(os.path.join(root, "deliverables", "t1-default-r0.context.json"), "w") as f:
        json.dump({"tool_texts": tool_texts, "messages": messages, "reply_text": reply}, f)


class CLI(unittest.TestCase):
    def run_main(self, argv):
        out = io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(io.StringIO()):
            code = d.main(argv)
        return code, out.getvalue()

    def test_eval_end_to_end_and_baseline(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = os.path.join(tmp, "res1")
            bad = outcome("permanent_failure", "observation_failed", "explain_blocker")
            write_eval(root, ['computer_browser {"action":"browser.navigate"}\n' + bad],
                       [{"text": "ok", "attachments": []}],
                       [{"id": "a", "kind": "chat", "status": "done", "final_origin": "say"}])
            code, out = self.run_main(["--eval", root])
            self.assertEqual(code, 1)
            self.assertIn("observation_failed", out)
            base = os.path.join(tmp, "base.json")
            code, _ = self.run_main(["--eval", root, "--write-baseline", base])
            self.assertEqual(code, 1)
            code, _ = self.run_main(["--eval", root, "--baseline", base])
            self.assertEqual(code, 0)

    def test_json_output_and_root_discovery(self):
        with tempfile.TemporaryDirectory() as tmp:
            write_eval(os.path.join(tmp, "res1"), ['say {"purpose":"answer","text":"hi"}\nposted'],
                       [{"text": "hi", "attachments": []}],
                       [{"id": "a", "kind": "chat", "status": "done", "final_origin": "say"}])
            code, out = self.run_main(["--eval", tmp, "--json"])
            self.assertEqual(code, 0)
            self.assertEqual(json.loads(out)["summary"]["runs_scanned"], 1)

    def test_usage_error(self):
        with self.assertRaises(SystemExit):
            with contextlib.redirect_stderr(io.StringIO()):
                d.main([])


class FakeServer(http.server.BaseHTTPRequestHandler):
    methods = []
    cookies = []

    def log_message(self, *a):
        pass

    def _send(self, obj):
        body = json.dumps(obj).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        FakeServer.methods.append("GET")
        FakeServer.cookies.append(self.headers.get("Cookie"))
        path = self.path.split("?")[0]
        if path == "/api/conversations":
            self._send({"conversations": [
                {"id": "c1", "name": "real", "updated_at": "2026-10-22T00:00:00Z"},
                {"id": "c2", "name": "评测-x", "updated_at": "2026-10-22T00:00:00Z"}]})
        elif path == "/api/conversations/c1/runs":
            self._send({"runs": [{"id": "run1", "kind": "chat", "status": "done", "final_origin": "say",
                                  "created_at": "2026-10-22T01:00:00Z"}], "has_more": False})
        elif path == "/api/conversations/c1/messages":
            self._send({"messages": [{"id": "m1", "seq": 1, "role": "assistant", "run_id": "run1",
                                      "content": "x [message_id=zz] y", "created_at": "2026-10-22T01:00:01Z"}],
                        "has_more": False})
        elif path == "/api/conversations/c1/tools":
            self._send({"activities": [{"name": "t", "arguments": "{}", "result": "Tool error: server not found",
                                        "status": "failed"}], "has_more": False})
        else:
            self.send_response(404)
            self.end_headers()

    def do_POST(self):
        FakeServer.methods.append("POST")
        self.send_response(405)
        self.end_headers()


class Live(unittest.TestCase):
    def test_remote_is_get_only_and_filters(self):
        FakeServer.methods, FakeServer.cookies = [], []
        srv = http.server.HTTPServer(("127.0.0.1", 0), FakeServer)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        try:
            with tempfile.NamedTemporaryFile("w", suffix=".txt", delete=False) as f:
                f.write("127.0.0.1\tFALSE\t/\tFALSE\t0\tsid\tabc\n")
            rem = d.Remote("http://127.0.0.1:%d" % srv.server_port, f.name)
            runs = list(d.load_remote(rem, since="2026-10-21T00:00:00Z", exclude_prefix="评测"))
            n, findings = d.scan(runs)
        finally:
            srv.shutdown()
            srv.server_close()
            os.unlink(f.name)
        self.assertEqual(n, 1)
        self.assertEqual(set(FakeServer.methods), {"GET"})
        self.assertIn("sid=abc", FakeServer.cookies)
        dets = {x["detector"] for x in findings}
        self.assertEqual(dets, {1, 4})


if __name__ == "__main__":
    unittest.main()
