package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

const (
	ES_URL            = "http://localhost:9200"
	POSTS_INDEX       = "posts"
	NUM_TEST_POSTS    = 10000
	NUM_QUERIES       = 5
	DEFAULT_FUZZY_LEVEL = 1
)

// Simple post representation for Elasticsearch/OpenSearch
type ESPost struct {
	Id        string `json:"id"`
	TeamId    string `json:"team_id"`
	ChannelId string `json:"channel_id"`
	UserId    string `json:"user_id"`
	Message   string `json:"message"`
	CreateAt  int64  `json:"create_at"`
	DeleteAt  int64  `json:"delete_at"`
	Hashtags  string `json:"hashtags"`
}

// Our own simple hashtag parsing implementation
func parseHashtags(text string) []string {
	r := regexp.MustCompile(`#\w+`)
	hashtags := r.FindAllString(text, -1)
	
	// Remove the # prefix
	for i, tag := range hashtags {
		hashtags[i] = strings.TrimPrefix(tag, "#")
	}
	
	return hashtags
}

func main() {
	// Check if OpenSearch/Elasticsearch is running
	resp, err := http.Get(ES_URL)
	if err != nil {
		fmt.Printf("Error connecting to search engine: %s\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Error connecting to search engine: status code %d\n", resp.StatusCode)
		os.Exit(1)
	}

	// Read response to get version info
	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(body, &result)

	fmt.Printf("Successfully connected to search engine: %s\n", result["version"].(map[string]interface{})["number"])

	// Generate test data
	fmt.Println("\nGenerating test posts...")
	posts := generateTestPosts(NUM_TEST_POSTS)
	
	// Index posts in Elasticsearch/OpenSearch
	fmt.Println("Indexing posts in search engine...")
	setupElasticsearch()

	// Generate search queries
	searchTerms := []string{"important", "urgent", "meeting", "project", "deadline"}
	
	// Add more typos to test fuzzy matching
	searchTermsWithTypos := []string{"importent", "urgant", "meetng", "porject", "deadlien"}
	allTerms := append(searchTerms, searchTermsWithTypos...)
	
	// Benchmark Elasticsearch/OpenSearch search
	fmt.Println("\nBenchmarking Elasticsearch/OpenSearch search with fuzzy matching:")
	esResults := benchmarkElasticsearch(allTerms, DEFAULT_FUZZY_LEVEL, NUM_QUERIES)
	
	// Benchmark SQL-like search (simulated)
	fmt.Println("\nBenchmarking SQL-like search (exact match only):")
	sqlResults := benchmarkSQLSearch(posts, allTerms, NUM_QUERIES)
	
	// Print comparison
	fmt.Printf("\nResults Comparison (average over %d queries):\n", NUM_QUERIES)
	fmt.Printf("%-20s %-20s %-20s\n", "Search Type", "Avg Time (ms)", "Avg Results")
	fmt.Printf("%-20s %-20.2f %-20.2f\n", "Elasticsearch", average(esResults["time"]), average(esResults["results"]))
	fmt.Printf("%-20s %-20.2f %-20.2f\n", "SQL", average(sqlResults["time"]), average(sqlResults["results"]))
	
	// Compare fuzzy vs exact
	fmt.Printf("\nAccuracy Improvement:\n")
	resultsDiff := average(esResults["results"]) - average(sqlResults["results"])
	if resultsDiff > 0 {
		fmt.Printf("Elasticsearch/OpenSearch fuzzy search found %.2f more relevant results on average\n", resultsDiff)
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

func generateTestPosts(count int) []*model.Post {
	posts := make([]*model.Post, count)
	searchTerms := []string{"important", "urgent", "meeting", "discussion", "project", "deadline", "review", "feedback", "update", "status"}
	
	// Add some common typos for fuzzy matching testing
	typos := map[string]string{
		"important": "importent",
		"urgent": "urgant",
		"meeting": "meetng",
		"project": "porject",
		"deadline": "deadlien",
	}
	
	for i := 0; i < count; i++ {
		userID := model.NewId()
		channelID := model.NewId()
		
		var message string
		r := rand.Intn(10)
		if r < 3 { 
			// 30% of posts have search terms
			term := searchTerms[rand.Intn(len(searchTerms))]
			message = fmt.Sprintf("This is post %d with %s content", i, term)
		} else if r < 4 {
			// 10% have typos
			term := searchTerms[rand.Intn(len(searchTerms))]
			if typo, exists := typos[term]; exists {
				message = fmt.Sprintf("This is post %d with %s content", i, typo)
			} else {
				message = fmt.Sprintf("This is post %d with regular content", i)
			}
		} else {
			message = fmt.Sprintf("This is post %d with regular content", i)
		}
		
		posts[i] = &model.Post{
			Id:        model.NewId(),
			UserId:    userID,
			ChannelId: channelID,
			Message:   message,
			CreateAt:  model.GetMillis() - int64(count-i)*1000, // Older to newer
			UpdateAt:  model.GetMillis() - int64(count-i)*1000,
			DeleteAt:  0,
		}
	}
	
	return posts
}

func setupElasticsearch() {
	// Check if the index exists first
	client := &http.Client{}
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
				"message": { "type": "text" },
				"hashtags": { "type": "keyword" }
			}
		}
	}`
	
	req, _ := http.NewRequest("PUT", fmt.Sprintf("%s/%s", ES_URL, POSTS_INDEX), strings.NewReader(indexSettings))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	
	if err != nil || resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("Failed to create index: %v, status: %d, response: %s\n", err, resp.StatusCode, string(body))
		os.Exit(1)
	}
	resp.Body.Close()
	
	// Index the sample data
	fmt.Println("Indexing sample data...")
	posts := generateTestPosts(NUM_TEST_POSTS)
	for _, post := range posts {
		// Simple hashtag extraction
		hashtags := extractHashtags(post.Message)
		
		// Create the document
		docBody := fmt.Sprintf(`{
			"message": "%s",
			"hashtags": %s
		}`, post.Message, hashtagsToJSON(hashtags))
		
		// Index the document
		req, _ := http.NewRequest("POST", 
			fmt.Sprintf("%s/%s/_doc/%s", ES_URL, POSTS_INDEX, generateID()), 
			strings.NewReader(docBody))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		
		if err != nil {
			fmt.Printf("Error indexing document: %v\n", err)
			continue
		}
		resp.Body.Close()
	}
	
	// Allow time for indexing to complete
	time.Sleep(2 * time.Second)
	fmt.Println("Elasticsearch setup completed.")
}

func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func extractHashtags(text string) []string {
	var hashtags []string
	words := strings.Fields(text)
	
	for _, word := range words {
		if strings.HasPrefix(word, "#") {
			hashtags = append(hashtags, strings.ToLower(strings.TrimPrefix(word, "#")))
		}
	}
	
	return hashtags
}

func hashtagsToJSON(hashtags []string) string {
	if len(hashtags) == 0 {
		return "[]"
	}
	
	jsonTags, err := json.Marshal(hashtags)
	if err != nil {
		return "[]"
	}
	
	return string(jsonTags)
}

func benchmarkElasticsearch(searchTerms []string, fuzzyLevel int, iterations int) map[string][]float64 {
	results := map[string][]float64{
		"time":    make([]float64, 0, iterations),
		"results": make([]float64, 0, iterations),
	}
	
	client := &http.Client{}
	
	for i := 0; i < iterations; i++ {
		// Select a random search term
		term := searchTerms[rand.Intn(len(searchTerms))]
		
		// Build query with fuzzy matching
		query := map[string]interface{}{
			"query": map[string]interface{}{
				"bool": map[string]interface{}{
					"should": []map[string]interface{}{
						{
							"match": map[string]interface{}{
								"message": map[string]interface{}{
									"query":     term,
									"fuzziness": fuzzyLevel,
									"operator":  "and",
									"boost":     1.5, // Boost exact matches
								},
							},
						},
						{
							"match": map[string]interface{}{
								"hashtags": map[string]interface{}{
									"query":     term,
									"fuzziness": fuzzyLevel,
								},
							},
						},
					},
					"minimum_should_match": 1,
				},
			},
			"highlight": map[string]interface{}{
				"fields": map[string]interface{}{
					"message": map[string]interface{}{},
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
		
		fmt.Printf("Elasticsearch/OpenSearch search for '%s': %dms, %d results\n", 
			term, duration, resultData.Hits.Total.Value)
		
		results["time"] = append(results["time"], float64(duration))
		results["results"] = append(results["results"], float64(resultData.Hits.Total.Value))
	}
	
	return results
}

func benchmarkSQLSearch(posts []*model.Post, searchTerms []string, iterations int) map[string][]float64 {
	results := map[string][]float64{
		"time":    make([]float64, 0, iterations),
		"results": make([]float64, 0, iterations),
	}
	
	for i := 0; i < iterations; i++ {
		// Select a random search term
		term := searchTerms[rand.Intn(len(searchTerms))]
		
		// Perform SQL-like search (exact match only)
		start := time.Now()
		matches := 0
		
		for _, post := range posts {
			// Simulate SQL LIKE '%term%'
			if strings.Contains(strings.ToLower(post.Message), strings.ToLower(term)) {
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

func searchElasticsearch(query string, fuzzy bool) ([]string, int64) {
	startTime := time.Now()
	
	// Build the search query
	var requestBody string
	if fuzzy {
		requestBody = fmt.Sprintf(`{
			"query": {
				"multi_match": {
					"query": "%s",
					"fields": ["message"],
					"fuzziness": "AUTO"
				}
			},
			"size": 100
		}`, query)
	} else {
		requestBody = fmt.Sprintf(`{
			"query": {
				"match": {
					"message": "%s"
				}
			},
			"size": 100
		}`, query)
	}
	
	// Execute the search request
	client := &http.Client{}
	url := fmt.Sprintf("%s/%s/_search", ES_URL, POSTS_INDEX)
	
	req, _ := http.NewRequest("POST", url, strings.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("Error executing search:", err)
		return nil, 0
	}
	defer resp.Body.Close()
	
	// Read and parse the response
	body, _ := io.ReadAll(resp.Body)
	
	var searchResponse struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []struct {
				ID     string `json:"_id"`
				Source struct {
					Message string `json:"message"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	
	err = json.Unmarshal(body, &searchResponse)
	if err != nil {
		fmt.Println("Error parsing search response:", err)
		fmt.Println("Response body:", string(body))
		return nil, 0
	}
	
	// Extract post IDs from results
	results := make([]string, len(searchResponse.Hits.Hits))
	for i, hit := range searchResponse.Hits.Hits {
		results[i] = hit.ID
	}
	
	elapsedTime := time.Since(startTime).Milliseconds()
	return results, elapsedTime
}