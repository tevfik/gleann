#!/usr/bin/env bash
# Bash completion for gleann CLI
# Install: source this file or copy to /etc/bash_completion.d/gleann

_gleann_indexes() {
    local index_dir="${GLEANN_INDEX_DIR:-$HOME/.gleann/indexes}"
    if [[ -d "$index_dir" ]]; then
        find "$index_dir" -maxdepth 1 -type d -exec basename {} \; 2>/dev/null | grep -v "^indexes$"
    fi
}

_gleann() {
    local cur prev words cword
    _init_completion || return

    local commands="index search ask serve graph chat mcp tui setup config version help"
    
    # Command-specific flags
    local index_flags="--path --model --provider --backend --batch-size --concurrency --chunk-size --chunk-overlap --extensions --ignore --ollama-host --anthropic-api-key --openai-api-key --json"
    local search_flags="--top-k --metric --docs --rerank --rerank-model --no-cache --no-limit --json"
    local ask_flags="--interactive --continue --continue-last --title --role --format --raw --quiet --word-wrap --no-cache --no-limit --rerank --rerank-model --llm-model --llm-provider"
    local chat_flags="--list --show --show-last --delete --delete-older-than --llm-model --llm-provider --rerank --rerank-model"
    local serve_flags="--port --host"
    local graph_flags="--show --stats --export --format"
    local config_flags="--get --set --unset --list"

    # Complete commands
    if [[ $cword -eq 1 ]]; then
        COMPREPLY=($(compgen -W "$commands" -- "$cur"))
        return
    fi

    local cmd="${words[1]}"

    case "$cmd" in
        index)
            case "$prev" in
                --path)
                    _filedir -d
                    return
                    ;;
                --backend)
                    COMPREPLY=($(compgen -W "hnsw faiss" -- "$cur"))
                    return
                    ;;
                --provider)
                    COMPREPLY=($(compgen -W "ollama openai anthropic llamacpp" -- "$cur"))
                    return
                    ;;
                --extensions)
                    COMPREPLY=($(compgen -W ".py .js .go .rs .java .cpp .c .ts .tsx .jsx" -- "$cur"))
                    return
                    ;;
                *)
                    if [[ "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "$index_flags" -- "$cur"))
                    else
                        # First non-option arg is index name
                        return
                    fi
                    ;;
            esac
            ;;
        search)
            case "$prev" in
                --metric)
                    COMPREPLY=($(compgen -W "cosine dot ip l2" -- "$cur"))
                    return
                    ;;
                *)
                    if [[ "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "$search_flags" -- "$cur"))
                    elif [[ $cword -eq 2 ]]; then
                        # Complete index name
                        COMPREPLY=($(compgen -W "$(_gleann_indexes)" -- "$cur"))
                    fi
                    ;;
            esac
            ;;
        ask)
            case "$prev" in
                --role)
                    COMPREPLY=($(compgen -W "code shell explain architect debug test document" -- "$cur"))
                    return
                    ;;
                --format)
                    COMPREPLY=($(compgen -W "json markdown raw" -- "$cur"))
                    return
                    ;;
                --llm-provider)
                    COMPREPLY=($(compgen -W "ollama openai anthropic llamacpp" -- "$cur"))
                    return
                    ;;
                --continue)
                    # Would need to parse conversation IDs from ~/.gleann/conversations/
                    return
                    ;;
                *)
                    if [[ "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "$ask_flags" -- "$cur"))
                    elif [[ $cword -eq 2 ]] && [[ "$cur" != -* ]]; then
                        # First arg can be index name (optional)
                        COMPREPLY=($(compgen -W "$(_gleann_indexes)" -- "$cur"))
                    fi
                    ;;
            esac
            ;;
        chat)
            case "$prev" in
                --show|--delete)
                    # Would need conversation IDs
                    return
                    ;;
                --llm-provider)
                    COMPREPLY=($(compgen -W "ollama openai anthropic llamacpp" -- "$cur"))
                    return
                    ;;
                *)
                    if [[ "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "$chat_flags" -- "$cur"))
                    elif [[ $cword -eq 2 ]]; then
                        # Complete index name (optional)
                        COMPREPLY=($(compgen -W "$(_gleann_indexes)" -- "$cur"))
                    fi
                    ;;
            esac
            ;;
        serve)
            if [[ "$cur" == -* ]]; then
                COMPREPLY=($(compgen -W "$serve_flags" -- "$cur"))
            fi
            ;;
        graph)
            case "$prev" in
                --format)
                    COMPREPLY=($(compgen -W "dot json" -- "$cur"))
                    return
                    ;;
                *)
                    if [[ "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "$graph_flags" -- "$cur"))
                    elif [[ $cword -eq 2 ]]; then
                        # Complete index name
                        COMPREPLY=($(compgen -W "$(_gleann_indexes)" -- "$cur"))
                    fi
                    ;;
            esac
            ;;
        config)
            case "$prev" in
                --get|--set|--unset)
                    COMPREPLY=($(compgen -W "embedding.provider embedding.model llm.provider llm.model ollama.host" -- "$cur"))
                    return
                    ;;
                *)
                    if [[ "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "$config_flags" -- "$cur"))
                    fi
                    ;;
            esac
            ;;
        info|delete)
            # Complete index name
            if [[ $cword -eq 2 ]]; then
                COMPREPLY=($(compgen -W "$(_gleann_indexes)" -- "$cur"))
            fi
            ;;
    esac
}

complete -F _gleann gleann
