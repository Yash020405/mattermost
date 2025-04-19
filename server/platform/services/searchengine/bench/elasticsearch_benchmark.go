package bench

import (
	"context"
	"encoding/csv"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/mattermost/mattermost/server/v8/platform/services/searchengine"
	"github.com/mattermost/mattermost/server/v8/platform/services/searchengine/bleveengine"
	"github.com/mattermost/mattermost/server/v8/platform/services/searchengine/elasticsearchengine"
)

// BenchmarkConfig holds configuration for the benchmark
type BenchmarkConfig struct {
	// Number of posts to generate for testing
	NumPosts int
	// Number of users to simulate
	NumUsers int
	// Number of channels to distribute posts across
	NumChannels int
	// Search terms to test
	SearchTerms []string
	// Number of iterations for each search
	NumIterations int
	// Whether to include typos in the search to test fuzzy matching
	TestFuzzySearch bool
	// Output file for results
	OutputFile string
	// Elasticsearch connection URL
	ElasticsearchURL string
	// Logger instance
	Logger *mlog.Logger
}

// BenchmarkResult stores results from a benchmark run
type BenchmarkResult struct {
	Engine        string
	Term          string
	ResponseTime  float64 // in milliseconds
	ResultCount   int
	MemoryUsage   int64 // in bytes
	CPUUsage      float64
	Accuracy      float64 // percentage of expected results found
	IndexingTime  float64 // in milliseconds (only for initial indexing)
	SearchSuccess bool
}

// ElasticsearchBenchmark provides methods to benchmark Elasticsearch vs. SQL search
type ElasticsearchBenchmark struct {
	config       *BenchmarkConfig
	posts        []*model.Post
	users        []*model.User
	channels     []*model.Channel
	teams        []*model.Team
	esEngine     searchengine.SearchEngineInterface
	bleveEngine  searchengine.SearchEngineInterface
	sqlResults   map[string][]string // baseline results from SQL for comparison
	expectedHits map[string][]string // posts we know should match each search term
	logger       *mlog.Logger
}

// NewElasticsearchBenchmark creates a new benchmark instance
func NewElasticsearchBenchmark(config *BenchmarkConfig) *ElasticsearchBenchmark {
	if config.Logger == nil {
		config.Logger = mlog.CreateConsoleLogger(true, mlog.LvlInfo)
	}

	return &ElasticsearchBenchmark{
		config:       config,
		sqlResults:   make(map[string][]string),
		expectedHits: make(map[string][]string),
		logger:       config.Logger,
	}
}

// Setup prepares the benchmark environment
func (b *ElasticsearchBenchmark) Setup() error {
	b.logger.Info("Setting up benchmark environment...")

	// Generate test data
	b.generateTestData()

	// Initialize Elasticsearch engine
	cfg := model.Config{}
	cfg.SetDefaults()

	// Configure Elasticsearch
	*cfg.ElasticsearchSettings.ConnectionURL = b.config.ElasticsearchURL
	*cfg.ElasticsearchSettings.Username = ""
	*cfg.ElasticsearchSettings.Password = ""
	*cfg.ElasticsearchSettings.Sniff = false
	*cfg.ElasticsearchSettings.EnableIndexing = true
	*cfg.ElasticsearchSettings.EnableSearching = true
	*cfg.ElasticsearchSettings.EnableAutocomplete = true
	*cfg.ElasticsearchSettings.PostIndexReplicas = 0
	*cfg.ElasticsearchSettings.PostIndexShards = 1
	*cfg.ElasticsearchSettings.BatchSize = 2000
	*cfg.ElasticsearchSettings.RequestTimeoutSeconds = 30

	// Initialize Elasticsearch engine
	esEngine, err := elasticsearchengine.NewElasticsearchEngine(b.logger, &cfg)
	if err != nil {
		return fmt.Errorf("failed to create Elasticsearch engine: %w", err)
	}
	b.esEngine = esEngine

	// Start the engine
	if err := b.esEngine.Start(); err != nil {
		return fmt.Errorf("failed to start Elasticsearch engine: %w", err)
	}

	// Initialize Bleve engine for comparison
	*cfg.BleveSettings.IndexDir = "./bleve_bench"
	*cfg.BleveSettings.EnableIndexing = true
	*cfg.BleveSettings.EnableSearching = true
	
	bleveEngine := bleveengine.NewBleveEngine(&cfg)
	b.bleveEngine = bleveEngine

	// Start the Bleve engine
	if err := b.bleveEngine.Start(); err != nil {
		return fmt.Errorf("failed to start Bleve engine: %w", err)
	}

	return nil
}

