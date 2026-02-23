#!/usr/bin/env python3
"""
Download SWE-bench Lite dataset and prepare it for token efficiency testing.

Usage:
    python download_swe_bench.py --output ./swe-bench-lite.jsonl
"""

import argparse
import json
import os
import sys
import urllib.request
from pathlib import Path


def download_swe_bench_lite(output_path: str) -> int:
    """
    Download SWE-bench Lite dataset from Hugging Face.
    
    Returns number of tasks downloaded.
    """
    # SWE-bench Lite is available on Hugging Face datasets
    # URL format for raw JSONL
    url = "https://huggingface.co/datasets/princeton-nlp/SWE-bench_Lite/resolve/main/swe-bench-lite.jsonl"
    
    print(f"Downloading SWE-bench Lite dataset...")
    print(f"Source: {url}")
    print(f"Output: {output_path}")
    
    try:
        urllib.request.urlretrieve(url, output_path)
    except Exception as e:
        print(f"Error downloading: {e}")
        print("\nFalling back to generating synthetic SWE-bench-like tasks...")
        return generate_synthetic_tasks(output_path)
    
    # Count tasks
    count = 0
    with open(output_path, 'r') as f:
        for line in f:
            if line.strip():
                count += 1
    
    print(f"✓ Downloaded {count} tasks")
    return count


def generate_synthetic_tasks(output_path: str, count: int = 100) -> int:
    """Generate synthetic tasks that mirror SWE-bench Lite complexity."""
    
    # Real SWE-bench Lite repos and typical issue types
    repos = [
        "scikit-learn/scikit-learn",
        "django/django",
        "pandas-dev/pandas",
        "matplotlib/matplotlib",
        "pytest-dev/pytest",
        "pallets/flask",
        "psf/requests",
        "sphinx-doc/sphinx",
    ]
    
    templates = [
        {
            "type": "bug_fix",
            "problem": "Bug: {component} raises {exception} when {condition}. The error occurs in {location}.",
            "hints": "Check the handling of edge cases in the {component} function."
        },
        {
            "type": "feature_request",
            "problem": "Feature: Add support for {feature} in {component}. Currently, this is not supported.",
            "hints": "Consider adding a new parameter to handle {feature}."
        },
        {
            "type": "performance",
            "problem": "Performance: {operation} is O(n²) but could be O(n). Large inputs cause timeouts.",
            "hints": "Look for nested loops in the {component} implementation."
        },
        {
            "type": "api_fix",
            "problem": "API: {component} returns inconsistent types. Should always return {expected_type}.",
            "hints": "Add type checking and conversion in the return statement."
        },
    ]
    
    components = [
        "DataFrame.to_csv", "QuerySet.filter", "metrics.accuracy_score",
        "plt.savefig", "session.get", "config.load", "parser.parse_args",
        "Model.fit", "Image.resize", "json.dumps", "DataLoader.__init__",
    ]
    
    exceptions = [
        "IndexError", "KeyError", "ValueError", "TypeError", 
        "AttributeError", "RuntimeError", "AssertionError"
    ]
    
    conditions = [
        "input is empty", "index is out of bounds", "key is missing",
        "type is incorrect", "file doesn't exist", "network is unavailable"
    ]
    
    features = [
        "async/await", "type hints", "caching", "validation",
        "streaming", "batching", "compression", "encryption"
    ]
    
    operations = [
        "search", "sort", "filter", "aggregate", "merge", "join"
    ]
    
    import random
    random.seed(42)  # Reproducible
    
    tasks = []
    for i in range(count):
        template = random.choice(templates)
        repo = random.choice(repos)
        
        problem = template["problem"].format(
            component=random.choice(components),
            exception=random.choice(exceptions),
            condition=random.choice(conditions),
            location=f"line {random.randint(50, 500)}",
            feature=random.choice(features),
            operation=random.choice(operations),
            expected_type=random.choice(["dict", "list", "str", "None"])
        )
        
        hint = template["hints"].format(
            component=random.choice(components),
            feature=random.choice(features)
        )
        
        task = {
            "instance_id": f"synthetic-{i:04d}",
            "repo": repo,
            "base_commit": f"abc{random.randint(10000, 99999)}",
            "problem_statement": problem,
            "hints_text": hint,
            "task_type": template["type"]
        }
        tasks.append(task)
    
    with open(output_path, 'w') as f:
        for task in tasks:
            f.write(json.dumps(task) + '\n')
    
    print(f"✓ Generated {count} synthetic tasks")
    return count


def analyze_tasks(tasks_path: str):
    """Analyze the tasks and print statistics."""
    
    repos = {}
    types = {}
    total_chars = 0
    count = 0
    
    with open(tasks_path, 'r') as f:
        for line in f:
            if not line.strip():
                continue
            task = json.loads(line)
            count += 1
            
            repo = task.get('repo', 'unknown')
            repos[repo] = repos.get(repo, 0) + 1
            
            task_type = task.get('task_type', 'unknown')
            types[task_type] = types.get(task_type, 0) + 1
            
            problem = task.get('problem_statement', '')
            total_chars += len(problem)
    
    print(f"\n=== Dataset Statistics ===")
    print(f"Total tasks: {count}")
    print(f"Avg problem length: {total_chars // count} chars")
    
    print(f"\nBy Repository:")
    for repo, n in sorted(repos.items(), key=lambda x: -x[1]):
        print(f"  {repo}: {n}")
    
    print(f"\nBy Type:")
    for t, n in sorted(types.items(), key=lambda x: -x[1]):
        print(f"  {t}: {n}")


def main():
    parser = argparse.ArgumentParser(description='Download SWE-bench Lite dataset')
    parser.add_argument('--output', '-o', default='swe-bench-lite.jsonl',
                       help='Output file path')
    parser.add_argument('--synthetic', '-s', action='store_true',
                       help='Generate synthetic tasks even if download available')
    parser.add_argument('--count', '-n', type=int, default=100,
                       help='Number of synthetic tasks to generate')
    parser.add_argument('--analyze', '-a', action='store_true',
                       help='Analyze existing dataset')
    
    args = parser.parse_args()
    
    if args.analyze:
        if os.path.exists(args.output):
            analyze_tasks(args.output)
        else:
            print(f"File not found: {args.output}")
            sys.exit(1)
        return
    
    if args.synthetic:
        count = generate_synthetic_tasks(args.output, args.count)
    else:
        count = download_swe_bench_lite(args.output)
    
    if count > 0:
        analyze_tasks(args.output)
        print(f"\n✓ Ready to run benchmarks with {count} tasks")
        print(f"  Run: go run run_swe_bench_lite.go -tasks={args.output}")
    else:
        print("\n✗ Failed to prepare tasks")
        sys.exit(1)


if __name__ == '__main__':
    main()
