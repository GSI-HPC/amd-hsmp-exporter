# Topology test fixtures

The `genoa/`, `milan/` and `rome/` trees are synthesized fixtures
produced by `gen.py`. They mirror the structural properties of real EPYC
nodes that the topology code must get right: the CPU enumeration order
(all thread-0 siblings first, then all thread-1 siblings), per-socket APIC
id offsets that make Linux CPU numbers diverge from APIC ids, sparse
`core_id` values, and the family 0x17 (Rome) no-SMT case. The core count
is cut down to four per socket so the fixtures stay reviewable.

They are deliberately not byte captures of real machines. When capturing
fixtures from real Genoa/Milan/Rome nodes (recommended before relying on a
new kernel or BIOS generation), place them here as additional directories
(e.g. `genoa-captured/`) with the same layout:

```
<name>/proc/cpuinfo
<name>/sys/devices/system/cpu/cpu<N>/topology/{physical_package_id,core_id,thread_siblings_list}
```

and add a case to `topology_test.go`. Capture with:

```
mkdir -p fixture/proc
cp /proc/cpuinfo fixture/proc/
for d in /sys/devices/system/cpu/cpu[0-9]*/topology; do
  out="fixture/sys${d#/sys}"
  mkdir -p "$out"
  cp "$d"/physical_package_id "$d"/core_id "$d"/thread_siblings_list "$out"/
done
```
