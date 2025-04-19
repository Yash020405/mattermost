package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"html/template"
	"os"
	"strconv"
	"strings"
)

const (
	DEFAULT_INPUT_FILE  = "search_comparison_results.csv"
	DEFAULT_OUTPUT_FILE = "benchmark_results.html"
)

var (
	inputFile  string
	outputFile string
)

func init() {
	flag.StringVar(&inputFile, "in", DEFAULT_INPUT_FILE, "Input CSV file with benchmark results")
	flag.StringVar(&outputFile, "out", DEFAULT_OUTPUT_FILE, "Output HTML file for visualization")
}

// BenchResult represents a single benchmark result
type BenchResult struct {
	Engine       string
	Query        string
	NumPosts     int
	ExecTimeMs   float64
	ResultsFound int
	IsFuzzy      bool
}

func main() {
	flag.Parse()
	
	// Read benchmark results from CSV
	fmt.Println("Reading benchmark results from", inputFile)
	results, err := readBenchResults(inputFile)
	if err != nil {
		fmt.Printf("Error reading benchmark results: %v\n", err)
		return
	}
	
	// Organize results into suitable format for charts
	exactTimes, fuzzyTimes, exactResults, fuzzyResults := organizeResults(results)
	
	// Generate HTML with charts
	fmt.Println("Generating visualization...")
	err = generateHTMLVisualization(outputFile, exactTimes, fuzzyTimes, exactResults, fuzzyResults)
	if err != nil {
		fmt.Printf("Error generating visualization: %v\n", err)
		return
	}
	
	fmt.Printf("Visualization created at %s\n", outputFile)
}

func readBenchResults(filePath string) ([]BenchResult, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	
	reader := csv.NewReader(file)
	
	// Read header
	header, err := reader.Read()
	if err != nil {
		return nil, err
	}
	
	// Find column indexes
	engineIdx := indexOf(header, "Engine")
	queryIdx := indexOf(header, "Query")
	numPostsIdx := indexOf(header, "NumPosts")
	execTimeIdx := indexOf(header, "ExecTimeMs")
	resultsFoundIdx := indexOf(header, "ResultsFound")
	isFuzzyIdx := indexOf(header, "IsFuzzy")
	
	if engineIdx < 0 || queryIdx < 0 || numPostsIdx < 0 || execTimeIdx < 0 || resultsFoundIdx < 0 || isFuzzyIdx < 0 {
		return nil, fmt.Errorf("missing required columns in CSV header")
	}
	
	// Read all the records
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	
	// Parse the records into BenchResult structs
	results := make([]BenchResult, 0, len(records))
	for _, record := range records {
		numPosts, _ := strconv.Atoi(record[numPostsIdx])
		execTime, _ := strconv.ParseFloat(record[execTimeIdx], 64)
		resultsFound, _ := strconv.Atoi(record[resultsFoundIdx])
		isFuzzy, _ := strconv.ParseBool(record[isFuzzyIdx])
		
		result := BenchResult{
			Engine:       record[engineIdx],
			Query:        record[queryIdx],
			NumPosts:     numPosts,
			ExecTimeMs:   execTime,
			ResultsFound: resultsFound,
			IsFuzzy:      isFuzzy,
		}
		
		results = append(results, result)
	}
	
	return results, nil
}

func indexOf(slice []string, item string) int {
	for i, s := range slice {
		if s == item {
			return i
		}
	}
	return -1
}

func organizeResults(results []BenchResult) (map[string][]float64, map[string][]float64, map[string][]int, map[string][]int) {
	// Extract the unique engines and queries
	engines := make(map[string]bool)
	queries := make(map[string]bool)
	
	for _, r := range results {
		engines[r.Engine] = true
		queries[r.Query] = true
	}
	
	// Create maps for data series
	exactTimes := make(map[string][]float64)
	fuzzyTimes := make(map[string][]float64)
	exactResults := make(map[string][]int)
	fuzzyResults := make(map[string][]int)
	
	// Initialize maps with empty arrays for each engine
	for engine := range engines {
		exactTimes[engine] = make([]float64, 0)
		fuzzyTimes[engine] = make([]float64, 0)
		exactResults[engine] = make([]int, 0)
		fuzzyResults[engine] = make([]int, 0)
	}
	
	// Group results by engine and fuzzy status
	for _, r := range results {
		if r.IsFuzzy {
			fuzzyTimes[r.Engine] = append(fuzzyTimes[r.Engine], r.ExecTimeMs)
			fuzzyResults[r.Engine] = append(fuzzyResults[r.Engine], r.ResultsFound)
		} else {
			exactTimes[r.Engine] = append(exactTimes[r.Engine], r.ExecTimeMs)
			exactResults[r.Engine] = append(exactResults[r.Engine], r.ResultsFound)
		}
	}
	
	return exactTimes, fuzzyTimes, exactResults, fuzzyResults
}

