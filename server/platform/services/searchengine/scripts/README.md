# Mattermost Elasticsearch Integration

This directory contains scripts to help set up and optimize Elasticsearch for Mattermost.

## Quick Start

### 1. Set up Elasticsearch

The easiest way to set up Elasticsearch is using the provided Docker Compose file:

```bash
cd /home/user/Desktop/mattermost
docker-compose -f docker-compose-search.yml up -d elasticsearch kibana
```

Wait for Elasticsearch to start (usually takes about 30 seconds).

### 2. Run the Setup Script

This script sets up Elasticsearch with the proper index mappings and settings for Mattermost:

```bash
cd /home/user/Desktop/mattermost/server/platform/services/searchengine/scripts
chmod +x setup_elasticsearch.sh
./setup_elasticsearch.sh
```

### 3. Configure Mattermost

Edit your Mattermost configuration file (config.json) to enable Elasticsearch:

```json
{
  "ElasticsearchSettings": {
    "ConnectionUrl": "http://localhost:9200",
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
    "IndexPrefix": "mattermost",
    "LiveIndexingBatchSize": 1000,
    "BulkIndexingTimeWindowSeconds": 3600,
    "RequestTimeoutSeconds": 30
  }
}
```

### 4. Run Benchmarks

To test the performance of your setup, run the benchmark tools:

```bash
cd /home/user/Desktop/mattermost/server/platform/services/searchengine/bench
go build -o search_benchmark benchmark_cmd.go
./search_benchmark -es-url http://localhost:9200 -posts 10000 -users 100 -channels 10
```

For more extensive testing, increase the number of posts:

```bash
./search_benchmark -es-url http://localhost:9200 -posts 100000 -users 500 -channels 50
```

## Production Recommendations

For production environments, consider the following optimizations:

### Elasticsearch Configuration

1. **Memory**: Set JVM heap size to 50% of available RAM, but not more than 32GB:
   ```
   -Xms4g -Xmx4g
   ```

2. **Indexing Performance**: Adjust the following settings for better indexing performance:
   ```
   index.refresh_interval: 30s
   index.translog.flush_threshold_size: 1gb
   ```

3. **Search Performance**: Configure appropriate sharding:
   ```
   index.number_of_shards: 3      # For <10M messages
   index.number_of_shards: 5      # For 10-50M messages
   index.number_of_shards: 10     # For >50M messages
   ```

### Mattermost Configuration

1. **Batch Sizes**: Adjust batch sizes based on server capacity:
   ```
   "LiveIndexingBatchSize": 2000,         # For servers with 8GB+ RAM
   "BulkIndexingTimeWindowSeconds": 1800  # 30 minute batching window
   ```

2. **Index Settings**: For larger deployments, increase replication:
   ```
   "PostIndexReplicas": 1,     # For high availability
   "PostIndexShards": 3        # For better parallelism
   ```

## Monitoring and Maintenance

### Monitoring Elasticsearch

Monitor Elasticsearch using Kibana or directly via the API:

```bash
# Check cluster health
curl http://localhost:9200/_cluster/health?pretty

# Check index stats
curl http://localhost:9200/mattermost_posts/_stats?pretty

# Monitor indexing performance
curl http://localhost:9200/_nodes/stats/indices/indexing?pretty
```

### Maintenance Tasks

1. **Optimize Indices**: Run periodically for heavily fragmented indices:
   ```bash
   curl -X POST "http://localhost:9200/mattermost_posts/_forcemerge?max_num_segments=1"
   ```

2. **Backup Indices**: Create regular snapshots:
   ```bash
   # Register repository
   curl -X PUT "http://localhost:9200/_snapshot/mattermost_backup" -H 'Content-Type: application/json' -d'
   {
     "type": "fs",
     "settings": {
       "location": "/path/to/backups"
     }
   }'
   
   # Create snapshot
   curl -X PUT "http://localhost:9200/_snapshot/mattermost_backup/snapshot_1"
   ```

## Troubleshooting

### Common Issues

1. **Not Enough Memory**: If Elasticsearch is crashing, check if it has enough memory:
   ```bash
   # Check logs
   docker logs mattermost-elasticsearch
   ```

2. **Slow Indexing**: If indexing is slow, check for throttling:
   ```bash
   curl http://localhost:9200/_nodes/stats/indices/indexing?pretty
   ```

3. **Search Not Working**: Verify indices exist and have documents:
   ```bash
   curl http://localhost:9200/_cat/indices?v
   curl http://localhost:9200/mattermost_posts/_count
   ```

## Contact

If you have any questions or need help, please open an issue on the Mattermost GitHub repository. 