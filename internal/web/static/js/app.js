function app() {
    return {
        entries: [],
        namespaces: [],
        containers: [],
        filters: {
            namespace: '',
            container: '',
            minSeverity: 0,
            search: '',
            timeSpan: 'live'
        },
        tailing: true,
        connected: false,
        showShortcuts: false,
        eventSource: null,
        stats: {
            totalEntries: 0,
            diskSizeBytes: 0
        },
        maxEntries: 1000,
        oldestLoadedId: null,    // Cursor for backward pagination
        hasMoreOlder: true,      // Whether more historical entries exist
        loadingOlder: false,     // Prevent concurrent requests
        selectedEntry: null,     // Currently selected log entry for detail panel
        detailPanelOpen: false,  // Whether detail panel is visible

        init() {
            this.loadFilters();
            this.loadStats();

            if (this.isLiveMode()) {
                this.startTailing();
            } else {
                this.loadHistoricalLogs();
            }

            // Refresh filters periodically
            setInterval(() => this.loadFilters(), 30000);
            // Refresh stats periodically
            setInterval(() => this.loadStats(), 10000);
        },

        isLiveMode() {
            return this.filters.timeSpan === 'live';
        },

        async loadFilters() {
            try {
                const [nsResp, cResp] = await Promise.all([
                    fetch('/api/filters/namespaces'),
                    fetch('/api/filters/containers')
                ]);
                this.namespaces = await nsResp.json();
                this.containers = await cResp.json();
            } catch (err) {
                console.error('Failed to load filters:', err);
            }
        },

        async loadStats() {
            try {
                const resp = await fetch('/api/stats');
                this.stats = await resp.json();
            } catch (err) {
                console.error('Failed to load stats:', err);
            }
        },

        stopStreaming() {
            if (this.eventSource) {
                this.eventSource.close();
                this.eventSource = null;
            }
            this.connected = false;
        },

        async loadHistoricalLogs() {
            this.stopStreaming();

            const params = new URLSearchParams();
            if (this.filters.namespace) params.set('namespace', this.filters.namespace);
            if (this.filters.container) params.set('container', this.filters.container);
            if (this.filters.minSeverity) params.set('minSeverity', this.filters.minSeverity);
            if (this.filters.search) params.set('search', this.filters.search);

            const timeSpanMinutes = parseInt(this.filters.timeSpan);
            if (timeSpanMinutes > 0) {
                const startTime = new Date(Date.now() - timeSpanMinutes * 60 * 1000);
                params.set('startTime', startTime.toISOString());
            }

            params.set('order', 'desc');
            params.set('limit', '100');

            try {
                const resp = await fetch(`/api/logs?${params}`);
                const data = await resp.json();

                if (data.entries && data.entries.length > 0) {
                    // Reverse to show chronological order (oldest first in array)
                    this.entries = data.entries.reverse();
                    this.oldestLoadedId = this.entries[0].id;
                    this.hasMoreOlder = data.hasMore;

                    // Scroll to bottom to show newest entries
                    this.$nextTick(() => {
                        const container = this.$refs.logContainer;
                        if (container) {
                            container.scrollTop = container.scrollHeight;
                        }
                    });
                } else {
                    this.entries = [];
                    this.oldestLoadedId = null;
                    this.hasMoreOlder = false;
                }
            } catch (err) {
                console.error('Failed to load historical logs:', err);
            }
        },

        startTailing() {
            if (this.eventSource) {
                this.eventSource.close();
            }

            const params = new URLSearchParams();
            if (this.filters.namespace) params.set('namespace', this.filters.namespace);
            if (this.filters.container) params.set('container', this.filters.container);
            if (this.filters.minSeverity) params.set('minSeverity', this.filters.minSeverity);
            if (this.filters.search) params.set('search', this.filters.search);
            // Note: Live mode doesn't use time filter - streams all new entries

            this.eventSource = new EventSource(`/api/logs/stream?${params}`);

            this.eventSource.onopen = () => {
                this.connected = true;
            };

            this.eventSource.onmessage = (e) => {
                const entry = JSON.parse(e.data);
                this.entries.push(entry);

                // Track oldest loaded ID for backward pagination
                if (this.oldestLoadedId === null || entry.id < this.oldestLoadedId) {
                    this.oldestLoadedId = entry.id;
                }

                // Keep max entries in memory (trim oldest when tailing)
                while (this.entries.length > this.maxEntries) {
                    const removed = this.entries.shift();
                    // Update oldest ID when removing from front
                    if (this.entries.length > 0) {
                        this.oldestLoadedId = this.entries[0].id;
                    }
                }

                // Auto-scroll if tailing
                if (this.tailing) {
                    this.$nextTick(() => {
                        const container = this.$refs.logContainer;
                        if (container) {
                            container.scrollTop = container.scrollHeight;
                        }
                    });
                }
            };

            this.eventSource.onerror = () => {
                this.connected = false;
                // Reconnect after 2 seconds
                setTimeout(() => {
                    if (!this.connected) {
                        this.startTailing();
                    }
                }, 2000);
            };
        },

        toggleTail() {
            this.tailing = !this.tailing;
            if (this.tailing) {
                const container = this.$refs.logContainer;
                if (container) {
                    container.scrollTop = container.scrollHeight;
                }
            }
        },

        applyFilters() {
            this.entries = [];
            this.oldestLoadedId = null;
            this.hasMoreOlder = true;
            this.loadingOlder = false;

            if (this.isLiveMode()) {
                this.tailing = true;
                this.startTailing();
            } else {
                this.tailing = false;
                this.loadHistoricalLogs();
            }
        },

        clearLogs() {
            this.entries = [];
            this.oldestLoadedId = null;
            this.hasMoreOlder = true;
        },

        async loadOlderEntries() {
            if (this.loadingOlder || !this.hasMoreOlder || this.entries.length === 0) {
                return;
            }

            this.loadingOlder = true;

            // Build query params matching current filters
            const params = new URLSearchParams();
            if (this.filters.namespace) params.set('namespace', this.filters.namespace);
            if (this.filters.container) params.set('container', this.filters.container);
            if (this.filters.minSeverity) params.set('minSeverity', this.filters.minSeverity);
            if (this.filters.search) params.set('search', this.filters.search);

            // Apply time range filter for historical mode
            if (!this.isLiveMode()) {
                const timeSpanMinutes = parseInt(this.filters.timeSpan);
                if (timeSpanMinutes > 0) {
                    const startTime = new Date(Date.now() - timeSpanMinutes * 60 * 1000);
                    params.set('startTime', startTime.toISOString());
                }
            }

            // Use beforeId for backward pagination with descending order
            params.set('beforeId', this.oldestLoadedId);
            params.set('order', 'desc');
            params.set('limit', '100');

            try {
                const resp = await fetch(`/api/logs?${params}`);
                const data = await resp.json();

                if (!data.entries || data.entries.length === 0) {
                    this.hasMoreOlder = false;
                } else {
                    // Preserve scroll position
                    const container = this.$refs.logContainer;
                    const prevScrollHeight = container.scrollHeight;
                    const prevScrollTop = container.scrollTop;

                    // Prepend entries (API returns newest-first, so reverse for chronological order)
                    const olderEntries = data.entries.reverse();
                    this.entries = [...olderEntries, ...this.entries];

                    // Update cursor to oldest entry
                    this.oldestLoadedId = olderEntries[0].id;
                    this.hasMoreOlder = data.hasMore;

                    // Trim from end if exceeding maxEntries (remove newest when in historical mode)
                    while (this.entries.length > this.maxEntries) {
                        this.entries.pop();
                    }

                    // Restore scroll position after DOM update
                    this.$nextTick(() => {
                        const newScrollHeight = container.scrollHeight;
                        container.scrollTop = prevScrollTop + (newScrollHeight - prevScrollHeight);
                    });
                }
            } catch (err) {
                console.error('Failed to load older entries:', err);
            } finally {
                this.loadingOlder = false;
            }
        },

        handleScroll(event) {
            const container = event.target;
            const scrollThreshold = 200;

            // Detect scroll to top for loading older entries (works in both modes)
            if (container.scrollTop < scrollThreshold && !this.loadingOlder && this.hasMoreOlder) {
                this.loadOlderEntries();
            }

            // Disable tailing when user scrolls up (only in Live mode)
            if (this.isLiveMode() && this.tailing) {
                const isAtBottom = container.scrollHeight - container.scrollTop - container.clientHeight < 50;
                if (!isAtBottom) {
                    this.tailing = false;
                }
            }
        },

        handleKeydown(e) {
            // Ignore if typing in input/select
            if (e.target.tagName === 'INPUT' || e.target.tagName === 'SELECT' || e.target.tagName === 'TEXTAREA') {
                return;
            }

            switch (e.key) {
                case '/':
                    e.preventDefault();
                    this.$refs.searchInput.focus();
                    break;
                case '?':
                    e.preventDefault();
                    this.showShortcuts = !this.showShortcuts;
                    break;
                case 't':
                    e.preventDefault();
                    this.toggleTail();
                    break;
                case 'c':
                    e.preventDefault();
                    this.clearLogs();
                    break;
                case 'g':
                    e.preventDefault();
                    this.$refs.logContainer.scrollTop = 0;
                    this.tailing = false;
                    break;
                case 'G':
                    e.preventDefault();
                    this.$refs.logContainer.scrollTop = this.$refs.logContainer.scrollHeight;
                    this.tailing = true;
                    break;
                case 'u':
                    e.preventDefault();
                    this.loadOlderEntries();
                    break;
                case 'j':
                    e.preventDefault();
                    if (this.detailPanelOpen) {
                        this.navigateEntry('next');
                    }
                    break;
                case 'k':
                    e.preventDefault();
                    if (this.detailPanelOpen) {
                        this.navigateEntry('prev');
                    }
                    break;
                case '1':
                case '2':
                case '3':
                case '4':
                case '5':
                case '6':
                    e.preventDefault();
                    this.filters.minSeverity = parseInt(e.key);
                    this.applyFilters();
                    break;
                case 'Escape':
                    e.preventDefault();
                    if (this.detailPanelOpen) {
                        this.closeDetailPanel();
                    } else if (this.showShortcuts) {
                        this.showShortcuts = false;
                    } else {
                        this.filters = { namespace: '', container: '', minSeverity: 0, search: '', timeSpan: 'live' };
                        this.applyFilters();
                    }
                    break;
            }
        },

        formatTimestamp(nanos) {
            const date = new Date(nanos / 1000000);
            const pad = (n, w = 2) => String(n).padStart(w, '0');
            return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ` +
                   `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}.${pad(date.getMilliseconds(), 3)}`;
        },

        severityLabel(s) {
            const labels = ['UNK', 'TRC', 'DBG', 'INF', 'WRN', 'ERR', 'FTL'];
            return labels[s] || 'UNK';
        },

        severityClass(s) {
            const classes = [
                'text-gray-500',    // Unknown
                'text-gray-400',    // Trace
                'text-gray-300',    // Debug
                'text-blue-400',    // Info
                'text-yellow-400',  // Warn
                'text-red-400',     // Error
                'text-red-500'      // Fatal
            ];
            return classes[s] || 'text-gray-500';
        },

        severityRowClass(s) {
            if (s >= 6) return 'bg-red-900/30';   // Fatal
            if (s >= 5) return 'bg-red-900/20';   // Error
            if (s >= 4) return 'bg-yellow-900/10'; // Warn
            return '';
        },

        selectEntry(entry) {
            this.selectedEntry = entry;
            this.detailPanelOpen = true;
        },

        closeDetailPanel() {
            this.detailPanelOpen = false;
        },

        truncateValue(value, maxLen = 12) {
            if (!value) return '';
            if (value.length <= maxLen) return value;
            return value.substring(0, maxLen - 1) + '…';
        },

        navigateEntry(direction) {
            if (!this.selectedEntry || this.entries.length === 0) return;

            const currentIndex = this.entries.findIndex(e => e.id === this.selectedEntry.id);
            if (currentIndex === -1) return;

            const newIndex = direction === 'next'
                ? Math.min(currentIndex + 1, this.entries.length - 1)
                : Math.max(currentIndex - 1, 0);

            this.selectedEntry = this.entries[newIndex];

            // Scroll selected row into view
            this.$nextTick(() => {
                const row = this.$refs.logContainer?.querySelector(`tr[data-id="${this.selectedEntry.id}"]`);
                row?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
            });
        }
    };
}
