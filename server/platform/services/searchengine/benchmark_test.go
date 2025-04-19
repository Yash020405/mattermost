// +build ignore

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/blevesearch/bleve/v2"
	bleveMapping "github.com/blevesearch/bleve/v2/mapping"
	elasticsearch "github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// Simple document struct for testing
type TestDoc struct {
	ID      string    `json:"id"`
	Content string    `json:"content"`
	Created time.Time `json:"created"`
}

const (
	NUM_DOCS         = 1000
	NUM_SEARCH_TESTS = 100
)

func main() {
	fmt.Println("Running search engine benchmark test...")

	// Seed random number generator
	rand.Seed(time.Now().UnixNano())

	// Generate test data
	docs := generateTestDocs(NUM_DOCS)

	// Benchmark Elasticsearch
	esTime := benchmarkElasticsearch(docs)

	// Benchmark Bleve
	bleveTime := benchmarkBleve(docs)

	// Print results
	fmt.Println("\n======================================")
	fmt.Println("BENCHMARK RESULTS")
	fmt.Println("======================================")
	fmt.Printf("Elasticsearch: %.2f seconds\n", esTime.Seconds())
	fmt.Printf("Bleve:         %.2f seconds\n", bleveTime.Seconds())
	fmt.Printf("Ratio:         %.2fx\n", esTime.Seconds()/bleveTime.Seconds())
	fmt.Println("======================================")
}

func generateTestDocs(count int) []TestDoc {
	docs := make([]TestDoc, count)
	
	// Common words for generating content
	words := []string{
		"mattermost", "server", "message", "channel", "user", "team",
		"post", "notification", "slack", "integration", "plugin", "bot",
		"webhook", "emoji", "react", "file", "upload", "download",
		"search", "index", "database", "cache", "performance", "open",
		"source", "collaboration", "communication", "enterprise", "secure",
	}
	
	for i := 0; i < count; i++ {
		// Generate random content with 10-20 words
		content := ""
		numWords := rand.Intn(11) + 10
		for j := 0; j < numWords; j++ {
			content += words[rand.Intn(len(words))] + " "
		}
		
		docs[i] = TestDoc{
			ID:      fmt.Sprintf("doc%d", i+1),
			Content: content,
			Created: time.Now().Add(-time.Duration(rand.Intn(30)) * 24 * time.Hour),
		}
	}
	
	return docs
}

func benchmarkElasticsearch(docs []TestDoc) time.Duration {
	fmt.Println("\nBenchmarking Elasticsearch...")
	
	// Create Elasticsearch client
	cfg := elasticsearch.Config{
		Addresses: []string{"http://localhost:9200"},
	}
	client, err := elasticsearch.NewClient(cfg)
	if err != nil {
		fmt.Printf("Error creating the Elasticsearch client: %s\n", err)
		os.Exit(1)
	}
	
	// Check if Elasticsearch is running
	res, err := client.Info()
	if err != nil {
		fmt.Printf("Error connecting to Elasticsearch: %s\n", err)
		os.Exit(1)
	}
	res.Body.Close()
	
	// Create index
	indexName := "benchmark_test_" + strconv.FormatInt(time.Now().Unix(), 10)
	_, err = client.Indices.Create(indexName)
	if err != nil {
		fmt.Printf("Error creating Elasticsearch index: %s\n", err)
		os.Exit(1)
	}
	
	startTime := time.Now()
	
	// Index documents
	fmt.Printf("Indexing %d documents...\n", len(docs))
	indexStart := time.Now()
	for _, doc := range docs {
		docBytes, _ := json.Marshal(doc)
		req := esapi.IndexRequest{
			Index:      indexName,
			DocumentID: doc.ID,
			Body:       bytes.NewReader(docBytes),
		}
		res, err := req.Do(context.Background(), client)
		if err != nil {
			fmt.Printf("Error indexing document: %s\n", err)
			continue
		}
		res.Body.Close()
	}
	indexDuration := time.Since(indexStart)
	fmt.Printf("Indexing completed in %.2f seconds\n", indexDuration.Seconds())
	
	// Refresh index
	_, err = client.Indices.Refresh(client.Indices.Refresh.WithIndex(indexName))
	if err != nil {
		fmt.Printf("Error refreshing index: %s\n", err)
	}
	
	// Run searches
	fmt.Printf("Running %d search queries...\n", NUM_SEARCH_TESTS)
	searchStart := time.Now()
	searchWords := []string{"mattermost", "server", "channel", "team", "post"}
	for i := 0; i < NUM_SEARCH_TESTS; i++ {
		var buf bytes.Buffer
		query := map[string]interface{}{
			"query": map[string]interface{}{
				"match": map[string]interface{}{
					"content": searchWords[i%len(searchWords)],
				},
			},
		}
		json.NewEncoder(&buf).Encode(query)
		
		res, err := client.Search(
			client.Search.WithContext(context.Background()),
			client.Search.WithIndex(indexName),
			client.Search.WithBody(&buf),
			client.Search.WithTrackTotalHits(true),
		)
		if err != nil {
			fmt.Printf("Error searching: %s\n", err)
			continue
		}
		res.Body.Close()
	}
	searchDuration := time.Since(searchStart)
	fmt.Printf("Search completed in %.2f seconds\n", searchDuration.Seconds())
	
	// Delete index
	_, err = client.Indices.Delete([]string{indexName})
	if err != nil {
		fmt.Printf("Error deleting index: %s\n", err)
	}
	
	totalDuration := time.Since(startTime)
	fmt.Printf("Total Elasticsearch benchmark time: %.2f seconds\n", totalDuration.Seconds())
	
	return totalDuration
}

