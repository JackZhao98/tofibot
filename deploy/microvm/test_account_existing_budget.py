"""Isolated acceptance for external owner inventory; no production writes."""
import copy
from pathlib import Path
import tempfile
import unittest
import uuid

from account_capacity import CapacityLedger, AdmissionError
from account_existing_budget import planning_budget, GiB


class ExistingBudgetTests(unittest.TestCase):
    def inventory(self):
        inventory = dict(schema=1,host=dict(filesystem_total_bytes=100*GiB,filesystem_available_bytes=65*GiB,online_cpus=4),
                    external_computers=[dict(asset_id=label,slot=slot,quota_bytes=30*GiB,
                    disk_logical_bytes=30*GiB,ext4_bytes=30*GiB,vcpus=2,memory_mib=4096,active=True)
                    for label,slot in (("fixture-owner",1),("fixture-acceptance",21))])
        inventory["external_computers"].append(dict(asset_id="fixture-stopped",slot=22,quota_bytes=8*GiB,
            disk_logical_bytes=8*GiB,ext4_bytes=8*GiB,vcpus=2,memory_mib=2048,active=False))
        return inventory

    def test_full_owner_reserves_gate_new_account_and_survive_ledger_reopen(self):
        inventory=self.inventory()
        plan=planning_budget(inventory,GiB,8*GiB,2*GiB)
        self.assertEqual(plan["external_reserved_bytes"],68*GiB)
        self.assertEqual(plan["max_worker_vcpu_budget"],0)
        self.assertFalse(plan["ready"])
        with tempfile.TemporaryDirectory(dir="/tmp") as temporary:
            root=Path(temporary)
            for _ in range(2):
                ledger=CapacityLedger(root/"ledger.sqlite",root,GiB,2*GiB,
                    metrics=lambda:dict(total_bytes=100*GiB,available_bytes=65*GiB),
                    external_reserved_bytes=plan["external_reserved_bytes"],
                    per_account_internal_reserved_bytes=2*GiB,reserved_slots=plan["reserved_slots"])
                with self.assertRaises(AdmissionError):ledger.reserve(str(uuid.uuid4()),8*GiB)
                snapshot=ledger.snapshot()
                self.assertEqual(snapshot["external_reserved_bytes"],68*GiB)
                self.assertEqual(snapshot["accounts"],[])

    def test_stopped_or_disabled_owner_still_holds_full_disk_promise(self):
        inventory=self.inventory()
        for computer in inventory["external_computers"]:
            computer["active"]=False
            computer["disabled"]=True
            computer["disk_allocated_bytes"]=4096
        plan=planning_budget(inventory,GiB,8*GiB,2*GiB)
        self.assertEqual(plan["external_reserved_bytes"],68*GiB)
        self.assertEqual(plan["max_worker_vcpu_budget"],4)
        self.assertFalse(plan["ready"])

    def exclusive_inventory(self):
        inventory = self.inventory()
        inventory["host"].update(filesystem_type="ext4", filesystem_device=7)
        for index, computer in enumerate(inventory["external_computers"]):
            computer.update(disk_device=7, disk_inode=index+1, disk_links=1,
                            disk_allocated_bytes=4*GiB)
        return inventory

    def test_existing_blocks_are_not_reserved_twice_and_ledger_reopens(self):
        plan = planning_budget(self.exclusive_inventory(), GiB, 8*GiB, 2*GiB)
        self.assertEqual(plan["external_reserved_bytes"], 68*GiB)
        self.assertEqual(plan["external_unallocated_promises_bytes"], 56*GiB)
        self.assertEqual(plan["disk_admission_remaining_bytes"], 8*GiB)
        # 65 free - (68 promised - 12 allocated) - 1 headroom = 8, not old -4.
        with tempfile.TemporaryDirectory(dir="/tmp") as temporary:
            for _ in range(2):
                ledger = CapacityLedger(Path(temporary)/"ledger.sqlite", temporary, GiB, 2*GiB,
                    metrics=lambda:dict(total_bytes=100*GiB, available_bytes=65*GiB),
                    external_reserved_bytes=plan["external_unallocated_promises_bytes"])
                self.assertEqual(ledger.snapshot()["admission_remaining_bytes"], 8*GiB)

    def test_verified_jail_hardlink_is_credited_once(self):
        inventory = self.exclusive_inventory()
        for computer in inventory["external_computers"]:
            computer.update(disk_links=2, disk_all_links_accounted=True)
        self.assertEqual(planning_budget(inventory,GiB,8*GiB,2*GiB)
                         ["external_credited_allocated_bytes"], 12*GiB)

    def test_unknown_shared_other_device_and_duplicate_allocations(self):
        for change in (
            lambda i:i["host"].update(filesystem_type="btrfs"),
            lambda i:[c.update(disk_links=2) for c in i["external_computers"]],
            lambda i:[c.update(disk_device=8) for c in i["external_computers"]],
        ):
            inventory = self.exclusive_inventory(); change(inventory)
            self.assertEqual(planning_budget(inventory,GiB,8*GiB,2*GiB)
                             ["external_credited_allocated_bytes"], 0)
        inventory = self.exclusive_inventory()
        inventory["external_computers"][1]["disk_inode"] = 1
        with self.assertRaises(ValueError): planning_budget(inventory,GiB,8*GiB,2*GiB)

    def test_missing_geometry_duplicate_assets_and_unverified_numbers_fail(self):
        for mutate in (
            lambda i:i["external_computers"][0].pop("ext4_bytes"),
            lambda i:i["external_computers"].append(copy.deepcopy(i["external_computers"][0])),
            lambda i:i["external_computers"][0].update(quota_bytes=True),
            lambda i:i["external_computers"][0].update(ext4_bytes=8*GiB),
            lambda i:i.update(unconfigured_disks=["/fixture/unregistered/workspace.ext4"]),
        ):
            inventory=self.inventory();mutate(inventory)
            with self.assertRaises(ValueError):planning_budget(inventory,GiB,8*GiB,2*GiB)


if __name__ == "__main__":unittest.main()
