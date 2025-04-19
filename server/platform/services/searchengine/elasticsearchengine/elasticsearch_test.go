// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package elasticsearchengine

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/mattermost/mattermost/server/public/shared/request"
)

// ElasticsearchEngineTestSuite is a test suite for the Elasticsearch engine
type ElasticsearchEngineTestSuite struct {
	suite.Suite

	Engine      *ElasticsearchEngine
	Config      *model.Config
	Logger      *mlog.Logger
	ElasticURL  string
	testContext request.CTX
}

func TestElasticsearchEngineTestSuite(t *testing.T) {
	// Skip if not running a live test
	if os.Getenv("ES_LIVE_TEST") != "true" {
		t.Skip("Skipping Elasticsearch live test. Set ES_LIVE_TEST=true to enable.")
		return
	}

	suite.Run(t, &ElasticsearchEngineTestSuite{
		testContext: request.TestContext(t),
	})
}

func (s *ElasticsearchEngineTestSuite) SetupSuite() {
	s.Logger = mlog.CreateConsoleTestLogger(true, mlog.LvlDebug)

	// Get Elasticsearch URL from environment or use default
	s.ElasticURL = os.Getenv("ES_URL")
	if s.ElasticURL == "" {
		s.ElasticURL = "http://localhost:9200"
	}

	// Setup configuration
	s.Config = GetDefaultESConfig()
	*s.Config.ElasticsearchSettings.ConnectionURL = s.ElasticURL
	*s.Config.ElasticsearchSettings.EnableIndexing = true
	*s.Config.ElasticsearchSettings.EnableSearching = true
	*s.Config.ElasticsearchSettings.EnableAutocomplete = true
	*s.Config.ElasticsearchSettings.Sniff = false

	// Create the engine
	var err error
	s.Engine, err = NewElasticsearchEngine(s.Logger, s.Config)
	s.Require().NoError(err)

	// Start the engine
	s.Require().NoError(s.Engine.Start())

	// Ensure all indexes are cleaned up
	s.Engine.PurgeIndexes(s.testContext)
}

func (s *ElasticsearchEngineTestSuite) TearDownSuite() {
	if s.Engine != nil {
		s.Engine.Stop()
	}
	if s.Logger != nil {
		s.Logger.Shutdown()
	}
}

func (s *ElasticsearchEngineTestSuite) SetupTest() {
	// Clean up any existing indexes before each test
	s.Engine.PurgeIndexes(s.testContext)
	time.Sleep(1 * time.Second) // Give ES time to process
}

func (s *ElasticsearchEngineTestSuite) TestBasicOperations() {
	s.Run("Constructor", func() {
		// Test valid config
		engine, err := NewElasticsearchEngine(s.Logger, s.Config)
		s.Require().NoError(err)
		s.Require().NotNil(engine)
		s.Equal(ENGINE_NAME, engine.GetName())

		// Test invalid config (empty connection URL)
		invalidConfig := GetDefaultESConfig()
		*invalidConfig.ElasticsearchSettings.ConnectionURL = ""
		_, err = NewElasticsearchEngine(s.Logger, invalidConfig)
		s.Require().Error(err)
	})

	s.Run("StartStop", func() {
		engine, err := NewElasticsearchEngine(s.Logger, s.Config)
		s.Require().NoError(err)

		// Test Start
		err = engine.Start()
		s.Require().NoError(err)
		s.True(engine.IsActive())

		// Test Stop
		err = engine.Stop()
		s.Require().NoError(err)
		s.False(engine.IsActive())
	})
}

