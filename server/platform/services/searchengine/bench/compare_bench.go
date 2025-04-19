package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/mattermost/mattermost/server/v8/platform/services/searchengine/bleveengine"
	"github.com/mattermost/mattermost/server/v8/platform/services/searchengine/elasticsearchengine"
)

const (
	DEFAULT_ES_URL      = "http://localhost:9200"
	DEFAULT_NUM_POSTS   = 1000
	DEFAULT_NUM_USERS   = 10
	DEFAULT_NUM_CHANNELS = 5 
	DEFAULT_ITERATIONS  = 5
	DEFAULT_OUTPUT_FILE = "search_comparison_results.csv"
)

var (
	esURL        string
	numPosts     int
	numUsers     int
	numChannels  int
	iterations   int
	outputFile   string
	searchTerms  string
	fuzzySearch  bool
)

type BenchResult struct {
	Engine      string
	Query       string
	NumPosts    int
	ExecTimeMs  float64
	ResultsFound int
	IsFuzzy     bool
}

func init() {
	flag.StringVar(&esURL, "es-url", DEFAULT_ES_URL, "Elasticsearch URL")
	flag.IntVar(&numPosts, "posts", DEFAULT_NUM_POSTS, "Number of posts to index")
	flag.IntVar(&numUsers, "users", DEFAULT_NUM_USERS, "Number of users to generate")
	flag.IntVar(&numChannels, "channels", DEFAULT_NUM_CHANNELS, "Number of channels to generate")
	flag.IntVar(&iterations, "iter", DEFAULT_ITERATIONS, "Number of iterations for each search")
	flag.StringVar(&outputFile, "out", DEFAULT_OUTPUT_FILE, "Output CSV file for results")
	flag.StringVar(&searchTerms, "terms", "important,urgent,meeting,project,deadline", "Comma-separated search terms")
	flag.BoolVar(&fuzzySearch, "fuzzy", true, "Enable fuzzy search testing")
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
	
	// Setup logger
	logger := mlog.CreateConsoleLogger(true, mlog.LvlInfo)
	defer logger.Shutdown()
	
	// Generate test data
	fmt.Println("Generating test data...")
	posts, users, channels := generateTestData(r, numPosts, numUsers, numChannels)
	
	// Initialize Elasticsearch engine
	fmt.Println("Initializing Elasticsearch engine...")
	esEngine, err := elasticsearchengine.NewElasticsearchEngine(esURL, "mattermostsst_", nil, logger)
	if err != nil {
		fmt.Printf("Failed to initialize Elasticsearch: %v\n", err)
		return
	}
	
	// Initialize Bleve engine
	fmt.Println("Initializing Bleve engine...")
	bleveConfig := &model.Config{}
	bleveConfig.BleveSettings.EnableIndexing = model.NewBool(true)
	bleveConfig.BleveSettings.EnableSearching = model.NewBool(true)
	bleveConfig.BleveSettings.IndexDir = model.NewString("./bleve_bench")
	bleveEngine := bleveengine.NewBleveEngine(bleveConfig)
	
	// Start both engines
	esEngine.Start()
	bleveEngine.Start()
	defer esEngine.Stop()
	defer bleveEngine.Stop()
	
	// Index test data in both engines
	fmt.Println("Indexing test data in search engines...")
	
	// Index in Elasticsearch
	for _, post := range posts {
		teamId := getTeamIdForChannel(channels, post.ChannelId)
		if err := esEngine.IndexPost(post, teamId); err != nil {
			fmt.Printf("Error indexing post in Elasticsearch: %v\n", err)
		}
	}
	
	// Index in Bleve
	for _, post := range posts {
		teamId := getTeamIdForChannel(channels, post.ChannelId)
		if err := bleveEngine.IndexPost(post, teamId); err != nil {
			fmt.Printf("Error indexing post in Bleve: %v\n", err)
		}
	}
	
	// Give time for indexing to complete
	fmt.Println("Waiting for indexing to complete...")
	time.Sleep(2 * time.Second)
	
	// Refresh Elasticsearch indexes
	esEngine.RefreshIndexes(nil)
	
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
	fmt.Println("\nRunning benchmarks with exact terms...")
	for _, term := range terms {
		// Elasticsearch exact search
		esResults := runElasticsearchSearch(esEngine, channels, term, iterations, false)
		allResults = append(allResults, esResults...)
		
		// Bleve exact search  
		bleveResults := runBleveSearch(bleveEngine, channels, term, iterations, false)
		allResults = append(allResults, bleveResults...)
	}
	
	// Run benchmarks for fuzzy terms if enabled
	if fuzzySearch {
		fmt.Println("\nRunning benchmarks with typos to test fuzzy search...")
		for i, fuzzyTerm := range fuzzyTerms {
			fmt.Printf("Original term: '%s', Fuzzy term: '%s'\n", terms[i], fuzzyTerm)
			
			// Elasticsearch fuzzy search
			esResults := runElasticsearchSearch(esEngine, channels, fuzzyTerm, iterations, true)
			allResults = append(allResults, esResults...)
			
			// Bleve fuzzy search
			bleveResults := runBleveSearch(bleveEngine, channels, fuzzyTerm, iterations, true)
			allResults = append(allResults, bleveResults...)
		}
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

func getTeamIdForChannel(channels []*model.Channel, channelId string) string {
	for _, channel := range channels {
		if channel.Id == channelId {
			return channel.TeamId
		}
	}
	return ""
}

func runElasticsearchSearch(esEngine *elasticsearchengine.ElasticsearchEngine, channels []*model.Channel, term string, iterations int, isFuzzy bool) []BenchResult {
	results := []BenchResult{}
	
	for i := 0; i < iterations; i++ {
		// Create search params
		params := []*model.SearchParams{
			{
				Terms:          term,
				IsHashtag:      false,
				OrTerms:        false,
				IncludeDeleted: false,
				TimeZoneOffset: 0,
			},
		}
		
		// Execute search
		startTime := time.Now()
		postIds, _, err := esEngine.SearchPosts(model.ChannelList(channels), params, 0, 100)
		execTime := time.Since(startTime).Milliseconds()
		
		if err != nil {
			fmt.Printf("Error searching Elasticsearch: %v\n", err)
			continue
		}
		
		fmt.Printf("Elasticsearch search for '%s' (fuzzy: %t): %dms, %d results\n", 
			term, isFuzzy, execTime, len(postIds))
		
		results = append(results, BenchResult{
			Engine:       "Elasticsearch",
			Query:        term,
			NumPosts:     numPosts,
			ExecTimeMs:   float64(execTime),
			ResultsFound: len(postIds),
			IsFuzzy:      isFuzzy,
		})
	}
	
	return results
}

func runBleveSearch(bleveEngine *bleveengine.BleveEngine, channels []*model.Channel, term string, iterations int, isFuzzy bool) []BenchResult {
	results := []BenchResult{}
	
	for i := 0; i < iterations; i++ {
		// Create search params
		params := []*model.SearchParams{
			{
				Terms:          term,
				IsHashtag:      false,
				OrTerms:        false,
				IncludeDeleted: false,
				TimeZoneOffset: 0,
			},
		}
		
		// Execute search
		startTime := time.Now()
		postIds, _, err := bleveEngine.SearchPosts(model.ChannelList(channels), params, 0, 100)
		execTime := time.Since(startTime).Milliseconds()
		
		if err != nil {
			fmt.Printf("Error searching Bleve: %v\n", err)
			continue
		}
		
		fmt.Printf("Bleve search for '%s' (fuzzy: %t): %dms, %d results\n", 
			term, isFuzzy, execTime, len(postIds))
		
		results = append(results, BenchResult{
			Engine:       "Bleve",
			Query:        term,
			NumPosts:     numPosts,
			ExecTimeMs:   float64(execTime),
			ResultsFound: len(postIds),
			IsFuzzy:      isFuzzy,
		})
	}
	
	return results
}

func generateTestData(r *rand.Rand, numPosts, numUsers, numChannels int) ([]*model.Post, []*model.User, []*model.Channel) {
	// Generate users
	users := make([]*model.User, numUsers)
	for i := 0; i < numUsers; i++ {
		users[i] = &model.User{
			Id:       model.NewId(),
			Username: fmt.Sprintf("user%d", i),
			Email:    fmt.Sprintf("user%d@example.com", i),
		}
	}
	
	// Generate teams
	teamId := model.NewId()
	
	// Generate channels
	channels := make([]*model.Channel, numChannels)
	for i := 0; i < numChannels; i++ {
		channels[i] = &model.Channel{
			Id:     model.NewId(),
			TeamId: teamId,
			Type:   model.ChannelTypeOpen,
			Name:   fmt.Sprintf("channel-%d", i),
		}
	}
	
	// Generate posts
	posts := make([]*model.Post, numPosts)
	keywords := []string{"important", "urgent", "meeting", "project", "deadline", "review"}
	
	for i := 0; i < numPosts; i++ {
		// Decide if this post should contain a search term
		containsKeyword := r.Intn(10) < 3 // 30% of posts have keywords
		
		var message string
		if containsKeyword {
			keyword := keywords[r.Intn(len(keywords))]
			if r.Intn(10) < 2 { // 20% of these have typos
				// Introduce a simple typo
				keywordRunes := []rune(keyword)
				pos := r.Intn(len(keywordRunes) - 2) + 1
				keywordRunes[pos] = getRandomSimilarChar(keywordRunes[pos])
				keyword = string(keywordRunes)
			}
			message = fmt.Sprintf("This is post %d containing %s content for search testing", i, keyword)
		} else {
			message = fmt.Sprintf("This is regular post %d without any special keywords for search", i)
		}
		
		channelId := channels[r.Intn(numChannels)].Id
		userId := users[r.Intn(numUsers)].Id
		
		posts[i] = &model.Post{
			Id:        model.NewId(),
			ChannelId: channelId,
			UserId:    userId,
			Message:   message,
			CreateAt:  model.GetMillis() - int64(numPosts-i)*1000,
			UpdateAt:  model.GetMillis() - int64(numPosts-i)*1000,
		}
	}
	
	return posts, users, channels
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
	// Group results by engine and fuzzy status
	esExactTimes := []float64{}
	esFuzzyTimes := []float64{}
	esExactResults := []int{}
	esFuzzyResults := []int{}
	
	bleveExactTimes := []float64{}
	bleveFuzzyTimes := []float64{}
	bleveExactResults := []int{}
	bleveFuzzyResults := []int{}
	
	for _, result := range results {
		if result.Engine == "Elasticsearch" {
			if result.IsFuzzy {
				esFuzzyTimes = append(esFuzzyTimes, result.ExecTimeMs)
				esFuzzyResults = append(esFuzzyResults, result.ResultsFound)
			} else {
				esExactTimes = append(esExactTimes, result.ExecTimeMs)
				esExactResults = append(esExactResults, result.ResultsFound)
			}
		} else if result.Engine == "Bleve" {
			if result.IsFuzzy {
				bleveFuzzyTimes = append(bleveFuzzyTimes, result.ExecTimeMs)
				bleveFuzzyResults = append(bleveFuzzyResults, result.ResultsFound)
			} else {
				bleveExactTimes = append(bleveExactTimes, result.ExecTimeMs)
				bleveExactResults = append(bleveExactResults, result.ResultsFound)
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
	fmt.Printf("%-15s %-15s %-15.2f %-15.2f\n", "Bleve", "Exact", average(bleveExactTimes), average(floatFromInts(bleveExactResults)))
	fmt.Printf("%-15s %-15s %-15.2f %-15.2f\n", "Bleve", "Fuzzy", average(bleveFuzzyTimes), average(floatFromInts(bleveFuzzyResults)))
	fmt.Println("----------------------------------------------------")
	
	// Performance comparison
	fmt.Println("\nPerformance Comparison:")
	
	// Exact search comparison
	esExactAvg := average(esExactTimes)
	bleveExactAvg := average(bleveExactTimes)
	
	if esExactAvg == 0 || bleveExactAvg == 0 {
		fmt.Println("Can't compare performance - not enough data")
	} else if esExactAvg < bleveExactAvg {
		fmt.Printf("Elasticsearch is %.2f%% faster than Bleve for exact searches\n", 
			100 * (bleveExactAvg - esExactAvg) / bleveExactAvg)
	} else {
		fmt.Printf("Bleve is %.2f%% faster than Elasticsearch for exact searches\n", 
			100 * (esExactAvg - bleveExactAvg) / esExactAvg)
	}
	
	// Fuzzy search comparison
	if fuzzySearch {
		esFuzzyAvg := average(esFuzzyTimes)
		bleveFuzzyAvg := average(bleveFuzzyTimes)
		
		if esFuzzyAvg == 0 || bleveFuzzyAvg == 0 {
			fmt.Println("Can't compare fuzzy performance - not enough data")
		} else if esFuzzyAvg < bleveFuzzyAvg {
			fmt.Printf("Elasticsearch is %.2f%% faster than Bleve for fuzzy searches\n", 
				100 * (bleveFuzzyAvg - esFuzzyAvg) / bleveFuzzyAvg)
		} else {
			fmt.Printf("Bleve is %.2f%% faster than Elasticsearch for fuzzy searches\n", 
				100 * (esFuzzyAvg - bleveFuzzyAvg) / esFuzzyAvg)
		}
		
		// Accuracy comparison for fuzzy searches
		esFuzzyResultsAvg := average(floatFromInts(esFuzzyResults))
		bleveFuzzyResultsAvg := average(floatFromInts(bleveFuzzyResults))
		
		fmt.Println("\nFuzzy Search Accuracy Comparison:")
		if esFuzzyResultsAvg > bleveFuzzyResultsAvg {
			fmt.Printf("Elasticsearch found %.2f%% more results than Bleve with fuzzy searches\n", 
				100 * (esFuzzyResultsAvg - bleveFuzzyResultsAvg) / bleveFuzzyResultsAvg)
		} else if bleveFuzzyResultsAvg > esFuzzyResultsAvg {
			fmt.Printf("Bleve found %.2f%% more results than Elasticsearch with fuzzy searches\n", 
				100 * (bleveFuzzyResultsAvg - esFuzzyResultsAvg) / esFuzzyResultsAvg)
		} else {
			fmt.Println("Both engines found the same number of results with fuzzy searches")
		}
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