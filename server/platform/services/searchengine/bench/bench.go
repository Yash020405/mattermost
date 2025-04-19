// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/mattermost/mattermost/server/v8/channels/store"
	"github.com/mattermost/mattermost/server/v8/channels/store/storetest"
	"github.com/mattermost/mattermost/server/v8/platform/services/searchengine/elasticsearchengine"
)

const (
	DEFAULT_ES_URL     = "http://localhost:9200"
	DEFAULT_NUM_POSTS  = 10000
	DEFAULT_NUM_USERS  = 100
	DEFAULT_NUM_TEAMS  = 3
	DEFAULT_NUM_CHANNELS = 20
	DEFAULT_ITERATIONS = 10
	DEFAULT_QUERY     = "search term"
	DEFAULT_OUTPUT_FILE = "search_bench_results.csv"
)

type benchResult struct {
	searchType    string
	query         string
	numPosts      int
	execTimeMs    float64
	resultsFound  int
}

var (
	esURL       string
	numPosts    int
	numUsers    int
	numTeams    int
	numChannels int
	iterations  int
	queries     string
	outputFile  string
)

func init() {
	flag.StringVar(&esURL, "es-url", DEFAULT_ES_URL, "Elasticsearch URL")
	flag.IntVar(&numPosts, "posts", DEFAULT_NUM_POSTS, "Number of posts to generate")
	flag.IntVar(&numUsers, "users", DEFAULT_NUM_USERS, "Number of users to generate")
	flag.IntVar(&numTeams, "teams", DEFAULT_NUM_TEAMS, "Number of teams to generate")
	flag.IntVar(&numChannels, "channels", DEFAULT_NUM_CHANNELS, "Number of channels to generate")
	flag.IntVar(&iterations, "iter", DEFAULT_ITERATIONS, "Number of iterations for each search")
	flag.StringVar(&queries, "queries", DEFAULT_QUERY, "Comma-separated list of search queries")
	flag.StringVar(&outputFile, "out", DEFAULT_OUTPUT_FILE, "Output CSV file for results")
}

func main() {
	flag.Parse()

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
	headers := []string{"Search Type", "Query", "Num Posts", "Execution Time (ms)", "Results Found"}
	if err := writer.Write(headers); err != nil {
		fmt.Printf("Error writing CSV headers: %v\n", err)
		return
	}

	// Set up logger
	logger := mlog.CreateConsoleLogger(true, mlog.LvlInfo)
	defer logger.Shutdown()

	// Generate test data
	fmt.Println("Generating test data...")
	posts, users, teams, channels := generateTestData(numPosts, numUsers, numTeams, numChannels)

	// Initialize search store
	searchStore := storetest.NewMockSearchStore()
	for _, post := range posts {
		searchStore.On("SearchPostsInTeam", post.TeamId, model.SearchParams{Terms: post.Message}, false, 0, 100).Return([]*model.Post{post}, nil)
	}

	// Initialize Elasticsearch engine
	esEngine, err := elasticsearchengine.CreateTestEngine(logger, esURL)
	if err != nil {
		fmt.Printf("Failed to initialize Elasticsearch: %v\n", err)
		return
	}
	defer esEngine.Stop()

	// Index test data in Elasticsearch
	fmt.Println("Indexing test data in Elasticsearch...")
	for _, post := range posts {
		if err := esEngine.IndexPost(post, post.TeamId); err != nil {
			fmt.Printf("Failed to index post in Elasticsearch: %v\n", err)
			return
		}
	}

	// Give time for indexing to complete and refresh
	fmt.Println("Waiting for indexing to complete...")
	time.Sleep(2 * time.Second)
	esEngine.RefreshIndexes(nil)

	// Parse search queries
	searchQueries := strings.Split(queries, ",")

	// Run benchmarks
	fmt.Println("Running benchmarks...")
	allResults := []benchResult{}

	for _, query := range searchQueries {
		query = strings.TrimSpace(query)
		
		// Run SQL search
		sqlResults := runSQLSearchBench(searchStore, teams, channels, query, iterations)
		allResults = append(allResults, sqlResults...)
		
		// Run Elasticsearch search
		esResults := runESSearchBench(esEngine, teams, channels, query, iterations)
		allResults = append(allResults, esResults...)
	}

	// Write results to CSV
	for _, result := range allResults {
		row := []string{
			result.searchType,
			result.query,
			strconv.Itoa(result.numPosts),
			fmt.Sprintf("%.2f", result.execTimeMs),
			strconv.Itoa(result.resultsFound),
		}
		
		if err := writer.Write(row); err != nil {
			fmt.Printf("Error writing CSV row: %v\n", err)
			return
		}
	}

	fmt.Printf("Benchmark completed. Results written to %s\n", outputFile)
}