func generateHTMLVisualization(filePath string, exactTimes map[string][]float64, fuzzyTimes map[string][]float64, exactResults map[string][]int, fuzzyResults map[string][]int) error {
	// Calculate averages
	exactTimesAvg := make(map[string]float64)
	fuzzyTimesAvg := make(map[string]float64)
	exactResultsAvg := make(map[string]float64)
	fuzzyResultsAvg := make(map[string]float64)
	
	engines := make([]string, 0)
	
	for engine, times := range exactTimes {
		engines = append(engines, engine)
		exactTimesAvg[engine] = average(times)
		fuzzyTimesAvg[engine] = average(fuzzyTimes[engine])
		exactResultsAvg[engine] = float64Average(exactResults[engine])
		fuzzyResultsAvg[engine] = float64Average(fuzzyResults[engine])
	}
	
	// Prepare data for the template
	type TemplateData struct {
		Engines         []string
		ExactTimesJSON  string
		FuzzyTimesJSON  string
		ExactResultsJSON string
		FuzzyResultsJSON string
		Title           string
	}
	
	data := TemplateData{
		Engines:         engines,
		ExactTimesJSON:  mapToJSArray(exactTimesAvg),
		FuzzyTimesJSON:  mapToJSArray(fuzzyTimesAvg),
		ExactResultsJSON: mapToJSArray(exactResultsAvg),
		FuzzyResultsJSON: mapToJSArray(fuzzyResultsAvg),
		Title:           "Search Engine Benchmark Results",
	}
	
	// Create HTML template
	templateHTML := `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{.Title}}</title>
    <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
    <style>
        body {
            font-family: Arial, sans-serif;
            margin: 20px;
            background-color: #f5f5f5;
        }
        .container {
            display: flex;
            flex-wrap: wrap;
            justify-content: center;
            gap: 20px;
        }
        .chart-container {
            width: 600px;
            height: 400px;
            background-color: white;
            border-radius: 8px;
            box-shadow: 0 4px 8px rgba(0,0,0,0.1);
            padding: 20px;
            margin-bottom: 20px;
        }
        h1 {
            text-align: center;
            color: #333;
        }
        .summary {
            max-width: 800px;
            margin: 20px auto;
            background-color: white;
            border-radius: 8px;
            box-shadow: 0 4px 8px rgba(0,0,0,0.1);
            padding: 20px;
        }
    </style>
</head>
<body>
    <h1>{{.Title}}</h1>
    
    <div class="container">
        <div class="chart-container">
            <canvas id="executionTimeChart"></canvas>
        </div>
        <div class="chart-container">
            <canvas id="resultCountChart"></canvas>
        </div>
    </div>
    
    <div class="summary">
        <h2>Benchmark Summary</h2>
        <p>
            This benchmark compares the performance of different search engines in terms of execution time and search result accuracy.
            The tests were run with both exact matches and fuzzy matches (to simulate typos and misspellings).
        </p>
        <h3>Key Findings:</h3>
        <ul id="findings"></ul>
    </div>

    <script>
        // Chart data
        const engines = {{.Engines}};
        const exactTimes = {{.ExactTimesJSON}};
        const fuzzyTimes = {{.FuzzyTimesJSON}};
        const exactResults = {{.ExactResultsJSON}};
        const fuzzyResults = {{.FuzzyResultsJSON}};

        // Create execution time chart
        const timeCtx = document.getElementById('executionTimeChart').getContext('2d');
        new Chart(timeCtx, {
            type: 'bar',
            data: {
                labels: engines,
                datasets: [
                    {
                        label: 'Exact Match (ms)',
                        data: exactTimes,
                        backgroundColor: 'rgba(54, 162, 235, 0.7)',
                        borderColor: 'rgba(54, 162, 235, 1)',
                        borderWidth: 1
                    },
                    {
                        label: 'Fuzzy Match (ms)',
                        data: fuzzyTimes,
                        backgroundColor: 'rgba(255, 99, 132, 0.7)',
                        borderColor: 'rgba(255, 99, 132, 1)',
                        borderWidth: 1
                    }
                ]
            },
            options: {
                responsive: true,
                plugins: {
                    title: {
                        display: true,
                        text: 'Average Execution Time (lower is better)'
                    },
                },
                scales: {
                    y: {
                        beginAtZero: true,
                        title: {
                            display: true,
                            text: 'Time (ms)'
                        }
                    }
                }
            }
        });

        // Create result count chart
        const resultCtx = document.getElementById('resultCountChart').getContext('2d');
        new Chart(resultCtx, {
            type: 'bar',
            data: {
                labels: engines,
                datasets: [
                    {
                        label: 'Exact Match Results',
                        data: exactResults,
                        backgroundColor: 'rgba(75, 192, 192, 0.7)',
                        borderColor: 'rgba(75, 192, 192, 1)',
                        borderWidth: 1
                    },
                    {
                        label: 'Fuzzy Match Results',
                        data: fuzzyResults,
                        backgroundColor: 'rgba(255, 159, 64, 0.7)',
                        borderColor: 'rgba(255, 159, 64, 1)',
                        borderWidth: 1
                    }
                ]
            },
            options: {
                responsive: true,
                plugins: {
                    title: {
                        display: true,
                        text: 'Average Results Found (higher may be better)'
                    },
                },
                scales: {
                    y: {
                        beginAtZero: true,
                        title: {
                            display: true,
                            text: 'Count'
                        }
                    }
                }
            }
        });

        // Generate findings
        const findings = document.getElementById('findings');
        
        // Compare execution times
        if (engines.length > 1) {
            const fastestExact = engines.reduce((a, b) => exactTimes[engines.indexOf(a)] < exactTimes[engines.indexOf(b)] ? a : b);
            const fastestFuzzy = engines.reduce((a, b) => fuzzyTimes[engines.indexOf(a)] < fuzzyTimes[engines.indexOf(b)] ? a : b);
            
            const exactTimeFinding = document.createElement('li');
            exactTimeFinding.textContent = fastestExact + " has the fastest execution time for exact matches.";
            findings.appendChild(exactTimeFinding);
            
            const fuzzyTimeFinding = document.createElement('li');
            fuzzyTimeFinding.textContent = fastestFuzzy + " has the fastest execution time for fuzzy matches.";
            findings.appendChild(fuzzyTimeFinding);
        }
        
        // Compare fuzzy matching effectiveness
        for (let i = 0; i < engines.length; i++) {
            const engine = engines[i];
            const exactTime = exactTimes[i];
            const fuzzyTime = fuzzyTimes[i];
            const exactResult = exactResults[i];
            const fuzzyResult = fuzzyResults[i];
            
            const fuzzySlower = ((fuzzyTime - exactTime) / exactTime * 100).toFixed(1);
            const fuzzyMoreResults = ((fuzzyResult - exactResult) / exactResult * 100).toFixed(1);
            
            const timeFinding = document.createElement('li');
            if (fuzzyTime > exactTime) {
                timeFinding.textContent = engine + ": Fuzzy search is " + fuzzySlower + "% slower than exact search.";
            } else {
                timeFinding.textContent = engine + ": Fuzzy search is faster than exact search (unusual).";
            }
            findings.appendChild(timeFinding);
            
            const resultFinding = document.createElement('li');
            if (fuzzyResult > exactResult) {
                resultFinding.textContent = engine + ": Fuzzy search found " + fuzzyMoreResults + "% more results than exact search.";
            } else if (fuzzyResult < exactResult) {
                resultFinding.textContent = engine + ": Exact search found more results than fuzzy search (unusual).";
            } else {
                resultFinding.textContent = engine + ": Both search types found the same number of results.";
            }
            findings.appendChild(resultFinding);
        }
    </script>
</body>
</html>
`
	
	// Create new template and parse it
	tmpl, err := template.New("benchmarks").Parse(templateHTML)
	if err != nil {
		return err
	}
	
	// Create the output file
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	
	// Execute the template
	err = tmpl.Execute(file, data)
	if err != nil {
		return err
	}
	
	return nil
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func float64Average(values []int) float64 {
	if len(values) == 0 {
		return 0
	}
	
	var sum int
	for _, v := range values {
		sum += v
	}
	return float64(sum) / float64(len(values))
}

func mapToJSArray(m map[string]float64) string {
	// Convert map values to a JavaScript array format
	values := make([]string, 0, len(m))
	for _, v := range m {
		values = append(values, fmt.Sprintf("%.2f", v))
	}
	
	return "[" + strings.Join(values, ", ") + "]"
}