func benchmarkBleve(docs []TestDoc) time.Duration {
	fmt.Println("\nBenchmarking Bleve...")
	
	// Create a temporary directory for Bleve index
	indexPath := filepath.Join(os.TempDir(), "bleve_benchmark_"+strconv.FormatInt(time.Now().Unix(), 10))
	os.RemoveAll(indexPath)
	defer os.RemoveAll(indexPath)
	
	// Create mapping
	indexMapping := createBleveMapping()
	
	// Create index
	index, err := bleve.New(indexPath, indexMapping)
	if err != nil {
		fmt.Printf("Error creating Bleve index: %s\n", err)
		os.Exit(1)
	}
	defer index.Close()
	
	startTime := time.Now()
	
	// Index documents
	fmt.Printf("Indexing %d documents...\n", len(docs))
	indexStart := time.Now()
	for _, doc := range docs {
		err := index.Index(doc.ID, doc)
		if err != nil {
			fmt.Printf("Error indexing document: %s\n", err)
		}
	}
	indexDuration := time.Since(indexStart)
	fmt.Printf("Indexing completed in %.2f seconds\n", indexDuration.Seconds())
	
	// Run searches
	fmt.Printf("Running %d search queries...\n", NUM_SEARCH_TESTS)
	searchStart := time.Now()
	searchWords := []string{"mattermost", "server", "channel", "team", "post"}
	for i := 0; i < NUM_SEARCH_TESTS; i++ {
		query := bleve.NewMatchQuery(searchWords[i%len(searchWords)])
		searchRequest := bleve.NewSearchRequest(query)
		_, err := index.Search(searchRequest)
		if err != nil {
			fmt.Printf("Error searching: %s\n", err)
		}
	}
	searchDuration := time.Since(searchStart)
	fmt.Printf("Search completed in %.2f seconds\n", searchDuration.Seconds())
	
	totalDuration := time.Since(startTime)
	fmt.Printf("Total Bleve benchmark time: %.2f seconds\n", totalDuration.Seconds())
	
	return totalDuration
}

func createBleveMapping() bleveMapping.IndexMapping {
	// Create a document mapping
	docMapping := bleve.NewDocumentMapping()
	
	// Map fields
	contentFieldMapping := bleve.NewTextFieldMapping()
	contentFieldMapping.Analyzer = "standard"
	docMapping.AddFieldMappingsAt("content", contentFieldMapping)
	
	timeFieldMapping := bleve.NewDateTimeFieldMapping()
	docMapping.AddFieldMappingsAt("created", timeFieldMapping)
	
	// Create index mapping
	indexMapping := bleve.NewIndexMapping()
	indexMapping.AddDocumentMapping("test_doc", docMapping)
	indexMapping.DefaultAnalyzer = "standard"
	
	return indexMapping
} 