func (s *ElasticsearchEngineTestSuite) TestPostOperations() {
	s.Run("IndexAndSearchPost", func() {
		// Create a test post
		postId := model.NewId()
		channelId := model.NewId()
		userId := model.NewId()
		teamId := model.NewId()

		testMessage := "This is a test message for elasticsearch 搜索引擎"

		post := &model.Post{
			Id:        postId,
			ChannelId: channelId,
			UserId:    userId,
			Message:   testMessage,
			CreateAt:  model.GetMillis(),
		}

		// Index the post
		err := s.Engine.IndexPost(post, teamId)
		s.Require().NoError(err)

		// Refresh the index
		s.refreshPostIndex(post.CreateAt)

		// Search for the post
		searchParams := []*model.SearchParams{
			{
				Terms:     "test elasticsearch",
				IsHashtag: false,
				OrTerms:   false,
			},
		}

		channels := model.ChannelList{
			&model.Channel{
				Id: channelId,
			},
		}

		// Search posts
		postIds, matches, err := s.Engine.SearchPosts(channels, searchParams, 0, 10)
		s.Require().NoError(err)
		s.Contains(postIds, postId)
		s.NotEmpty(matches)

		// Test non-ASCII search
		searchParams = []*model.SearchParams{
			{
				Terms:     "搜索引擎",
				IsHashtag: false,
				OrTerms:   false,
			},
		}

		postIds, _, err = s.Engine.SearchPosts(channels, searchParams, 0, 10)
		s.Require().NoError(err)
		s.Contains(postIds, postId)

		// Test deleting post
		err = s.Engine.DeletePost(post)
		s.Require().NoError(err)

		// Refresh the index
		s.refreshPostIndex(post.CreateAt)

		// Verify post is no longer searchable
		postIds, _, err = s.Engine.SearchPosts(channels, searchParams, 0, 10)
		s.Require().NoError(err)
		s.NotContains(postIds, postId)
	})

	s.Run("DeleteChannelPosts", func() {
		// Create posts in different channels
		teamId := model.NewId()
		userId := model.NewId()
		channelId := model.NewId()
		channelToKeepId := model.NewId()

		// Create 5 posts in the channel that will be deleted
		var postsToDelete []*model.Post
		for i := 0; i < 5; i++ {
			post := &model.Post{
				Id:        model.NewId(),
				ChannelId: channelId,
				UserId:    userId,
				Message:   "Test post in channel to delete",
				CreateAt:  model.GetMillis(),
			}
			postsToDelete = append(postsToDelete, post)
			err := s.Engine.IndexPost(post, teamId)
			s.Require().NoError(err)
		}

		// Create 3 posts in the channel to keep
		var postsToKeep []*model.Post
		for i := 0; i < 3; i++ {
			post := &model.Post{
				Id:        model.NewId(),
				ChannelId: channelToKeepId,
				UserId:    userId,
				Message:   "Test post in channel to keep",
				CreateAt:  model.GetMillis(),
			}
			postsToKeep = append(postsToKeep, post)
			err := s.Engine.IndexPost(post, teamId)
			s.Require().NoError(err)
		}

		// Refresh the index
		s.refreshPostIndex(model.GetMillis())

		// Verify all posts are searchable
		channels := model.ChannelList{
			&model.Channel{Id: channelId},
			&model.Channel{Id: channelToKeepId},
		}
		searchParams := []*model.SearchParams{
			{
				Terms:     "Test post",
				IsHashtag: false,
				OrTerms:   false,
			},
		}
		postIds, _, err := s.Engine.SearchPosts(channels, searchParams, 0, 20)
		s.Require().NoError(err)
		s.Len(postIds, 8) // Total of 8 posts (5 to delete + 3 to keep)

		// Delete posts in one channel
		err = s.Engine.DeleteChannelPosts(s.testContext, channelId)
		s.Require().NoError(err)

		// Refresh the index
		s.refreshPostIndex(model.GetMillis())

		// Verify only posts in the kept channel are still searchable
		postIds, _, err = s.Engine.SearchPosts(channels, searchParams, 0, 20)
		s.Require().NoError(err)
		s.Len(postIds, 3) // Only 3 posts should remain

		// Verify each kept post is still there
		for _, post := range postsToKeep {
			s.Contains(postIds, post.Id)
		}

		// Verify each deleted post is gone
		for _, post := range postsToDelete {
			s.NotContains(postIds, post.Id)
		}
	})

	s.Run("DeleteUserPosts", func() {
		// Create posts from different users
		teamId := model.NewId()
		userId := model.NewId()
		userToKeepId := model.NewId()
		channelId := model.NewId()

		// Create 5 posts from the user that will be deleted
		var postsToDelete []*model.Post
		for i := 0; i < 5; i++ {
			post := &model.Post{
				Id:        model.NewId(),
				ChannelId: channelId,
				UserId:    userId,
				Message:   "Test post from user to delete",
				CreateAt:  model.GetMillis(),
			}
			postsToDelete = append(postsToDelete, post)
			err := s.Engine.IndexPost(post, teamId)
			s.Require().NoError(err)
		}

		// Create 3 posts from the user to keep
		var postsToKeep []*model.Post
		for i := 0; i < 3; i++ {
			post := &model.Post{
				Id:        model.NewId(),
				ChannelId: channelId,
				UserId:    userToKeepId,
				Message:   "Test post from user to keep",
				CreateAt:  model.GetMillis(),
			}
			postsToKeep = append(postsToKeep, post)
			err := s.Engine.IndexPost(post, teamId)
			s.Require().NoError(err)
		}

		// Refresh the index
		s.refreshPostIndex(model.GetMillis())

		// Verify all posts are searchable
		channels := model.ChannelList{
			&model.Channel{Id: channelId},
		}
		searchParams := []*model.SearchParams{
			{
				Terms:     "Test post",
				IsHashtag: false,
				OrTerms:   false,
			},
		}
		postIds, _, err := s.Engine.SearchPosts(channels, searchParams, 0, 20)
		s.Require().NoError(err)
		s.Len(postIds, 8) // Total of 8 posts (5 to delete + 3 to keep)

		// Delete posts from one user
		err = s.Engine.DeleteUserPosts(s.testContext, userId)
		s.Require().NoError(err)

		// Refresh the index
		s.refreshPostIndex(model.GetMillis())

		// Verify only posts from the kept user are still searchable
		postIds, _, err = s.Engine.SearchPosts(channels, searchParams, 0, 20)
		s.Require().NoError(err)
		s.Len(postIds, 3) // Only 3 posts should remain

		// Verify each kept post is still there
		for _, post := range postsToKeep {
			s.Contains(postIds, post.Id)
		}

		// Verify each deleted post is gone
		for _, post := range postsToDelete {
			s.NotContains(postIds, post.Id)
		}
	})
}

