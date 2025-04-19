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
	"strings"
	"time"

	"github.com/blevesearch/bleve/v2"
	bleveMapping "github.com/blevesearch/bleve/v2/mapping"
	elasticsearch "github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// RealWorldDoc represents a real-world document with complex fields
type RealWorldDoc struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	Author      string    `json:"author"`
	Tags        []string  `json:"tags"`
	Category    string    `json:"category"`
	ViewCount   int       `json:"view_count"`
	Created     time.Time `json:"created"`
	IsPublished bool      `json:"is_published"`
	Language    string    `json:"language"`
}

const (
	NUM_DOCS          = 50000  // Increase document count to simulate enterprise scale
	NUM_SEARCH_TESTS  = 200    // More search tests for better averages
	COMPLEX_QUERY_PCT = 60     // Higher percentage of complex queries
)

// Languages for content generation
var languages = []string{"en", "es", "fr", "de", "zh", "ja", "ru", "ar", "hi", "pt"}

func main() {
	fmt.Println("Running REAL-WORLD search engine benchmark test...")

	// Seed random number generator
	rand.Seed(time.Now().UnixNano())

	// Generate realistic test data
	docs := generateRealWorldDocs(NUM_DOCS)

	// Benchmark Elasticsearch
	esTime := benchmarkElasticsearch(docs)

	// Benchmark Bleve
	bleveTime := benchmarkBleve(docs)

	// Print results
	fmt.Println("\n======================================")
	fmt.Println("REAL-WORLD BENCHMARK RESULTS")
	fmt.Println("======================================")
	fmt.Printf("Elasticsearch: %.2f seconds\n", esTime.Seconds())
	fmt.Printf("Bleve:         %.2f seconds\n", bleveTime.Seconds())
	fmt.Printf("Ratio:         %.2fx\n", esTime.Seconds()/bleveTime.Seconds())
	fmt.Println("======================================")
	fmt.Println("Note: In this real-world scenario, Elasticsearch may be slower overall")
	fmt.Println("      but provides better relevance ranking, multilingual support,")
	fmt.Println("      complex query handling, and scalability that Bleve cannot match.")
}

func generateRealWorldDocs(count int) []RealWorldDoc {
	docs := make([]RealWorldDoc, count)
	
	// Common words for generating content
	words := []string{
		"mattermost", "server", "message", "channel", "user", "team",
		"post", "notification", "slack", "integration", "plugin", "bot",
		"webhook", "emoji", "react", "file", "upload", "download",
		"search", "index", "database", "cache", "performance", "open",
		"source", "collaboration", "communication", "enterprise", "secure",
		"platform", "mobile", "desktop", "web", "interface", "command",
		"authentication", "authorization", "permission", "role", "group",
		"admin", "manager", "member", "guest", "invite", "notification",
	}
	
	// Categories
	categories := []string{"General", "Development", "Operations", "Marketing", "Support", "Sales", "HR", "Finance"}
	
	// Authors
	authors := []string{"John Smith", "Emma Johnson", "Wei Chen", "Maria Garcia", "Ahmed Khan", 
		"Hiroshi Tanaka", "Olga Petrova", "Raj Patel", "Sophie Martin", "Carlos Rodriguez"}
	
	// Tags
	tagsList := []string{"important", "announcement", "question", "help", "bug", "feature", "release", 
		"documentation", "planning", "meeting", "discussion", "urgent", "followup", "resolved"}
	
	for i := 0; i < count; i++ {
		// Generate random language-appropriate content
		language := languages[rand.Intn(len(languages))]
		
		// Title (10-15 words)
		titleLen := rand.Intn(6) + 10
		title := generateContent(words, titleLen, language)
		
		// Content (100-500 words)
		contentLen := rand.Intn(401) + 100
		content := generateContent(words, contentLen, language)
		
		// Random tags (1-5 tags)
		numTags := rand.Intn(5) + 1
		tags := make([]string, numTags)
		for j := 0; j < numTags; j++ {
			tags[j] = tagsList[rand.Intn(len(tagsList))]
		}
		
		// Create document
		docs[i] = RealWorldDoc{
			ID:          fmt.Sprintf("doc%d", i+1),
			Title:       title,
			Content:     content,
			Author:      authors[rand.Intn(len(authors))],
			Tags:        tags,
			Category:    categories[rand.Intn(len(categories))],
			ViewCount:   rand.Intn(10000),
			Created:     time.Now().Add(-time.Duration(rand.Intn(365)) * 24 * time.Hour),
			IsPublished: rand.Intn(10) < 8, // 80% are published
			Language:    language,
		}
	}
	
	return docs
}

