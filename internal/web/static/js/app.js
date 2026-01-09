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
            timeSpan: 0
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

        init() {
            this.loadFilters();
            this.loadStats();
            this.startTailing();

            // Refresh filters periodically
            setInterval(() => this.loadFilters(), 30000);
            // Refresh stats periodically
            setInterval(() => this.loadStats(), 10000);
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

        startTailing() {
            if (this.eventSource) {
                this.eventSource.close();
            }

            const params = new URLSearchParams();
            if (this.filters.namespace) params.set('namespace', this.filters.namespace);
            if (this.filters.container) params.set('container', this.filters.container);
            if (this.filters.minSeverity) params.set('minSeverity', this.filters.minSeverity);
            if (this.filters.search) params.set('search', this.filters.search);
            if (this.filters.timeSpan > 0) {
                const startTime = new Date(Date.now() - this.filters.timeSpan * 60 * 1000);
                params.set('startTime', startTime.toISOString());
            }

            this.eventSource = new EventSource(`/api/logs/stream?${params}`);

            this.eventSource.onopen = () => {
                this.connected = true;
            };

            this.eventSource.onmessage = (e) => {
                const entry = JSON.parse(e.data);
                this.entries.push(entry);

                // Keep max entries in memory
                while (this.entries.length > this.maxEntries) {
                    this.entries.shift();
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
            this.startTailing();
        },

        clearLogs() {
            this.entries = [];
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
                    if (this.showShortcuts) {
                        this.showShortcuts = false;
                    } else {
                        this.filters = { namespace: '', container: '', minSeverity: 0, search: '', timeSpan: 0 };
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
        }
    };
}
