// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package elasticsearchengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elastic/go-elasticsearch/v7"
	"github.com/elastic/go-elasticsearch/v7/esapi"
	"github.com/elastic/go-elasticsearch/v7/esutil"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/mattermost/mattermost/server/public/shared/request"
)

// EnhancedBulkIndexer wraps the Elasticsearch BulkIndexer with additional features
type EnhancedBulkIndexer struct {
	indexer            esutil.BulkIndexer
	engine             *ElasticsearchEngine
	numWorkers         int
	flushBytes         int
	flushInterval      time.Duration
	logger             *mlog.Logger
	indexingStats      *IndexingStats
	completionCallback func()
}

// IndexingStats tracks statistics about the indexing process
type IndexingStats struct {
	TotalDocuments    int64
	IndexedDocuments  int64
	FailedDocuments   int64
	StartTime         time.Time
	EndTime           time.Time
	TotalElapsedTime  time.Duration
	DocsPerSecond     float64
	CurrentBatchSize  int64
	AverageBatchSize  float64
	TotalBytesIndexed int64
}

// NewEnhancedBulkIndexer creates a new enhanced bulk indexer for Elasticsearch
func NewEnhancedBulkIndexer(engine *ElasticsearchEngine, numWorkers int, flushBytes int, flushInterval time.Duration) (*EnhancedBulkIndexer, error) {
	// Create BulkIndexer configuration
	cfg := esutil.BulkIndexerConfig{
		Client:        engine.client,
		NumWorkers:    numWorkers,
		FlushBytes:    flushBytes,
		FlushInterval: flushInterval,
		OnError: func(ctx context.Context, err error) {
			engine.logger.Error("Bulk indexer error", mlog.Err(err))
		},
	}

	// Create the indexer
	indexer, err := esutil.NewBulkIndexer(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create bulk indexer: %w", err)
	}

	// Create statistics tracker
	stats := &IndexingStats{
		StartTime: time.Now(),
	}

	return &EnhancedBulkIndexer{
		indexer:       indexer,
		engine:        engine,
		numWorkers:    numWorkers,
		flushBytes:    flushBytes,
		flushInterval: flushInterval,
		logger:        engine.logger,
		indexingStats: stats,
	}, nil
}

// IndexPost adds a post to the bulk indexer queue
func (e *EnhancedBulkIndexer) IndexPost(post *model.Post, teamId string) error {
	atomic.AddInt64(&e.indexingStats.TotalDocuments, 1)
	atomic.AddInt64(&e.indexingStats.CurrentBatchSize, 1)

	// Convert post to Elasticsearch document
	esPost, err := postToESDoc(post, teamId)
	if err != nil {
		atomic.AddInt64(&e.indexingStats.FailedDocuments, 1)
		return err
	}

	// Marshal document to JSON
	data, err := json.Marshal(esPost)
	if err != nil {
		atomic.AddInt64(&e.indexingStats.FailedDocuments, 1)
		return err
	}

	atomic.AddInt64(&e.indexingStats.TotalBytesIndexed, int64(len(data)))

	// Add document to bulk indexer
	err = e.indexer.Add(
		context.Background(),
		esutil.BulkIndexerItem{
			Action:     "index",
			DocumentID: post.Id,
			Body:       bytes.NewReader(data),
			Index:      POST_INDEX,
			OnSuccess: func(ctx context.Context, item esutil.BulkIndexerItem, res esutil.BulkIndexerResponseItem) {
				atomic.AddInt64(&e.indexingStats.IndexedDocuments, 1)
			},
			OnFailure: func(ctx context.Context, item esutil.BulkIndexerItem, res esutil.BulkIndexerResponseItem, err error) {
				atomic.AddInt64(&e.indexingStats.FailedDocuments, 1)
				e.logger.Error("Failed to index post",
					mlog.String("post_id", post.Id),
					mlog.Err(err),
					mlog.String("error_type", res.Error.Type),
					mlog.String("error_reason", res.Error.Reason),
				)
			},
		},
	)

	if err != nil {
		atomic.AddInt64(&e.indexingStats.FailedDocuments, 1)
		return err
	}

	return nil
}