// generateContent creates realistic content with language-specific characteristics
func generateContent(words []string, numWords int, language string) string {
	content := ""
	
	// Add some language-specific words
	languageWords := map[string][]string{
		"en": {"the", "is", "and", "to", "of", "in", "for", "with", "on", "at"},
		"es": {"el", "la", "es", "y", "de", "en", "para", "con", "sobre", "por"},
		"fr": {"le", "la", "est", "et", "de", "dans", "pour", "avec", "sur", "à"},
		"de": {"der", "die", "das", "ist", "und", "zu", "in", "für", "mit", "auf"},
		"zh": {"的", "是", "不", "了", "在", "人", "有", "我", "他", "这"},
		"ja": {"の", "に", "は", "を", "た", "が", "で", "て", "と", "し"},
		"ru": {"и", "в", "не", "на", "я", "быть", "он", "с", "что", "а"},
		"ar": {"و", "في", "من", "إلى", "عن", "على", "أن", "مع", "هذا", "كان"},
		"hi": {"है", "का", "में", "और", "को", "से", "पर", "के", "एक", "यह"},
		"pt": {"o", "a", "é", "e", "de", "em", "para", "com", "um", "que"},
	}
	
	localWords := append(words, languageWords[language]...)
	
	for i := 0; i < numWords; i++ {
		// 80% standard vocabulary, 20% language-specific words
		if rand.Intn(10) < 8 {
			content += words[rand.Intn(len(words))] + " "
		} else {
			content += languageWords[language][rand.Intn(len(languageWords[language]))] + " "
		}
	}
	
	return content
}

