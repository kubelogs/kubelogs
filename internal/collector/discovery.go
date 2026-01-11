package collector

import (
	"context"
	"log/slog"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// PodEventType represents the type of pod lifecycle event.
type PodEventType int

const (
	ContainerStarted PodEventType = iota
	ContainerStopped
)

// PodEvent represents a pod lifecycle event.
type PodEvent struct {
	Type      PodEventType
	Container ContainerRef
}

// PodDiscovery watches for pod changes on the current node.
type PodDiscovery struct {
	nodeName  string
	clientset kubernetes.Interface
	events    chan PodEvent

	// Track container states to detect restarts
	containerStates map[string]containerState
	mu              sync.RWMutex

	factory  informers.SharedInformerFactory
	informer cache.SharedIndexInformer

	ctx context.Context
}

// containerState tracks a container's running state.
type containerState struct {
	running      bool
	restartCount int32
	containerID  string
}

// NewPodDiscovery creates a pod watcher for the given node.
func NewPodDiscovery(clientset kubernetes.Interface, nodeName string) *PodDiscovery {
	return &PodDiscovery{
		nodeName:        nodeName,
		clientset:       clientset,
		events:          make(chan PodEvent, 1000), // Increased from 100 to handle high pod churn
		containerStates: make(map[string]containerState),
	}
}

// Events returns the channel of pod events.
func (d *PodDiscovery) Events() <-chan PodEvent {
	return d.events
}

// Start begins watching pods. Blocks until ctx is canceled.
func (d *PodDiscovery) Start(ctx context.Context) error {
	d.ctx = ctx

	// Create informer factory with field selector for this node
	tweakListOptions := func(opts *metav1.ListOptions) {
		opts.FieldSelector = fields.OneTermEqualSelector("spec.nodeName", d.nodeName).String()
	}

	d.factory = informers.NewSharedInformerFactoryWithOptions(
		d.clientset,
		30*time.Second, // Resync period
		informers.WithTweakListOptions(tweakListOptions),
	)

	d.informer = d.factory.Core().V1().Pods().Informer()

	// Add event handlers
	_, err := d.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    d.onPodAdd,
		UpdateFunc: d.onPodUpdate,
		DeleteFunc: d.onPodDelete,
	})
	if err != nil {
		return err
	}

	// Start informer
	d.factory.Start(ctx.Done())

	// Wait for initial cache sync
	syncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if !cache.WaitForCacheSync(syncCtx.Done(), d.informer.HasSynced) {
		return &DiscoveryError{Message: "failed to sync pod cache"}
	}

	slog.Info("pod discovery started", "node", d.nodeName)

	// Block until context is done
	<-ctx.Done()
	return ctx.Err()
}

func (d *PodDiscovery) onPodAdd(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}

	d.processContainerStatuses(pod)
}

func (d *PodDiscovery) onPodUpdate(oldObj, newObj interface{}) {
	pod, ok := newObj.(*corev1.Pod)
	if !ok {
		return
	}

	d.processContainerStatuses(pod)
}

func (d *PodDiscovery) onPodDelete(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		// Handle deleted final state unknown
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			pod, ok = tombstone.Obj.(*corev1.Pod)
			if !ok {
				return
			}
		} else {
			return
		}
	}

	// Emit stopped events for all containers
	for _, cs := range pod.Status.ContainerStatuses {
		ref := ContainerRef{
			Namespace:     pod.Namespace,
			PodName:       pod.Name,
			PodUID:        string(pod.UID),
			ContainerName: cs.Name,
		}

		d.mu.Lock()
		delete(d.containerStates, ref.Key())
		d.mu.Unlock()

		d.emitEvent(PodEvent{
			Type:      ContainerStopped,
			Container: ref,
		})
	}
}

func (d *PodDiscovery) processContainerStatuses(pod *corev1.Pod) {
	for _, cs := range pod.Status.ContainerStatuses {
		ref := ContainerRef{
			Namespace:     pod.Namespace,
			PodName:       pod.Name,
			PodUID:        string(pod.UID),
			ContainerName: cs.Name,
		}
		key := ref.Key()

		isRunning := cs.State.Running != nil

		d.mu.Lock()
		prev, exists := d.containerStates[key]

		// Detect state changes
		if isRunning && (!exists || !prev.running || cs.ContainerID != prev.containerID) {
			// Container started or restarted
			d.containerStates[key] = containerState{
				running:      true,
				restartCount: cs.RestartCount,
				containerID:  cs.ContainerID,
			}
			d.mu.Unlock()

			d.emitEvent(PodEvent{
				Type:      ContainerStarted,
				Container: ref,
			})
		} else if !isRunning && exists && prev.running {
			// Container stopped
			d.containerStates[key] = containerState{
				running:      false,
				restartCount: cs.RestartCount,
				containerID:  cs.ContainerID,
			}
			d.mu.Unlock()

			d.emitEvent(PodEvent{
				Type:      ContainerStopped,
				Container: ref,
			})
		} else {
			// No state change or initial non-running state
			if !exists && !isRunning {
				d.containerStates[key] = containerState{
					running:      false,
					restartCount: cs.RestartCount,
					containerID:  cs.ContainerID,
				}
			}
			d.mu.Unlock()
		}
	}
}

func (d *PodDiscovery) emitEvent(event PodEvent) {
	// Try non-blocking first
	select {
	case d.events <- event:
		return
	default:
	}

	// Channel is full - block with timeout to avoid silent drops
	slog.Warn("pod event channel full, waiting to emit",
		"type", event.Type,
		"container", event.Container.Key(),
		"bufferSize", len(d.events),
	)

	select {
	case d.events <- event:
	case <-d.ctx.Done():
		slog.Error("failed to emit pod event - context cancelled",
			"type", event.Type,
			"container", event.Container.Key(),
		)
	case <-time.After(5 * time.Second):
		slog.Error("failed to emit pod event - timeout after 5s",
			"type", event.Type,
			"container", event.Container.Key(),
		)
	}
}

// DiscoveryError represents a pod discovery error.
type DiscoveryError struct {
	Message string
}

func (e *DiscoveryError) Error() string {
	return "discovery: " + e.Message
}
