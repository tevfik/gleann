use anyhow::{Error as E, Result};
use candle_core::{Device, Tensor};
use candle_nn::VarBuilder;
use candle_transformers::models::bert::{BertModel, Config};
use hf_hub::{api::sync::Api, Repo, RepoType};
use std::ffi::CStr;
use libc::c_char;
use tokenizers::{PaddingParams, Tokenizer};
use std::slice;

#[repr(C)]
pub struct GleannEmbeddingResult {
    pub data: *const f32,
    pub rows: usize,
    pub cols: usize,
}

pub struct NativeComputer {
    model: BertModel,
    tokenizer: Tokenizer,
    device: Device,
}

impl NativeComputer {
    pub fn new(model_id: &str, revision: &str) -> Result<Self> {
        let device = Device::Cpu;
        let api = Api::new()?;
        let repo = api.repo(Repo::with_revision(
            model_id.to_string(),
            RepoType::Model,
            revision.to_string(),
        ));
        
        let tokenizer_filename = repo.get("tokenizer.json")?;
        let config_filename = repo.get("config.json")?;
        let weights_filename = repo.get("model.safetensors")?;

        let config = std::fs::read_to_string(config_filename)?;
        let config: Config = serde_json::from_str(&config)?;
        
        let mut tokenizer = Tokenizer::from_file(tokenizer_filename).map_err(|e| E::msg(e.to_string()))?;
        let pp = PaddingParams {
            strategy: tokenizers::PaddingStrategy::BatchLongest,
            ..Default::default()
        };
        tokenizer.with_padding(Some(pp));

        let vb = unsafe { VarBuilder::from_mmaped_safetensors(&[weights_filename], candle_core::DType::F32, &device)? };
        let model = BertModel::load(vb, &config)?;

        Ok(Self {
            model,
            tokenizer,
            device,
        })
    }

    pub fn compute(&self, texts: Vec<&str>) -> Result<Vec<f32>> {
        let tokens = self.tokenizer.encode_batch(texts.clone(), true).map_err(|e| E::msg(e.to_string()))?;
        let token_ids: Vec<Vec<u32>> = tokens.iter().map(|t| t.get_ids().to_vec()).collect();
        let token_type_ids: Vec<Vec<u32>> = tokens.iter().map(|t| t.get_type_ids().to_vec()).collect();
        
        // For attention mask, assuming 1 for real tokens and 0 for padding. 
        // Tokenizer automatically pads to BatchLongest.
        let mut attention_mask = Vec::new();
        for t in &tokens {
            let mask: Vec<u32> = t.get_attention_mask().iter().map(|&m| m).collect();
            attention_mask.push(mask);
        }

        let n_sentences = texts.len();
        let seq_len = token_ids[0].len();

        let token_ids = token_ids.into_iter().flatten().collect::<Vec<_>>();
        let token_type_ids = token_type_ids.into_iter().flatten().collect::<Vec<_>>();
        let attention_mask = attention_mask.into_iter().flatten().collect::<Vec<_>>();

        let token_ids = Tensor::from_vec(token_ids, (n_sentences, seq_len), &self.device)?;
        let token_type_ids = Tensor::from_vec(token_type_ids, (n_sentences, seq_len), &self.device)?;
        // BertModel in candle expects attention_mask as Tensor
        // But some versions don't take it directly in forward(), they might need custom handling.
        // Wait, candle_transformers::models::bert::BertModel forward signature usually doesn't take attention_mask.
        // Let's check signature. Actually, we can use the hidden state output directly.
        let embeddings = self.model.forward(&token_ids, &token_type_ids, None)?;
        
        // Apply mean pooling
        let (_b_size, _seq_len, _hidden_size) = embeddings.dims3()?;
        
        // Convert attention mask to tensor for pooling
        let mask = Tensor::from_vec(attention_mask, (n_sentences, seq_len), &self.device)?.to_dtype(candle_core::DType::F32)?;
        
        // embeddings: [batch, seq_len, hidden]
        // mask: [batch, seq_len] -> [batch, seq_len, 1]
        let mask = mask.unsqueeze(2)?.broadcast_as(embeddings.shape())?;
        
        let masked_embeddings = embeddings.broadcast_mul(&mask)?;
        let sum_embeddings = masked_embeddings.sum(1)?;
        // sum_mask: [batch, 1, 1]
        let sum_mask = mask.sum(1)?.clamp(1e-9, f64::MAX)?;
        
        let pooled = (sum_embeddings / sum_mask)?;
        
        // L2 normalize
        // We do it manually since `sqr` and `sum` are available.
        let squared = pooled.sqr()?;
        let norm = squared.sum_keepdim(1)?.sqrt()?;
        let normalized = pooled.broadcast_div(&norm)?;

        let result = normalized.flatten_all()?.to_vec1::<f32>()?;
        Ok(result)
    }
}

// ── C-ABI Export ─────────────────────────────────────────────────────────────

#[no_mangle]
pub extern "C" fn gleann_native_init() -> *mut NativeComputer {
    match NativeComputer::new("sentence-transformers/all-MiniLM-L6-v2", "refs/pr/21") {
        Ok(c) => Box::into_raw(Box::new(c)),
        Err(e) => {
            eprintln!("Failed to init NativeComputer: {}", e);
            std::ptr::null_mut()
        }
    }
}

#[no_mangle]
pub extern "C" fn gleann_native_free(ptr: *mut NativeComputer) {
    if !ptr.is_null() {
        unsafe {
            drop(Box::from_raw(ptr));
        }
    }
}

#[no_mangle]
pub extern "C" fn gleann_embed_texts(
    computer: *mut NativeComputer,
    texts_ptr: *const *const c_char,
    count: usize,
) -> *mut GleannEmbeddingResult {
    if computer.is_null() || texts_ptr.is_null() || count == 0 {
        return std::ptr::null_mut();
    }

    let comp = unsafe { &*computer };
    let c_strs = unsafe { slice::from_raw_parts(texts_ptr, count) };
    
    let mut rust_strs = Vec::with_capacity(count);
    for &c_str in c_strs {
        if c_str.is_null() {
            return std::ptr::null_mut();
        }
        let s = unsafe { CStr::from_ptr(c_str) };
        if let Ok(st) = s.to_str() {
            rust_strs.push(st);
        } else {
            return std::ptr::null_mut();
        }
    }

    let vec_result = match comp.compute(rust_strs) {
        Ok(v) => v,
        Err(e) => {
            eprintln!("Compute error: {}", e);
            return std::ptr::null_mut();
        }
    };

    let cols = 384; // all-MiniLM-L6-v2 size
    let rows = vec_result.len() / cols;

    let boxed_slice = vec_result.into_boxed_slice();
    let data = boxed_slice.as_ptr();
    std::mem::forget(boxed_slice);

    let result = Box::new(GleannEmbeddingResult {
        data,
        rows,
        cols,
    });

    Box::into_raw(result)
}

#[no_mangle]
pub extern "C" fn gleann_free_embedding_result(res: *mut GleannEmbeddingResult) {
    if !res.is_null() {
        unsafe {
            let result = Box::from_raw(res);
            let s = slice::from_raw_parts_mut(result.data as *mut f32, result.rows * result.cols);
            drop(Box::from_raw(s));
        }
    }
}
