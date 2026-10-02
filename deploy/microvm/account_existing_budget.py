#!/usr/bin/env python3
"""Read-only planning check for trusted external computer inventory.

Existing disks stay external to the Worker ledger and keep their full promises,
including disabled/stopped computers. Only verified exclusive ext4 allocations on the measured filesystem are credited
against future physical growth; full logical promises remain recorded. This does not register or mutate a production service.
"""
import argparse
import json

GiB = 1024**3


def planning_budget(inventory, headroom_bytes, new_quota_bytes, new_internal_bytes):
    if not isinstance(inventory, dict) or inventory.get("schema") != 1:
        raise ValueError("inventory schema 1 required")
    for value in (headroom_bytes, new_quota_bytes, new_internal_bytes):
        if type(value) is not int or value < 0:
            raise ValueError("nonnegative integer planning amounts required")
    if inventory.get("unconfigured_disks"):
        raise ValueError("unconfigured workspace disks require capacity reconciliation")
    host = inventory.get("host")
    if not isinstance(host, dict):
        raise ValueError("host inventory required")
    total, available, cpus = (host.get(k) for k in ("filesystem_total_bytes", "filesystem_available_bytes", "online_cpus"))
    if (any(type(value) is not int for value in (total, available, cpus))
            or not 0 <= available <= total or total <= 0 or cpus <= 0):
        raise ValueError("invalid host inventory")
    computers = inventory.get("external_computers")
    if not isinstance(computers, list):
        raise ValueError("complete external computer inventory required")
    labels, slots = set(), set()
    reserved = credited = active_cpu = active_memory = 0
    inodes = set()
    details = []
    for computer in computers:
        if not isinstance(computer, dict):
            raise ValueError("invalid external computer")
        label, slot, quota, disk, ext4, cpu, memory, active = (computer.get(k) for k in
            ("asset_id", "slot", "quota_bytes", "disk_logical_bytes", "ext4_bytes", "vcpus", "memory_mib", "active"))
        if (not isinstance(label, str) or not label or label in labels
                or any(type(value) is not int for value in (slot, quota, disk, ext4, cpu, memory))
                or slot in slots or not 1 <= slot <= 250 or not 8*GiB <= quota <= 1024*GiB
                or disk != quota or ext4 != quota or not 1 <= cpu <= 32
                or not 512 <= memory <= 32768 or type(active) is not bool):
            raise ValueError("external identity/resource/geometry inventory is incomplete or inconsistent")
        labels.add(label); slots.add(slot); reserved += quota
        allocated = computer.get("disk_allocated_bytes", 0)
        if type(allocated) is not int or allocated < 0:
            raise ValueError("invalid allocated block measurement")
        device, inode, links = (computer.get(k) for k in ("disk_device", "disk_inode", "disk_links"))
        exclusive = (host.get("filesystem_type") == "ext4"
                     and type(device) is int and device == host.get("filesystem_device")
                     and type(inode) is int and inode > 0 and type(links) is int and links >= 1
                     and (links == 1 or computer.get("disk_all_links_accounted") is True))
        identity = (device, inode)
        if exclusive and identity in inodes:
            raise ValueError("duplicate physical disk identity")
        if exclusive:
            inodes.add(identity)
        credit = min(quota, allocated) if exclusive else 0
        credited += credit
        details.append(dict(asset_id=label, quota_bytes=quota, allocated_bytes=allocated,
                            credited_allocated_bytes=credit, remaining_commitment_bytes=quota-credit))
        if active:
            active_cpu += cpu
            active_memory += memory
    unallocated = reserved - credited
    remaining = available - unallocated - headroom_bytes
    blockers = []
    if new_quota_bytes + new_internal_bytes > remaining:
        blockers.append("disk commitments leave insufficient space for the requested account and service reserve")
    if cpus - active_cpu <= 0:
        blockers.append("existing active vCPU commitments leave no conservative Worker CPU budget")
    return dict(external_reserved_bytes=reserved, external_credited_allocated_bytes=credited,
                external_unallocated_promises_bytes=unallocated, external_assets=details,
                allocation_credit_policy="unique same-device ext4 inode with all hardlinks accounted; unknown/shared allocations get zero credit",
                cpu_budget_policy="no vCPU commitment overcommit; not measured CPU utilization",
                reserved_slots=sorted(slots),
                external_active_vcpus=active_cpu, external_active_memory_mib=active_memory,
                max_worker_vcpu_budget=max(0, cpus-active_cpu),
                disk_admission_remaining_bytes=remaining,
                required_new_account_bytes=new_quota_bytes+new_internal_bytes,
                blockers=blockers, ready=not blockers, runtime_verified=False)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("inventory")
    parser.add_argument("--headroom-gib", type=int, required=True)
    parser.add_argument("--new-quota-gib", type=int, required=True)
    parser.add_argument("--new-internal-gib", type=int, required=True)
    args = parser.parse_args()
    try:
        with open(args.inventory, encoding="utf-8") as source:
            inventory = json.load(source)
        report = planning_budget(inventory, args.headroom_gib*GiB, args.new_quota_gib*GiB, args.new_internal_gib*GiB)
    except (ValueError, OSError) as error:
        parser.error(str(error))
    print(json.dumps(report, sort_keys=True))
    return 0 if report["ready"] else 2


if __name__ == "__main__":
    raise SystemExit(main())
