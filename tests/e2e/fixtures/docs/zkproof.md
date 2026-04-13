# Zero-Knowledge Proofs: Theory and Applications

## What is a Zero-Knowledge Proof?

A zero-knowledge proof (ZKP) is a cryptographic protocol allowing a prover to convince a verifier that a statement is true without revealing any information beyond the truth of the statement. Originally formulated by Goldwasser, Micali, and Rackoff (1985).

Three properties define a ZKP system:
1. **Completeness**: An honest prover can always convince an honest verifier
2. **Soundness**: A cheating prover cannot convince the verifier of a false statement (with negligible probability)
3. **Zero-knowledge**: The verifier learns nothing beyond the validity of the statement

## zk-SNARKs: Succinct Non-Interactive Arguments of Knowledge

zk-SNARKs (Zero-Knowledge Succinct Non-Interactive ARguments of Knowledge) enable non-interactive proofs with constant-size proofs (~200 bytes) verifiable in milliseconds, regardless of computation size.

The Groth16 scheme (2016) is the most widely deployed SNARK. It uses a bilinear pairing on elliptic curves (typically BN254 or BLS12-381) to construct a proof system with just 2 group elements plus a scalar field element in the proof.

**Arithmetic Circuit Representation**: Computations are encoded as rank-1 constraint systems (R1CS), then converted to quadratic arithmetic programs (QAP) via FFT over the polynomial domain.

## Trusted Setup and Structured Reference String

Groth16 requires a trusted setup ceremony to generate a structured reference string (SRS). The toxic waste (certain trapdoor values) must be securely destroyed; if known, a prover could forge proofs. Powers of Tau ceremonies (e.g., Zcash's "ceremony") use multi-party computation to make the setup trustworthy.

Newer schemes like PLONK use a universal SRS that can be reused across circuits. STARKs (Scalable Transparent ARguments of Knowledge) eliminate the trusted setup entirely using hash functions, at the cost of larger proof sizes (~100KB vs 200B).

## Elliptic Curve Pairings

zk-SNARKs rely on bilinear pairings: maps e: G₁ × G₂ → Gₜ where G₁, G₂ are elliptic curve groups and Gₜ is a multiplicative group. The pairing must satisfy e(aP, bQ) = e(P, Q)^(ab).

BLS12-381 is the preferred curve for modern ZK systems: 381-bit coordinates, 128-bit security, efficient pairing computation. The embedding degree of 12 provides a good security/efficiency tradeoff.

## Pedersen Commitments and Polynomial Commitments

Pedersen commitments are perfectly hiding, computationally binding commitment schemes: Commit(v, r) = vG + rH. They enable range proofs (proving v is in a specific range without revealing v).

Kate-Zaverucha-Goldberg (KZG) polynomial commitments allow a prover to commit to a polynomial and later prove evaluations. KZG uses a pairing-based construction and underpins PLONK and many layer-2 protocols.

## Recursive Proof Composition

Recursive SNARKs allow a proof to verify another proof inside a circuit, enabling proof aggregation and incremental verifiable computation (IVC). Nova (from Microsoft Research) uses folding schemes for efficient recursive proof composition without pairings.

zkEVM projects (zkSync Era, Starkware, Polygon zkEVM) use recursive SNARKs to prove EVM execution, enabling Ethereum Layer-2 scaling with on-chain proof verification. Proof generation takes 10–100 seconds; verification costs ~300,000 Ethereum gas.

## Applications

- **Private transactions**: Zcash uses Groth16 for shielded transactions
- **zkEVM rollups**: L2 scaling for Ethereum  
- **Identity verification**: Prove age without revealing birthdate
- **Machine learning inference**: ZK-ML proves correct model inference
- **Cross-chain bridges**: Prove state inclusion to foreign chains