// Run executes the benchmark and records results
func (b *ElasticsearchBenchmark) Run() ([]BenchmarkResult, error) {
	results := []BenchmarkResult{}

	// Index all posts in Elasticsearch
	b.logger.Info("Indexing posts in Elasticsearch...")
	esIndexingStart := time.Now()
	if err := b.indexPostsInElasticsearch(); err != nil {
		return nil, fmt.Errorf("failed to index posts in Elasticsearch: %w", err)
	}
	esIndexingTime := float64(time.Since(esIndexingStart).Milliseconds())
	
	// Index all posts in Bleve
	b.logger.Info("Indexing posts in Bleve...")
	bleveIndexingStart := time.Now()
	if err := b.indexPostsInBleve(); err != nil {
		return nil, fmt.Errorf("failed to index posts in Bleve: %w", err)
	}
	bleveIndexingTime := float64(time.Since(bleveIndexingStart).Milliseconds())

	// Record indexing times
	results = append(results, BenchmarkResult{
		Engine:       "elasticsearch",
		Term:         "indexing",
		IndexingTime: esIndexingTime,
	})
	
	results = append(results, BenchmarkResult{
		Engine:       "bleve",
		Term:         "indexing",
		IndexingTime: bleveIndexingTime,
	})

	// Run search benchmarks
	b.logger.Info("Running search benchmarks...")
	
	// Convert channels to channel list
	channelList := model.ChannelList{}
	for _, channel := range b.channels {
		channelList = append(channelList, channel)
	}

	// Test each search term
	for _, term := range b.config.SearchTerms {
		// Also test with typos if enabled
		terms := []string{term}
		if b.config.TestFuzzySearch {
			terms = append(terms, introduceTypo(term))
		}

		for _, searchTerm := range terms {
			// Create search params
			params := []*model.SearchParams{
				{
					Terms:      searchTerm,
					IsHashtag:  false,
					InChannels: extractChannelIds(channelList),
					OrTerms:    true,
				},
			}

			// Run Elasticsearch searches
			b.logger.Info(fmt.Sprintf("Testing Elasticsearch search with term: %s", searchTerm))
			esResponseTimes := []float64{}
			esResultCounts := []int{}
			
			for i := 0; i < b.config.NumIterations; i++ {
				start := time.Now()
				postIds, _, err := b.esEngine.SearchPosts(channelList, params, 0, 100)
				duration := float64(time.Since(start).Milliseconds())
				
				esResponseTimes = append(esResponseTimes, duration)
				if err == nil {
					esResultCounts = append(esResultCounts, len(postIds))
				} else {
					b.logger.Error("Elasticsearch search failed", mlog.Err(err))
				}
			}
			
			// Calculate average response time and result count
			esAvgResponseTime := average(esResponseTimes)
			esAvgResultCount := averageInt(esResultCounts)
			
			results = append(results, BenchmarkResult{
				Engine:        "elasticsearch",
				Term:          searchTerm,
				ResponseTime:  esAvgResponseTime,
				ResultCount:   esAvgResultCount,
				SearchSuccess: true,
			})

			// Run Bleve searches
			b.logger.Info(fmt.Sprintf("Testing Bleve search with term: %s", searchTerm))
			bleveResponseTimes := []float64{}
			bleveResultCounts := []int{}
			
			for i := 0; i < b.config.NumIterations; i++ {
				start := time.Now()
				postIds, _, err := b.bleveEngine.SearchPosts(channelList, params, 0, 100)
				duration := float64(time.Since(start).Milliseconds())
				
				bleveResponseTimes = append(bleveResponseTimes, duration)
				if err == nil {
					bleveResultCounts = append(bleveResultCounts, len(postIds))
				} else {
					b.logger.Error("Bleve search failed", mlog.Err(err))
				}
			}
			
			// Calculate average response time and result count
			bleveAvgResponseTime := average(bleveResponseTimes)
			bleveAvgResultCount := averageInt(bleveResultCounts)
			
			results = append(results, BenchmarkResult{
				Engine:        "bleve",
				Term:          searchTerm,
				ResponseTime:  bleveAvgResponseTime,
				ResultCount:   bleveAvgResultCount,
				SearchSuccess: true,
			})

			// Simulate SQL search (direct string matching)
			b.logger.Info(fmt.Sprintf("Testing SQL search with term: %s", searchTerm))
			sqlStart := time.Now()
			sqlResults := b.simulateSQLSearch(searchTerm)
			sqlDuration := float64(time.Since(sqlStart).Milliseconds())
			
			results = append(results, BenchmarkResult{
				Engine:        "sql",
				Term:          searchTerm,
				ResponseTime:  sqlDuration,
				ResultCount:   len(sqlResults),
				SearchSuccess: true,
			})
		}
	}

	// Save results to CSV
	if err := b.saveResultsToCSV(results); err != nil {
		return results, fmt.Errorf("failed to save results: %w", err)
	}

	return results, nil
}

