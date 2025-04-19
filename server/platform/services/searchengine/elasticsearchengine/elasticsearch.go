// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package elasticsearchengine

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"strconv"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/elastic/go-elasticsearch/v8/esutil"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/mattermost/mattermost/server/public/shared/request"
	"github.com/mattermost/mattermost/server/v8/platform/services/searchengine"
)

const (
	ENGINE_NAME          = "elasticsearch"
	MAX_ELASTICSEARCH_VERSION = 8
	POST_INDEX           = "posts"
	USER_INDEX           = "users"
	CHANNEL_INDEX        = "channels"
	FILE_INDEX           = "files"
	DEFAULT_FUZZY_LEVEL   = 1  // Default fuzziness level for search
	REQUEST_TIMEOUT_SECONDS = 30
)

// ESPost represents a post stored in Elasticsearch
type ESPost struct {
	Id          string    `json:"id"`
	TeamId      string    `json:"team_id"`
	ChannelId   string    `json:"channel_id"`
	UserId      string    `json:"user_id"`
	Message     string    `json:"message"`
	Type        string    `json:"type"`
	CreateAt    int64     `json:"create_at"`
	UpdateAt    int64     `json:"update_at"`
	DeleteAt    int64     `json:"delete_at"`
	Hashtags    string    `json:"hashtags"`
}

// ESUser represents a user stored in Elasticsearch
type ESUser struct {
	Id            string   `json:"id"`
	Username      string   `json:"username"`
	Nickname      string   `json:"nickname"`
	FirstName     string   `json:"first_name"`
	LastName      string   `json:"last_name"`
	Email         string   `json:"email"`
	ChannelIds    []string `json:"channel_ids"`
	TeamIds       []string `json:"team_ids"`
	Roles         string   `json:"roles"`
	AllowMarketing bool     `json:"allow_marketing"`
	CreateAt      int64    `json:"create_at"`
	DeleteAt      int64    `json:"delete_at"`
}

// ESChannel represents a channel stored in Elasticsearch
type ESChannel struct {
	Id            string   `json:"id"`
	TeamId        string   `json:"team_id"`
	Type          string   `json:"type"` // Store as string instead of model.ChannelType
	DisplayName   string   `json:"display_name"`
	Name          string   `json:"name"`
	Header        string   `json:"header"`
	Purpose       string   `json:"purpose"`
	CreateAt      int64    `json:"create_at"`
	DeleteAt      int64    `json:"delete_at"`
}

// ESFile represents a file stored in Elasticsearch
type ESFile struct {
	Id          string `json:"id"`
	CreatorId   string `json:"creator_id"`
	PostId      string `json:"post_id"`
	ChannelId   string `json:"channel_id"`
	CreateAt    int64  `json:"create_at"`
	UpdateAt    int64  `json:"update_at"`
	DeleteAt    int64  `json:"delete_at"`
	Name        string `json:"name"`
	Extension   string `json:"extension"`
	Size        int64  `json:"size"`
	MimeType    string `json:"mime_type"`
	Content     string `json:"content"`
}

// ElasticsearchEngine provides an implementation of the SearchEngineInterface using Elasticsearch.
type ElasticsearchEngine struct {
	client        *elasticsearch.Client
	bulkProcessor esutil.BulkIndexer
	mutex         sync.RWMutex
	ready         int32
	version       int
	fullVersion   string
	configCache   *model.Config
	logger        *mlog.Logger
	plugins       []string
}

// NewElasticsearchEngine creates a new instance of the Elasticsearch engine.
func NewElasticsearchEngine(logger *mlog.Logger, cfg *model.Config) (*ElasticsearchEngine, error) {
	connectionUrl := *cfg.ElasticsearchSettings.ConnectionURL
	username := *cfg.ElasticsearchSettings.Username
	password := *cfg.ElasticsearchSettings.Password
	sniff := *cfg.ElasticsearchSettings.Sniff
	
	config := elasticsearch.Config{
		Addresses: []string{connectionUrl},
		Username:  username,
		Password:  password,
	}

	if sniff {
		config.DiscoverNodesOnStart = true
	}

	client, err := elasticsearch.NewClient(config)
	if err != nil {
		return nil, err
	}

	engine := &ElasticsearchEngine{
		client:       client,
		configCache:  cfg,
		logger:       logger,
	}

	return engine, nil
}

func (e *ElasticsearchEngine) Start() *model.AppError {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	if atomic.LoadInt32(&e.ready) != 0 {
		return model.NewAppError("ElasticsearchEngine.Start", "searchengine.elasticsearch.already_started.error", nil, "", http.StatusInternalServerError)
	}

	if err := e.setupClient(); err != nil {
		return err
	}

	// Fetch the ElasticSearch version
	response, err := e.client.Info()
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.Start", "searchengine.elasticsearch.connection_error", nil, err.Error(), http.StatusInternalServerError)
	}
	var info map[string]interface{}
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		return model.NewAppError("ElasticsearchEngine.Start", "searchengine.elasticsearch.unmarshal_error", nil, err.Error(), http.StatusInternalServerError)
	}
	response.Body.Close()

	if info["version"] == nil || info["version"].(map[string]interface{})["number"] == nil {
		return model.NewAppError("ElasticsearchEngine.Start", "searchengine.elasticsearch.version_error", nil, "", http.StatusInternalServerError)
	}

	version := info["version"].(map[string]interface{})["number"].(string)
	major, _, _ := strings.Cut(version, ".")
	majorVersion, _ := strconv.Atoi(major)
	e.version = majorVersion
	e.fullVersion = version
	e.logger.Info("Initialized Elasticsearch", mlog.String("version", version))

	// Initialize bulk indexer for enhanced indexing performance
	numWorkers := 4
	flushBytes := 5 * 1024 * 1024 // 5MB
	flushInterval := 30 * time.Second
	
	indexer, err := NewEnhancedBulkIndexer(e, numWorkers, flushBytes, flushInterval)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.Start", "searchengine.elasticsearch.bulk_indexer_error", nil, err.Error(), http.StatusInternalServerError)
	}
	e.bulkProcessor = indexer.indexer

	// Get cluster plugins
	pluginsResponse, err := e.client.Nodes.Info(
		e.client.Nodes.Info.WithPretty(),
	)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.Start", "searchengine.elasticsearch.get_plugins_error", nil, err.Error(), http.StatusInternalServerError)
	}
	
	var nodesInfo map[string]interface{}
	if err := json.NewDecoder(pluginsResponse.Body).Decode(&nodesInfo); err != nil {
		return model.NewAppError("ElasticsearchEngine.Start", "searchengine.elasticsearch.unmarshal_plugins_error", nil, err.Error(), http.StatusInternalServerError)
	}
	pluginsResponse.Body.Close()

	e.plugins = []string{}
	if nodesInfo["nodes"] != nil {
		for _, value := range nodesInfo["nodes"].(map[string]interface{}) {
			nodeInfo := value.(map[string]interface{})
			if nodeInfo["plugins"] != nil {
				plugins := nodeInfo["plugins"].([]interface{})
				for _, plugin := range plugins {
					pluginInfo := plugin.(map[string]interface{})
					e.plugins = append(e.plugins, pluginInfo["name"].(string))
				}
				break
			}
		}
	}

	atomic.StoreInt32(&e.ready, 1)
	return nil
}

func (e *ElasticsearchEngine) Stop() *model.AppError {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	if atomic.LoadInt32(&e.ready) == 0 {
		return nil
	}
	
	if e.bulkProcessor != nil {
		e.bulkProcessor.Close(context.Background())
		e.bulkProcessor = nil
	}
	
	atomic.StoreInt32(&e.ready, 0)
	return nil
}

