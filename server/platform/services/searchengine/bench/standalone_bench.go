package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
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
	BLEVE_URL         = "http://localhost:7700"
	POSTS_INDEX       = "posts_bench"
	NUM_TEST_POSTS    = 1000
	NUM_QUERIES       = 5
	DEFAULT_FUZZY_LEVEL = 1
	DEFAULT_OUTPUT_FILE = "search_comparison_results.csv"
)

type Post struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
	Message   string `json:"message"`
	CreateAt  int64  `json:"create_at"`
	TeamID    string `json:"team_id"`
}

type BenchResult struct {
	Engine      string
	Query       string
	NumPosts    int
	ExecTimeMs  float64
	ResultsFound int
	IsFuzzy     bool
}

var (
	esURL        string
	bleveURL     string
	numPosts     int
	iterations   int
	outputFile   string
	searchTerms  string
)

func init() {
	flag.StringVar(&esURL, "es-url", ES_URL, "Elasticsearch URL")
	flag.StringVar(&bleveURL, "bleve-url", BLEVE_URL, "Bleve server URL")
	flag.IntVar(&numPosts, "posts", NUM_TEST_POSTS, "Number of posts to index")
	flag.IntVar(&iterations, "iter", NUM_QUERIES, "Number of iterations for each search")
	flag.StringVar(&outputFile, "out", DEFAULT_OUTPUT_FILE, "Output CSV file for results")
	flag.StringVar(&searchTerms, "terms", "important,urgent,meeting,project,deadline", "Comma-separated search terms")
}

func main() {
	flag.Parse()
	
	// Initialize random number generator
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	
	// Create CSV output file
	file, err := os.Create(outputFile)
	if err != nil {
		fmt.Printf("Failed to create output file: %v\n", err)
		return
	}
	defer file.Close()
	
	writer := csv.NewWriter(file)
	defer writer.Flush()
	
	// Write CSV headers
	headers := []string{"Engine", "Query", "NumPosts", "ExecTimeMs", "ResultsFound", "IsFuzzy"}
	writer.Write(headers)
	
	// Generate test data
	fmt.Println("Generating test data...")
	posts := generateTestPosts(r, numPosts)
	
	// Check if Elasticsearch is running
	fmt.Println("Checking Elasticsearch connection...")
	if !checkElasticsearchConnection() {
		fmt.Println("Unable to connect to Elasticsearch. Make sure it's running.")
		return
	}
	
	// Setup Elasticsearch
	setupElasticsearchIndex()
	
	// Index data in Elasticsearch
	fmt.Println("Indexing test data in Elasticsearch...")
	indexPostsInElasticsearch(posts)
	
	// Give time for indexing to complete
	fmt.Println("Waiting for indexing to complete...")
	time.Sleep(2 * time.Second)
	refreshElasticsearchIndex()
	
	// Parse search terms
	terms := strings.Split(searchTerms, ",")
	
	// Prepare fuzzy terms by introducing typos
	fuzzyTerms := make([]string, len(terms))
	for i, term := range terms {
		if len(term) > 3 {
			// Introduce a simple typo by replacing a character
			pos := r.Intn(len(term) - 2) + 1
			chars := []rune(term)
			chars[pos] = getRandomSimilarChar(chars[pos])
			fuzzyTerms[i] = string(chars)
		} else {
			fuzzyTerms[i] = term
		}
	}
	
	allResults := []BenchResult{}
	
	// Run benchmarks for exact terms
	fmt.Println("\nRunning Elasticsearch benchmarks with exact terms...")
	for _, term := range terms {
		// Elasticsearch exact search
		esResults := runElasticsearchSearch(term, iterations, false)
		allResults = append(allResults, esResults...)
	}
	
	// Run benchmarks for fuzzy terms
	fmt.Println("\nRunning Elasticsearch benchmarks with fuzzy terms (simulating typos)...")
	for i, fuzzyTerm := range fuzzyTerms {
		fmt.Printf("Original term: '%s', Fuzzy term: '%s'\n", terms[i], fuzzyTerm)
		
		// Elasticsearch fuzzy search
		esResults := runElasticsearchSearch(fuzzyTerm, iterations, true)
		allResults = append(allResults, esResults...)
	}
	
	// Write results to CSV
	for _, result := range allResults {
		row := []string{
			result.Engine,
			result.Query,
			fmt.Sprintf("%d", result.NumPosts),
			fmt.Sprintf("%.2f", result.ExecTimeMs),
			fmt.Sprintf("%d", result.ResultsFound),
			fmt.Sprintf("%t", result.IsFuzzy),
		}
		
		if err := writer.Write(row); err != nil {
			fmt.Printf("Error writing CSV row: %v\n", err)
		}
	}
	
	// Print summary
	printSummary(allResults)
	
	fmt.Printf("\nBenchmark completed. Results written to %s\n", outputFile)
}