// Cleanup cleans up resources after benchmarking
func (b *ElasticsearchBenchmark) Cleanup() error {
	// Stop engines
	if b.esEngine != nil {
		if err := b.esEngine.Stop(); err != nil {
			return fmt.Errorf("failed to stop Elasticsearch engine: %w", err)
		}
	}
	
	if b.bleveEngine != nil {
		if err := b.bleveEngine.Stop(); err != nil {
			return fmt.Errorf("failed to stop Bleve engine: %w", err)
		}
	}
	
	// Remove Bleve index files
	os.RemoveAll("./bleve_bench")
	
	return nil
}

// generateTestData creates test posts, users, channels and teams
func (b *ElasticsearchBenchmark) generateTestData() {
	b.logger.Info(fmt.Sprintf("Generating test data: %d posts, %d users, %d channels", 
		b.config.NumPosts, b.config.NumUsers, b.config.NumChannels))
	
	// Create teams
	b.teams = []*model.Team{
		{
			Id:          model.NewId(),
			DisplayName: "Team Alpha",
			Name:        "team-alpha",
			Type:        model.TeamOpen,
		},
	}

	// Create users
	b.users = make([]*model.User, b.config.NumUsers)
	for i := 0; i < b.config.NumUsers; i++ {
		b.users[i] = &model.User{
			Id:       model.NewId(),
			Username: fmt.Sprintf("user%d", i),
			Email:    fmt.Sprintf("user%d@example.com", i),
		}
	}
	
	// Create channels
	b.channels = make([]*model.Channel, b.config.NumChannels)
	for i := 0; i < b.config.NumChannels; i++ {
		b.channels[i] = &model.Channel{
			Id:          model.NewId(),
			TeamId:      b.teams[0].Id,
			DisplayName: fmt.Sprintf("Channel %d", i),
			Name:        fmt.Sprintf("channel-%d", i),
			Type:        model.ChannelTypeOpen,
		}
	}
	
	// Create posts with search terms embedded in some of them
	searchTerms := b.config.SearchTerms
	b.posts = make([]*model.Post, b.config.NumPosts)
	
	for i := 0; i < b.config.NumPosts; i++ {
		userId := b.users[rand.Intn(len(b.users))].Id
		channelId := b.channels[rand.Intn(len(b.channels))].Id
		
		// Decide if this post should contain a search term
		var message string
		if rand.Intn(10) < 3 { // 30% chance to include a search term
			term := searchTerms[rand.Intn(len(searchTerms))]
			message = fmt.Sprintf("This is post %d with %s content", i, term)
			
			// Track this post as an expected hit for this term
			if _, ok := b.expectedHits[term]; !ok {
				b.expectedHits[term] = []string{}
			}
			postId := model.NewId()
			b.expectedHits[term] = append(b.expectedHits[term], postId)
			
			b.posts[i] = &model.Post{
				Id:        postId,
				UserId:    userId,
				ChannelId: channelId,
				Message:   message,
				CreateAt:  model.GetMillis() - int64(b.config.NumPosts-i)*1000, // Older to newer
			}
		} else {
			message = fmt.Sprintf("This is post %d with no search terms", i)
			b.posts[i] = &model.Post{
				Id:        model.NewId(),
				UserId:    userId,
				ChannelId: channelId,
				Message:   message,
				CreateAt:  model.GetMillis() - int64(b.config.NumPosts-i)*1000,
			}
		}
	}
}

