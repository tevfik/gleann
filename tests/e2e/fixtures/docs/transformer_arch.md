# Transformer Architecture: Attention Is All You Need

## Overview

The Transformer architecture, introduced by Vaswani et al. (2017) in "Attention Is All You Need", revolutionized sequence modeling by replacing recurrent networks with attention mechanisms entirely. It enables parallel training and captures long-range dependencies more effectively than LSTMs or GRUs.

The original Transformer consists of an encoder-decoder architecture. The encoder processes the input sequence; the decoder generates the output sequence autoregressively.

## Multi-Head Self-Attention

The core mechanism is scaled dot-product attention:
```
Attention(Q, K, V) = softmax(QKᵀ / √dₖ) · V
```

Q (queries), K (keys), and V (values) are linear projections of the input. The scaling factor 1/√dₖ prevents dot products from growing large in high dimensions, which would push softmax into regions with small gradients.

Multi-head attention concatenates h attention heads operating in parallel:
```
MultiHead(Q, K, V) = Concat(head₁,...,headₕ) · Wᴼ
```
Each head learns different aspects of the attention pattern — some attend to syntactic relations, others to semantic co-occurrence.

## Positional Encoding

Since Transformers lack recurrence, positional information must be injected explicitly. Sinusoidal positional encoding assigns each position a unique pattern:
```
PE(pos, 2i)   = sin(pos / 10000^(2i/d_model))
PE(pos, 2i+1) = cos(pos / 10000^(2i/d_model))
```

Modern architectures replace fixed sinusoidal encodings with learned positional embeddings (BERT) or Rotary Position Embeddings (RoPE, used in Llama and Mistral) which generalize better to unseen sequence lengths.

## Layer Normalization and Feed-Forward Sublayer

Each Transformer block consists of:
1. Multi-head self-attention sublayer
2. Position-wise feed-forward sublayer (FFN): FFN(x) = max(0, xW₁ + b₁)W₂ + b₂
3. Residual connections around each sublayer
4. Layer normalization

Pre-norm Transformers apply layer normalization before each sublayer (before residual), improving training stability. Post-norm (original) applies normalization after. Modern LLMs (GPT-3, Llama) use pre-norm with RMSNorm.

## Causal Masking and Autoregressive Generation

For language modeling, causal masking (lower-triangular attention mask) prevents each position from attending to future positions. This enables autoregressive generation: tokens are predicted from left to right.

Key-query-value projection matrices (Wᴷ, Wᵠ, Wⱽ) are the learnable parameters of attention. Grouped Query Attention (GQA) uses fewer key/value heads than query heads, reducing KV cache memory during inference.

## BERT and Masked Language Modeling

BERT (Bidirectional Encoder Representations from Transformers) uses encoder-only architecture with two pretraining tasks:
1. **Masked Language Model (MLM)**: Predict randomly masked tokens using bidirectional context
2. **Next Sentence Prediction (NSP)**: Predict if two sentences are consecutive

BERT's bidirectional attention gives it superior performance on NLU tasks. It cannot do generation. GPT uses decoder-only architecture for generative tasks.

## Scaling Laws and Modern LLMs

Chinchilla scaling laws (Hoffmann et al. 2022) show optimal model size given compute budget: for N parameters, train on ~20N tokens. GPT-4 has ~1.8T parameters in a mixture-of-experts (MoE) architecture.

Flash Attention optimizes attention computation via IO-aware tiling, reducing memory from O(n²) to O(n) and speeding up training by 2–4x. Sparse attention and sliding window attention (Longformer, Mistral) extend context windows to 128K+ tokens.