func runSQLSearchBench(searchStore *storetest.MockSearchStore, teams []*model.Team, channels []*model.Channel, query string, iterations int) []benchResult {
	results := []benchResult{}
	
	for i := 0; i < iterations; i++ {
		// Select a random team for this iteration
		team := teams[rand.Intn(len(teams))]
		
		// Generate channel list
		channelList := model.ChannelList{}
		for _, channel := range channels {
			if channel.TeamId == team.Id {
				channelList = append(channelList, channel)
			}
		}
		
		if len(channelList) == 0 {
			continue
		}
		
		// Create search params
		params := []*model.SearchParams{
			{
				Terms: query,
			},
		}
		
		// Execute SQL search with timing
		startTime := time.Now()
		posts, _, _ := store.SearchPostsInTeamForUserWithSearchParams(searchStore, team.Id, "user", params, 0, 100, true)
		duration := time.Since(startTime).Milliseconds()
		
		results = append(results, benchResult{
			searchType:   "SQL",
			query:        query,
			numPosts:     numPosts,
			execTimeMs:   float64(duration),
			resultsFound: len(posts),
		})
	}
	
	return results
}

func runESSearchBench(esEngine *elasticsearchengine.ElasticsearchEngine, teams []*model.Team, channels []*model.Channel, query string, iterations int) []benchResult {
	results := []benchResult{}
	
	for i := 0; i < iterations; i++ {
		// Select a random team for this iteration
		team := teams[rand.Intn(len(teams))]
		
		// Generate channel list
		channelList := model.ChannelList{}
		for _, channel := range channels {
			if channel.TeamId == team.Id {
				channelList = append(channelList, channel)
			}
		}
		
		if len(channelList) == 0 {
			continue
		}
		
		// Create search params
		params := []*model.SearchParams{
			{
				Terms: query,
			},
		}
		
		// Execute Elasticsearch search with timing
		startTime := time.Now()
		postIds, _, _ := esEngine.SearchPosts(channelList, params, 0, 100)
		duration := time.Since(startTime).Milliseconds()
		
		results = append(results, benchResult{
			searchType:   "Elasticsearch",
			query:        query,
			numPosts:     numPosts,
			execTimeMs:   float64(duration),
			resultsFound: len(postIds),
		})
	}
	
	return results
}

func generateTestData(numPosts, numUsers, numTeams, numChannels int) ([]*model.Post, []*model.User, []*model.Team, []*model.Channel) {
	// Generate users
	users := make([]*model.User, numUsers)
	for i := 0; i < numUsers; i++ {
		users[i] = &model.User{
			Id:       model.NewId(),
			Username: fmt.Sprintf("user%d", i),
		}
	}
	
	// Generate teams
	teams := make([]*model.Team, numTeams)
	for i := 0; i < numTeams; i++ {
		teams[i] = &model.Team{
			Id:          model.NewId(),
			DisplayName: fmt.Sprintf("Team %d", i),
			Name:        fmt.Sprintf("team-%d", i),
		}
	}
	
	// Generate channels
	channels := make([]*model.Channel, numChannels)
	for i := 0; i < numChannels; i++ {
		teamIndex := i % numTeams
		channels[i] = &model.Channel{
			Id:          model.NewId(),
			TeamId:      teams[teamIndex].Id,
			DisplayName: fmt.Sprintf("Channel %d", i),
			Name:        fmt.Sprintf("channel-%d", i),
			Type:        model.ChannelTypeOpen,
		}
	}
	
	// Generate posts with content that includes search terms
	posts := make([]*model.Post, numPosts)
	searchTerms := []string{"important", "urgent", "meeting", "discussion", "project", "deadline", "review", "feedback", "update", "status"}
	
	for i := 0; i < numPosts; i++ {
		userIndex := i % numUsers
		channelIndex := i % numChannels
		
		// Randomly include some search terms
		var message string
		if rand.Intn(10) < 3 { // 30% of posts will have search terms
			term := searchTerms[rand.Intn(len(searchTerms))]
			message = fmt.Sprintf("This is post %d with %s content", i, term)
		} else {
			message = fmt.Sprintf("This is post %d with regular content", i)
		}
		
		posts[i] = &model.Post{
			Id:        model.NewId(),
			UserId:    users[userIndex].Id,
			ChannelId: channels[channelIndex].Id,
			TeamId:    channels[channelIndex].TeamId,
			Message:   message,
			CreateAt:  model.GetMillis() - int64(numPosts-i)*1000, // Older to newer
		}
	}
	
	return posts, users, teams, channels
}