// indexPostsInElasticsearch indexes all test posts in Elasticsearch
func (b *ElasticsearchBenchmark) indexPostsInElasticsearch() error {
	var wg sync.WaitGroup
	errChan := make(chan error, len(b.posts))
	
	// Use a worker pool to index posts in parallel
	workers := 10
	postChan := make(chan *model.Post, workers)
	
	// Start workers
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for post := range postChan {
				if err := b.esEngine.IndexPost(post, b.teams[0].Id); err != nil {
					errChan <- err
					return
				}
			}
		}()
	}
	
	// Send posts to workers
	for _, post := range b.posts {
		postChan <- post
	}
	close(postChan)
	
	// Wait for all workers to finish
	wg.Wait()
	close(errChan)
	
	// Check for errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}
	
	return nil
}

// indexPostsInBleve indexes all test posts in Bleve
func (b *ElasticsearchBenchmark) indexPostsInBleve() error {
	var wg sync.WaitGroup
	errChan := make(chan error, len(b.posts))
	
	// Use a worker pool to index posts in parallel
	workers := 10
	postChan := make(chan *model.Post, workers)
	
	// Start workers
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for post := range postChan {
				if err := b.bleveEngine.IndexPost(post, b.teams[0].Id); err != nil {
					errChan <- err
					return
				}
			}
		}()
	}
	
	// Send posts to workers
	for _, post := range b.posts {
		postChan <- post
	}
	close(postChan)
	
	// Wait for all workers to finish
	wg.Wait()
	close(errChan)
	
	// Check for errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}
	
	return nil
}

// simulateSQLSearch simulates a SQL-based search (direct string matching)
func (b *ElasticsearchBenchmark) simulateSQLSearch(term string) []string {
	results := []string{}
	
	// Simple string matching to simulate SQL LIKE queries
	for _, post := range b.posts {
		if strings.Contains(strings.ToLower(post.Message), strings.ToLower(term)) {
			results = append(results, post.Id)
		}
	}
	
	return results
}

// saveResultsToCSV saves benchmark results to a CSV file
func (b *ElasticsearchBenchmark) saveResultsToCSV(results []BenchmarkResult) error {
	if b.config.OutputFile == "" {
		b.config.OutputFile = fmt.Sprintf("search_benchmark_%d.csv", time.Now().Unix())
	}
	
	file, err := os.Create(b.config.OutputFile)
	if err != nil {
		return err
	}
	defer file.Close()
	
	writer := csv.NewWriter(file)
	defer writer.Flush()
	
	// Write header
	header := []string{"Engine", "Term", "ResponseTime(ms)", "ResultCount", "MemoryUsage(bytes)", "CPUUsage", "Accuracy", "IndexingTime(ms)"}
	if err := writer.Write(header); err != nil {
		return err
	}
	
	// Write results
	for _, result := range results {
		row := []string{
			result.Engine,
			result.Term,
			strconv.FormatFloat(result.ResponseTime, 'f', 2, 64),
			strconv.Itoa(result.ResultCount),
			strconv.FormatInt(result.MemoryUsage, 10),
			strconv.FormatFloat(result.CPUUsage, 'f', 2, 64),
			strconv.FormatFloat(result.Accuracy, 'f', 2, 64),
			strconv.FormatFloat(result.IndexingTime, 'f', 2, 64),
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	
	return nil
}

// Helper functions
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

func averageInt(values []int) int {
	if len(values) == 0 {
		return 0
	}
	
	var sum int
	for _, v := range values {
		sum += v
	}
	return sum / len(values)
}

func introduceTypo(s string) string {
	if len(s) <= 1 {
		return s
	}
	
	// Simple typo: swap two adjacent characters
	pos := rand.Intn(len(s) - 1)
	chars := []rune(s)
	chars[pos], chars[pos+1] = chars[pos+1], chars[pos]
	
	return string(chars)
}

func extractChannelIds(channels model.ChannelList) []string {
	ids := make([]string, len(channels))
	for i, channel := range channels {
		ids[i] = channel.Id
	}
	return ids
} 