func (s *ElasticsearchEngineTestSuite) TestUserOperations() {
	s.Run("IndexAndSearchUser", func() {
		// Create a test user
		userId := model.NewId()
		teamId := model.NewId()
		channelId := model.NewId()

		user := &model.User{
			Id:        userId,
			Username:  "testuser",
			Email:     "test@example.com",
			Nickname:  "Test User",
			FirstName: "Test",
			LastName:  "User",
			CreateAt:  model.GetMillis(),
		}

		// Index the user
		err := s.Engine.IndexUser(user)
		s.Require().NoError(err)

		// Refresh the index
		refreshReq := s.Engine.client.Indices.Refresh
		_, err = refreshReq(refreshReq.WithIndex(s.Engine.GetFullUserIndexName()))
		s.Require().NoError(err)

		// Search for the user in a channel
		userIds, err := s.Engine.SearchUsersInChannel(teamId, channelId, "testuser", 0, 10)
		s.Require().NoError(err)
		
		// Since the user is not actually in the channel in this test,
		// this is just testing the search functionality without errors
		s.NotNil(userIds)

		// Test delete user
		err = s.Engine.DeleteUser(user)
		s.Require().NoError(err)

		// Refresh the index
		_, err = refreshReq(refreshReq.WithIndex(s.Engine.GetFullUserIndexName()))
		s.Require().NoError(err)
	})
}

