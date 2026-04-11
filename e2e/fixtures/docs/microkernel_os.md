# Microkernel Operating System Design

## Monolithic vs. Microkernel Architecture

Traditional monolithic kernels (Linux, BSD) run all OS services — file system, network stack, device drivers, memory management — in kernel space. Any kernel bug can crash or compromise the entire system. Linux kernel code: ~30 million lines.

Microkernel design minimizes the trusted computing base (TCB) by reducing kernel code to the absolute minimum: address space management, thread scheduling, and inter-process communication (IPC). All other services run as unprivileged user-space servers.

## The L4 Microkernel Family

Jochen Liedtke designed L4 in 1993, demonstrating that IPC path length (the critical performance metric) could be reduced to ~50 cycles on modern processors — 10–100x faster than Mach microkernel. This disproved the prevailing belief that microkernels were inherently slow.

The L4 microkernel family includes:
- **L4/x86**: Liedtke's original implementation
- **L4Ka::Pistachio**: Research-oriented, cross-platform
- **OKL4**: Commercial derivative (used in 2 billion+ devices via mobile baseband)
- **Fiasco.OC**: Dresden TU, real-time capable
- **seL4**: NICTA/Data61, formally verified

## seL4: Formal Verification

seL4 is the world's first fully formally verified OS kernel. The verification proof (200,000 lines of Isabelle/HOL) establishes:

1. **Functional correctness**: Implementation matches abstract specification
2. **Memory safety**: No buffer overflows, no dangling pointers
3. **Information flow security**: Non-interference between domains
4. **Integrity**: User processes cannot corrupt each other's state
5. **Confidentiality**: No information leaks between protection domains

The proof covers 8,700 lines of C and assembler. seL4 is used in aerospace (DARPA HACMS drone), medical devices, and automotive systems requiring formal safety certification.

## Capability-Based Access Control

seL4 uses capability-based security: every kernel object (memory region, thread, IPC endpoint) is accessed via an unforgeable capability (a permission token). Capabilities flow through the system explicitly — no ambient authority. Revocation is supported by deleting the capability from the source.

The capability space (CSpace) is a tree of CNodes (arrays of capability slots). Thread Control Blocks (TCBs), VSpaces (virtual address spaces), and Endpoints are all first-class kernel objects accessed only through capabilities.

## IPC and Message Passing

IPC is the central communication mechanism. seL4 IPC transfers data via registers (fast path: 4 machine words in ~300 cycles on ARM) or shared memory for larger buffers.

Synchronous IPC (rendezvous): sender blocks until receiver accepts. Asynchronous notifications: signal delivery with minimal overhead. Reply capabilities enable client-server patterns without trust escalation.

## Exokernel and Library OS

The exokernel approach (MIT, 1994) takes minimality further: the kernel only multiplexes hardware, exposing it directly to applications. Library OSes built atop the exokernel (libOS) implement OS abstractions in user space, customized per application.

Drawbridge (Microsoft Research) and Unikernels (MirageOS, Unikraft) revive library OS concepts for cloud computing — running applications with a minimal, application-specific OS layer.

## User-Space Drivers and Fault Isolation

Microkernels run device drivers as user-space servers in isolated protection domains. A buggy NIC driver crashes its own process; the kernel restarts it without affecting other processes. MINIX 3 demonstrated this with live driver replacement.

Comparison: Linux monolithic achieves higher raw performance but crashes due to driver bugs. seL4 provides mathematically verified isolation at ~5% performance overhead versus Linux for typical workloads.