func benchmarkElasticsearch(docs []RealWorldDoc) time.Duration {
	fmt.Println("\nBenchmarking Elasticsearch (REAL-WORLD SCENARIO)...")
	
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
	
	// Create index with advanced mappings for language support
	indexName := "benchmark_real_" + strconv.FormatInt(time.Now().Unix(), 10)
	
	// Create index with language analyzers
	createESIndex(client, indexName)
	
	startTime := time.Now()
	
	// Index documents
	fmt.Printf("Indexing %d documents...\n", len(docs))
	indexStart := time.Now()
	
	// Use bulk indexing for better performance
	var buf bytes.Buffer
	for _, doc := range docs {
		// Create metadata for the bulk operation
		meta := []byte(fmt.Sprintf(`{ "index" : { "_id" : "%s" } }%s`, doc.ID, "\n"))
		
		// Prepare the document
		data, err := json.Marshal(doc)
		if err != nil {
			fmt.Printf("Error marshaling document: %s\n", err)
			continue
		}
		
		// Append newline to the document data
		data = append(data, "\n"...)
		
		// Append metadata and document data to the buffer
		buf.Grow(len(meta) + len(data))
		buf.Write(meta)
		buf.Write(data)
		
		// Execute bulk request when buffer exceeds 5MB
		if buf.Len() >= 5*1024*1024 {
			res, err := client.Bulk(bytes.NewReader(buf.Bytes()), client.Bulk.WithIndex(indexName))
			if err != nil {
				fmt.Printf("Error sending bulk request: %s\n", err)
			}
			if res != nil {
				res.Body.Close()
			}
			buf.Reset()
		}
	}
	
	// Send any remaining documents in the buffer
	if buf.Len() > 0 {
		res, err := client.Bulk(bytes.NewReader(buf.Bytes()), client.Bulk.WithIndex(indexName))
		if err != nil {
			fmt.Printf("Error sending bulk request: %s\n", err)
		}
		if res != nil {
			res.Body.Close()
		}
	}
	
	indexDuration := time.Since(indexStart)
	fmt.Printf("Indexing completed in %.2f seconds\n", indexDuration.Seconds())
	
	// Refresh index
	_, err = client.Indices.Refresh(client.Indices.Refresh.WithIndex(indexName))
	if err != nil {
		fmt.Printf("Error refreshing index: %s\n", err)
	}
	
	// Run searches
	fmt.Printf("Running %d search queries (including %d%% complex queries)...\n", 
		NUM_SEARCH_TESTS, COMPLEX_QUERY_PCT)
	
	searchStart := time.Now()
	runElasticsearchSearches(client, indexName, NUM_SEARCH_TESTS)
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

func createESIndex(client *elasticsearch.Client, indexName string) {
	// Define index settings with language analyzers
	indexSettings := `{
		"settings": {
			"number_of_shards": 1,
			"number_of_replicas": 0,
			"analysis": {
				"analyzer": {
					"english_analyzer": {
						"type": "standard",
						"stopwords": "_english_"
					},
					"spanish_analyzer": {
						"type": "standard",
						"stopwords": "_spanish_"
					},
					"french_analyzer": {
						"type": "standard",
						"stopwords": "_french_"
					},
					"german_analyzer": {
						"type": "standard",
						"stopwords": "_german_"
					},
					"chinese_analyzer": {
						"type": "standard",
						"stopwords": "_cjk_"
					}
				}
			}
		},
		"mappings": {
			"properties": {
				"title": {
					"type": "text",
					"fields": {
						"en": {"type": "text", "analyzer": "english_analyzer"},
						"es": {"type": "text", "analyzer": "spanish_analyzer"},
						"fr": {"type": "text", "analyzer": "french_analyzer"},
						"de": {"type": "text", "analyzer": "german_analyzer"},
						"zh": {"type": "text", "analyzer": "chinese_analyzer"}
					}
				},
				"content": {
					"type": "text",
					"fields": {
						"en": {"type": "text", "analyzer": "english_analyzer"},
						"es": {"type": "text", "analyzer": "spanish_analyzer"},
						"fr": {"type": "text", "analyzer": "french_analyzer"},
						"de": {"type": "text", "analyzer": "german_analyzer"},
						"zh": {"type": "text", "analyzer": "chinese_analyzer"}
					}
				},
				"author": {"type": "keyword"},
				"tags": {"type": "keyword"},
				"category": {"type": "keyword"},
				"view_count": {"type": "integer"},
				"created": {"type": "date"},
				"is_published": {"type": "boolean"},
				"language": {"type": "keyword"}
			}
		}
	}`

	// Create index with settings
	res, err := client.Indices.Create(
		indexName,
		client.Indices.Create.WithBody(strings.NewReader(indexSettings)),
	)

	if err != nil {
		fmt.Printf("Error creating Elasticsearch index: %s\n", err)
		os.Exit(1)
	}
	defer res.Body.Close()

	if res.IsError() {
		fmt.Printf("Error creating Elasticsearch index: %s\n", res.String())
		os.Exit(1)
	}

	fmt.Println("Elasticsearch index created with language-specific mappings")
}

func runElasticsearchSearches(client *elasticsearch.Client, indexName string, numSearches int) {
	// Words for search
	searchWords := []string{"mattermost", "server", "channel", "team", "message", "integration", "performance"}
	
	// Track successful searches
	successCount := 0
	
	for i := 0; i < numSearches; i++ {
		var buf bytes.Buffer
		var query map[string]interface{}
		
		// Decide whether to use a simple or complex query
		if rand.Intn(100) < COMPLEX_QUERY_PCT {
			// Complex query (demonstrating ES capabilities)
			complexQueryType := rand.Intn(5)
			
			switch complexQueryType {
			case 0:
				// Multi-field search with boosting
				query = map[string]interface{}{
					"query": map[string]interface{}{
						"multi_match": map[string]interface{}{
							"query": searchWords[rand.Intn(len(searchWords))],
							"fields": []string{"title^3", "content", "author^2"},
							"type": "best_fields",
							"fuzziness": "AUTO"
						},
					},
				}
			case 1:
				// Boolean query with filters
				query = map[string]interface{}{
					"query": map[string]interface{}{
						"bool": map[string]interface{}{
							"must": map[string]interface{}{
								"match": map[string]interface{}{
									"content": searchWords[rand.Intn(len(searchWords))],
								},
							},
							"filter": []map[string]interface{}{
								{
									"term": map[string]interface{}{
										"is_published": true,
									},
								},
								{
									"range": map[string]interface{}{
										"view_count": map[string]interface{}{
											"gte": 100,
										},
									},
								},
							},
						},
					},
				}
			case 2:
				// Phrase query with slop
				query = map[string]interface{}{
					"query": map[string]interface{}{
						"match_phrase": map[string]interface{}{
							"content": map[string]interface{}{
								"query": fmt.Sprintf("%s %s", 
									searchWords[rand.Intn(len(searchWords))], 
									searchWords[rand.Intn(len(searchWords))]),
								"slop": 2,
							},
						},
					},
				}
			case 3:
				// Query by language
				randomLang := languages[rand.Intn(len(languages))]
				languageField := "content"
				if randomLang == "en" || randomLang == "es" || randomLang == "fr" || randomLang == "de" || randomLang == "zh" {
					languageField = fmt.Sprintf("content.%s", randomLang)
				}
				
				query = map[string]interface{}{
					"query": map[string]interface{}{
						"bool": map[string]interface{}{
							"must": map[string]interface{}{
								"match": map[string]interface{}{
									languageField: searchWords[rand.Intn(len(searchWords))],
								},
							},
							"filter": map[string]interface{}{
								"term": map[string]interface{}{
									"language": randomLang,
								},
							},
						},
					},
				}
			case 4:
				// Aggregation query (count by category)
				query = map[string]interface{}{
					"size": 0,
					"query": map[string]interface{}{
						"match": map[string]interface{}{
							"content": searchWords[rand.Intn(len(searchWords))],
						},
					},
					"aggs": map[string]interface{}{
						"categories": map[string]interface{}{
							"terms": map[string]interface{}{
								"field": "category",
							},
						},
					},
				}
			}
		} else {
			// Simple query (baseline)
			query = map[string]interface{}{
				"query": map[string]interface{}{
					"match": map[string]interface{}{
						"content": searchWords[rand.Intn(len(searchWords))],
					},
				},
			}
		}
		
		// Execute search
		if err := json.NewEncoder(&buf).Encode(query); err != nil {
			fmt.Printf("Error encoding query: %s\n", err)
			continue
		}
		
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
		successCount++
	}
	
	fmt.Printf("Successfully executed %d/%d Elasticsearch searches\n", successCount, numSearches)
}

func benchmarkBleve(docs []RealWorldDoc) time.Duration {
	fmt.Println("\nBenchmarking Bleve (REAL-WORLD SCENARIO)...")
	
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
	
	// Use batch indexing for better performance
	batch := index.NewBatch()
	batchSize := 100
	batchCount := 0
	
	for i, doc := range docs {
		err := batch.Index(doc.ID, doc)
		if err != nil {
			fmt.Printf("Error indexing document: %s\n", err)
		}
		
		batchCount++
		
		// Execute batch when it reaches the batch size
		if batchCount >= batchSize || i == len(docs)-1 {
			err = index.Batch(batch)
			if err != nil {
				fmt.Printf("Error executing batch: %s\n", err)
			}
			
			// Reset batch
			batch = index.NewBatch()
			batchCount = 0
		}
	}
	
	indexDuration := time.Since(indexStart)
	fmt.Printf("Indexing completed in %.2f seconds\n", indexDuration.Seconds())
	
	// Run searches
	fmt.Printf("Running %d search queries (including %d%% complex queries)...\n", 
		NUM_SEARCH_TESTS, COMPLEX_QUERY_PCT)
	
	searchStart := time.Now()
	runBleveSearches(index, NUM_SEARCH_TESTS)
	searchDuration := time.Since(searchStart)
	
	fmt.Printf("Search completed in %.2f seconds\n", searchDuration.Seconds())
	
	totalDuration := time.Since(startTime)
	fmt.Printf("Total Bleve benchmark time: %.2f seconds\n", totalDuration.Seconds())
	
	return totalDuration
}

func createBleveMapping() bleveMapping.IndexMapping {
	// Create document mapping
	docMapping := bleve.NewDocumentMapping()
	
	// Map fields
	titleFieldMapping := bleve.NewTextFieldMapping()
	titleFieldMapping.Analyzer = "standard"
	docMapping.AddFieldMappingsAt("title", titleFieldMapping)
	
	contentFieldMapping := bleve.NewTextFieldMapping()
	contentFieldMapping.Analyzer = "standard"
	docMapping.AddFieldMappingsAt("content", contentFieldMapping)
	
	authorFieldMapping := bleve.NewKeywordFieldMapping()
	docMapping.AddFieldMappingsAt("author", authorFieldMapping)
	
	tagsFieldMapping := bleve.NewKeywordFieldMapping()
	docMapping.AddFieldMappingsAt("tags", tagsFieldMapping)
	
	categoryFieldMapping := bleve.NewKeywordFieldMapping()
	docMapping.AddFieldMappingsAt("category", categoryFieldMapping)
	
	viewCountFieldMapping := bleve.NewNumericFieldMapping()
	docMapping.AddFieldMappingsAt("view_count", viewCountFieldMapping)
	
	timeFieldMapping := bleve.NewDateTimeFieldMapping()
	docMapping.AddFieldMappingsAt("created", timeFieldMapping)
	
	boolFieldMapping := bleve.NewBooleanFieldMapping()
	docMapping.AddFieldMappingsAt("is_published", boolFieldMapping)
	
	languageFieldMapping := bleve.NewKeywordFieldMapping()
	docMapping.AddFieldMappingsAt("language", languageFieldMapping)
	
	// Create index mapping
	indexMapping := bleve.NewIndexMapping()
	indexMapping.AddDocumentMapping("real_world_doc", docMapping)
	indexMapping.DefaultAnalyzer = "standard"
	
	return indexMapping
}

func runBleveSearches(index bleve.Index, numSearches int) {
	// Words for search
	searchWords := []string{"mattermost", "server", "channel", "team", "message", "integration", "performance"}
	
	// Track successful searches
	successCount := 0
	
	for i := 0; i < numSearches; i++ {
		var searchRequest *bleve.SearchRequest
		
		// Decide whether to use a simple or complex query
		if rand.Intn(100) < COMPLEX_QUERY_PCT {
			// Complex query (trying to mimic ES capabilities as much as possible)
			complexQueryType := rand.Intn(5)
			
			switch complexQueryType {
			case 0:
				// Multi-field search
				query := bleve.NewMatchQuery(searchWords[rand.Intn(len(searchWords))])
				searchRequest = bleve.NewSearchRequest(query)
				searchRequest.Fields = []string{"title", "content", "author"}
				
			case 1:
				// Boolean query with filters
				boolQuery := bleve.NewBooleanQuery()
				
				// Must: content matches keyword
				matchQuery := bleve.NewMatchQuery(searchWords[rand.Intn(len(searchWords))])
				matchQuery.SetField("content")
				boolQuery.AddMust(matchQuery)
				
				// Filter: is_published is true
				publishedQuery := bleve.NewBoolFieldQuery(true)
				publishedQuery.SetField("is_published")
				boolQuery.AddFilter(publishedQuery)
				
				// Filter: view_count >= 100
				viewCountQuery := bleve.NewNumericRangeQuery(&[]float64{100.0}[0], nil)
				viewCountQuery.SetField("view_count")
				boolQuery.AddFilter(viewCountQuery)
				
				searchRequest = bleve.NewSearchRequest(boolQuery)
				
			case 2:
				// Phrase query (closest to slop)
				words := []string{
					searchWords[rand.Intn(len(searchWords))],
					searchWords[rand.Intn(len(searchWords))],
				}
				phrase := strings.Join(words, " ")
				query := bleve.NewMatchPhraseQuery(phrase)
				searchRequest = bleve.NewSearchRequest(query)
				
			case 3:
				// Query by language
				randomLang := languages[rand.Intn(len(languages))]
				languageField := "content"
				if randomLang == "en" || randomLang == "es" || randomLang == "fr" || randomLang == "de" || randomLang == "zh" {
					languageField = fmt.Sprintf("content.%s", randomLang)
				}
				
				query := bleve.NewMatchQuery(searchWords[rand.Intn(len(searchWords))])
				query.SetField(languageField)
				searchRequest = bleve.NewSearchRequest(query)
				
			case 4:
				// Aggregation query (facet by category)
				query := bleve.NewMatchQuery(searchWords[rand.Intn(len(searchWords))])
				searchRequest = bleve.NewSearchRequest(query)
				searchRequest.AddFacet("categories", bleve.NewFacetRequest("category", 10))
			}
		} else {
			// Simple query (baseline)
			query := bleve.NewMatchQuery(searchWords[rand.Intn(len(searchWords))])
			query.SetField("content")
			searchRequest = bleve.NewSearchRequest(query)
		}
		
		// Limit results
		searchRequest.Size = 10
		
		// Execute search
		_, err := index.Search(searchRequest)
		if err != nil {
			fmt.Printf("Error searching: %s\n", err)
			continue
		}
		
		successCount++
	}
	
	fmt.Printf("Successfully executed %d/%d Bleve searches\n", successCount, numSearches)
} 