func (s *ElasticsearchEngineTestSuite) TestChannelOperations() {
	s.Run("IndexAndSearchChannel", func() {
		// Create a test channel
		channelId := model.NewId()
		teamId := model.NewId()

		channel := &model.Channel{
			Id:          channelId,
			TeamId:      teamId,
			DisplayName: "Test Channel",
			Name:        "test-channel",
			Type:        model.ChannelTypeOpen,
			CreateAt:    model.GetMillis(),
		}

		// Index the channel
		err := s.Engine.IndexChannel(channel)
		s.Require().NoError(err)

		// Refresh the index
		refreshReq := s.Engine.client.Indices.Refresh
		_, err = refreshReq(refreshReq.WithIndex(s.Engine.GetFullChannelIndexName()))
		s.Require().NoError(err)

		// Search for the channel
		term := "test"
		channels, err := s.Engine.SearchChannels(teamId, term)
		s.Require().NoError(err)
		s.NotEmpty(channels)
		s.Equal(channelId, channels[0].Id)

		// Test delete channel
		err = s.Engine.DeleteChannel(channel)
		s.Require().NoError(err)

		// Refresh the index
		_, err = refreshReq(refreshReq.WithIndex(s.Engine.GetFullChannelIndexName()))
		s.Require().NoError(err)

		// Verify channel is no longer searchable
		channels, err = s.Engine.SearchChannels(teamId, term)
		s.Require().NoError(err)
		s.Empty(channels)
	})
}

