#!/bin/bash
# Setup script for Elasticsearch with Mattermost
# This script initializes Elasticsearch with the correct settings and mappings for Mattermost

set -e

# Configuration
ES_HOST=${ES_HOST:-localhost}
ES_PORT=${ES_PORT:-9200}
ES_URL="http://${ES_HOST}:${ES_PORT}"
INDEX_PREFIX=${INDEX_PREFIX:-mattermost}
POST_INDEX="${INDEX_PREFIX}_posts"
USER_INDEX="${INDEX_PREFIX}_users"
CHANNEL_INDEX="${INDEX_PREFIX}_channels"
FILE_INDEX="${INDEX_PREFIX}_files"

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${YELLOW}Setting up Elasticsearch for Mattermost${NC}"
echo "Using Elasticsearch at ${ES_URL}"

# Check if Elasticsearch is running
echo "Checking Elasticsearch connection..."
if ! curl -s --connect-timeout 5 "${ES_URL}" > /dev/null; then
    echo -e "${RED}Error: Could not connect to Elasticsearch at ${ES_URL}${NC}"
    echo "Please ensure Elasticsearch is running or set a different URL with ES_HOST and ES_PORT environment variables"
    exit 1
fi

# Get Elasticsearch version
ES_VERSION=$(curl -s "${ES_URL}" | jq -r '.version.number')
echo -e "Connected to Elasticsearch ${GREEN}${ES_VERSION}${NC}"

# Create post index with appropriate mappings
echo "Creating post index: ${POST_INDEX}"
curl -X DELETE "${ES_URL}/${POST_INDEX}" 2>/dev/null || true
curl -X PUT "${ES_URL}/${POST_INDEX}" -H 'Content-Type: application/json' -d '{
  "settings": {
    "number_of_shards": 1,
    "number_of_replicas": 0,
    "index": {
      "analysis": {
        "analyzer": {
          "mattermost_analyzer": {
            "type": "custom",
            "tokenizer": "standard",
            "filter": [
              "lowercase",
              "asciifolding"
            ]
          }
        }
      }
    }
  },
  "mappings": {
    "properties": {
      "id": { "type": "keyword" },
      "team_id": { "type": "keyword" },
      "channel_id": { "type": "keyword" },
      "user_id": { "type": "keyword" },
      "message": {
        "type": "text",
        "analyzer": "mattermost_analyzer",
        "search_analyzer": "mattermost_analyzer"
      },
      "type": { "type": "keyword" },
      "create_at": { "type": "date", "format": "epoch_millis" },
      "update_at": { "type": "date", "format": "epoch_millis" },
      "delete_at": { "type": "date", "format": "epoch_millis" },
      "hashtags": {
        "type": "text",
        "analyzer": "mattermost_analyzer",
        "search_analyzer": "mattermost_analyzer"
      }
    }
  }
}'

# Create user index with appropriate mappings
echo "Creating user index: ${USER_INDEX}"
curl -X DELETE "${ES_URL}/${USER_INDEX}" 2>/dev/null || true
curl -X PUT "${ES_URL}/${USER_INDEX}" -H 'Content-Type: application/json' -d '{
  "settings": {
    "number_of_shards": 1,
    "number_of_replicas": 0,
    "index": {
      "analysis": {
        "analyzer": {
          "mattermost_analyzer": {
            "type": "custom",
            "tokenizer": "standard",
            "filter": [
              "lowercase",
              "asciifolding"
            ]
          }
        }
      }
    }
  },
  "mappings": {
    "properties": {
      "id": { "type": "keyword" },
      "username": {
        "type": "text",
        "analyzer": "mattermost_analyzer", 
        "search_analyzer": "mattermost_analyzer",
        "fields": {
          "keyword": { "type": "keyword" }
        }
      },
      "first_name": {
        "type": "text",
        "analyzer": "mattermost_analyzer", 
        "search_analyzer": "mattermost_analyzer"
      },
      "last_name": {
        "type": "text",
        "analyzer": "mattermost_analyzer", 
        "search_analyzer": "mattermost_analyzer"
      },
      "nickname": {
        "type": "text",
        "analyzer": "mattermost_analyzer", 
        "search_analyzer": "mattermost_analyzer"
      },
      "email": {
        "type": "text",
        "analyzer": "mattermost_analyzer", 
        "search_analyzer": "mattermost_analyzer",
        "fields": {
          "keyword": { "type": "keyword" }
        }
      },
      "channel_ids": { "type": "keyword" },
      "team_ids": { "type": "keyword" },
      "create_at": { "type": "date", "format": "epoch_millis" },
      "delete_at": { "type": "date", "format": "epoch_millis" }
    }
  }
}'

