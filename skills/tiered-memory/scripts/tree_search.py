#!/usr/bin/env python3
"""
LLM-Powered Tree Search for Tiered Memory

Reasons through the memory tree index to find relevant categories,
instead of simple keyword matching.

Modes:
  - keyword: Simple keyword overlap (fast, no LLM)
  - llm: LLM reasons about relevance (accurate, requires LLM)

Usage:
  tree_search.py --query "..." --tree-file path --mode keyword
  tree_search.py --query "..." --tree-file path --mode llm --llm-prompt-file prompt.txt
  tree_search.py --query "..." --tree-file path --mode llm --llm-endpoint http://... --api-key XXX
"""

import argparse
import json
import os
import re
import sys
import urllib.request
import ssl

# 创建 SSL 上下文（处理证书问题）
ssl_context = ssl.create_default_context()
ssl_context.check_hostname = False
ssl_context.verify_mode = ssl.CERT_NONE

# ─── Keyword Search (Fast, No LLM) ───

def search_keyword(tree, query, top_k=5):
    """
    Keyword-based search: find nodes by term overlap.
    """
    query_words = set(query.lower().split())
    results = []
    
    for path, node in tree.items():
        if path == 'root':
            continue
        
        if isinstance(node, dict):
            desc = node.get('desc', '')
        elif isinstance(node, str):
            desc = node
        else:
            continue
        
        path_words = set(re.split(r'[/_\-\s]', path.lower()))
        desc_words = set(desc.lower().split())
        all_words = path_words | desc_words
        
        overlap = len(query_words & all_words)
        
        if overlap > 0:
            score = overlap / max(len(query_words), 1)
            
            warm_count = node.get('warm_count', 0) if isinstance(node, dict) else 0
            cold_count = node.get('cold_count', 0) if isinstance(node, dict) else 0
            
            importance_boost = min(1.0 + (warm_count * 0.1) + (cold_count * 0.01), 2.0)
            score *= importance_boost
            
            import time
            last_access = node.get('last_access', 0) if isinstance(node, dict) else 0
            if last_access > 0:
                age_days = (time.time() - last_access) / 86400
                recency_boost = 1.0 + (0.5 if age_days < 7 else 0.2 if age_days < 30 else 0)
                score *= recency_boost
            
            reason = f"Keywords: {', '.join(query_words & all_words)}"
            
            results.append({
                'path': path,
                'relevance': round(score, 3),
                'reason': reason,
                'warm_count': warm_count,
                'cold_count': cold_count
            })
    
    results.sort(key=lambda x: x['relevance'], reverse=True)
    return results[:top_k]


# ─── LLM Search (Accurate, Needs LLM) ───

def build_llm_prompt(tree, query):
    """Build prompt for LLM to reason about tree relevance."""
    tree_simple = []
    for path, node in tree.items():
        if path == 'root':
            continue
        tree_simple.append({
            'path': path,
            'desc': node.get('desc', '') if isinstance(node, dict) else '',
            'warm': node.get('warm_count', 0) if isinstance(node, dict) else 0,
            'cold': node.get('cold_count', 0) if isinstance(node, dict) else 0
        })
    
    tree_simple.sort(key=lambda x: x['warm'] + x['cold'], reverse=True)
    
    tree_lines = []
    for item in tree_simple[:50]:
        tree_lines.append(
            f"  {item['path']} — {item['desc']} (warm:{item['warm']}, cold:{item['cold']})"
        )
    
    tree_text = '\n'.join(tree_lines)
    
    prompt = f"""You are a memory retrieval system. Given a memory tree index and a user query, identify which categories are relevant.

Memory Tree Index:
{tree_text}

User Query: {query}

Return a JSON array of relevant categories. Example:
[{{"path": "preferences", "relevance": 0.9, "reason": "match"}}]

Rules:
- relevance: 0.0-1.0
- Return 1-5 categories max
- If nothing matches, return []

Output JSON only, no other text:"""

    return prompt


