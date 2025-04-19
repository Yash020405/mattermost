// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package elasticsearchengine

import (
	"github.com/mattermost/mattermost/server/v8/channels/app"
	"github.com/mattermost/mattermost/server/v8/channels/app/platform"
)

func init() {
	// Register our Elasticsearch implementation
	app.RegisterSearchEngineInitializer(func(ps *platform.PlatformService) {
		if ps != nil {
			InitializeElasticsearch(ps)
		}
	})
}