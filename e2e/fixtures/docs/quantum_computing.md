# Quantum Computing: Fundamentals and Applications

## Qubit and Superposition

A qubit is the fundamental unit of quantum information. Unlike classical bits that hold either 0 or 1, a qubit can exist in a superposition of both states simultaneously, described by a complex linear combination |ψ⟩ = α|0⟩ + β|1⟩ where |α|² + |β|² = 1. This quantum parallelism is what gives quantum computers their potential advantage.

Superposition collapses to a definite state upon measurement — a process known as wavefunction collapse. The probabilities of each outcome are determined by the squared amplitudes of the coefficients.

## Quantum Entanglement

Quantum entanglement occurs when two or more qubits become correlated in such a way that the quantum state of each cannot be described independently of the others. A maximally entangled Bell state is: |Φ⁺⟩ = (|00⟩ + |11⟩)/√2.

Entangled pairs can be used for quantum teleportation and superdense coding, enabling communication tasks impossible for classical systems.

## Decoherence and Error Correction

Decoherence is the primary practical obstacle in quantum computing. Qubits are extremely sensitive to environmental disturbances — thermal fluctuations, electromagnetic noise, and cosmic radiation — causing the quantum state to lose coherence and collapse prematurely.

The coherence time (T1 for energy relaxation, T2 for phase decoherence) determines how long computations can run before errors accumulate. Superconducting qubits typically achieve T1 of 100–500 microseconds.

Quantum error correction (QEC) encodes logical qubits across multiple physical qubits. The surface code is the most promising QEC scheme, requiring approximately 1000 physical qubits per logical qubit at current error rates.

## Quantum Gates

Quantum gates are unitary transformations on qubit states. The Hadamard gate puts a qubit in equal superposition: H|0⟩ = (|0⟩ + |1⟩)/√2. The CNOT gate entangles two qubits. The T-gate and Clifford gates form a universal gate set.

Spin-orbit coupling in semiconductor qubits enables electrically controlled spin manipulation without magnetic fields, reducing cross-talk in dense qubit arrays.

## NISQ Era and Quantum Advantage

The Noisy Intermediate-Scale Quantum (NISQ) era describes today's quantum processors with 50–1000 noisy qubits. Variational Quantum Eigensolver (VQE) and Quantum Approximate Optimization Algorithm (QAOA) are NISQ-native algorithms designed to tolerate noise.

Shor's algorithm for integer factorization achieves exponential speedup over classical algorithms, threatening RSA encryption. Grover's search algorithm provides quadratic speedup. Quantum annealing (used by D-Wave systems) solves optimization problems via adiabatic evolution.

## Physical Implementations

- **Superconducting qubits**: IBM, Google, Rigetti. Operate at 15 millikelvin.
- **Trapped ion qubits**: IonQ, Quantinuum. Highest gate fidelity (~99.9%).
- **Photonic qubits**: PsiQuantum. Room temperature operation.
- **Topological qubits**: Microsoft. Majorana fermion braiding for inherent error protection.

Gate fidelity and the quantum volume metric characterize system performance. Google claimed quantum supremacy in 2019 with 53 qubits completing a specific sampling task in 200 seconds vs 10,000 years classically.