func checkElasticsearchConnection() bool {
	resp, err := http.Get(esURL)
	if err != nil {
		fmt.Printf("Error connecting to Elasticsearch: %s\n", err)
		return false
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Error connecting to Elasticsearch: status code %d\n", resp.StatusCode)
		return false
	}
	
	// Read response to get version info
	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(body, &result)
	
	version := "unknown"
	if v, ok := result["version"].(map[string]interface{})["number"]; ok {
		version = v.(string)
	}
	
	fmt.Printf("Connected to Elasticsearch %s at %s\n", version, esURL)
	return true
}

func setupElasticsearchIndex() {
	// Delete the index if it exists
	client := &http.Client{}
	req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/%s", esURL, POSTS_INDEX), nil)
	client.Do(req)
	
	// Create the index with mappings
	indexSettings := `{
		"settings": {
			"number_of_shards": 1,
			"number_of_replicas": 0
		},
		"mappings": {
			"properties": {
				"message": { 
					"type": "text",
					"analyzer": "standard"
				},
				"channel_id": { "type": "keyword" },
				"user_id": { "type": "keyword" },
				"team_id": { "type": "keyword" },
				"create_at": { "type": "date" }
			}
		}
	}`
	
	req, _ = http.NewRequest("PUT", fmt.Sprintf("%s/%s", esURL, POSTS_INDEX), strings.NewReader(indexSettings))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	
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
	
	fmt.Println("Elasticsearch index created successfully")
}

func indexPostsInElasticsearch(posts []Post) {
	client := &http.Client{}
	
	// Use bulk indexing for better performance
	var bulkBody strings.Builder
	
	for _, post := range posts {
		// Add action line
		bulkBody.WriteString(fmt.Sprintf(`{"index":{"_index":"%s","_id":"%s"}}`, POSTS_INDEX, post.ID))
		bulkBody.WriteString("\n")
		
		// Add document line
		postJSON, _ := json.Marshal(post)
		bulkBody.Write(postJSON)
		bulkBody.WriteString("\n")
		
		// Send batch of 500 posts at a time
		if bulkBody.Len() > 1000000 {
			req, _ := http.NewRequest("POST", fmt.Sprintf("%s/_bulk", esURL), strings.NewReader(bulkBody.String()))
			req.Header.Set("Content-Type", "application/x-ndjson")
			resp, err := client.Do(req)
			
			if err != nil {
				fmt.Printf("Error bulk indexing: %v\n", err)
			} else {
				resp.Body.Close()
			}
			
			// Reset the string builder
			bulkBody.Reset()
		}
	}
	
	// Send any remaining posts
	if bulkBody.Len() > 0 {
		req, _ := http.NewRequest("POST", fmt.Sprintf("%s/_bulk", esURL), strings.NewReader(bulkBody.String()))
		req.Header.Set("Content-Type", "application/x-ndjson")
		resp, err := client.Do(req)
		
		if err != nil {
			fmt.Printf("Error bulk indexing: %v\n", err)
		} else {
			resp.Body.Close()
		}
	}
}

