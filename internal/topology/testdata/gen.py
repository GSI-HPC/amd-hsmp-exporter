# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 GSI Helmholtzzentrum für Schwerionenforschung GmbH
#
# Generates the synthesized fixture trees in this directory. See README.md
# for what these fixtures are and are not. Run from this directory:
#
#   python3 gen.py
import os
import shutil

HERE = os.path.dirname(os.path.abspath(__file__))


def emit(name, family, model, model_name, sockets, core_ids, smt, apic):
    """Write a fixture tree.

    sockets:  number of sockets
    core_ids: list of core_id values per socket (same for every socket)
    smt:      2 for SMT-enabled, 1 for SMT-off
    apic:     function (socket, core_index, thread) -> apicid

    CPU enumeration mirrors EPYC systems: all thread-0 siblings first
    (socket 0's cores, then socket 1's, ...), then all thread-1 siblings in
    the same order.
    """
    root = os.path.join(HERE, name)
    shutil.rmtree(root, ignore_errors=True)
    ncores = len(core_ids)
    cpus = []  # (cpu, socket, core_id, thread, apicid)
    cpu = 0
    for thread in range(smt):
        for socket in range(sockets):
            for ci in range(ncores):
                cpus.append((cpu, socket, core_ids[ci], thread,
                             apic(socket, ci, thread)))
                cpu += 1

    siblings = sockets * ncores * smt // sockets
    lines = []
    for (c, socket, core_id, thread, apicid) in cpus:
        lines.append(f"""processor\t: {c}
vendor_id\t: AuthenticAMD
cpu family\t: {family}
model\t\t: {model}
model name\t: {model_name}
stepping\t: 1
microcode\t: 0xa000000
cpu MHz\t\t: 2450.000
cache size\t: 1024 KB
physical id\t: {socket}
siblings\t: {siblings}
core id\t\t: {core_id}
cpu cores\t: {ncores}
apicid\t\t: {apicid}
initial apicid\t: {apicid}
fpu\t\t: yes
fpu_exception\t: yes
cpuid level\t: 16
wp\t\t: yes
flags\t\t: fpu vme de pse tsc msr pae mce cx8 apic sep mtrr pge mca cmov
bogomips\t: 4900.00
TLB size\t: 3584 4K pages
clflush size\t: 64
cache_alignment\t: 64
address sizes\t: 48 bits physical, 48 bits virtual
power management: ts ttp tm hwpstate cpb eff_freq_ro [13] [14]
""")
    procdir = os.path.join(root, "proc")
    os.makedirs(procdir)
    with open(os.path.join(procdir, "cpuinfo"), "w") as f:
        f.write("\n".join(lines))

    for (c, socket, core_id, thread, apicid) in cpus:
        tdir = os.path.join(root, "sys", "devices", "system", "cpu",
                            f"cpu{c}", "topology")
        os.makedirs(tdir)
        sibs = sorted(cc for (cc, s, ci, t, a) in cpus
                      if s == socket and ci == core_id)
        with open(os.path.join(tdir, "physical_package_id"), "w") as f:
            f.write(f"{socket}\n")
        with open(os.path.join(tdir, "core_id"), "w") as f:
            f.write(f"{core_id}\n")
        with open(os.path.join(tdir, "thread_siblings_list"), "w") as f:
            f.write(",".join(str(s) for s in sibs) + "\n")
    print(f"{name}: {len(cpus)} cpus")


# Genoa-like: dual socket, SMT on, sparse core_ids on both sockets, APIC ids
# offset per socket (stride 16) — Linux CPU number != APIC id for every CPU
# beyond socket 0 thread 0.
emit("genoa", 25, 17, "AMD EPYC 9654 96-Core Processor",
     sockets=2, core_ids=[0, 1, 4, 5], smt=2,
     apic=lambda s, ci, t: s * 16 + ci * 2 + t)

# Milan-like: single socket, SMT on — thread-1 siblings diverge from the
# identity mapping (cpu4 has APIC id 1).
emit("milan", 25, 1, "AMD EPYC 7513 32-Core Processor",
     sockets=1, core_ids=[0, 1, 2, 3], smt=2,
     apic=lambda s, ci, t: ci * 2 + t)

# Rome-like (family 0x17 — the no-/dev/hsmp case): single socket, SMT off,
# identity APIC mapping.
emit("rome", 23, 49, "AMD EPYC 7302 16-Core Processor",
     sockets=1, core_ids=[0, 1, 2, 3], smt=1,
     apic=lambda s, ci, t: ci)