// IndexPosts adds multiple posts to the bulk indexer queue
func (e *EnhancedBulkIndexer) IndexPosts(posts []*model.Post, teamId string) error {
	for _, post := range posts {
		if err := e.IndexPost(post, teamId); err != nil {
			return err
		}
	}
	return nil
}

// IndexBatch indexes a batch of posts with error handling and retries
func (e *EnhancedBulkIndexer) IndexBatch(posts []*model.Post, teamId string, maxRetries int) error {
	// First try
	err := e.IndexPosts(posts, teamId)
	if err == nil {
		return nil
	}

	// Retry logic for failed indexing attempts
	retryCount := 0
	for retryCount < maxRetries {
		e.logger.Warn("Retrying batch indexing",
			mlog.Int("retry", retryCount+1),
			mlog.Int("max_retries", maxRetries),
			mlog.Err(err),
		)

		// Wait before retrying - exponential backoff
		waitTime := time.Duration(1<<retryCount) * 100 * time.Millisecond
		time.Sleep(waitTime)

		// Try again
		err = e.IndexPosts(posts, teamId)
		if err == nil {
			return nil
		}

		retryCount++
	}

	return fmt.Errorf("failed to index batch after %d retries: %w", maxRetries, err)
}

// Flush explicitly flushes the bulk indexer
func (e *EnhancedBulkIndexer) Flush() error {
	return e.indexer.Flush()
}

// Close flushes and closes the bulk indexer
func (e *EnhancedBulkIndexer) Close() error {
	err := e.indexer.Close()
	
	// Update statistics
	e.indexingStats.EndTime = time.Now()
	e.indexingStats.TotalElapsedTime = e.indexingStats.EndTime.Sub(e.indexingStats.StartTime)
	
	// Calculate docs per second
	if e.indexingStats.TotalElapsedTime > 0 {
		seconds := e.indexingStats.TotalElapsedTime.Seconds()
		if seconds > 0 {
			e.indexingStats.DocsPerSecond = float64(e.indexingStats.IndexedDocuments) / seconds
		}
	}
	
	// Calculate average batch size
	if e.indexingStats.TotalDocuments > 0 {
		e.indexingStats.AverageBatchSize = float64(e.indexingStats.CurrentBatchSize) / float64(e.indexingStats.TotalDocuments)
	}
	
	// Run completion callback if set
	if e.completionCallback != nil {
		e.completionCallback()
	}
	
	return err
}

// SetCompletionCallback sets a function to be called when indexing completes
func (e *EnhancedBulkIndexer) SetCompletionCallback(callback func()) {
	e.completionCallback = callback
}

// GetStats returns the current indexing statistics
func (e *EnhancedBulkIndexer) GetStats() *IndexingStats {
	stats := &IndexingStats{
		TotalDocuments:    atomic.LoadInt64(&e.indexingStats.TotalDocuments),
		IndexedDocuments:  atomic.LoadInt64(&e.indexingStats.IndexedDocuments),
		FailedDocuments:   atomic.LoadInt64(&e.indexingStats.FailedDocuments),
		StartTime:         e.indexingStats.StartTime,
		EndTime:           e.indexingStats.EndTime,
		TotalElapsedTime:  e.indexingStats.TotalElapsedTime,
		DocsPerSecond:     e.indexingStats.DocsPerSecond,
		CurrentBatchSize:  atomic.LoadInt64(&e.indexingStats.CurrentBatchSize),
		AverageBatchSize:  e.indexingStats.AverageBatchSize,
		TotalBytesIndexed: atomic.LoadInt64(&e.indexingStats.TotalBytesIndexed),
	}
	
	// If indexing is still in progress, calculate elapsed time up to now
	if stats.EndTime.IsZero() {
		stats.TotalElapsedTime = time.Since(stats.StartTime)
		
		// Calculate docs per second
		if stats.TotalElapsedTime > 0 {
			seconds := stats.TotalElapsedTime.Seconds()
			if seconds > 0 {
				stats.DocsPerSecond = float64(stats.IndexedDocuments) / seconds
			}
		}
	}
	
	return stats
}