func refreshElasticsearchIndex() {
	client := &http.Client{}
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/%s/_refresh", esURL, POSTS_INDEX), nil)
	resp, err := client.Do(req)
	
	if err != nil {
		fmt.Printf("Error refreshing index: %v\n", err)
	} else {
		resp.Body.Close()
	}
}

func runElasticsearchSearch(term string, iterations int, isFuzzy bool) []BenchResult {
	results := []BenchResult{}
	client := &http.Client{}
	
	for i := 0; i < iterations; i++ {
		// Build query based on whether we want fuzzy matching
		var query map[string]interface{}
		
		if isFuzzy {
			query = map[string]interface{}{
				"query": map[string]interface{}{
					"match": map[string]interface{}{
						"message": map[string]interface{}{
							"query":     term,
							"fuzziness": "AUTO",
							"operator":  "or",
						},
					},
				},
				"size": 100,
			}
		} else {
			query = map[string]interface{}{
				"query": map[string]interface{}{
					"match": map[string]interface{}{
						"message": map[string]interface{}{
							"query":    term,
							"operator": "or",
						},
					},
				},
				"size": 100,
			}
		}
		
		// Execute search
		queryJSON, _ := json.Marshal(query)
		url := fmt.Sprintf("%s/%s/_search", esURL, POSTS_INDEX)
		
		startTime := time.Now()
		req, _ := http.NewRequest("POST", url, bytes.NewReader(queryJSON))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		execTime := time.Since(startTime).Milliseconds()
		
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
			Took int `json:"took"`
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
		
		fmt.Printf("Elasticsearch search for '%s' (fuzzy: %t): %dms, %d results, ES took: %dms\n", 
			term, isFuzzy, execTime, resultData.Hits.Total.Value, resultData.Took)
		
		results = append(results, BenchResult{
			Engine:       "Elasticsearch",
			Query:        term,
			NumPosts:     numPosts,
			ExecTimeMs:   float64(execTime),
			ResultsFound: resultData.Hits.Total.Value,
			IsFuzzy:      isFuzzy,
		})
	}
	
	return results
}

func generateTestPosts(r *rand.Rand, count int) []Post {
	posts := make([]Post, count)
	keywords := []string{"important", "urgent", "meeting", "project", "deadline", "review"}
	
	// Generate a team ID
	teamID := fmt.Sprintf("team%d", r.Intn(3)+1)
	
	// Generate some channel IDs
	channelIDs := []string{}
	for i := 0; i < 5; i++ {
		channelIDs = append(channelIDs, fmt.Sprintf("channel%d", i+1))
	}
	
	// Generate some user IDs
	userIDs := []string{}
	for i := 0; i < 10; i++ {
		userIDs = append(userIDs, fmt.Sprintf("user%d", i+1))
	}
	
	for i := 0; i < count; i++ {
		// Generate message content
		var message string
		containsKeyword := r.Intn(10) < 3 // 30% contain keywords
		
		if containsKeyword {
			keyword := keywords[r.Intn(len(keywords))]
			if r.Intn(10) < 2 { // 20% of these have typos
				// Introduce a typo
				keywordRunes := []rune(keyword)
				pos := r.Intn(len(keywordRunes) - 2) + 1
				keywordRunes[pos] = getRandomSimilarChar(keywordRunes[pos])
				keyword = string(keywordRunes)
			}
			message = fmt.Sprintf("This is post %d containing %s content for search testing", i, keyword)
		} else {
			message = fmt.Sprintf("This is regular post %d without any special keywords for search", i)
		}
		
		// Create post
		posts[i] = Post{
			ID:        fmt.Sprintf("post%d", i+1),
			ChannelID: channelIDs[r.Intn(len(channelIDs))],
			UserID:    userIDs[r.Intn(len(userIDs))],
			TeamID:    teamID,
			Message:   message,
			CreateAt:  time.Now().UnixNano()/int64(time.Millisecond) - int64(count-i)*1000,
		}
	}
	
	return posts
}