# Create channel index with appropriate mappings
echo "Creating channel index: ${CHANNEL_INDEX}"
curl -X DELETE "${ES_URL}/${CHANNEL_INDEX}" 2>/dev/null || true
curl -X PUT "${ES_URL}/${CHANNEL_INDEX}" -H 'Content-Type: application/json' -d '{
  "settings": {
    "number_of_shards": 1,
    "number_of_replicas": 0,
    "index": {
      "analysis": {
        "analyzer": {
          "mattermost_analyzer": {
            "type": "custom",
            "tokenizer": "standard",
            "filter": [
              "lowercase",
              "asciifolding"
            ]
          }
        }
      }
    }
  },
  "mappings": {
    "properties": {
      "id": { "type": "keyword" },
      "team_id": { "type": "keyword" },
      "display_name": {
        "type": "text",
        "analyzer": "mattermost_analyzer", 
        "search_analyzer": "mattermost_analyzer"
      },
      "name": {
        "type": "text",
        "analyzer": "mattermost_analyzer", 
        "search_analyzer": "mattermost_analyzer"
      },
      "header": {
        "type": "text",
        "analyzer": "mattermost_analyzer", 
        "search_analyzer": "mattermost_analyzer"
      },
      "purpose": {
        "type": "text",
        "analyzer": "mattermost_analyzer", 
        "search_analyzer": "mattermost_analyzer"
      },
      "type": { "type": "keyword" },
      "create_at": { "type": "date", "format": "epoch_millis" },
      "delete_at": { "type": "date", "format": "epoch_millis" }
    }
  }
}'

# Create file index with appropriate mappings
echo "Creating file index: ${FILE_INDEX}"
curl -X DELETE "${ES_URL}/${FILE_INDEX}" 2>/dev/null || true
curl -X PUT "${ES_URL}/${FILE_INDEX}" -H 'Content-Type: application/json' -d '{
  "settings": {
    "number_of_shards": 1,
    "number_of_replicas": 0,
    "index": {
      "analysis": {
        "analyzer": {
          "mattermost_analyzer": {
            "type": "custom",
            "tokenizer": "standard",
            "filter": [
              "lowercase",
              "asciifolding"
            ]
          }
        }
      }
    }
  },
  "mappings": {
    "properties": {
      "id": { "type": "keyword" },
      "creator_id": { "type": "keyword" },
      "post_id": { "type": "keyword" },
      "channel_id": { "type": "keyword" },
      "name": {
        "type": "text",
        "analyzer": "mattermost_analyzer", 
        "search_analyzer": "mattermost_analyzer"
      },
      "extension": { "type": "keyword" },
      "content": {
        "type": "text",
        "analyzer": "mattermost_analyzer", 
        "search_analyzer": "mattermost_analyzer"
      },
      "mime_type": { "type": "keyword" },
      "create_at": { "type": "date", "format": "epoch_millis" },
      "update_at": { "type": "date", "format": "epoch_millis" },
      "delete_at": { "type": "date", "format": "epoch_millis" }
    }
  }
}'

# Configure Elasticsearch for optimal performance
echo "Configuring Elasticsearch for optimal performance"
curl -X PUT "${ES_URL}/_cluster/settings" -H 'Content-Type: application/json' -d '{
  "persistent": {
    "indices.query.bool.max_clause_count": 10000
  }
}'

# Add index templates for future indices
echo "Adding index templates"
curl -X PUT "${ES_URL}/_template/mattermost_template" -H 'Content-Type: application/json' -d '{
  "index_patterns": ["'"${INDEX_PREFIX}"'_*"],
  "settings": {
    "index": {
      "refresh_interval": "1s",
      "number_of_shards": 1,
      "number_of_replicas": 0
    }
  }
}'

echo -e "${GREEN}Elasticsearch setup complete!${NC}"
echo -e "Created indices:"
echo -e "  - ${POST_INDEX}"
echo -e "  - ${USER_INDEX}"
echo -e "  - ${CHANNEL_INDEX}"
echo -e "  - ${FILE_INDEX}"
echo -e "\nUse these settings in your Mattermost config.json:"
echo -e '{
  "ElasticsearchSettings": {
    "ConnectionUrl": "'"${ES_URL}"'",
    "Username": "",
    "Password": "",
    "EnableIndexing": true,
    "EnableSearching": true,
    "EnableAutocomplete": true,
    "Sniff": false,
    "PostIndexReplicas": 0,
    "PostIndexShards": 1,
    "ChannelIndexReplicas": 0,
    "ChannelIndexShards": 1,
    "UserIndexReplicas": 0,
    "UserIndexShards": 1,
    "IndexPrefix": "'"${INDEX_PREFIX}"'",
    "LiveIndexingBatchSize": 1000,
    "BulkIndexingTimeWindowSeconds": 3600,
    "RequestTimeoutSeconds": 30
  }
}' 