func (s *ElasticsearchEngineTestSuite) TestFileOperations() {
	s.Run("IndexAndSearchFileInfo", func() {
		// Create a test file info
		fileId := model.NewId()
		channelId := model.NewId()
		userId := model.NewId()
		postId := model.NewId()

		fileInfo := &model.FileInfo{
			Id:        fileId,
			CreatorId: userId,
			PostId:    postId,
			ChannelId: channelId,
			Name:      "test_document.txt",
			Extension: "txt",
			MimeType:  "text/plain",
			Content:   "This is a test document for elasticsearch search",
			CreateAt:  model.GetMillis(),
		}

		// Index the file info
		err := s.Engine.IndexFile(fileInfo)
		s.Require().NoError(err)

		// Refresh the index
		refreshReq := s.Engine.client.Indices.Refresh
		_, err = refreshReq(refreshReq.WithIndex(s.Engine.GetFullFileInfoIndexName()))
		s.Require().NoError(err)

		// Search for the file
		searchParams := []*model.SearchParams{
			{
				Terms:     "test document",
				IsHashtag: false,
				OrTerms:   false,
			},
		}

		channels := model.ChannelList{
			&model.Channel{
				Id: channelId,
			},
		}

		// Search files
		fileIds, _, err := s.Engine.SearchFiles(channels, searchParams, 0, 10)
		s.Require().NoError(err)
		s.Contains(fileIds, fileId)

		// Test delete file
		err = s.Engine.DeleteFile(fileInfo)
		s.Require().NoError(err)

		// Refresh the index
		_, err = refreshReq(refreshReq.WithIndex(s.Engine.GetFullFileInfoIndexName()))
		s.Require().NoError(err)

		// Verify file is no longer searchable
		fileIds, _, err = s.Engine.SearchFiles(channels, searchParams, 0, 10)
		s.Require().NoError(err)
		s.NotContains(fileIds, fileId)
	})

	s.Run("DeletePostFiles", func() {
		// Create file infos with different post IDs
		channelId := model.NewId()
		userId := model.NewId()
		postId := model.NewId()
		otherPostId := model.NewId()

		// Create 3 files for the post to delete
		var filesToDelete []*model.FileInfo
		for i := 0; i < 3; i++ {
			fileInfo := &model.FileInfo{
				Id:        model.NewId(),
				CreatorId: userId,
				PostId:    postId,
				ChannelId: channelId,
				Name:      "test_document_to_delete.txt",
				Extension: "txt",
				MimeType:  "text/plain",
				Content:   "This is a test document that should be deleted",
				CreateAt:  model.GetMillis(),
			}
			filesToDelete = append(filesToDelete, fileInfo)
			err := s.Engine.IndexFile(fileInfo)
			s.Require().NoError(err)
		}

		// Create 2 files for another post that should be kept
		var filesToKeep []*model.FileInfo
		for i := 0; i < 2; i++ {
			fileInfo := &model.FileInfo{
				Id:        model.NewId(),
				CreatorId: userId,
				PostId:    otherPostId,
				ChannelId: channelId,
				Name:      "test_document_to_keep.txt",
				Extension: "txt",
				MimeType:  "text/plain",
				Content:   "This is a test document that should be kept",
				CreateAt:  model.GetMillis(),
			}
			filesToKeep = append(filesToKeep, fileInfo)
			err := s.Engine.IndexFile(fileInfo)
			s.Require().NoError(err)
		}

		// Refresh the index
		refreshReq := s.Engine.client.Indices.Refresh
		_, err := refreshReq(refreshReq.WithIndex(s.Engine.GetFullFileInfoIndexName()))
		s.Require().NoError(err)

		// Verify all files are searchable
		channels := model.ChannelList{
			&model.Channel{Id: channelId},
		}
		searchParams := []*model.SearchParams{
			{
				Terms:     "test document",
				IsHashtag: false,
				OrTerms:   false,
			},
		}
		fileIds, _, err := s.Engine.SearchFiles(channels, searchParams, 0, 20)
		s.Require().NoError(err)
		s.Len(fileIds, 5) // 3 to delete + 2 to keep

		// Delete files for one post
		err = s.Engine.DeletePostFiles(s.testContext, postId)
		s.Require().NoError(err)

		// Refresh the index
		_, err = refreshReq(refreshReq.WithIndex(s.Engine.GetFullFileInfoIndexName()))
		s.Require().NoError(err)

		// Verify only files from the other post are searchable
		fileIds, _, err = s.Engine.SearchFiles(channels, searchParams, 0, 20)
		s.Require().NoError(err)
		s.Len(fileIds, 2) // Only 2 files should remain

		// Verify each kept file is still there
		for _, file := range filesToKeep {
			s.Contains(fileIds, file.Id)
		}

		// Verify each deleted file is gone
		for _, file := range filesToDelete {
			s.NotContains(fileIds, file.Id)
		}
	})

	s.Run("DeleteUserFiles", func() {
		// Create file infos with different user IDs
		channelId := model.NewId()
		userId := model.NewId()
		otherUserId := model.NewId()
		postId := model.NewId()

		// Create 3 files for the user to delete
		var filesToDelete []*model.FileInfo
		for i := 0; i < 3; i++ {
			fileInfo := &model.FileInfo{
				Id:        model.NewId(),
				CreatorId: userId,
				PostId:    postId,
				ChannelId: channelId,
				Name:      "test_document_user_to_delete.txt",
				Extension: "txt",
				MimeType:  "text/plain",
				Content:   "This is a test document from a user that should be deleted",
				CreateAt:  model.GetMillis(),
			}
			filesToDelete = append(filesToDelete, fileInfo)
			err := s.Engine.IndexFile(fileInfo)
			s.Require().NoError(err)
		}

		// Create 2 files for another user that should be kept
		var filesToKeep []*model.FileInfo
		for i := 0; i < 2; i++ {
			fileInfo := &model.FileInfo{
				Id:        model.NewId(),
				CreatorId: otherUserId,
				PostId:    postId,
				ChannelId: channelId,
				Name:      "test_document_user_to_keep.txt",
				Extension: "txt",
				MimeType:  "text/plain",
				Content:   "This is a test document from a user that should be kept",
				CreateAt:  model.GetMillis(),
			}
			filesToKeep = append(filesToKeep, fileInfo)
			err := s.Engine.IndexFile(fileInfo)
			s.Require().NoError(err)
		}

		// Refresh the index
		refreshReq := s.Engine.client.Indices.Refresh
		_, err := refreshReq(refreshReq.WithIndex(s.Engine.GetFullFileInfoIndexName()))
		s.Require().NoError(err)

		// Verify all files are searchable
		channels := model.ChannelList{
			&model.Channel{Id: channelId},
		}
		searchParams := []*model.SearchParams{
			{
				Terms:     "test document",
				IsHashtag: false,
				OrTerms:   false,
			},
		}
		fileIds, _, err := s.Engine.SearchFiles(channels, searchParams, 0, 20)
		s.Require().NoError(err)
		s.Len(fileIds, 5) // 3 to delete + 2 to keep

		// Delete files for one user
		err = s.Engine.DeleteUserFiles(s.testContext, userId)
		s.Require().NoError(err)

		// Refresh the index
		_, err = refreshReq(refreshReq.WithIndex(s.Engine.GetFullFileInfoIndexName()))
		s.Require().NoError(err)

		// Verify only files from the other user are searchable
		fileIds, _, err = s.Engine.SearchFiles(channels, searchParams, 0, 20)
		s.Require().NoError(err)
		s.Len(fileIds, 2) // Only 2 files should remain

		// Verify each kept file is still there
		for _, file := range filesToKeep {
			s.Contains(fileIds, file.Id)
		}

		// Verify each deleted file is gone
		for _, file := range filesToDelete {
			s.NotContains(fileIds, file.Id)
		}
	})
}

