#!/bin/bash
# Script to run search benchmarks against both SQL and Elasticsearch

# Default Elasticsearch URL (override with environment variable)
ES_URL=${ES_URL:-"http://localhost:9200"}

# Check if Elasticsearch is available
echo "Testing connection to Elasticsearch at $ES_URL..."
if ! curl -s --connect-timeout 5 "$ES_URL" > /dev/null; then
    echo "Error: Could not connect to Elasticsearch at $ES_URL"
    echo "Please ensure Elasticsearch is running or set a different URL with ES_URL environment variable"
    exit 1
fi

# Ensure go binary is available
if ! command -v go &> /dev/null; then
    echo "Error: go is not installed or not in PATH"
    exit 1
fi

# Build benchmark tool
echo "Building benchmark tool..."
cd "$(dirname "$0")"
go build -o search_bench .

if [ ! -f "./search_bench" ]; then
    echo "Failed to build benchmark tool"
    exit 1
fi

# Run benchmarks with different dataset sizes
echo "Running benchmarks..."

# Small dataset (10K posts)
echo "Testing with 10K posts..."
./search_bench -es-url "$ES_URL" -posts 10000 -users 100 -queries "important,urgent,meeting,project" -out "bench_results_10k.csv"

# Medium dataset (100K posts)
echo "Testing with 100K posts..."
./search_bench -es-url "$ES_URL" -posts 100000 -users 500 -queries "important,urgent,meeting,project" -out "bench_results_100k.csv"

# Large dataset (500K posts, if you have enough memory)
echo "Testing with 500K posts (this might take a while)..."
./search_bench -es-url "$ES_URL" -posts 500000 -users 1000 -queries "important,urgent,meeting,project" -out "bench_results_500k.csv"

echo "Benchmarks completed. Results are in bench_results_*.csv files"