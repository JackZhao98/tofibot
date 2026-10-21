"""Shared helpers for the built-in skill script tests (Python 3.9+, stdlib only)."""
from __future__ import annotations

import importlib.util
import os
import shutil
import subprocess
import sys
import unittest

SKILLS = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def script_path(skill, name):
    return os.path.join(SKILLS, skill, "scripts", name)


def load(skill, name):
    """Import scripts/<name>.py of a skill as a module."""
    path = script_path(skill, name + ".py")
    sys.path.insert(0, os.path.dirname(path))
    try:
        spec = importlib.util.spec_from_file_location("skill_%s_%s" % (skill.replace("-", "_"), name), path)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module
    finally:
        sys.path.pop(0)


def run_script(skill, name, *args, **kwargs):
    return subprocess.run([sys.executable, script_path(skill, name + ".py")] + [str(a) for a in args],
                          capture_output=True, text=True, timeout=kwargs.get("timeout", 240), cwd=kwargs.get("cwd"))


def _missing_modules(modules):
    return [m for m in modules if importlib.util.find_spec(m) is None]


def need_modules(*modules):
    """Skip a test (loudly, on stderr and in the report) when a library is absent."""
    missing = _missing_modules(modules)
    if missing:
        print("SKIPPED (library not installed on this machine): %s" % ", ".join(missing), file=sys.stderr)
    return unittest.skipIf(bool(missing), "SKIPPED: python library missing: %s" % ", ".join(missing))


def need_programs(*programs):
    missing = [p for p in programs if shutil.which(p) is None]
    if missing:
        print("SKIPPED (program not installed on this machine): %s" % ", ".join(missing), file=sys.stderr)
    return unittest.skipIf(bool(missing), "SKIPPED: program missing: %s" % ", ".join(missing))
