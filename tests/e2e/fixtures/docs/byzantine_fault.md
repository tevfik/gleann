# Byzantine Fault Tolerance in Distributed Systems

## The Byzantine Generals Problem

The Byzantine Generals Problem, formulated by Lamport, Shostak, and Pease (1982), describes the challenge of reaching consensus among distributed components when some may be faulty or actively malicious. The scenario: Byzantine generals must coordinate an attack, but some may be traitors sending contradictory messages.

A system with f Byzantine faulty nodes requires at least 3f+1 total nodes to guarantee correctness. This is a fundamental lower bound on Byzantine fault tolerant consensus.

## Paxos Algorithm

Paxos, invented by Leslie Lamport, is the foundational consensus algorithm for crash fault tolerant systems. Multi-Paxos handles sequences of values using a single stable leader to minimize message rounds. Paxos requires 2f+1 nodes to tolerate f crash failures.

Phases: Prepare/Promise (phase 1) establishes a ballot number and discovers any previously accepted values. Accept/Accepted (phase 2) proposes and commits a value. The leader coordinates both phases.

## PBFT: Practical Byzantine Fault Tolerance

Castro and Liskov introduced PBFT in 1999, the first practical BFT protocol with O(n²) message complexity. PBFT operates in three phases per request: pre-prepare, prepare, and commit. A quorum certificate requires 2f+1 matching messages.

PBFT requires a view change protocol when the primary becomes faulty. The view-change protocol is the most complex component, requiring 3f+1 replicas total.

## HotStuff and Tendermint

Modern BFT protocols reduce communication complexity. HotStuff achieves linear message complexity O(n) per phase using threshold signatures and a pipelined three-phase structure. The chained HotStuff variant enables pipelining across consecutive blocks.

Tendermint uses a propose-prevote-precommit round structure with a rotating leader election. Its safety is guaranteed regardless of network asynchrony (Byzantine fault tolerance under partial synchrony via GST assumption).

## Leader Election and Quorum Systems

Leader election in the presence of Byzantine faults requires careful design. Raft's randomized timeouts work for crash-tolerant systems. For Byzantine settings, PBFT uses view-change protocols. HotStuff uses a timeout QC (Quorum Certificate) to advance rounds.

A quorum system assigns overlapping subsets of nodes such that any two quorums share at least one honest node. For BFT quorums, majority is insufficient — a 2f+1 supermajority is required.

## Blockchain Consensus

Bitcoin uses Nakamoto consensus (Proof of Work) which is probabilistic and Byzantine fault tolerant under honest majority assumption. Ethereum's Gasper protocol combines LMD-GHOST fork choice with Casper FFG finality gadget.

Hotstuff-based state machine replication underlies LibraBFT (Diem), DiemBFT v4, and Jolteon. These achieve deterministic finality in ~2 round trips under optimistic conditions.

## Applied Metrics

- PBFT throughput: ~10,000 TPS for small committees (f=1, n=4)
- HotStuff: ~100,000 TPS with threshold BLS signatures
- Tendermint: ~10,000 TPS with 100ms block time
- Communication complexity: PBFT O(n²), HotStuff O(n), both per consensus round
