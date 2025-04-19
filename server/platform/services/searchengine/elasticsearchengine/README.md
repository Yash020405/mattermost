# Elasticsearch Engine Tests

This directory contains tests for the Elasticsearch implementation in Mattermost.

## Running the Tests

The tests in this directory are designed to run against a live Elasticsearch instance. By default, they will be skipped unless the environment variable `ES_LIVE_TEST` is set to `true`.

### Prerequisites

1. A running Elasticsearch instance (version 7.x or 8.x)
2. The Elasticsearch instance should be accessible on the default URL (`http://localhost:9200`) or you can specify a custom URL using the `ES_URL` environment variable.

### Running with a Local Elasticsearch Instance

1. Start your local Elasticsearch instance:
   ```bash
   # Using Docker
   docker run -d --name elasticsearch -p 9200:9200 -p 9300:9300 -e "discovery.type=single-node" -e "xpack.security.enabled=false" elasticsearch:8.6.0
   ```

2. Run the tests with the environment variable:
   ```bash
   ES_LIVE_TEST=true go test -v ./platform/services/searchengine/elasticsearchengine
   ```

### Running with a Remote Elasticsearch Instance

If your Elasticsearch instance is on a different host or port, use the environment variable:

```bash
ES_LIVE_TEST=true ES_URL=http://your-es-host:9200 go test -v ./platform/services/searchengine/elasticsearchengine
```

## Comparing Elasticsearch with Bleve Engine

To compare performance and functionality between Elasticsearch and Bleve engines, you can run tests for both and analyze the results.

### Running Bleve Tests

Bleve engine tests run without any external dependencies, as they use an in-memory storage:

```bash
go test -v ./platform/services/searchengine/bleveengine
```

### Performance Comparison

To compare performance between the two engines:

1. Run the basic performance benchmark for both engines:

   ```bash
   # For Elasticsearch
   ES_LIVE_TEST=true go test -v ./platform/services/searchengine/elasticsearchengine -bench=. -benchtime=10s

   # For Bleve
   go test -v ./platform/services/searchengine/bleveengine -bench=. -benchtime=10s
   ```

2. For more detailed benchmarks, you can use the benchmark tool in the `searchengine/bench` directory.

### Functionality Comparison

Both search engines implement the same `SearchEngineInterface` as defined in `searchengine/interface.go`, ensuring consistent functionality. The tests cover the core operations for both engines:

1. **Basic Operations:** Engine initialization, start/stop, configuration
2. **Indexing and Searching:** Post, User, Channel, File
3. **Deletion:** Delete operations (post, channel, user, file)
4. **Administrative Functions:** Configuration testing, index purging

## Test Coverage

The tests cover the following functionality:

1. **Basic Operations**:
   - Constructor functionality
   - Start and stop operations
   - Configuration testing

2. **Indexing and Searching**:
   - Post indexing and searching
   - User indexing and searching
   - Channel indexing and searching
   - File indexing and searching

3. **Deletion Operations**:
   - Post deletion
   - User deletion
   - Channel deletion
   - File deletion
   - Channel posts deletion
   - User posts deletion
   - Post files deletion
   - User files deletion

4. **Admin Operations**:
   - Configuration testing
   - Index purging

## Key Differences Between Elasticsearch and Bleve Engines

While both engines implement the same interface, there are some important differences to note:

1. **Deployment**: 
   - Elasticsearch requires a separate server setup
   - Bleve is embedded and requires no external services

2. **Scalability**:
   - Elasticsearch offers superior scalability for large installations
   - Bleve works well for small to medium deployments

3. **Features**:
   - Elasticsearch offers more advanced search features (better multi-language support, phonetic matching)
   - Bleve is simpler to set up and maintain but has fewer advanced features

4. **Performance**:
   - Elasticsearch generally has better query performance for large datasets
   - Bleve has lower latency for small deployments and fewer resources

## Debugging Tests

If tests are failing, check the following:

1. Verify that your Elasticsearch instance is running and accessible
2. Check Elasticsearch logs for any errors
3. Verify the connection URL is correct
4. If using Elasticsearch 8.x, make sure security is disabled or proper credentials are provided

## Testing in CI Environment

For CI environments, you can configure Docker containers for both engines and run the tests in parallel.

Example GitHub Actions workflow:

```yaml
jobs:
  test:
    runs-on: ubuntu-latest
    services:
      elasticsearch:
        image: elasticsearch:8.6.0
        env:
          discovery.type: single-node
          xpack.security.enabled: false
        ports:
          - 9200:9200
    steps:
      - uses: actions/checkout@v2
      - name: Set up Go
        uses: actions/setup-go@v2
        with:
          go-version: 1.19
      - name: Run Elasticsearch tests
        run: ES_LIVE_TEST=true go test -v ./platform/services/searchengine/elasticsearchengine
        env:
          ES_LIVE_TEST: "true"
      - name: Run Bleve tests
        run: go test -v ./platform/services/searchengine/bleveengine
``` 