func getRandomSimilarChar(char rune) rune {
	// Common typos for certain characters
	similarChars := map[rune][]rune{
		'a': {'s', 'q', 'z'},
		'b': {'v', 'g', 'h', 'n'},
		'c': {'x', 'v', 'd'},
		'd': {'s', 'f', 'e'},
		'e': {'w', 'r', 'd'},
		'i': {'u', 'o', 'k', 'j'},
		'm': {'n', 'j', 'k'},
		'n': {'m', 'b'},
		'o': {'i', 'p', 'l'},
		'p': {'o', 'l'},
		'r': {'e', 't', 'd', 'f'},
		's': {'a', 'd', 'w'},
		't': {'r', 'y', 'g'},
		'u': {'y', 'i', 'j', 'h'},
	}
	
	if alternatives, ok := similarChars[char]; ok && len(alternatives) > 0 {
		return alternatives[rand.Intn(len(alternatives))]
	}
	
	// Default: just return the original char
	return char
}

func printSummary(results []BenchResult) {
	// Group results by fuzzy status
	esExactTimes := []float64{}
	esFuzzyTimes := []float64{}
	esExactResults := []int{}
	esFuzzyResults := []int{}
	
	for _, result := range results {
		if result.Engine == "Elasticsearch" {
			if result.IsFuzzy {
				esFuzzyTimes = append(esFuzzyTimes, result.ExecTimeMs)
				esFuzzyResults = append(esFuzzyResults, result.ResultsFound)
			} else {
				esExactTimes = append(esExactTimes, result.ExecTimeMs)
				esExactResults = append(esExactResults, result.ResultsFound)
			}
		}
	}
	
	// Calculate averages
	fmt.Println("\nSummary Results:")
	fmt.Println("----------------------------------------------------")
	fmt.Printf("%-15s %-15s %-15s %-15s\n", "Engine", "Query Type", "Avg Time (ms)", "Avg Results")
	fmt.Println("----------------------------------------------------")
	fmt.Printf("%-15s %-15s %-15.2f %-15.2f\n", "Elasticsearch", "Exact", average(esExactTimes), average(floatFromInts(esExactResults)))
	fmt.Printf("%-15s %-15s %-15.2f %-15.2f\n", "Elasticsearch", "Fuzzy", average(esFuzzyTimes), average(floatFromInts(esFuzzyResults)))
	fmt.Println("----------------------------------------------------")
	
	// Fuzzy vs exact comparison for Elasticsearch
	esExactAvg := average(esExactTimes)
	esFuzzyAvg := average(esFuzzyTimes)
	esExactResAvg := average(floatFromInts(esExactResults))
	esFuzzyResAvg := average(floatFromInts(esFuzzyResults))
	
	fmt.Println("\nElasticsearch Fuzzy vs Exact Comparison:")
	if esFuzzyAvg > esExactAvg {
		fmt.Printf("Fuzzy searches are %.2f%% slower than exact searches\n", 
			100 * (esFuzzyAvg - esExactAvg) / esExactAvg)
	} else {
		fmt.Printf("Fuzzy searches are %.2f%% faster than exact searches (unusual!)\n", 
			100 * (esExactAvg - esFuzzyAvg) / esExactAvg)
	}
	
	if esFuzzyResAvg > esExactResAvg {
		fmt.Printf("Fuzzy searches found %.2f%% more results than exact searches\n", 
			100 * (esFuzzyResAvg - esExactResAvg) / esExactResAvg)
	} else if esExactResAvg > esFuzzyResAvg {
		fmt.Printf("Exact searches found %.2f%% more results than fuzzy searches (unusual!)\n", 
			100 * (esExactResAvg - esFuzzyResAvg) / esFuzzyResAvg)
	} else {
		fmt.Println("Fuzzy and exact searches found the same number of results")
	}
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func floatFromInts(values []int) []float64 {
	floats := make([]float64, len(values))
	for i, v := range values {
		floats[i] = float64(v)
	}
	return floats
}