func (e *ElasticsearchEngine) IsActive() bool {
	return atomic.LoadInt32(&e.ready) == 1 && e.IsEnabled()
}

func (e *ElasticsearchEngine) IsEnabled() bool {
	return *e.configCache.ElasticsearchSettings.EnableIndexing
}

func (e *ElasticsearchEngine) IsIndexingEnabled() bool {
	return *e.configCache.ElasticsearchSettings.EnableIndexing
}

func (e *ElasticsearchEngine) IsSearchEnabled() bool {
	return *e.configCache.ElasticsearchSettings.EnableSearching && *e.configCache.ElasticsearchSettings.EnableIndexing
}

func (e *ElasticsearchEngine) IsAutocompletionEnabled() bool {
	return *e.configCache.ElasticsearchSettings.EnableAutocomplete && *e.configCache.ElasticsearchSettings.EnableIndexing
}

func (e *ElasticsearchEngine) IsIndexingSync() bool {
	return *e.configCache.ElasticsearchSettings.LiveIndexingBatchSize <= 1
}

func (e *ElasticsearchEngine) GetName() string {
	return ENGINE_NAME
}

func (e *ElasticsearchEngine) GetVersion() int {
	return e.version
}

func (e *ElasticsearchEngine) GetFullVersion() string {
	return e.fullVersion
}

func (e *ElasticsearchEngine) GetPlugins() []string {
	return e.plugins
}

func (e *ElasticsearchEngine) UpdateConfig(cfg *model.Config) {
	e.configCache = cfg
}

func (e *ElasticsearchEngine) indexExists(indexName string) (bool, error) {
	req := esapi.IndicesExistsRequest{
		Index: []string{indexName},
	}
	
	res, err := req.Do(context.Background(), e.client)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	
	return res.StatusCode == http.StatusOK, nil
}

func (e *ElasticsearchEngine) createIndex(indexName string) error {
	var mapping string
	
	switch indexName {
	case POST_INDEX:
		mapping = `{
			"mappings": {
				"properties": {
					"id": { "type": "keyword" },
					"team_id": { "type": "keyword" },
					"channel_id": { "type": "keyword" },
					"user_id": { "type": "keyword" },
					"message": { 
						"type": "text",
						"analyzer": "standard",
						"search_analyzer": "standard"
					},
					"type": { "type": "keyword" },
					"create_at": { "type": "long" },
					"update_at": { "type": "long" },
					"delete_at": { "type": "long" },
					"hashtags": { "type": "text" }
				}
			}
		}`
	case USER_INDEX:
		mapping = `{
			"mappings": {
				"properties": {
					"id": { "type": "keyword" },
					"username": { 
						"type": "text",
						"analyzer": "standard",
						"fields": {
							"keyword": { "type": "keyword" }
						}
					},
					"nickname": { "type": "text" },
					"first_name": { "type": "text" },
					"last_name": { "type": "text" },
					"email": { "type": "keyword" },
					"channel_ids": { "type": "keyword" },
					"team_ids": { "type": "keyword" },
					"roles": { "type": "keyword" },
					"allow_marketing": { "type": "boolean" },
					"create_at": { "type": "long" },
					"delete_at": { "type": "long" }
				}
			}
		}`
	case CHANNEL_INDEX:
		mapping = `{
			"mappings": {
				"properties": {
					"id": { "type": "keyword" },
					"team_id": { "type": "keyword" },
					"type": { "type": "keyword" },
					"display_name": { "type": "text" },
					"name": { 
						"type": "text",
						"fields": {
							"keyword": { "type": "keyword" }
						}
					},
					"header": { "type": "text" },
					"purpose": { "type": "text" },
					"create_at": { "type": "long" },
					"delete_at": { "type": "long" }
				}
			}
		}`
	case FILE_INDEX:
		mapping = `{
			"mappings": {
				"properties": {
					"id": { "type": "keyword" },
					"creator_id": { "type": "keyword" },
					"post_id": { "type": "keyword" },
					"channel_id": { "type": "keyword" },
					"create_at": { "type": "long" },
					"update_at": { "type": "long" },
					"delete_at": { "type": "long" },
					"name": { "type": "text" },
					"extension": { "type": "keyword" },
					"size": { "type": "long" },
					"mime_type": { "type": "keyword" },
					"content": { "type": "text" }
				}
			}
		}`
	}
	
	req := esapi.IndicesCreateRequest{
		Index: indexName,
		Body:  strings.NewReader(mapping),
	}
	
	res, err := req.Do(context.Background(), e.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	
	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return err
		}
		return model.NewAppError("createIndex", "searchengine.elasticsearch.create_index.app_error", nil, res.String(), res.StatusCode).Wrap(err)
	}
	
	return nil
}

// TestConfig tests the connection to Elasticsearch and verifies the configuration.
func (e *ElasticsearchEngine) TestConfig(rctx request.CTX, cfg *model.Config) *model.AppError {
	if !*cfg.ElasticsearchSettings.EnableIndexing {
		return model.NewAppError("ElasticsearchEngine.TestConfig", "searchengine.elasticsearch.disabled", nil, "", http.StatusNotImplemented)
	}
	
	connectionUrl := *cfg.ElasticsearchSettings.ConnectionURL
	username := *cfg.ElasticsearchSettings.Username
	password := *cfg.ElasticsearchSettings.Password
	sniff := *cfg.ElasticsearchSettings.Sniff
	
	config := elasticsearch.Config{
		Addresses: []string{connectionUrl},
		Username:  username,
		Password:  password,
	}
	
	if sniff {
		config.DiscoverNodesOnStart = true
	}
	
	client, err := elasticsearch.NewClient(config)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.TestConfig", "searchengine.elasticsearch.connection_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()
	
	resp, err := client.Info(client.Info.WithContext(ctx))
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.TestConfig", "searchengine.elasticsearch.connection_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer resp.Body.Close()
	
	if resp.IsError() {
		return model.NewAppError("ElasticsearchEngine.TestConfig", "searchengine.elasticsearch.connection_failed", nil, resp.String(), resp.StatusCode)
	}
	
	return nil
}

// CreatePost transforms a Mattermost post to an Elasticsearch document
func postToESDoc(post *model.Post, teamId string) (*ESPost, error) {
	hashtags, _ := model.ParseHashtags(post.Message)
	hashtagsStr := strings.Join(hashtags, " ")
	return &ESPost{
		Id:        post.Id,
		TeamId:    teamId,
		ChannelId: post.ChannelId,
		UserId:    post.UserId,
		Message:   post.Message,
		Type:      post.Type,
		CreateAt:  post.CreateAt,
		UpdateAt:  post.UpdateAt,
		DeleteAt:  post.DeleteAt,
		Hashtags:  hashtagsStr,
	}, nil
}

