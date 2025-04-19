package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/mattermost/mattermost/server/v8/platform/services/searchengine/bench"
)

func main() {
	// Parse command line arguments
	esURL := flag.String("es-url", "http://localhost:9200", "Elasticsearch URL")
	numPosts := flag.Int("posts", 10000, "Number of posts to generate")
	numUsers := flag.Int("users", 100, "Number of users to generate")
	numChannels := flag.Int("channels", 10, "Number of channels to distribute posts across")
	searchTermsStr := flag.String("terms", "important,urgent,meeting,project", "Comma-separated list of search terms")
	iterations := flag.Int("iterations", 5, "Number of iterations for each search")
	fuzzy := flag.Bool("fuzzy", true, "Test fuzzy search with typos")
	outputFile := flag.String("output", fmt.Sprintf("search_benchmark_%d.csv", time.Now().Unix()), "Output CSV file")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	flag.Parse()

	// Setup logger
	level := mlog.LvlInfo
	if *verbose {
		level = mlog.LvlDebug
	}
	logger := mlog.CreateConsoleLogger(true, level)

	// Parse search terms
	searchTerms := strings.Split(*searchTermsStr, ",")
	for i, term := range searchTerms {
		searchTerms[i] = strings.TrimSpace(term)
	}

	// Create benchmark config
	config := &bench.BenchmarkConfig{
		NumPosts:         *numPosts,
		NumUsers:         *numUsers,
		NumChannels:      *numChannels,
		SearchTerms:      searchTerms,
		NumIterations:    *iterations,
		TestFuzzySearch:  *fuzzy,
		OutputFile:       *outputFile,
		ElasticsearchURL: *esURL,
		Logger:           logger,
	}

	// Create and run benchmark
	benchmark := bench.NewElasticsearchBenchmark(config)

	// Setup benchmark environment
	fmt.Println("Setting up benchmark environment...")
	if err := benchmark.Setup(); err != nil {
		fmt.Fprintf(os.Stderr, "Error setting up benchmark: %v\n", err)
		os.Exit(1)
	}

	// Run benchmark
	fmt.Printf("Starting benchmark with %d posts, %d iterations per search term...\n", *numPosts, *iterations)
	results, err := benchmark.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running benchmark: %v\n", err)
		benchmark.Cleanup()
		os.Exit(1)
	}

	// Print results summary
	printResultsSummary(results, searchTerms)

	// Cleanup
	fmt.Println("Cleaning up...")
	if err := benchmark.Cleanup(); err != nil {
		fmt.Fprintf(os.Stderr, "Error cleaning up: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Benchmark complete. Results saved to %s\n", *outputFile)
}

func printResultsSummary(results []bench.BenchmarkResult, searchTerms []string) {
	// Group results by engine and term
	engineResults := make(map[string]map[string]bench.BenchmarkResult)
	
	for _, result := range results {
		if _, ok := engineResults[result.Engine]; !ok {
			engineResults[result.Engine] = make(map[string]bench.BenchmarkResult)
		}
		engineResults[result.Engine][result.Term] = result
	}

	// Print indexing time comparison
	fmt.Println("\nIndexing Performance:")
	fmt.Printf("%-15s %-20s\n", "Engine", "Indexing Time (ms)")
	fmt.Printf("%-15s %-20s\n", "--------", "----------------")
	
	for engine, results := range engineResults {
		if indexResult, ok := results["indexing"]; ok {
			fmt.Printf("%-15s %-20.2f\n", engine, indexResult.IndexingTime)
		}
	}

	// Print search performance by term
	fmt.Println("\nSearch Performance by Term:")
	fmt.Printf("%-15s %-15s %-20s %-15s\n", "Engine", "Term", "Response Time (ms)", "Result Count")
	fmt.Printf("%-15s %-15s %-20s %-15s\n", "--------", "----", "----------------", "------------")
	
	for _, term := range searchTerms {
		for engine, results := range engineResults {
			if result, ok := results[term]; ok && result.Term != "indexing" {
				fmt.Printf("%-15s %-15s %-20.2f %-15d\n", 
					engine, term, result.ResponseTime, result.ResultCount)
			}
		}
		// Add a blank line between terms
		fmt.Println()
	}

	// Print summary comparison
	fmt.Println("\nOverall Performance Summary:")
	fmt.Printf("%-15s %-20s %-15s\n", "Engine", "Avg Response (ms)", "Avg Results")
	fmt.Printf("%-15s %-20s %-15s\n", "--------", "----------------", "-----------")
	
	for engine, results := range engineResults {
		var totalResponseTime float64
		var totalResultCount int
		var count int
		
		for term, result := range results {
			if term != "indexing" {
				totalResponseTime += result.ResponseTime
				totalResultCount += result.ResultCount
				count++
			}
		}
		
		if count > 0 {
			fmt.Printf("%-15s %-20.2f %-15.2f\n", 
				engine, 
				totalResponseTime/float64(count), 
				float64(totalResultCount)/float64(count))
		}
	}
} 