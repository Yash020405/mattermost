// +build ignore

package main

import (
	"fmt"
	"os"
	"time"
	
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
)

// Simple document struct for testing
type TestDoc struct {
	ID      string    `json:"id"`
	Content string    `json:"content"`
	Created time.Time `json:"created"`
}

func main() {
	fmt.Println("Running Bleve standalone test...")

	// Create a temporary index
	indexPath := "test_bleve_index"
	os.RemoveAll(indexPath) // Clean up any previous index

	// Create a document mapping
	indexMapping := createMapping()

	// Create a new index
	index, err := bleve.New(indexPath, indexMapping)
	if err != nil {
		fmt.Printf("Error creating index: %s\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(indexPath) // Clean up after test
	fmt.Println("✅ Index created successfully")

	// Index a document
	doc := TestDoc{
		ID:      "doc1",
		Content: "This is a test document for Bleve",
		Created: time.Now(),
	}
	err = index.Index(doc.ID, doc)
	if err != nil {
		fmt.Printf("Error indexing document: %s\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Document indexed successfully (ID: %s)\n", doc.ID)

	// Search for documents
	query := bleve.NewMatchQuery("test document")
	search := bleve.NewSearchRequest(query)
	searchResults, err := index.Search(search)
	if err != nil {
		fmt.Printf("Error searching documents: %s\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Found %d documents:\n", searchResults.Total)
	for _, hit := range searchResults.Hits {
		fmt.Printf("  ID: %s, Score: %f\n", hit.ID, hit.Score)
	}

	// Delete the document
	err = index.Delete(doc.ID)
	if err != nil {
		fmt.Printf("Error deleting document: %s\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Document deleted successfully (ID: %s)\n", doc.ID)

	// Verify document is gone
	search = bleve.NewSearchRequest(query)
	searchResults, err = index.Search(search)
	if err != nil {
		fmt.Printf("Error searching documents: %s\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ After deletion: Found %d documents\n", searchResults.Total)

	// Close the index
	err = index.Close()
	if err != nil {
		fmt.Printf("Error closing index: %s\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ Index closed successfully")

	fmt.Println("Bleve test completed successfully!")
}

func createMapping() mapping.IndexMapping {
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