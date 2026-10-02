#!/usr/bin/env python3
"""Measure configured external disks with an isolated temporary admission ledger.

Only owned /tmp ledger files are created. No disk, guest, or service is changed.
Reservations exercise concurrency but never provision or launch a computer.
"""
import argparse
import concurrent.futures
import datetime
import json
from pathlib import Path
import tempfile
import uuid
from account_capacity import AdmissionError, CapacityLedger


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True)
    parser.add_argument("--quota-gib", type=int, default=8)
    args = parser.parse_args()
    config = json.loads(Path(args.config).read_text())
    with tempfile.TemporaryDirectory(prefix="tofi-capacity-probe-", dir="/tmp") as temporary:
        root = Path(temporary).resolve()
        ledger = CapacityLedger(root/"ledger.sqlite", root,
            config["headroom_bytes"], config["warning_bytes"],
            external_disks=config["external_disks"], reserved_slots=config["reserved_slots"],
            external_reserved_bytes=config["external_reserved_bytes"],
            per_account_internal_reserved_bytes=config["per_account_internal_reserved_bytes"])
        before = ledger.snapshot()
        def reserve(_):
            try:
                result = ledger.reserve(str(uuid.uuid4()), args.quota_gib*1024**3)
                return {"reserved": True, "slot": result["slot"]}
            except AdmissionError as error:
                return {"reserved": False, "error": str(error)}
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            outcomes = list(pool.map(reserve, range(2)))
        after = ledger.snapshot()
        print(json.dumps(dict(mode="isolated-ledger-live-read-only-disk-probe",
                              observed_at_utc=datetime.datetime.now(datetime.timezone.utc).isoformat(), before=before,
                              concurrent_reservations=outcomes, after=after,
                              vm_started=False, production_changed=False), sort_keys=True, indent=2))


if __name__ == "__main__":
    main()