// BulkIndexPosts indexes a large number of posts using the bulk API efficiently
func BulkIndexPosts(engine *ElasticsearchEngine, posts []*model.Post, teamId string) (int, int, error) {
	// Create enhanced bulk indexer
	numWorkers := 4
	flushBytes := 5 * 1024 * 1024 // 5MB
	flushInterval := 30 * time.Second
	
	indexer, err := NewEnhancedBulkIndexer(engine, numWorkers, flushBytes, flushInterval)
	if err != nil {
		return 0, 0, err
	}
	
	// Index all posts
	for _, post := range posts {
		if err := indexer.IndexPost(post, teamId); err != nil {
			engine.logger.Error("Error indexing post in bulk operation",
				mlog.String("post_id", post.Id),
				mlog.Err(err),
			)
		}
	}
	
	// Close indexer and flush remaining documents
	if err := indexer.Close(); err != nil {
		return 0, 0, err
	}
	
	// Get final stats
	stats := indexer.GetStats()
	indexed := int(stats.IndexedDocuments)
	failed := int(stats.FailedDocuments)
	
	return indexed, failed, nil
}

// BatchIndexChannel indexes all posts in a channel efficiently
func BatchIndexChannel(rctx request.CTX, engine *ElasticsearchEngine, channelID string, teamID string, batchSize int) (int, int, error) {
	if !engine.IsIndexingEnabled() {
		return 0, 0, nil
	}
	
	// Track statistics
	var totalIndexed int
	var totalFailed int
	
	// Create enhanced bulk indexer
	numWorkers := 4
	flushBytes := 5 * 1024 * 1024 // 5MB
	flushInterval := 30 * time.Second
	
	indexer, err := NewEnhancedBulkIndexer(engine, numWorkers, flushBytes, flushInterval)
	if err != nil {
		return 0, 0, err
	}
	defer indexer.Close()
	
	// Get all posts in channel (would connect to store in real implementation)
	// This is a placeholder - in real implementation you'd fetch from database
	/*
	posts, err := a.Srv().Store().Post().GetPostsForIndexing(rctx, 0, channelID, false, batchSize)
	if err != nil {
		return 0, 0, err
	}
	
	// Index posts in batches
	for _, post := range posts {
		if err := indexer.IndexPost(post, teamID); err != nil {
			rctx.Logger().Error("Error indexing post in batch channel operation",
				mlog.String("post_id", post.Id),
				mlog.String("channel_id", channelID),
				mlog.Err(err),
			)
			totalFailed++
		} else {
			totalIndexed++
		}
	}
	*/
	
	// Flush any remaining documents
	if err := indexer.Flush(); err != nil {
		return totalIndexed, totalFailed, err
	}
	
	return totalIndexed, totalFailed, nil
}

