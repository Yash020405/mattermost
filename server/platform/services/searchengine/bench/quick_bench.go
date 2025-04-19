package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	ES_URL            = "http://localhost:9200"
	POSTS_INDEX       = "posts"
	NUM_TEST_POSTS    = 1000
	NUM_QUERIES       = 5
	DEFAULT_FUZZY_LEVEL = 1
)

// Generate a test post with a simple message
func generatePost(i int, includeSearchTerm bool) map[string]interface{} {
	id := fmt.Sprintf("post%d", i)
	message := fmt.Sprintf("This is post %d with regular content", i)
	
	if includeSearchTerm && i%3 == 0 {
		// Every third post has a search term
		terms := []string{"important", "urgent", "meeting", "project", "deadline"}
		term := terms[i%len(terms)]
		message = fmt.Sprintf("This is post %d with %s content", i, term)
	}
	
	return map[string]interface{}{
		"id":        id,
		"message":   message,
		"create_at": time.Now().Unix() - int64(NUM_TEST_POSTS-i),
	}
}

func main() {
	// Use the newer Go pattern for random number generation
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	
	// Check if Elasticsearch is running
	resp, err := http.Get(ES_URL)
	if err != nil {
		fmt.Printf("Error connecting to Elasticsearch: %s\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Error connecting to Elasticsearch: status code %d\n", resp.StatusCode)
		os.Exit(1)
	}
	
	// Read response to get version info
	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(body, &result)
	
	version := "unknown"
	if v, ok := result["version"].(map[string]interface{})["number"]; ok {
		version = v.(string)
	}
	
	fmt.Printf("Connected to Elasticsearch %s at %s\n", version, ES_URL)
	
	// Create test data
	fmt.Println("\nGenerating test posts...")
	posts := make([]map[string]interface{}, NUM_TEST_POSTS)
	for i := 0; i < NUM_TEST_POSTS; i++ {
		posts[i] = generatePost(i, true)
	}
	
	// Setup Elasticsearch
	fmt.Println("Setting up Elasticsearch...")
	setupElasticsearch(posts)
	
	// Generate search queries
	searchTerms := []string{"important", "urgent", "meeting", "project", "deadline"}
	searchTermsWithTypos := []string{"importent", "urgant", "meetng", "porject", "deadlien"}
	allTerms := append(searchTerms, searchTermsWithTypos...)
	
	// Benchmark Elasticsearch search
	fmt.Println("\nBenchmarking Elasticsearch search with fuzzy matching:")
	esResults := benchmarkElasticsearch(r, allTerms, DEFAULT_FUZZY_LEVEL, NUM_QUERIES)
	
	// Benchmark SQL-like search
	fmt.Println("\nBenchmarking SQL-like search (exact match only):")
	sqlResults := benchmarkSQLSearch(r, posts, allTerms, NUM_QUERIES)
	
	// Print comparison
	fmt.Printf("\nResults Comparison (average over %d queries):\n", NUM_QUERIES)
	fmt.Printf("%-20s %-20s %-20s\n", "Search Type", "Avg Time (ms)", "Avg Results")
	fmt.Printf("%-20s %-20.2f %-20.2f\n", "Elasticsearch", average(esResults["time"]), average(esResults["results"]))
	fmt.Printf("%-20s %-20.2f %-20.2f\n", "SQL", average(sqlResults["time"]), average(sqlResults["results"]))
	
	// Compare fuzzy vs exact
	fmt.Printf("\nAccuracy Improvement:\n")
	resultsDiff := average(esResults["results"]) - average(sqlResults["results"])
	if resultsDiff > 0 {
		fmt.Printf("Elasticsearch fuzzy search found %.2f more relevant results on average\n", resultsDiff)
		fmt.Printf("(This demonstrates the effectiveness of fuzzy matching for handling typos and variations)\n")
	}
}

