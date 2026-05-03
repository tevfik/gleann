# Transformer Architecture

The Transformer is a neural network architecture introduced in 2017 by Vaswani et al.
in the paper "Attention Is All You Need". It replaced recurrent layers with a pure
self-attention mechanism, enabling parallel training over long sequences.

## Core Components

### Self-Attention
Every token attends to every other token in the sequence. The mechanism computes
three projections — Query (Q), Key (K), and Value (V) — and produces a weighted
sum of values where weights are derived from the scaled dot-product of Q and K.

### Multi-Head Attention
Several attention heads run in parallel, each learning a different relational
pattern. Their outputs are concatenated and projected back to the model dimension.

### Positional Encoding
Because the architecture is order-agnostic, positional encodings (sinusoidal or
learned) are added to token embeddings to inject sequence order information.

### Feed-Forward Network
A position-wise two-layer MLP applied independently to each token after attention.

## Encoder vs Decoder
- Encoder-only models (BERT) excel at understanding tasks.
- Decoder-only models (GPT) excel at text generation.
- Encoder-decoder models (T5) handle sequence-to-sequence tasks.

## Why Transformers Won
- Parallelizable training (no sequential RNN dependency)
- Long-range dependencies via direct attention links
- Scales predictably with parameters and data (scaling laws)