// IndexPost indexes a post in Elasticsearch
func (e *ElasticsearchEngine) IndexPost(post *model.Post, teamId string) *model.AppError {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 {
		return model.NewAppError("ElasticsearchEngine.IndexPost", "searchengine.elasticsearch.not_ready", nil, "", http.StatusInternalServerError)
	}

	if !e.IsIndexingEnabled() {
		return model.NewAppError("ElasticsearchEngine.IndexPost", "searchengine.elasticsearch.indexing_disabled", nil, "", http.StatusInternalServerError)
	}

	// Convert post to Elasticsearch document
	esPost, err := postToESDoc(post, teamId)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.IndexPost", "searchengine.elasticsearch.index_post_error", nil, err.Error(), http.StatusInternalServerError)
	}

	// Marshal document to JSON
	data, err := json.Marshal(esPost)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.IndexPost", "searchengine.elasticsearch.marshal_error", nil, err.Error(), http.StatusInternalServerError)
	}

	// Check if we should use the bulk indexer
	if e.bulkProcessor != nil {
		// Use the bulk indexer for better performance
		err = e.bulkProcessor.Add(
			context.Background(),
			esutil.BulkIndexerItem{
				Action:     "index",
				DocumentID: post.Id,
				Body:       bytes.NewReader(data),
				Index:      POST_INDEX,
				OnSuccess: func(ctx context.Context, item esutil.BulkIndexerItem, res esutil.BulkIndexerResponseItem) {
					e.logger.Debug("Successfully indexed post with bulk processor", 
						mlog.String("post_id", post.Id),
					)
				},
				OnFailure: func(ctx context.Context, item esutil.BulkIndexerItem, res esutil.BulkIndexerResponseItem, err error) {
					e.logger.Error("Failed to index post with bulk processor",
						mlog.String("post_id", post.Id),
						mlog.Err(err),
						mlog.String("error_type", res.Error.Type),
						mlog.String("error_reason", res.Error.Reason),
					)
				},
			},
		)
		if err != nil {
			return model.NewAppError("ElasticsearchEngine.IndexPost", "searchengine.elasticsearch.bulk_index_error", nil, err.Error(), http.StatusInternalServerError)
		}
		return nil
	}

	// Fall back to direct indexing if bulk processor is not available
	req := esapi.IndexRequest{
		Index:      POST_INDEX,
		DocumentID: post.Id,
		Body:       strings.NewReader(string(data)),
		Refresh:    "false",
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.IndexPost", "searchengine.elasticsearch.index_post_error", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.IsError() {
		return model.NewAppError("ElasticsearchEngine.IndexPost", "searchengine.elasticsearch.index_post_error", nil, res.String(), res.StatusCode)
	}

	return nil
}

// DeletePost removes a post from the Elasticsearch index
func (e *ElasticsearchEngine) DeletePost(post *model.Post) *model.AppError {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 {
		return model.NewAppError("ElasticsearchEngine.DeletePost", "searchengine.elasticsearch.not_ready", nil, "", http.StatusInternalServerError)
	}

	// Use bulk processor if available
	if e.bulkProcessor != nil {
		err := e.bulkProcessor.Add(context.Background(), esutil.BulkIndexerItem{
			Index:      POST_INDEX,
			Action:     "delete",
			DocumentID: post.Id,
		})
		if err != nil {
			return model.NewAppError("ElasticsearchEngine.DeletePost", "searchengine.elasticsearch.delete_post_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return nil
	}

	// Otherwise, delete synchronously
	req := esapi.DeleteRequest{
		Index:      POST_INDEX,
		DocumentID: post.Id,
		Refresh:    "true", // Make this write visible for search immediately
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.DeletePost", "searchengine.elasticsearch.delete_post_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return nil // Document was already deleted or never existed
	}

	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return model.NewAppError("ElasticsearchEngine.DeletePost", "searchengine.elasticsearch.delete_post_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return model.NewAppError("ElasticsearchEngine.DeletePost", "searchengine.elasticsearch.delete_post_failed", nil, res.String(), res.StatusCode)
	}

	return nil
}

// DeleteChannelPosts removes all posts for a channel from the Elasticsearch index
func (e *ElasticsearchEngine) DeleteChannelPosts(rctx request.CTX, channelID string) *model.AppError {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 {
		return model.NewAppError("ElasticsearchEngine.DeleteChannelPosts", "searchengine.elasticsearch.not_ready", nil, "", http.StatusInternalServerError)
	}

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"term": map[string]interface{}{
				"channel_id": channelID,
			},
		},
	}

	queryJSON, err := json.Marshal(query)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.DeleteChannelPosts", "searchengine.elasticsearch.delete_channel_posts_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	shouldRefresh := true
	req := esapi.DeleteByQueryRequest{
		Index:   []string{POST_INDEX},
		Body:    strings.NewReader(string(queryJSON)),
		Refresh: &shouldRefresh,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.DeleteChannelPosts", "searchengine.elasticsearch.delete_channel_posts_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return model.NewAppError("ElasticsearchEngine.DeleteChannelPosts", "searchengine.elasticsearch.delete_channel_posts_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return model.NewAppError("ElasticsearchEngine.DeleteChannelPosts", "searchengine.elasticsearch.delete_channel_posts_failed", nil, res.String(), res.StatusCode)
	}

	return nil
}

// DeleteUserPosts removes all posts created by a user from the Elasticsearch index
func (e *ElasticsearchEngine) DeleteUserPosts(rctx request.CTX, userID string) *model.AppError {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 {
		return model.NewAppError("ElasticsearchEngine.DeleteUserPosts", "searchengine.elasticsearch.not_ready", nil, "", http.StatusInternalServerError)
	}

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"term": map[string]interface{}{
				"user_id": userID,
			},
		},
	}

	queryJSON, err := json.Marshal(query)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.DeleteUserPosts", "searchengine.elasticsearch.delete_user_posts_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	shouldRefresh := true
	req := esapi.DeleteByQueryRequest{
		Index:   []string{POST_INDEX},
		Body:    strings.NewReader(string(queryJSON)),
		Refresh: &shouldRefresh,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.DeleteUserPosts", "searchengine.elasticsearch.delete_user_posts_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return model.NewAppError("ElasticsearchEngine.DeleteUserPosts", "searchengine.elasticsearch.delete_user_posts_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return model.NewAppError("ElasticsearchEngine.DeleteUserPosts", "searchengine.elasticsearch.delete_user_posts_failed", nil, res.String(), res.StatusCode)
	}

	return nil
}