func (s *ElasticsearchEngineTestSuite) TestAdminOperations() {
	s.Run("TestConfig", func() {
		// Test valid config
		err := s.Engine.TestConfig(context.Background(), s.Config)
		s.Require().NoError(err)

		// Test invalid config
		invalidConfig := GetDefaultESConfig()
		*invalidConfig.ElasticsearchSettings.ConnectionURL = "http://invalid-host:9200"
		
		err = s.Engine.TestConfig(context.Background(), invalidConfig)
		s.Require().Error(err)
	})

	s.Run("PurgeIndexes", func() {
		// Create a test index
		indexName := "test_" + model.NewId()
		createReq := s.Engine.client.Indices.Create
		_, err := createReq(indexName)
		s.Require().NoError(err)

		// Verify index exists
		existsReq := s.Engine.client.Indices.Exists
		resp, err := existsReq(existsReq.WithIndex(indexName))
		s.Require().NoError(err)
		s.Equal(200, resp.StatusCode)

		// Purge indexes (we'll use PurgeIndexList since it's more targeted)
		err = s.Engine.PurgeIndexList(context.Background(), []string{indexName})
		s.Require().NoError(err)

		// Verify index doesn't exist
		resp, err = existsReq(existsReq.WithIndex(indexName))
		s.Require().NoError(err)
		s.Equal(404, resp.StatusCode)
	})
}

// Helper methods

// refreshPostIndex refreshes the post index containing the post with the given createAt timestamp
func (s *ElasticsearchEngineTestSuite) refreshPostIndex(createAt int64) {
	indexName := BuildPostIndexName(*s.Config.ElasticsearchSettings.AggregatePostsAfterDays, 
		IndexBasePosts, 
		IndexBasePosts_MONTH, 
		time.Now(), 
		createAt)
	refreshReq := s.Engine.client.Indices.Refresh
	_, err := refreshReq(refreshReq.WithIndex(indexName))
	s.Require().NoError(err)
}

// createPost is a helper function to create a test post
func createPost(userId, channelId string) *model.Post {
	return &model.Post{
		Id:        model.NewId(),
		UserId:    userId,
		ChannelId: channelId,
		Message:   "test message",
		CreateAt:  model.GetMillis(),
	}
}

// verifyDocumentExists checks if a document exists in an Elasticsearch index
func verifyDocumentExists(t *testing.T, engine *ElasticsearchEngine, indexName, docId string) bool {
	getReq := engine.client.Get
	resp, err := getReq(indexName, docId)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return false
	}

	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return false
	}

	return result["found"].(bool)
}

// generateBulkPosts creates many posts for performance testing
func generateBulkPosts(userId, channelId string, count int) []*model.Post {
	posts := make([]*model.Post, count)
	for i := 0; i < count; i++ {
		posts[i] = &model.Post{
			Id:        model.NewId(),
			UserId:    userId,
			ChannelId: channelId,
			Message:   "bulk test message " + model.NewId(),
			CreateAt:  model.GetMillis(),
		}
	}
	return posts
} 