def search_llm(tree, query, llm_endpoint=None, api_key=None, model=None, prompt_file=None, top_k=5):
    """
    LLM-powered search: uses LLM to reason about tree relevance.
    
    Args:
        tree (dict): Memory tree index
        query (str): Search query
        llm_endpoint (str): HTTP endpoint for LLM completion
        api_key (str): API key for authentication
        model (str): Model name (for OpenAI-compatible APIs)
        prompt_file (str): Write prompt to file instead of calling LLM
        top_k (int): Max results
    
    Returns:
        list: [{path, relevance, reason}]
    """
    prompt = build_llm_prompt(tree, query)
    
    # If prompt_file, just write and exit
    if prompt_file:
        with open(prompt_file, 'w') as f:
            f.write(prompt)
        print(f"Prompt written to {prompt_file}", file=sys.stderr)
        sys.exit(0)
    
    # Call LLM endpoint
    if not llm_endpoint:
        print("Error: --llm-endpoint required for LLM mode (or use --llm-prompt-file)", file=sys.stderr)
        print("Falling back to keyword search", file=sys.stderr)
        return search_keyword(tree, query, top_k)
    
    # Detect OpenAI-compatible format (contains /v1/ or openai or api.z.ai)
    use_openai_format = any(x in llm_endpoint for x in ['/v1/', 'openai', 'integrate.api', 'api.z.ai'])
    
    headers = {'Content-Type': 'application/json'}
    if api_key:
        headers['Authorization'] = f'Bearer {api_key}'
    
    if use_openai_format:
        # OpenAI-compatible chat completions format
        payload = {
            'messages': [{'role': 'user', 'content': prompt}],
            'max_tokens': 500,
            'temperature': 0.3
        }
        if model:
            payload['model'] = model
    else:
        # Simple prompt format
        payload = {
            'prompt': prompt,
            'max_tokens': 500,
            'temperature': 0.3,
            'stop': ['\n\n']
        }
    
    try:
        req = urllib.request.Request(
            llm_endpoint,
            data=json.dumps(payload).encode(),
            headers=headers
        )
        
        with urllib.request.urlopen(req, timeout=30, context=ssl_context) as resp:
            result = json.load(resp)
            
            # Extract response text from various formats
            response_text = ''
            if 'choices' in result and result['choices']:
                # OpenAI format
                choice = result['choices'][0]
                if isinstance(choice.get('message'), dict):
                    msg = choice['message']
                    # ZAI API 可能把内容放在 content 或 reasoning_content
                    response_text = msg.get('content', '') or msg.get('reasoning_content', '')
                elif isinstance(choice.get('text'), str):
                    response_text = choice['text']
            elif 'text' in result:
                response_text = result['text']
            elif 'response' in result:
                response_text = result['response']
            elif 'content' in result:
                response_text = result['content']
            
            # Debug output
            if not response_text:
                print(f"Debug: Empty response. Result: {json.dumps(result, ensure_ascii=False)[:500]}", file=sys.stderr)
            print(f"Debug: response_text = {response_text[:200] if response_text else 'EMPTY'}", file=sys.stderr)
            
            if '```json' in response_text:
                response_text = response_text.split('```json')[1].split('```')[0]
            elif '```' in response_text:
                response_text = response_text.split('```')[1].split('```')[0]
            
            response_text = response_text.strip()
            if not response_text.startswith('['):
                start = response_text.find('[')
                end = response_text.rfind(']')
                if start >= 0 and end > start:
                    response_text = response_text[start:end+1]
            
            results = json.loads(response_text)
            
            # Validate results
            validated = []
            for item in results:
                if item.get('path') in tree:
                    validated.append(item)
            
            return validated[:top_k]
    
    except Exception as e:
        print(f"LLM search failed: {e}", file=sys.stderr)
        print("Falling back to keyword search", file=sys.stderr)
        return search_keyword(tree, query, top_k)


# ─── CLI ───

def main():
    parser = argparse.ArgumentParser(
        description='Tree-based memory search with LLM reasoning',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  # Keyword search (fast, no LLM)
  tree_search.py --query "garden project" --tree-file memory-tree.json --mode keyword

  # LLM search with API key (OpenAI-compatible)
  tree_search.py --query "what did we decide?" \\
    --tree-file memory-tree.json --mode llm \\
    --llm-endpoint https://api.z.ai/api/coding/paas/v4/chat/completions \\
    --api-key $ZAI_API_KEY --model glm-4.7-flash

  # Generate prompt for external LLM
  tree_search.py --query "BSC integration" --tree-file memory-tree.json \\
    --mode llm --llm-prompt-file prompt.txt
        """
    )
    
    parser.add_argument('--query', required=True, help='Search query')
    parser.add_argument('--tree-file', required=True, help='Path to memory-tree.json')
    parser.add_argument('--mode', choices=['keyword', 'llm'], default='keyword',
                        help='Search mode (default: keyword)')
    parser.add_argument('--llm-endpoint', help='LLM HTTP endpoint (for --mode llm)')
    parser.add_argument('--api-key', help='API key for LLM endpoint')
    parser.add_argument('--model', help='Model name (for OpenAI-compatible APIs)')
    parser.add_argument('--llm-prompt-file', help='Write LLM prompt to file instead of calling')
    parser.add_argument('--top-k', type=int, default=5, help='Max results (default: 5)')
    parser.add_argument('--output', help='Write results to file')
    
    args = parser.parse_args()
    
    # Load tree
    try:
        with open(args.tree_file) as f:
            tree = json.load(f)
    except Exception as e:
        print(f"Error loading tree: {e}", file=sys.stderr)
        sys.exit(1)
    
    # Search
    if args.mode == 'keyword':
        results = search_keyword(tree, args.query, args.top_k)
    else:
        results = search_llm(tree, args.query, args.llm_endpoint, args.api_key, args.model, args.llm_prompt_file, args.top_k)
    
    # Output
    output = {
        'query': args.query,
        'mode': args.mode,
        'results': results,
        'count': len(results)
    }
    
    if args.output:
        with open(args.output, 'w') as f:
            json.dump(output, f, indent=2)
        print(f"Written to {args.output}", file=sys.stderr)
    else:
        print(json.dumps(output, indent=2))


if __name__ == '__main__':
    main()