// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package elasticsearchengine

import (
	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/mattermost/mattermost/server/v8/channels/app/platform"
	"github.com/mattermost/mattermost/server/v8/platform/services/searchengine"
)

// RegisterWithSearchBroker registers the Elasticsearch engine with the search broker
func RegisterWithSearchBroker(ps *platform.PlatformService) error {
	// Create a new Elasticsearch engine with the platform's config
	engine, err := NewElasticsearchEngine(ps.GetLogger(), ps.Config())
	if err != nil {
		return err
	}

	// Register the engine with the search broker
	ps.SearchEngine().RegisterElasticsearchEngine(engine)

	// Start the engine if it's enabled in the config
	if *ps.Config().ElasticsearchSettings.EnableIndexing {
		if err := engine.Start(); err != nil {
			return err
		}
		
		ps.GetLogger().Info(
			"Elasticsearch engine registered and started successfully",
			mlog.String("version", engine.GetFullVersion()),
		)
	}

	return nil
}

// InitializeElasticsearch initializes the Elasticsearch engine
func InitializeElasticsearch(ps *platform.PlatformService) {
	// Register our implementation with the platform
	platform.RegisterElasticsearchInterface(func(s *platform.PlatformService) searchengine.SearchEngineInterface {
		engine, err := NewElasticsearchEngine(s.GetLogger(), s.Config())
		if err != nil {
			s.GetLogger().Error("Failed to initialize Elasticsearch engine", mlog.Err(err))
			return nil
		}
		return engine
	})
}