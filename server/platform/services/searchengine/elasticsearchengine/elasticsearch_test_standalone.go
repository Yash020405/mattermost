// +build ignore

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// Simple document struct for testing
type TestDoc struct {
	ID      string    `json:"id"`
	Content string    `json:"content"`
	Created time.Time `json:"created"`
}

func main() {
	fmt.Println("Running Elasticsearch standalone test...")

	// Create Elasticsearch client
	cfg := elasticsearch.Config{
		Addresses: []string{"http://localhost:9200"},
	}
	client, err := elasticsearch.NewClient(cfg)
	if err != nil {
		fmt.Printf("Error creating the client: %s\n", err)
		os.Exit(1)
	}

	// Check if Elasticsearch is running
	res, err := client.Info()
	if err != nil {
		fmt.Printf("Error getting info: %s\n", err)
		os.Exit(1)
	}
	defer res.Body.Close()

	// Display cluster info
	var info map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		fmt.Printf("Error parsing response body: %s\n", err)
		os.Exit(1)
	}
	fmt.Println("Connected to Elasticsearch cluster:")
	fmt.Printf("  Name:    %s\n", info["name"])
	fmt.Printf("  Version: %s\n", info["version"].(map[string]interface{})["number"])

	// Create test index
	indexName := "test_index"
	createIndex(client, indexName)

	// Index a document
	doc := TestDoc{
		ID:      "doc1",
		Content: "This is a test document for Elasticsearch",
		Created: time.Now(),
	}
	indexDocument(client, indexName, doc)

	// Refresh index
	refreshIndex(client, indexName)

	// Search for documents
	searchDocuments(client, indexName, "test document")

	// Delete the document
	deleteDocument(client, indexName, "doc1")

	// Clean up - delete index
	deleteIndex(client, indexName)

	fmt.Println("Elasticsearch test completed successfully!")
}

func createIndex(client *elasticsearch.Client, indexName string) {
	res, err := client.Indices.Delete([]string{indexName})
	if err != nil || (res.StatusCode != 404 && res.StatusCode != 200) {
		fmt.Printf("Cannot delete index: %s\n", err)
	}

	res, err = client.Indices.Create(indexName)
	if err != nil {
		fmt.Printf("Cannot create index: %s\n", err)
		os.Exit(1)
	}
	if res.IsError() {
		fmt.Printf("Error creating index: %s\n", res.String())
		os.Exit(1)
	}
	fmt.Println("✅ Index created successfully")
}

func indexDocument(client *elasticsearch.Client, indexName string, doc TestDoc) {
	docBytes, err := json.Marshal(doc)
	if err != nil {
		fmt.Printf("Error marshaling document: %s\n", err)
		os.Exit(1)
	}

	req := esapi.IndexRequest{
		Index:      indexName,
		DocumentID: doc.ID,
		Body:       bytes.NewReader(docBytes),
		Refresh:    "true",
	}

	res, err := req.Do(context.Background(), client)
	if err != nil {
		fmt.Printf("Error indexing document: %s\n", err)
		os.Exit(1)
	}
	defer res.Body.Close()

	if res.IsError() {
		fmt.Printf("Error indexing document: %s\n", res.String())
		os.Exit(1)
	}

	var r map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		fmt.Printf("Error parsing response body: %s\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Document indexed successfully (ID: %s)\n", doc.ID)
}

func refreshIndex(client *elasticsearch.Client, indexName string) {
	res, err := client.Indices.Refresh(
		client.Indices.Refresh.WithIndex(indexName),
	)
	if err != nil {
		fmt.Printf("Error refreshing index: %s\n", err)
		os.Exit(1)
	}
	defer res.Body.Close()

	if res.IsError() {
		fmt.Printf("Error refreshing index: %s\n", res.String())
		os.Exit(1)
	}
	fmt.Println("✅ Index refreshed successfully")
}

func searchDocuments(client *elasticsearch.Client, indexName string, query string) {
	// Build the search query
	var buf bytes.Buffer
	searchQuery := map[string]interface{}{
		"query": map[string]interface{}{
			"match": map[string]interface{}{
				"content": query,
			},
		},
	}
	if err := json.NewEncoder(&buf).Encode(searchQuery); err != nil {
		fmt.Printf("Error encoding search query: %s\n", err)
		os.Exit(1)
	}

	// Perform the search
	res, err := client.Search(
		client.Search.WithContext(context.Background()),
		client.Search.WithIndex(indexName),
		client.Search.WithBody(&buf),
		client.Search.WithTrackTotalHits(true),
	)
	if err != nil {
		fmt.Printf("Error searching documents: %s\n", err)
		os.Exit(1)
	}
	defer res.Body.Close()

	if res.IsError() {
		fmt.Printf("Error searching documents: %s\n", res.String())
		os.Exit(1)
	}

	var r map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		fmt.Printf("Error parsing search response: %s\n", err)
		os.Exit(1)
	}

	// Extract and display search hits
	hits := r["hits"].(map[string]interface{})["hits"].([]interface{})
	fmt.Printf("✅ Found %d documents:\n", len(hits))

	for _, hit := range hits {
		source := hit.(map[string]interface{})["_source"].(map[string]interface{})
		fmt.Printf("  ID: %s, Content: %s\n", source["id"], source["content"])
	}
}

func deleteDocument(client *elasticsearch.Client, indexName string, docId string) {
	req := esapi.DeleteRequest{
		Index:      indexName,
		DocumentID: docId,
		Refresh:    "true",
	}

	res, err := req.Do(context.Background(), client)
	if err != nil {
		fmt.Printf("Error deleting document: %s\n", err)
		os.Exit(1)
	}
	defer res.Body.Close()

	if res.IsError() {
		fmt.Printf("Error deleting document: %s\n", res.String())
		os.Exit(1)
	}

	fmt.Printf("✅ Document deleted successfully (ID: %s)\n", docId)
}

func deleteIndex(client *elasticsearch.Client, indexName string) {
	res, err := client.Indices.Delete([]string{indexName})
	if err != nil {
		fmt.Printf("Error deleting index: %s\n", err)
		os.Exit(1)
	}
	defer res.Body.Close()

	if res.IsError() {
		fmt.Printf("Error deleting index: %s\n", res.String())
		os.Exit(1)
	}

	fmt.Println("✅ Index deleted successfully")
} 