// SearchPosts performs a search for posts in Elasticsearch with fuzzy matching and ranking
func (e *ElasticsearchEngine) SearchPosts(channels model.ChannelList, searchParams []*model.SearchParams, page, perPage int) ([]string, model.PostSearchMatches, *model.AppError) {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if !e.IsSearchEnabled() || atomic.LoadInt32(&e.ready) == 0 {
		return nil, nil, model.NewAppError("ElasticsearchEngine.SearchPosts", "searchengine.elasticsearch.search_posts_disabled", nil, "", http.StatusInternalServerError)
	}

	// Create channel filter from the list of channels
	var channelIds []string
	for _, channel := range channels {
		channelIds = append(channelIds, channel.Id)
	}

	// Construct elasticsearch query
	query := buildSearchQuery(searchParams, channelIds, DEFAULT_FUZZY_LEVEL)

	// Execute search query
	searchBody, err := json.Marshal(query)
	if err != nil {
		return nil, nil, model.NewAppError("ElasticsearchEngine.SearchPosts", "searchengine.elasticsearch.search_posts_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	// Convert int parameters to int pointers for the request
	fromVal := page * perPage
	sizeVal := perPage
	
	req := esapi.SearchRequest{
		Index: []string{POST_INDEX},
		Body:  strings.NewReader(string(searchBody)),
		From:  &fromVal,
		Size:  &sizeVal,
		Sort:  []string{"_score:desc", "create_at:desc"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return nil, nil, model.NewAppError("ElasticsearchEngine.SearchPosts", "searchengine.elasticsearch.search_posts_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return nil, nil, model.NewAppError("ElasticsearchEngine.SearchPosts", "searchengine.elasticsearch.search_posts_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return nil, nil, model.NewAppError("ElasticsearchEngine.SearchPosts", "searchengine.elasticsearch.search_posts_failed", nil, res.String(), res.StatusCode)
	}

	var resultData struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []struct {
				ID      string          `json:"_id"`
				Score   float64         `json:"_score"`
				Source  json.RawMessage `json:"_source"`
				Highlight struct {
					Message []string `json:"message"`
				} `json:"highlight"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&resultData); err != nil {
		return nil, nil, model.NewAppError("ElasticsearchEngine.SearchPosts", "searchengine.elasticsearch.search_posts_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	// Extract post IDs and create post search matches
	postIds := make([]string, 0, len(resultData.Hits.Hits))
	matches := make(model.PostSearchMatches)

	for _, hit := range resultData.Hits.Hits {
		postIds = append(postIds, hit.ID)
		
		// Extract highlighted text for matched terms
		if len(hit.Highlight.Message) > 0 {
			matches[hit.ID] = hit.Highlight.Message
		}
	}

	return postIds, matches, nil
}

// buildSearchQuery constructs the Elasticsearch query for searching posts with fuzzy search capabilities
func buildSearchQuery(searchParams []*model.SearchParams, channelIds []string, fuzzyLevel int) map[string]interface{} {
	// Main boolean query
	boolQuery := map[string]interface{}{
		"filter": []map[string]interface{}{
			{
				"terms": map[string]interface{}{
					"channel_id": channelIds,
				},
			},
			{
				"term": map[string]interface{}{
					"delete_at": 0,
				},
			},
		},
		"should": []map[string]interface{}{},
		"must_not": []map[string]interface{}{},
	}

	// Process each search parameter
	for _, params := range searchParams {
		// Terms to search for
		if params.Terms != "" {
			fuzzyQuery := map[string]interface{}{
				"multi_match": map[string]interface{}{
					"query":  params.Terms,
					"fields": []string{"message", "hashtags"},
					"fuzziness": fuzzyLevel,
					"operator": "and",
					"boost": 1.5, // Boost exact matches
				},
			}
			
			boolQuery["should"] = append(boolQuery["should"].([]map[string]interface{}), fuzzyQuery)
		}

		// Excluded terms
		if params.ExcludedTerms != "" {
			excludeQuery := map[string]interface{}{
				"multi_match": map[string]interface{}{
					"query":  params.ExcludedTerms,
					"fields": []string{"message", "hashtags"},
				},
			}
			boolQuery["must_not"] = append(boolQuery["must_not"].([]map[string]interface{}), excludeQuery)
		}

		// Filter by users
		if len(params.FromUsers) > 0 {
			usersFilter := map[string]interface{}{
				"terms": map[string]interface{}{
					"user_id": params.FromUsers,
				},
			}
			boolQuery["filter"] = append(boolQuery["filter"].([]map[string]interface{}), usersFilter)
		}

		// Exclude users
		if len(params.ExcludedUsers) > 0 {
			excludeUsersFilter := map[string]interface{}{
				"terms": map[string]interface{}{
					"user_id": params.ExcludedUsers,
				},
			}
			boolQuery["must_not"] = append(boolQuery["must_not"].([]map[string]interface{}), excludeUsersFilter)
		}

		// Filter by specific channels within allowable channels
		if len(params.InChannels) > 0 {
			inChannelsFilter := map[string]interface{}{
				"terms": map[string]interface{}{
					"channel_id": params.InChannels,
				},
			}
			boolQuery["filter"] = append(boolQuery["filter"].([]map[string]interface{}), inChannelsFilter)
		}

		// Exclude specific channels
		if len(params.ExcludedChannels) > 0 {
			excludeChannelsFilter := map[string]interface{}{
				"terms": map[string]interface{}{
					"channel_id": params.ExcludedChannels,
				},
			}
			boolQuery["must_not"] = append(boolQuery["must_not"].([]map[string]interface{}), excludeChannelsFilter)
		}

		// Date filters
		if params.OnDate != "" {
			// Convert the date string to Unix timestamp (milliseconds)
			before, after := params.GetOnDateMillis()
			dateFilter := map[string]interface{}{
				"range": map[string]interface{}{
					"create_at": map[string]interface{}{
						"gte": before,
						"lte": after,
					},
				},
			}
			boolQuery["filter"] = append(boolQuery["filter"].([]map[string]interface{}), dateFilter)
		} else {
			// Handle before/after date filters
			if params.AfterDate != "" || params.BeforeDate != "" {
				rangeQuery := map[string]interface{}{}
				
				if params.AfterDate != "" {
					rangeQuery["gte"] = params.GetAfterDateMillis()
				}
				
				if params.BeforeDate != "" {
					rangeQuery["lte"] = params.GetBeforeDateMillis()
				}
				
				dateFilter := map[string]interface{}{
					"range": map[string]interface{}{
						"create_at": rangeQuery,
					},
				}
				boolQuery["filter"] = append(boolQuery["filter"].([]map[string]interface{}), dateFilter)
			}
		}
	}

	// Add highlighting configuration
	highlight := map[string]interface{}{
		"fields": map[string]interface{}{
			"message": map[string]interface{}{
				"number_of_fragments": 3,
				"fragment_size": 150,
				"pre_tags": []string{"<span class='search-highlight'>"},
				"post_tags": []string{"</span>"},
			},
		},
	}

	// Assemble the final query
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": boolQuery,
		},
		"highlight": highlight,
	}

	return query
}

// PurgeIndexes deletes and recreates all indices
func (e *ElasticsearchEngine) PurgeIndexes(rctx request.CTX) *model.AppError {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	if atomic.LoadInt32(&e.ready) == 0 {
		return model.NewAppError("ElasticsearchEngine.PurgeIndexes", "searchengine.elasticsearch.not_ready", nil, "", http.StatusInternalServerError)
	}

	// Delete all indices
	indices := []string{POST_INDEX, USER_INDEX, CHANNEL_INDEX, FILE_INDEX}
	for _, index := range indices {
		req := esapi.IndicesDeleteRequest{
			Index: []string{index},
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
		defer cancel()

		res, err := req.Do(ctx, e.client)
		if err != nil {
			return model.NewAppError("ElasticsearchEngine.PurgeIndexes", "searchengine.elasticsearch.purge_indexes_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		defer res.Body.Close()

		// Ignore 404 errors (index not found)
		if res.StatusCode != http.StatusNotFound && res.IsError() {
			var responseMap map[string]interface{}
			if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
				return model.NewAppError("ElasticsearchEngine.PurgeIndexes", "searchengine.elasticsearch.purge_indexes_failed", nil, err.Error(), http.StatusInternalServerError)
			}
			return model.NewAppError("ElasticsearchEngine.PurgeIndexes", "searchengine.elasticsearch.purge_indexes_failed", nil, res.String(), res.StatusCode)
		}
	}

	// Recreate all indices
	for _, index := range indices {
		if err := e.createIndex(index); err != nil {
			return model.NewAppError("ElasticsearchEngine.PurgeIndexes", "searchengine.elasticsearch.create_index_failed", nil, err.Error(), http.StatusInternalServerError)
		}
	}

	return nil
}

// PurgeIndexList deletes and recreates specific indices (currently only supports posts index)
func (e *ElasticsearchEngine) PurgeIndexList(rctx request.CTX, indexes []string) *model.AppError {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	if atomic.LoadInt32(&e.ready) == 0 {
		return model.NewAppError("ElasticsearchEngine.PurgeIndexList", "searchengine.elasticsearch.not_ready", nil, "", http.StatusInternalServerError)
	}

	// Only allow purging posts index for now
	for _, index := range indexes {
		if index != POST_INDEX {
			return model.NewAppError("ElasticsearchEngine.PurgeIndexList", "searchengine.elasticsearch.invalid_index_to_purge", nil, "index="+index, http.StatusBadRequest)
		}
	}

	// Delete and recreate posts index
	req := esapi.IndicesDeleteRequest{
		Index: []string{POST_INDEX},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.PurgeIndexList", "searchengine.elasticsearch.purge_index_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	// Ignore 404 errors (index not found)
	if res.StatusCode != http.StatusNotFound && res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return model.NewAppError("ElasticsearchEngine.PurgeIndexList", "searchengine.elasticsearch.purge_index_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return model.NewAppError("ElasticsearchEngine.PurgeIndexList", "searchengine.elasticsearch.purge_index_failed", nil, res.String(), res.StatusCode)
	}

	// Recreate posts index
	if err := e.createIndex(POST_INDEX); err != nil {
		return model.NewAppError("ElasticsearchEngine.PurgeIndexList", "searchengine.elasticsearch.create_index_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	return nil
}

// RefreshIndexes refreshes all indices to make recent changes available for search
func (e *ElasticsearchEngine) RefreshIndexes(rctx request.CTX) *model.AppError {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 {
		return model.NewAppError("ElasticsearchEngine.RefreshIndexes", "searchengine.elasticsearch.not_ready", nil, "", http.StatusInternalServerError)
	}

	req := esapi.IndicesRefreshRequest{
		Index: []string{POST_INDEX, USER_INDEX, CHANNEL_INDEX, FILE_INDEX},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.RefreshIndexes", "searchengine.elasticsearch.refresh_indexes_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return model.NewAppError("ElasticsearchEngine.RefreshIndexes", "searchengine.elasticsearch.refresh_indexes_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return model.NewAppError("ElasticsearchEngine.RefreshIndexes", "searchengine.elasticsearch.refresh_indexes_failed", nil, res.String(), res.StatusCode)
	}

	return nil
}

// IndexChannel indexes a channel in Elasticsearch
func (e *ElasticsearchEngine) IndexChannel(rctx request.CTX, channel *model.Channel, userIDs, teamMemberIDs []string) *model.AppError {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 {
		return model.NewAppError("ElasticsearchEngine.IndexChannel", "searchengine.elasticsearch.not_ready", nil, "", http.StatusInternalServerError)
	}

	esChannel := &ESChannel{
		Id:          channel.Id,
		TeamId:      channel.TeamId,
		Type:        string(channel.Type),
		DisplayName: channel.DisplayName,
		Name:        channel.Name,
		Header:      channel.Header,
		Purpose:     channel.Purpose,
		CreateAt:    channel.CreateAt,
		DeleteAt:    channel.DeleteAt,
	}

	docJSON, err := json.Marshal(esChannel)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.IndexChannel", "searchengine.elasticsearch.index_channel_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	req := esapi.IndexRequest{
		Index:      CHANNEL_INDEX,
		DocumentID: channel.Id,
		Body:       strings.NewReader(string(docJSON)),
		Refresh:    "true",
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.IndexChannel", "searchengine.elasticsearch.index_channel_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return model.NewAppError("ElasticsearchEngine.IndexChannel", "searchengine.elasticsearch.index_channel_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return model.NewAppError("ElasticsearchEngine.IndexChannel", "searchengine.elasticsearch.index_channel_failed", nil, res.String(), res.StatusCode)
	}

	return nil
}

// SearchChannels searches for channels matching the given term
func (e *ElasticsearchEngine) SearchChannels(teamId, userID string, term string, isGuest, includeDeleted bool) ([]string, *model.AppError) {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 || !e.IsSearchEnabled() {
		return nil, model.NewAppError("ElasticsearchEngine.SearchChannels", "searchengine.elasticsearch.search_channels_disabled", nil, "", http.StatusNotImplemented)
	}

	// Build query for matching channel names and display names
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []map[string]interface{}{
					{
						"match_phrase_prefix": map[string]interface{}{
							"name": map[string]interface{}{
								"query": term,
								"boost": 3.0, // Boost name matches
							},
						},
					},
					{
						"match_phrase_prefix": map[string]interface{}{
							"display_name": map[string]interface{}{
								"query": term,
								"boost": 2.0, // Boost display_name matches
							},
						},
					},
					{
						"match": map[string]interface{}{
							"purpose": term,
						},
					},
					{
						"match": map[string]interface{}{
							"header": term,
						},
					},
				},
				"filter": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"team_id": teamId,
						},
					},
				},
			},
		},
		"size": 100, // Limit results to 100 channels
	}

	// Add filters for deleted channels
	if !includeDeleted {
		query["query"].(map[string]interface{})["bool"].(map[string]interface{})["filter"] = append(
			query["query"].(map[string]interface{})["bool"].(map[string]interface{})["filter"].([]map[string]interface{}),
			map[string]interface{}{
				"term": map[string]interface{}{
					"delete_at": 0,
				},
			},
		)
	}

	queryJSON, err := json.Marshal(query)
	if err != nil {
		return nil, model.NewAppError("ElasticsearchEngine.SearchChannels", "searchengine.elasticsearch.search_channels_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	req := esapi.SearchRequest{
		Index: []string{CHANNEL_INDEX},
		Body:  strings.NewReader(string(queryJSON)),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return nil, model.NewAppError("ElasticsearchEngine.SearchChannels", "searchengine.elasticsearch.search_channels_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return nil, model.NewAppError("ElasticsearchEngine.SearchChannels", "searchengine.elasticsearch.search_channels_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return nil, model.NewAppError("ElasticsearchEngine.SearchChannels", "searchengine.elasticsearch.search_channels_failed", nil, res.String(), res.StatusCode)
	}

	var resultData struct {
		Hits struct {
			Hits []struct {
				ID string `json:"_id"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&resultData); err != nil {
		return nil, model.NewAppError("ElasticsearchEngine.SearchChannels", "searchengine.elasticsearch.search_channels_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	channelIds := make([]string, 0, len(resultData.Hits.Hits))
	for _, hit := range resultData.Hits.Hits {
		channelIds = append(channelIds, hit.ID)
	}

	return channelIds, nil
}

// DeleteChannel removes a channel from the Elasticsearch index
func (e *ElasticsearchEngine) DeleteChannel(channel *model.Channel) *model.AppError {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 {
		return model.NewAppError("ElasticsearchEngine.DeleteChannel", "searchengine.elasticsearch.not_ready", nil, "", http.StatusInternalServerError)
	}

	req := esapi.DeleteRequest{
		Index:      CHANNEL_INDEX,
		DocumentID: channel.Id,
		Refresh:    "true",
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.DeleteChannel", "searchengine.elasticsearch.delete_channel_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return nil // Document was already deleted or never existed
	}

	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return model.NewAppError("ElasticsearchEngine.DeleteChannel", "searchengine.elasticsearch.delete_channel_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return model.NewAppError("ElasticsearchEngine.DeleteChannel", "searchengine.elasticsearch.delete_channel_failed", nil, res.String(), res.StatusCode)
	}

	return nil
}

// IndexUser indexes a user in Elasticsearch
func (e *ElasticsearchEngine) IndexUser(rctx request.CTX, user *model.User, teamsIds, channelsIds []string) *model.AppError {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 {
		return model.NewAppError("ElasticsearchEngine.IndexUser", "searchengine.elasticsearch.not_ready", nil, "", http.StatusInternalServerError)
	}

	esUser := &ESUser{
		Id:             user.Id,
		Username:       user.Username,
		Nickname:       user.Nickname,
		FirstName:      user.FirstName,
		LastName:       user.LastName,
		Email:          user.Email,
		ChannelIds:     channelsIds,
		TeamIds:        teamsIds,
		Roles:          user.Roles,
		AllowMarketing: user.AllowMarketing,
		CreateAt:       user.CreateAt,
		DeleteAt:       user.DeleteAt,
	}

	docJSON, err := json.Marshal(esUser)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.IndexUser", "searchengine.elasticsearch.index_user_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	req := esapi.IndexRequest{
		Index:      USER_INDEX,
		DocumentID: user.Id,
		Body:       strings.NewReader(string(docJSON)),
		Refresh:    "true",
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.IndexUser", "searchengine.elasticsearch.index_user_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return model.NewAppError("ElasticsearchEngine.IndexUser", "searchengine.elasticsearch.index_user_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return model.NewAppError("ElasticsearchEngine.IndexUser", "searchengine.elasticsearch.index_user_failed", nil, res.String(), res.StatusCode)
	}

	return nil
}

// SearchUsersInChannel searches for users in a specific channel
func (e *ElasticsearchEngine) SearchUsersInChannel(teamId, channelId string, restrictedToChannels []string, term string, options *model.UserSearchOptions) ([]string, []string, *model.AppError) {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 || !e.IsSearchEnabled() {
		return nil, nil, model.NewAppError("ElasticsearchEngine.SearchUsersInChannel", "searchengine.elasticsearch.search_users_disabled", nil, "", http.StatusInternalServerError)
	}

	// Build a query to find users in the specific channel
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"multi_match": map[string]interface{}{
							"query":  term,
							"fields": []string{"username", "first_name", "last_name", "nickname", "email"},
							"fuzziness": "AUTO",
						},
					},
					{
						"term": map[string]interface{}{
							"channel_ids": channelId,
						},
					},
					{
						"term": map[string]interface{}{
							"delete_at": 0,
						},
					},
				},
			},
		},
		"size": 100, // Limit to 100 users
	}

	queryJSON, err := json.Marshal(query)
	if err != nil {
		return nil, nil, model.NewAppError("ElasticsearchEngine.SearchUsersInChannel", "searchengine.elasticsearch.search_users_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	req := esapi.SearchRequest{
		Index: []string{USER_INDEX},
		Body:  strings.NewReader(string(queryJSON)),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return nil, nil, model.NewAppError("ElasticsearchEngine.SearchUsersInChannel", "searchengine.elasticsearch.search_users_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return nil, nil, model.NewAppError("ElasticsearchEngine.SearchUsersInChannel", "searchengine.elasticsearch.search_users_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return nil, nil, model.NewAppError("ElasticsearchEngine.SearchUsersInChannel", "searchengine.elasticsearch.search_users_failed", nil, res.String(), res.StatusCode)
	}

	var resultData struct {
		Hits struct {
			Hits []struct {
				ID string `json:"_id"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&resultData); err != nil {
		return nil, nil, model.NewAppError("ElasticsearchEngine.SearchUsersInChannel", "searchengine.elasticsearch.search_users_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	userIds := make([]string, len(resultData.Hits.Hits))
	for i, hit := range resultData.Hits.Hits {
		userIds[i] = hit.ID
	}

	return userIds, nil, nil
}

// SearchUsersInTeam searches for users in a specific team
func (e *ElasticsearchEngine) SearchUsersInTeam(teamId string, restrictedToChannels []string, term string, options *model.UserSearchOptions) ([]string, *model.AppError) {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 || !e.IsSearchEnabled() {
		return nil, model.NewAppError("ElasticsearchEngine.SearchUsersInTeam", "searchengine.elasticsearch.search_users_disabled", nil, "", http.StatusInternalServerError)
	}

	// Build a query to find users in the specific team
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"multi_match": map[string]interface{}{
							"query":  term,
							"fields": []string{"username", "first_name", "last_name", "nickname", "email"},
							"fuzziness": "AUTO",
						},
					},
					{
						"term": map[string]interface{}{
							"team_ids": teamId,
						},
					},
					{
						"term": map[string]interface{}{
							"delete_at": 0,
						},
					},
				},
			},
		},
		"size": 100, // Limit to 100 users
	}

	// Add channel restriction if needed
	if len(restrictedToChannels) > 0 {
		channelsFilter := map[string]interface{}{
			"terms": map[string]interface{}{
				"channel_ids": restrictedToChannels,
			},
		}
		query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"] = append(
			query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must"].([]map[string]interface{}),
			channelsFilter,
		)
	}

	queryJSON, err := json.Marshal(query)
	if err != nil {
		return nil, model.NewAppError("ElasticsearchEngine.SearchUsersInTeam", "searchengine.elasticsearch.search_users_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	req := esapi.SearchRequest{
		Index: []string{USER_INDEX},
		Body:  strings.NewReader(string(queryJSON)),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return nil, model.NewAppError("ElasticsearchEngine.SearchUsersInTeam", "searchengine.elasticsearch.search_users_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return nil, model.NewAppError("ElasticsearchEngine.SearchUsersInTeam", "searchengine.elasticsearch.search_users_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return nil, model.NewAppError("ElasticsearchEngine.SearchUsersInTeam", "searchengine.elasticsearch.search_users_failed", nil, res.String(), res.StatusCode)
	}

	var resultData struct {
		Hits struct {
			Hits []struct {
				ID string `json:"_id"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&resultData); err != nil {
		return nil, model.NewAppError("ElasticsearchEngine.SearchUsersInTeam", "searchengine.elasticsearch.search_users_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	userIds := make([]string, len(resultData.Hits.Hits))
	for i, hit := range resultData.Hits.Hits {
		userIds[i] = hit.ID
	}

	return userIds, nil
}

// DeleteUser removes a user from the Elasticsearch index
func (e *ElasticsearchEngine) DeleteUser(user *model.User) *model.AppError {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 {
		return model.NewAppError("ElasticsearchEngine.DeleteUser", "searchengine.elasticsearch.not_ready", nil, "", http.StatusInternalServerError)
	}

	req := esapi.DeleteRequest{
		Index:      USER_INDEX,
		DocumentID: user.Id,
		Refresh:    "true",
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.DeleteUser", "searchengine.elasticsearch.delete_user_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return nil // Document was already deleted or never existed
	}

	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return model.NewAppError("ElasticsearchEngine.DeleteUser", "searchengine.elasticsearch.delete_user_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return model.NewAppError("ElasticsearchEngine.DeleteUser", "searchengine.elasticsearch.delete_user_failed", nil, res.String(), res.StatusCode)
	}

	return nil
}

// IndexFile indexes a file in Elasticsearch
func (e *ElasticsearchEngine) IndexFile(file *model.FileInfo, channelId string) *model.AppError {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 {
		return model.NewAppError("ElasticsearchEngine.IndexFile", "searchengine.elasticsearch.not_ready", nil, "", http.StatusInternalServerError)
	}

	esFile := &ESFile{
		Id:        file.Id,
		CreatorId: file.CreatorId,
		PostId:    file.PostId,
		ChannelId: channelId,
		CreateAt:  file.CreateAt,
		UpdateAt:  file.UpdateAt,
		DeleteAt:  file.DeleteAt,
		Name:      file.Name,
		Extension: file.Extension,
		Size:      file.Size,
		MimeType:  file.MimeType,
		// Content is left blank here, would need to extract file content for real implementation
	}

	docJSON, err := json.Marshal(esFile)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.IndexFile", "searchengine.elasticsearch.index_file_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	req := esapi.IndexRequest{
		Index:      FILE_INDEX,
		DocumentID: file.Id,
		Body:       strings.NewReader(string(docJSON)),
		Refresh:    "true",
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.IndexFile", "searchengine.elasticsearch.index_file_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return model.NewAppError("ElasticsearchEngine.IndexFile", "searchengine.elasticsearch.index_file_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return model.NewAppError("ElasticsearchEngine.IndexFile", "searchengine.elasticsearch.index_file_failed", nil, res.String(), res.StatusCode)
	}

	return nil
}

// SearchFiles searches for files matching the given query
func (e *ElasticsearchEngine) SearchFiles(channels model.ChannelList, searchParams []*model.SearchParams, page, perPage int) ([]string, *model.AppError) {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if !e.IsSearchEnabled() || atomic.LoadInt32(&e.ready) == 0 {
		return nil, model.NewAppError("ElasticsearchEngine.SearchFiles", "searchengine.elasticsearch.search_files_disabled", nil, "", http.StatusInternalServerError)
	}

	// Create channel filter from the list of channels
	var channelIds []string
	for _, channel := range channels {
		channelIds = append(channelIds, channel.Id)
	}

	// Main boolean query
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"filter": []map[string]interface{}{
					{
						"terms": map[string]interface{}{
							"channel_id": channelIds,
						},
					},
					{
						"term": map[string]interface{}{
							"delete_at": 0,
						},
					},
				},
				"should": []map[string]interface{}{},
				"must_not": []map[string]interface{}{},
			},
		},
		"from": page * perPage,
		"size": perPage,
		"sort": []map[string]interface{}{
			{
				"create_at": map[string]interface{}{
					"order": "desc",
				},
			},
		},
	}

	// Process each search parameter
	for _, params := range searchParams {
		// Terms to search for in file names and content
		if params.Terms != "" {
			termsQuery := map[string]interface{}{
				"multi_match": map[string]interface{}{
					"query":  params.Terms,
					"fields": []string{"name", "content"},
					"fuzziness": DEFAULT_FUZZY_LEVEL,
				},
			}
			query["query"].(map[string]interface{})["bool"].(map[string]interface{})["should"] = append(
				query["query"].(map[string]interface{})["bool"].(map[string]interface{})["should"].([]map[string]interface{}),
				termsQuery,
			)
		}

		// File extensions filter
		if len(params.Extensions) > 0 {
			extensionsFilter := map[string]interface{}{
				"terms": map[string]interface{}{
					"extension": params.Extensions,
				},
			}
			query["query"].(map[string]interface{})["bool"].(map[string]interface{})["filter"] = append(
				query["query"].(map[string]interface{})["bool"].(map[string]interface{})["filter"].([]map[string]interface{}),
				extensionsFilter,
			)
		}

		// Excluded file extensions
		if len(params.ExcludedExtensions) > 0 {
			excludedExtensionsFilter := map[string]interface{}{
				"terms": map[string]interface{}{
					"extension": params.ExcludedExtensions,
				},
			}
			query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must_not"] = append(
				query["query"].(map[string]interface{})["bool"].(map[string]interface{})["must_not"].([]map[string]interface{}),
				excludedExtensionsFilter,
			)
		}

		// Date filters
		if params.AfterDate != "" || params.BeforeDate != "" {
			dateFilter := map[string]interface{}{
				"range": map[string]interface{}{
					"create_at": map[string]interface{}{},
				},
			}

			if params.AfterDate != "" {
				dateFilter["range"].(map[string]interface{})["create_at"].(map[string]interface{})["gte"] = params.GetAfterDateMillis()
			}

			if params.BeforeDate != "" {
				dateFilter["range"].(map[string]interface{})["create_at"].(map[string]interface{})["lte"] = params.GetBeforeDateMillis()
			}

			query["query"].(map[string]interface{})["bool"].(map[string]interface{})["filter"] = append(
				query["query"].(map[string]interface{})["bool"].(map[string]interface{})["filter"].([]map[string]interface{}),
				dateFilter,
			)
		}
	}

	// Require at least one "should" condition
	if len(query["query"].(map[string]interface{})["bool"].(map[string]interface{})["should"].([]map[string]interface{})) > 0 {
		query["query"].(map[string]interface{})["bool"].(map[string]interface{})["minimum_should_match"] = 1
	}

	queryJSON, err := json.Marshal(query)
	if err != nil {
		return nil, model.NewAppError("ElasticsearchEngine.SearchFiles", "searchengine.elasticsearch.search_files_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	req := esapi.SearchRequest{
		Index: []string{FILE_INDEX},
		Body:  strings.NewReader(string(queryJSON)),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return nil, model.NewAppError("ElasticsearchEngine.SearchFiles", "searchengine.elasticsearch.search_files_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return nil, model.NewAppError("ElasticsearchEngine.SearchFiles", "searchengine.elasticsearch.search_files_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return nil, model.NewAppError("ElasticsearchEngine.SearchFiles", "searchengine.elasticsearch.search_files_failed", nil, res.String(), res.StatusCode)
	}

	var resultData struct {
		Hits struct {
			Hits []struct {
				ID string `json:"_id"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&resultData); err != nil {
		return nil, model.NewAppError("ElasticsearchEngine.SearchFiles", "searchengine.elasticsearch.search_files_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	fileIds := make([]string, len(resultData.Hits.Hits))
	for i, hit := range resultData.Hits.Hits {
		fileIds[i] = hit.ID
	}

	return fileIds, nil
}

// DeleteFile removes a file from the Elasticsearch index
func (e *ElasticsearchEngine) DeleteFile(fileID string) *model.AppError {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 {
		return model.NewAppError("ElasticsearchEngine.DeleteFile", "searchengine.elasticsearch.not_ready", nil, "", http.StatusInternalServerError)
	}

	req := esapi.DeleteRequest{
		Index:      FILE_INDEX,
		DocumentID: fileID,
		Refresh:    "true",
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.DeleteFile", "searchengine.elasticsearch.delete_file_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return nil // Document was already deleted or never existed
	}

	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return model.NewAppError("ElasticsearchEngine.DeleteFile", "searchengine.elasticsearch.delete_file_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return model.NewAppError("ElasticsearchEngine.DeleteFile", "searchengine.elasticsearch.delete_file_failed", nil, res.String(), res.StatusCode)
	}

	return nil
}

// DeletePostFiles deletes all files associated with a post
func (e *ElasticsearchEngine) DeletePostFiles(rctx request.CTX, postID string) *model.AppError {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 {
		return model.NewAppError("ElasticsearchEngine.DeletePostFiles", "searchengine.elasticsearch.not_ready", nil, "", http.StatusInternalServerError)
	}

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"term": map[string]interface{}{
				"post_id": postID,
			},
		},
	}

	queryJSON, err := json.Marshal(query)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.DeletePostFiles", "searchengine.elasticsearch.delete_post_files_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	shouldRefresh := true
	req := esapi.DeleteByQueryRequest{
		Index:   []string{FILE_INDEX},
		Body:    strings.NewReader(string(queryJSON)),
		Refresh: &shouldRefresh,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.DeletePostFiles", "searchengine.elasticsearch.delete_post_files_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return model.NewAppError("ElasticsearchEngine.DeletePostFiles", "searchengine.elasticsearch.delete_post_files_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return model.NewAppError("ElasticsearchEngine.DeletePostFiles", "searchengine.elasticsearch.delete_post_files_failed", nil, res.String(), res.StatusCode)
	}

	return nil
}

// DeleteUserFiles deletes all files created by a user
func (e *ElasticsearchEngine) DeleteUserFiles(rctx request.CTX, userID string) *model.AppError {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 {
		return model.NewAppError("ElasticsearchEngine.DeleteUserFiles", "searchengine.elasticsearch.not_ready", nil, "", http.StatusInternalServerError)
	}

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"term": map[string]interface{}{
				"creator_id": userID,
			},
		},
	}

	queryJSON, err := json.Marshal(query)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.DeleteUserFiles", "searchengine.elasticsearch.delete_user_files_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	shouldRefresh := true
	req := esapi.DeleteByQueryRequest{
		Index:   []string{FILE_INDEX},
		Body:    strings.NewReader(string(queryJSON)),
		Refresh: &shouldRefresh,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.DeleteUserFiles", "searchengine.elasticsearch.delete_user_files_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return model.NewAppError("ElasticsearchEngine.DeleteUserFiles", "searchengine.elasticsearch.delete_user_files_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return model.NewAppError("ElasticsearchEngine.DeleteUserFiles", "searchengine.elasticsearch.delete_user_files_failed", nil, res.String(), res.StatusCode)
	}

	return nil
}

// DeleteFilesBatch deletes a batch of files based on creation time and limit
func (e *ElasticsearchEngine) DeleteFilesBatch(rctx request.CTX, endTime, limit int64) *model.AppError {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 {
		return model.NewAppError("ElasticsearchEngine.DeleteFilesBatch", "searchengine.elasticsearch.not_ready", nil, "", http.StatusInternalServerError)
	}

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"range": map[string]interface{}{
				"create_at": map[string]interface{}{
					"lte": endTime,
				},
			},
		},
		"size": limit,
	}

	queryJSON, err := json.Marshal(query)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.DeleteFilesBatch", "searchengine.elasticsearch.delete_files_batch_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	shouldRefresh := true
	req := esapi.DeleteByQueryRequest{
		Index:   []string{FILE_INDEX},
		Body:    strings.NewReader(string(queryJSON)),
		Refresh: &shouldRefresh,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.DeleteFilesBatch", "searchengine.elasticsearch.delete_files_batch_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return model.NewAppError("ElasticsearchEngine.DeleteFilesBatch", "searchengine.elasticsearch.delete_files_batch_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return model.NewAppError("ElasticsearchEngine.DeleteFilesBatch", "searchengine.elasticsearch.delete_files_batch_failed", nil, res.String(), res.StatusCode)
	}

	return nil
}

// DataRetentionDeleteIndexes deletes indices based on data retention policies
func (e *ElasticsearchEngine) DataRetentionDeleteIndexes(rctx request.CTX, cutoff time.Time) *model.AppError {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 {
		return model.NewAppError("ElasticsearchEngine.DataRetentionDeleteIndexes", "searchengine.elasticsearch.not_ready", nil, "", http.StatusInternalServerError)
	}

	cutoffMillis := cutoff.UnixNano() / int64(time.Millisecond)

	// Delete posts older than the cutoff
	postsQuery := map[string]interface{}{
		"query": map[string]interface{}{
			"range": map[string]interface{}{
				"create_at": map[string]interface{}{
					"lte": cutoffMillis,
				},
			},
		},
	}

	postsQueryJSON, err := json.Marshal(postsQuery)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.DataRetentionDeleteIndexes", "searchengine.elasticsearch.data_retention_delete_indexes_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	shouldRefresh := true
	req := esapi.DeleteByQueryRequest{
		Index:   []string{POST_INDEX},
		Body:    strings.NewReader(string(postsQueryJSON)),
		Refresh: &shouldRefresh,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(REQUEST_TIMEOUT_SECONDS)*time.Second)
	defer cancel()

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.DataRetentionDeleteIndexes", "searchengine.elasticsearch.data_retention_delete_indexes_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer res.Body.Close()

	if res.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&responseMap); err != nil {
			return model.NewAppError("ElasticsearchEngine.DataRetentionDeleteIndexes", "searchengine.elasticsearch.data_retention_delete_indexes_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return model.NewAppError("ElasticsearchEngine.DataRetentionDeleteIndexes", "searchengine.elasticsearch.data_retention_delete_indexes_failed", nil, res.String(), res.StatusCode)
	}

	// Also delete files older than the cutoff
	filesQuery := map[string]interface{}{
		"query": map[string]interface{}{
			"range": map[string]interface{}{
				"create_at": map[string]interface{}{
					"lte": cutoffMillis,
				},
			},
		},
	}

	filesQueryJSON, err := json.Marshal(filesQuery)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.DataRetentionDeleteIndexes", "searchengine.elasticsearch.data_retention_delete_indexes_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	fileReq := esapi.DeleteByQueryRequest{
		Index:   []string{FILE_INDEX},
		Body:    strings.NewReader(string(filesQueryJSON)),
		Refresh: &shouldRefresh,
	}

	fileRes, err := fileReq.Do(ctx, e.client)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.DataRetentionDeleteIndexes", "searchengine.elasticsearch.data_retention_delete_indexes_failed", nil, err.Error(), http.StatusInternalServerError)
	}
	defer fileRes.Body.Close()

	if fileRes.IsError() {
		var responseMap map[string]interface{}
		if err := json.NewDecoder(fileRes.Body).Decode(&responseMap); err != nil {
			return model.NewAppError("ElasticsearchEngine.DataRetentionDeleteIndexes", "searchengine.elasticsearch.data_retention_delete_indexes_failed", nil, err.Error(), http.StatusInternalServerError)
		}
		return model.NewAppError("ElasticsearchEngine.DataRetentionDeleteIndexes", "searchengine.elasticsearch.data_retention_delete_indexes_failed", nil, fileRes.String(), fileRes.StatusCode)
	}

	return nil
}

// Add a new method for batch indexing posts - useful for initial data loading
func (e *ElasticsearchEngine) BatchIndexPosts(posts []*model.Post, teamId string) *model.AppError {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 {
		return model.NewAppError("ElasticsearchEngine.BatchIndexPosts", "searchengine.elasticsearch.not_ready", nil, "", http.StatusInternalServerError)
	}

	if !e.IsIndexingEnabled() {
		return model.NewAppError("ElasticsearchEngine.BatchIndexPosts", "searchengine.elasticsearch.indexing_disabled", nil, "", http.StatusInternalServerError)
	}

	// Use the enhanced bulk indexing function
	indexed, failed, err := BulkIndexPosts(e, posts, teamId)
	if err != nil {
		return model.NewAppError("ElasticsearchEngine.BatchIndexPosts", "searchengine.elasticsearch.bulk_indexing_error", nil, err.Error(), http.StatusInternalServerError)
	}

	e.logger.Info("Batch indexed posts in Elasticsearch",
		mlog.Int("indexed", indexed),
		mlog.Int("failed", failed),
		mlog.Int("total", len(posts)),
	)

	return nil
}

// Add an optimized search method that can be used as an alternative to the standard SearchPosts
func (e *ElasticsearchEngine) SearchPostsOptimized(channels model.ChannelList, searchParams []*model.SearchParams, page, perPage int) ([]string, model.PostSearchMatches, *model.AppError) {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	if atomic.LoadInt32(&e.ready) == 0 || !e.IsSearchEnabled() {
		return nil, nil, model.NewAppError("ElasticsearchEngine.SearchPostsOptimized", "searchengine.elasticsearch.search_posts_disabled", nil, "", http.StatusInternalServerError)
	}

	// Use the optimized search function
	postIds, matches, err := OptimizedSearchPosts(e, channels, searchParams, page, perPage)
	if err != nil {
		return nil, nil, model.NewAppError("ElasticsearchEngine.SearchPostsOptimized", "searchengine.elasticsearch.search_posts_failed", nil, err.Error(), http.StatusInternalServerError)
	}

	return postIds, matches, nil
}