package loadgen

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/kubelogs/kubelogs/api/storagepb"
)

// Predefined realistic Kubernetes namespaces
var defaultNamespaces = []string{
	"default",
	"kube-system",
	"production",
	"staging",
	"monitoring",
	"logging",
	"ingress-nginx",
	"cert-manager",
}

// Predefined deployment name prefixes
var deploymentPrefixes = []string{
	"api-server",
	"web-frontend",
	"worker",
	"scheduler",
	"cache",
	"database",
	"queue-processor",
	"auth-service",
	"gateway",
	"metrics-collector",
}

// Container name options
var containerNames = []string{
	"main",
	"sidecar",
	"init-container",
	"proxy",
	"logger",
}

// Realistic log message templates by severity
var logTemplates = map[uint32][]string{
	0: { // UNKNOWN
		"Unclassified event occurred",
	},
	1: { // TRACE
		"Entering function processRequest",
		"Exiting function handleConnection",
		"Trace point reached: checkpoint-%d",
	},
	2: { // DEBUG
		"Processing request id=%d",
		"Cache lookup for key: user:%d",
		"Database query executed in %dms",
		"HTTP request: GET /api/v1/resource/%d",
		"Goroutine pool size: %d",
	},
	3: { // INFO
		"Server started successfully on port %d",
		"Request completed: status=200 duration=%dms",
		"User authenticated: user_id=%d",
		"Job completed: processed %d items",
		"Health check passed",
		"Configuration reloaded",
		"Connected to database",
		"Cache warmed up with %d entries",
	},
	4: { // WARN
		"Request took longer than expected: duration=%dms",
		"Retry attempt %d for operation",
		"Connection pool exhausted, waiting for connection",
		"Rate limit approaching: current=%d",
		"Deprecated API endpoint called: /api/v1/legacy",
		"Certificate expires in %d days",
	},
	5: { // ERROR
		"Failed to connect to database: connection refused",
		"Request failed: status=500 error=\"internal server error\"",
		"Timeout waiting for response: exceeded %dms",
		"Invalid request payload: missing required field",
		"Authentication failed for user: invalid credentials",
		"Out of memory: requested=%dMB",
		"Circuit breaker opened for service: upstream-api",
	},
	6: { // FATAL
		"FATAL: Unable to start server: port %d already in use",
		"PANIC: nil pointer dereference in handler",
		"FATAL: Database migration failed: incompatible schema",
		"PANIC: stack overflow detected",
	},
}

// Generator creates realistic fake log entries.
type Generator struct {
	rng  *rand.Rand
	cfg  Config
	pods []podInfo
}

type podInfo struct {
	namespace  string
	name       string
	containers []string
}

// NewGenerator creates a new log generator.
func NewGenerator(cfg Config) *Generator {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Generate namespaces (use predefined ones, then add random if needed)
	namespaces := make([]string, 0, cfg.Namespaces)
	for i := 0; i < cfg.Namespaces && i < len(defaultNamespaces); i++ {
		namespaces = append(namespaces, defaultNamespaces[i])
	}
	for i := len(namespaces); i < cfg.Namespaces; i++ {
		namespaces = append(namespaces, fmt.Sprintf("namespace-%d", i))
	}

	// Generate pods distributed across namespaces
	pods := make([]podInfo, 0, cfg.Pods)
	for i := 0; i < cfg.Pods; i++ {
		ns := namespaces[i%len(namespaces)]
		prefix := deploymentPrefixes[rng.Intn(len(deploymentPrefixes))]

		// Generate Kubernetes-style pod name: deployment-xxxxx-xxxxx
		podName := fmt.Sprintf("%s-%s-%s",
			prefix,
			randomString(rng, 5),
			randomString(rng, 5),
		)

		// Each pod has 1-3 containers
		numContainers := rng.Intn(3) + 1
		containers := make([]string, numContainers)
		containers[0] = "main" // Always have main container
		for j := 1; j < numContainers; j++ {
			containers[j] = containerNames[rng.Intn(len(containerNames)-1)+1]
		}

		pods = append(pods, podInfo{
			namespace:  ns,
			name:       podName,
			containers: containers,
		})
	}

	return &Generator{
		rng:  rng,
		cfg:  cfg,
		pods: pods,
	}
}

// Next generates the next log entry.
func (g *Generator) Next() *storagepb.LogEntry {
	// Select random pod
	pod := g.pods[g.rng.Intn(len(g.pods))]
	container := pod.containers[g.rng.Intn(len(pod.containers))]

	// Determine severity based on error rate
	severity := g.randomSeverity()

	// Generate message from template
	message := g.randomMessage(severity)

	return &storagepb.LogEntry{
		TimestampNanos: time.Now().UnixNano(),
		Namespace:      pod.namespace,
		Pod:            pod.name,
		Container:      container,
		Severity:       severity,
		Message:        message,
		Attributes: map[string]string{
			"generator": "kubelogs-loadgen",
			"node":      "loadgen-node",
		},
	}
}

func (g *Generator) randomSeverity() uint32 {
	roll := g.rng.Intn(100)

	// Distribution based on error rate
	if roll < g.cfg.ErrorRate/2 {
		return 6 // FATAL (rare)
	}
	if roll < g.cfg.ErrorRate {
		return 5 // ERROR
	}
	if roll < g.cfg.ErrorRate+10 {
		return 4 // WARN
	}
	if roll < g.cfg.ErrorRate+60 {
		return 3 // INFO (most common)
	}
	if roll < g.cfg.ErrorRate+85 {
		return 2 // DEBUG
	}
	return 1 // TRACE
}

func (g *Generator) randomMessage(severity uint32) string {
	templates := logTemplates[severity]
	if len(templates) == 0 {
		templates = logTemplates[3] // fallback to INFO
	}

	template := templates[g.rng.Intn(len(templates))]

	// Replace format specifiers with random values
	return fmt.Sprintf(template, g.rng.Intn(10000))
}

func randomString(rng *rand.Rand, length int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	result := make([]byte, length)
	for i := range result {
		result[i] = chars[rng.Intn(len(chars))]
	}
	return string(result)
}