func average(values []float64) float64 {
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func setupElasticsearch(posts []map[string]interface{}) {
	client := &http.Client{}
	
	// Check if the index exists
	checkReq, _ := http.NewRequest("HEAD", fmt.Sprintf("%s/%s", ES_URL, POSTS_INDEX), nil)
	resp, err := client.Do(checkReq)
	
	// If the index exists, delete it
	if err == nil && resp.StatusCode == 200 {
		deleteReq, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/%s", ES_URL, POSTS_INDEX), nil)
		client.Do(deleteReq)
	}
	
	// Create the index with mappings
	indexSettings := `{
		"settings": {
			"number_of_shards": 1,
			"number_of_replicas": 0
		},
		"mappings": {
			"properties": {
				"message": { "type": "text" }
			}
		}
	}`
	
	req, _ := http.NewRequest("PUT", fmt.Sprintf("%s/%s", ES_URL, POSTS_INDEX), strings.NewReader(indexSettings))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	
	if err != nil || resp.StatusCode >= 400 {
		fmt.Printf("Failed to create index: %v\n", err)
		if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			fmt.Printf("Response: %s\n", string(body))
			resp.Body.Close()
		}
		os.Exit(1)
	}
	resp.Body.Close()
	
	// Index the posts
	fmt.Println("Indexing posts...")
	for _, post := range posts {
		docBody, _ := json.Marshal(post)
		
		req, _ := http.NewRequest("POST", 
			fmt.Sprintf("%s/%s/_doc/%s", ES_URL, POSTS_INDEX, post["id"]), 
			bytes.NewReader(docBody))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		
		if err != nil {
			fmt.Printf("Error indexing document: %v\n", err)
			continue
		}
		resp.Body.Close()
	}
	
	// Allow time for indexing to complete
	fmt.Println("Refreshing index...")
	refreshReq, _ := http.NewRequest("POST", fmt.Sprintf("%s/%s/_refresh", ES_URL, POSTS_INDEX), nil)
	client.Do(refreshReq)
}

func benchmarkElasticsearch(r *rand.Rand, searchTerms []string, fuzzyLevel int, iterations int) map[string][]float64 {
	results := map[string][]float64{
		"time":    make([]float64, 0, iterations),
		"results": make([]float64, 0, iterations),
	}
	
	client := &http.Client{}
	
	for i := 0; i < iterations; i++ {
		// Select a random search term
		term := searchTerms[r.Intn(len(searchTerms))]
		
		// Build query with fuzzy matching
		query := map[string]interface{}{
			"query": map[string]interface{}{
				"match": map[string]interface{}{
					"message": map[string]interface{}{
						"query":     term,
						"fuzziness": fuzzyLevel,
					},
				},
			},
			"size": 100,
		}
		
		// Execute search
		queryJSON, _ := json.Marshal(query)
		url := fmt.Sprintf("%s/%s/_search", ES_URL, POSTS_INDEX)
		
		start := time.Now()
		req, _ := http.NewRequest("POST", url, bytes.NewReader(queryJSON))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		duration := time.Since(start).Milliseconds()
		
		// Process results
		if err != nil {
			fmt.Printf("Error searching: %s\n", err)
			continue
		}
		
		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(resp.Body)
			fmt.Printf("Error searching: %s\n", string(body))
			resp.Body.Close()
			continue
		}
		
		var resultData struct {
			Hits struct {
				Total struct {
					Value int `json:"value"`
				} `json:"total"`
				Hits []struct {
					ID string `json:"_id"`
				} `json:"hits"`
			} `json:"hits"`
		}
		
		if err := json.NewDecoder(resp.Body).Decode(&resultData); err != nil {
			fmt.Printf("Error parsing search results: %s\n", err)
			resp.Body.Close()
			continue
		}
		resp.Body.Close()
		
		fmt.Printf("Elasticsearch search for '%s': %dms, %d results\n", 
			term, duration, resultData.Hits.Total.Value)
		
		results["time"] = append(results["time"], float64(duration))
		results["results"] = append(results["results"], float64(resultData.Hits.Total.Value))
	}
	
	return results
}

func benchmarkSQLSearch(r *rand.Rand, posts []map[string]interface{}, searchTerms []string, iterations int) map[string][]float64 {
	results := map[string][]float64{
		"time":    make([]float64, 0, iterations),
		"results": make([]float64, 0, iterations),
	}
	
	for i := 0; i < iterations; i++ {
		// Select a random search term
		term := searchTerms[r.Intn(len(searchTerms))]
		
		// Perform SQL-like search (exact match only)
		start := time.Now()
		matches := 0
		
		for _, post := range posts {
			// Simulate SQL LIKE '%term%'
			if strings.Contains(strings.ToLower(post["message"].(string)), strings.ToLower(term)) {
				matches++
			}
		}
		
		duration := time.Since(start).Milliseconds()
		fmt.Printf("SQL-like search for '%s': %dms, %d results\n", 
			term, duration, matches)
		
		results["time"] = append(results["time"], float64(duration))
		results["results"] = append(results["results"], float64(matches))
	}
	
	return results
} 