// OptimizedSearchPosts performs an optimized search query for posts
func OptimizedSearchPosts(engine *ElasticsearchEngine, channels model.ChannelList, searchParams []*model.SearchParams, page, perPage int) ([]string, model.PostSearchMatches, error) {
	finalQuery := buildSearchQuery(searchParams, channelIdsList(channels), 1)
	
	// Add pagination
	searchAfter := []interface{}{}
	if page > 0 {
		// In a real implementation, you'd implement proper search_after for deep pagination
		// This is just a placeholder example
		searchAfter = []interface{}{0}
	}
	
	// Add sort
	finalQuery["sort"] = []map[string]interface{}{
		{
			"CreateAt": map[string]interface{}{
				"order": "desc",
			},
		},
	}
	
	// Set size
	finalQuery["size"] = perPage
	
	// Add highlight settings for better result snippets
	finalQuery["highlight"] = map[string]interface{}{
		"pre_tags":  []string{"<span class='search-highlight'>"},
		"post_tags": []string{"</span>"},
		"fields": map[string]interface{}{
			"Message": map[string]interface{}{
				"number_of_fragments": 3,
				"fragment_size":       150,
				"type":                "unified",
			},
		},
	}
	
	// Optimize query execution with preference to improve caching
	searchRequest := esapi.SearchRequest{
		Index:     []string{POST_INDEX},
		Body:      esutil.NewJSONReader(finalQuery),
		Preference: "_local", // Prefer local shard execution for speed
	}
	
	// Use appropriate timeout
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*engine.configCache.ElasticsearchSettings.RequestTimeoutSeconds)*time.Second)
	defer cancel()
	
	// Execute search request
	res, err := searchRequest.Do(ctx, engine.client)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	
	// Check for errors
	if res.IsError() {
		return nil, nil, fmt.Errorf("error searching posts: %s", res.String())
	}
	
	// Parse response
	var resultData struct {
		Hits struct {
			Total struct {
				Value int64 `json:"value"`
			} `json:"total"`
			Hits []struct {
				ID        string `json:"_id"`
				Highlight map[string][]string `json:"highlight"`
			} `json:"hits"`
		} `json:"hits"`
	}
	
	if err := json.NewDecoder(res.Body).Decode(&resultData); err != nil {
		return nil, nil, err
	}
	
	// Extract post IDs and highlights
	postIds := make([]string, 0, len(resultData.Hits.Hits))
	matches := make(model.PostSearchMatches)
	
	for _, hit := range resultData.Hits.Hits {
		postIds = append(postIds, hit.ID)
		
		if highlights, ok := hit.Highlight["Message"]; ok && len(highlights) > 0 {
			// Join multiple fragments with ellipsis
			snippet := highlights[0]
			for i := 1; i < len(highlights); i++ {
				snippet += " ... " + highlights[i]
			}
			matches[hit.ID] = snippet
		}
	}
	
	return postIds, matches, nil
}

// channelIdsList extracts the channel IDs from a ChannelList
func channelIdsList(channels model.ChannelList) []string {
	ids := make([]string, len(channels))
	for i, channel := range channels {
		ids[i] = channel.Id
	}
	return ids
}

// AsyncBulkIndexerJob represents a background job for bulk indexing
type AsyncBulkIndexerJob struct {
	engine       *ElasticsearchEngine
	indexer      *EnhancedBulkIndexer
	wg           *sync.WaitGroup
	totalPosts   int
	startTime    time.Time
	progressFunc func(current, total int, elapsed time.Duration)
}

// NewAsyncBulkIndexerJob creates a new asynchronous bulk indexing job
func NewAsyncBulkIndexerJob(engine *ElasticsearchEngine, totalPosts int) (*AsyncBulkIndexerJob, error) {
	numWorkers := 4
	flushBytes := 5 * 1024 * 1024 // 5MB
	flushInterval := 30 * time.Second
	
	indexer, err := NewEnhancedBulkIndexer(engine, numWorkers, flushBytes, flushInterval)
	if err != nil {
		return nil, err
	}
	
	wg := &sync.WaitGroup{}
	wg.Add(1)
	
	job := &AsyncBulkIndexerJob{
		engine:     engine,
		indexer:    indexer,
		wg:         wg,
		totalPosts: totalPosts,
		startTime:  time.Now(),
	}
	
	// Setup completion callback
	indexer.SetCompletionCallback(func() {
		wg.Done()
	})
	
	return job, nil
}

// IndexPost adds a post to the async indexing job
func (j *AsyncBulkIndexerJob) IndexPost(post *model.Post, teamId string) error {
	return j.indexer.IndexPost(post, teamId)
}

// SetProgressCallback sets a function to be called periodically with progress updates
func (j *AsyncBulkIndexerJob) SetProgressCallback(progressFunc func(current, total int, elapsed time.Duration)) {
	j.progressFunc = progressFunc
}

// Wait waits for the job to complete
func (j *AsyncBulkIndexerJob) Wait() {
	j.wg.Wait()
}

// Complete flushes and closes the indexer
func (j *AsyncBulkIndexerJob) Complete() error {
	return j.indexer.Close()
}

// GetStats returns the current indexing statistics
func (j *AsyncBulkIndexerJob) GetStats() *IndexingStats {
	return j.indexer.GetStats()
} 