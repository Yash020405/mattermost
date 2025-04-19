// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package elasticsearchengine

import (
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
)

// GetDefaultESConfig returns a default Elasticsearch configuration
func GetDefaultESConfig() *model.Config {
	config := &model.Config{}
	config.SetDefaults()

	// Configure Elasticsearch settings
	config.ElasticsearchSettings.ConnectionURL = model.NewString("http://localhost:9200")
	config.ElasticsearchSettings.Username = model.NewString("")
	config.ElasticsearchSettings.Password = model.NewString("")
	config.ElasticsearchSettings.Sniff = model.NewBool(true)
	config.ElasticsearchSettings.EnableIndexing = model.NewBool(true)
	config.ElasticsearchSettings.EnableSearching = model.NewBool(true)
	config.ElasticsearchSettings.EnableAutocomplete = model.NewBool(true)
	config.ElasticsearchSettings.LiveIndexingBatchSize = model.NewInt(1)
	
	return config
}

// CreateTestEngine creates a new Elasticsearch engine for testing purposes
func CreateTestEngine(logger *mlog.Logger, connectionUrl string) (*ElasticsearchEngine, error) {
	config := GetDefaultESConfig()
	
	if connectionUrl != "" {
		config.ElasticsearchSettings.ConnectionURL = model.NewString(connectionUrl)
	}
	
	engine, err := NewElasticsearchEngine(logger, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create elasticsearch engine: %w", err)
	}
	
	if appErr := engine.Start(); appErr != nil {
		return nil, fmt.Errorf("failed to start elasticsearch engine: %s", appErr.Error())
	}
	
	return engine, nil
}